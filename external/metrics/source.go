//go:build metrics

// Package metrics is the Prometheus exporter of coddy serve: the process's
// counters, each family in a private set of github.com/VictoriaMetrics/metrics,
// the state read from one snapshot per scrape, and the listener that serves
// them. Every file of the package is built with the metrics tag, and
// cmd/coddy/metrics.go is its only importer.
package metrics

import (
	"log/slog"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
	"github.com/EvilFreelancer/coddy-agent/internal/status"
)

// Source is what the exporter reads off the process it runs in.
type Source struct {
	// Status reads the process once: the subsystems, the turns and tasks in
	// flight, the disk of the session store.
	Status func() status.Snapshot
	// Cfg is the live configuration, read when an attempt is observed and
	// when a scrape asks for the token.
	Cfg func() *config.Config
	// Mgr is the session manager, nil in a process without a session store.
	Mgr *session.Manager
	// AddConfigObserver registers fn for every replacement of the live
	// configuration and returns the function that removes it.
	AddConfigObserver func(func(*config.Config)) (remove func())
	Log               *slog.Logger
}
