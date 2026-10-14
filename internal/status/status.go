// Package status is the vocabulary of what a running coddy serve process says
// about itself. It imports no other package of the module, so the process that
// builds a snapshot and the surfaces that render one share these types without
// sharing a dependency.
package status

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
