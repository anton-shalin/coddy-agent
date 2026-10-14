// Package status is the vocabulary of what a running coddy serve process says
// about itself. It imports no other package of the module but the version, so
// the process that builds a snapshot and the surfaces that render one share
// these types without sharing a dependency.
package status

import (
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
	"time"

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
	Subsystems []Subsystem
	// RestartRequired is true when a listener's address moved and nothing will
	// restart the process by itself.
	RestartRequired bool
	// RestartRefused is why a dispatcher would not restart into the saved
	// configuration: the message of the check that rejects it. Empty otherwise.
	RestartRefused string
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

// Subsystem is one row of the process's subsystem table.
type Subsystem struct {
	// Kind names the subsystem (serve.Kind).
	Kind string
	// State is one of the State constants.
	State string
	// Address is the listener address the running instance is bound to, empty
	// for a subsystem without a listener or one that is not running.
	Address string
	// PendingAddress is the address the live configuration moved a running
	// listener to, empty when it did not.
	PendingAddress string
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
