//go:build metrics

package metrics

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/httpx"
	"github.com/EvilFreelancer/coddy-agent/internal/logger"
)

// shutdownGrace is how long a scrape in flight may take to finish once the
// exporter is asked to stop.
const shutdownGrace = 5 * time.Second

// Options configure Serve.
type Options struct {
	// Addr is the address to listen on.
	Addr string
	// Cfg is the live configuration; the token is read from it on every request.
	Cfg func() *config.Config
	Log *slog.Logger
	// Collector holds the counters to serve. It outlives Serve, so a listener
	// rebuilt on another address continues them.
	Collector *Collector
}

// Serve runs the exporter until ctx is cancelled. A bind error returns at once.
func Serve(ctx context.Context, opts Options) error {
	if opts.Collector == nil || opts.Cfg == nil || opts.Log == nil {
		return errors.New("metrics: Collector, Cfg and Log are required")
	}
	log := logger.Component(opts.Log, "metrics")
	token := func() string {
		if cfg := opts.Cfg(); cfg != nil {
			return cfg.Metrics.Token
		}
		return ""
	}
	ln, err := net.Listen("tcp", opts.Addr)
	if err != nil {
		return err
	}
	if cfg := opts.Cfg(); cfg != nil && cfg.Metrics.ExposedWithoutToken() {
		log.Warn("the metrics exporter listens beyond this machine with no token: anybody who can reach the port reads its figures (set metrics.token, or bind metrics.host to 127.0.0.1)", "addr", opts.Addr)
	}
	log.Info("metrics exporter listening", "addr", ln.Addr().String())

	server := httpx.NewServer(opts.Addr, opts.Collector.Handler(token))
	errs := make(chan error, 1)
	go func() { errs <- server.Serve(ln) }()

	select {
	case err := <-errs:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			log.Warn("metrics shutdown timed out, closing anyway", "grace", shutdownGrace)
			_ = server.Close()
		}
		return nil
	}
}
