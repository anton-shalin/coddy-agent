package serve

import (
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/bgtask"
	"github.com/EvilFreelancer/coddy-agent/internal/platform"
	"github.com/EvilFreelancer/coddy-agent/internal/version"
)

// Subsystem states, as the supervisor reads them against the live configuration.
const (
	// StateRunning is a subsystem the configuration enables and the process runs.
	StateRunning = "running"
	// StateStarting is one the configuration enables and the supervisor has not
	// applied the configuration for yet.
	StateStarting = "starting"
	// StateStopped is one the configuration enables, the supervisor applied, and
	// the process does not run (this binary cannot run it).
	StateStopped = "stopped"
	// StateRetained is one the configuration disables and the process still
	// runs: the last surface is kept, and any other is about to stop.
	StateRetained = "retained"
)

// Snapshot is what one coddy serve process reads off itself at one moment.
// Every part is read on its own, so two parts can disagree by what happened
// between the reads.
type Snapshot struct {
	Build Build
	// Subsystems are the rows of the subsystem table, in declaration order.
	Subsystems []SubsystemStatus
	// RestartRequired is true when a listener's address moved and nothing will
	// restart the process by itself.
	RestartRequired bool
	// TurnsRunning counts the sessions with a turn running in this process.
	TurnsRunning int
	// OldestTurnStartedAt is when the longest-running of them began, zero when
	// none runs.
	OldestTurnStartedAt time.Time
	// Tasks counts the unfinished background tasks of the model, by kind and then
	// status. The runtime's own system tasks are not counted.
	Tasks map[string]map[string]int
	// Disk is the disk of the session store, nil when it was not read: no store
	// in this process, a platform without the call, or a failed read.
	Disk *Disk
}

// Build is what the binary says it is.
type Build struct {
	Version   string
	Revision  string // empty when the binary carries no vcs.revision
	GoVersion string
	GOOS      string
	GOARCH    string
	// Tags are the build tags of the binary.
	Tags []string
}

// SubsystemStatus is one row of the process's subsystem table.
type SubsystemStatus struct {
	Kind Kind
	// State is one of the State constants.
	State string
}

// Disk is the room on one disk: what a process without privileges can still
// write there, and the disk's size.
type Disk struct {
	AvailableBytes, SizeBytes uint64
}

var currentBuild = sync.OnceValue(func() Build {
	info, _ := debug.ReadBuildInfo()
	return buildOf(info)
})

// CurrentBuild describes the running binary. It is read once.
func CurrentBuild() Build { return currentBuild() }

func buildOf(info *debug.BuildInfo) Build {
	b := Build{
		Version:   version.Get(),
		GoVersion: runtime.Version(),
		GOOS:      runtime.GOOS,
		GOARCH:    runtime.GOARCH,
	}
	if info == nil {
		return b
	}
	for _, setting := range info.Settings {
		switch {
		case setting.Key == "vcs.revision":
			b.Revision = setting.Value
		case setting.Key == "-tags" && setting.Value != "":
			b.Tags = strings.Split(setting.Value, ",")
		}
	}
	return b
}

// SetSupervisor names the supervisor whose reading of the live configuration
// Status reports. The supervisor needs the runtime to be built and the runtime
// needs the supervisor to answer for it, so the link is made after both exist.
func (r *Runtime) SetSupervisor(sup *Supervisor) { r.sup.Store(sup) }

// Status reads what this process knows about itself, part by part and without
// writing anything: the binary, the supervisor's reading of the live
// configuration (when SetSupervisor named one), the turns and tasks in flight,
// and the disk of the session store. A part the process has no source for is
// left out.
func (r *Runtime) Status() Snapshot {
	snap := Snapshot{Build: CurrentBuild(), Tasks: make(map[string]map[string]int)}
	if sup := r.sup.Load(); sup != nil {
		if live := r.Cfg(); live != nil {
			snap.Subsystems, snap.RestartRequired = sup.Status(live)
		}
	}
	if r.Mgr != nil {
		snap.TurnsRunning = len(r.Mgr.ActiveTurnSessionIDs())
		snap.OldestTurnStartedAt, _ = r.Mgr.OldestTurnStartedAt()
	}
	for kind, byStatus := range bgtask.Default().CountUnfinished() {
		counts := make(map[string]int, len(byStatus))
		for st, n := range byStatus {
			counts[string(st)] = n
		}
		snap.Tasks[string(kind)] = counts
	}
	if r.Store != nil {
		read := r.diskFree
		if read == nil {
			read = platform.DiskFree
		}
		if available, size, err := read(r.Store.Root); err == nil {
			snap.Disk = &Disk{AvailableBytes: available, SizeBytes: size}
		}
	}
	return snap
}
