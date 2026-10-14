//go:build metrics

package metrics

import (
	"bytes"
	"strings"
	"time"

	vm "github.com/VictoriaMetrics/metrics"

	"github.com/EvilFreelancer/coddy-agent/internal/bgtask"
	"github.com/EvilFreelancer/coddy-agent/internal/serve"
	"github.com/EvilFreelancer/coddy-agent/internal/status"
)

func gauge(name, help string) family { return family{name: name, typ: "gauge", help: help} }

var (
	buildInfo = gauge("coddy_build_info",
		"Build of the running binary; the value is always 1.")
	subsystemUp = gauge("coddy_subsystem_up",
		"1 for a subsystem the process runs or is starting, 0 for one that is enabled and not running; the exporter itself is left out.")
	restartRequired = gauge("coddy_restart_required",
		"1 when a saved listen address waits for a restart that nothing will perform on its own.")
	turnsRunning = gauge("coddy_turns_running",
		"Sessions with a turn running in this process. The turn families are read independently, so started minus ended need not equal it.")
	turnOldest = gauge("coddy_turn_oldest_running_seconds",
		"Age of the longest-running turn, 0 when none runs. It does not prove a turn is stuck: waits for a permission or a usage limit are legitimate.")
	backgroundTasks = gauge("coddy_background_tasks",
		"Background tasks of the model that are queued or running.")
	diskAvailable = gauge("coddy_sessions_disk_available_bytes",
		"Free space a non-root writer can use on the disk holding the session store.")
	diskSize = gauge("coddy_sessions_disk_size_bytes",
		"Size of the disk holding the session store.")
)

// The kinds and statuses coddy_background_tasks always lists, so a quiet pool
// is a row of zeros rather than missing series.
var (
	taskKinds    = []bgtask.Kind{bgtask.KindCommand, bgtask.KindAgent, bgtask.KindServer}
	taskStatuses = []bgtask.Status{bgtask.StatusQueued, bgtask.StatusRunning}
)

// writeState writes what the process says about itself, from one
// status.Snapshot taken for the scrape. It never leaves the process: no
// provider, bot API or MCP server is asked anything.
func writeState(b *bytes.Buffer, snap status.Snapshot) {
	var rows bytes.Buffer
	flush := func(f family) {
		f.writeRows(b, &rows)
		rows.Reset()
	}

	bi := snap.Build
	vm.WriteGaugeUint64(&rows, series(buildInfo.name,
		"version", bi.Version, "revision", bi.Revision, "goversion", bi.GoVersion,
		"goos", bi.GOOS, "goarch", bi.GOARCH, "tags", strings.Join(bi.Tags, ",")), 1)
	flush(buildInfo)

	for _, sub := range snap.Subsystems {
		if sub.Kind == string(serve.KindMetrics) {
			continue
		}
		up := 1.0
		if sub.State == status.StateStopped {
			up = 0
		}
		vm.WriteGaugeFloat64(&rows, series(subsystemUp.name, "subsystem", sub.Kind), up)
	}
	flush(subsystemUp)

	vm.WriteGaugeFloat64(&rows, restartRequired.name, boolValue(snap.RestartRequired))
	flush(restartRequired)
	vm.WriteGaugeFloat64(&rows, turnsRunning.name, float64(snap.TurnsRunning))
	flush(turnsRunning)
	oldest := 0.0
	if !snap.OldestTurnStartedAt.IsZero() {
		oldest = max(0, time.Since(snap.OldestTurnStartedAt).Seconds())
	}
	vm.WriteGaugeFloat64(&rows, turnOldest.name, oldest)
	flush(turnOldest)

	for _, kind := range taskKinds {
		for _, st := range taskStatuses {
			vm.WriteGaugeFloat64(&rows, series(backgroundTasks.name, "kind", string(kind), "status", string(st)),
				float64(snap.Tasks[string(kind)][string(st)]))
		}
	}
	flush(backgroundTasks)

	if snap.Disk != nil {
		vm.WriteGaugeUint64(&rows, diskAvailable.name, snap.Disk.AvailableBytes)
		flush(diskAvailable)
		vm.WriteGaugeUint64(&rows, diskSize.name, snap.Disk.SizeBytes)
		flush(diskSize)
	}
}

func boolValue(b bool) float64 {
	if b {
		return 1
	}
	return 0
}
