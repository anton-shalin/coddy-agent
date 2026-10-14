package serve

import (
	"github.com/EvilFreelancer/coddy-agent/internal/bgtask"
	"github.com/EvilFreelancer/coddy-agent/internal/platform"
	"github.com/EvilFreelancer/coddy-agent/internal/status"
)

// SetSupervisor names the supervisor whose reading of the live configuration
// Status reports. The supervisor needs the runtime to be built and the runtime
// needs the supervisor to answer for it, so the link is made after both exist.
func (r *Runtime) SetSupervisor(sup *Supervisor) { r.sup.Store(sup) }

// Status reads what this process knows about itself, part by part and without
// writing anything: the binary, the supervisor's reading of the live
// configuration (when SetSupervisor named one), the turns and tasks in flight,
// and the disk of the session store. A part the process has no source for is
// left out.
func (r *Runtime) Status() status.Snapshot {
	snap := status.Snapshot{Build: status.CurrentBuild(), Tasks: make(map[string]map[string]int)}
	if sup := r.sup.Load(); sup != nil {
		if live := r.Cfg(); live != nil {
			snap.Subsystems, snap.RestartRequired, snap.RestartRefused = sup.Status(live)
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
			snap.Disk = &status.Disk{AvailableBytes: available, SizeBytes: size}
		}
	}
	return snap
}
