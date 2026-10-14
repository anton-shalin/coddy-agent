//go:build metrics

package metrics

import (
	"bytes"
	"crypto/subtle"
	"net/http"
	"strings"
	"sync"
	"time"

	vm "github.com/VictoriaMetrics/metrics"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// attemptBuckets are the bounds of coddy_llm_attempt_duration_seconds: an
// attempt lasts as long as its whole stream.
var attemptBuckets = []float64{0.5, 1, 2.5, 5, 10, 30, 60, 120, 300, 600}

// What a scrape is held to: at most this many at once, each within this long.
const (
	maxScrapesInFlight = 2
	scrapeTimeout      = 10 * time.Second
	expositionType     = "text/plain; version=0.0.4; charset=utf-8"
)

// Collector is the state of the exporter of one coddy serve process: the
// instruments the observers feed, each family in a private set, and the
// readers of the process's state. It outlives the listener that serves it, so
// a listener rebuilt on another address continues the same counters.
type Collector struct {
	src    Source
	budget *budget

	// families are the instrument families in the order a scrape writes them.
	families []*instruments

	attempts       *instruments
	attemptSeconds *instruments
	inputTokens    *instruments
	cachedTokens   *instruments
	outputTokens   *instruments
	turnsStarted   *vm.Counter
	turnsEnded     *vm.Counter
	reloads        *vm.Counter

	closeOnce sync.Once
	removers  []func()
}

// NewCollector builds the families and starts observing: the provider
// attempts of the whole process, the turns of src.Mgr and the replacements of
// the live configuration. Close stops that.
func NewCollector(src Source) *Collector {
	c := &Collector{src: src, budget: newBudget()}
	add := func(name, typ, help string) *instruments {
		f := newInstruments(family{name: name, typ: typ, help: help})
		c.families = append(c.families, f)
		return f
	}
	// A counter without labels exists from the start, so it reads 0 until it moves.
	counter := func(name, help string) *vm.Counter { return add(name, "counter", help).set.NewCounter(name) }

	c.attempts = add("coddy_llm_attempts_total", "counter",
		"Provider invocation attempts, retries included; some fail before a request is sent.")
	c.attemptSeconds = add("coddy_llm_attempt_duration_seconds", "histogram",
		"Duration of a provider invocation attempt, the whole stream included.")
	c.inputTokens = add("coddy_llm_input_tokens_total", "counter", "Input tokens the provider reported for attempts that returned a response.")
	c.cachedTokens = add("coddy_llm_cached_input_tokens_total", "counter", "Cached input tokens: a subset of the input tokens.")
	c.outputTokens = add("coddy_llm_output_tokens_total", "counter", "Output tokens the provider reported for attempts that returned a response.")
	c.turnsStarted = counter("coddy_turns_started_total", "Turns started in this process. The turn families are read independently, so started minus ended need not equal coddy_turns_running.")
	c.turnsEnded = counter("coddy_turns_ended_total", "Turns ended in this process. The turn families are read independently, so started minus ended need not equal coddy_turns_running.")
	c.reloads = counter("coddy_config_reloads_total", "Replacements of the live configuration in this process.")

	c.seed(src.Cfg())
	c.removers = append(c.removers, llm.AddCallObserver(c.observeCall))
	if src.Mgr != nil {
		c.removers = append(c.removers, src.Mgr.AddTurnObserver(c.observeTurn))
	}
	if src.AddConfigObserver != nil {
		c.removers = append(c.removers, src.AddConfigObserver(c.observeConfig))
	}
	return c
}

// Close stops the observers. The counters stay readable, and a second Close
// does nothing.
func (c *Collector) Close() {
	c.closeOnce.Do(func() {
		for _, remove := range c.removers {
			remove()
		}
	})
}

// observeCall counts one provider invocation attempt. The labels are decided
// here, against the configuration live now, and never again: a rename or a
// removal later leaves what was counted where it was.
func (c *Collector) observeCall(ev llm.CallEvent) {
	provider, model := c.budget.labels(c.src.Cfg(), ev.Provider, ev.Model)
	c.attempts.counter("provider", provider, "model", model, "outcome", ev.Outcome).Inc()
	c.attemptSeconds.histogram(attemptBuckets, "provider", provider).Update(ev.Duration.Seconds())
	addTokens(c.inputTokens, ev.InputTokens, provider, model)
	addTokens(c.cachedTokens, ev.CachedInputTokens, provider, model)
	addTokens(c.outputTokens, ev.OutputTokens, provider, model)
}

// addTokens counts n tokens, when an attempt reported any.
func addTokens(f *instruments, n int, provider, model string) {
	if n > 0 {
		f.counter("provider", provider, "model", model).Add(n)
	}
}

// observeTurn counts the two edges of a turn; a woken edge announces a turn
// that already started.
func (c *Collector) observeTurn(ev session.TurnEvent) {
	switch ev.Phase {
	case session.TurnPhaseStarted:
		c.turnsStarted.Inc()
	case session.TurnPhaseEnded:
		c.turnsEnded.Inc()
	}
}

func (c *Collector) observeConfig(next *config.Config) {
	c.reloads.Inc()
	c.seed(next)
}

// seed gives every provider and declared model of cfg that the budget admits a
// series at zero for each outcome, so a rate over its first failure has a
// sample to start from, and the other model beside it for what cfg does not
// declare.
func (c *Collector) seed(cfg *config.Config) {
	if cfg == nil {
		return
	}
	seeded := map[[2]string]bool{}
	seed := func(provider, model string) {
		pair := [2]string{provider, model}
		if provider == otherLabel || seeded[pair] {
			return
		}
		seeded[pair] = true
		for _, outcome := range llm.CallOutcomes() {
			c.attempts.counter("provider", provider, "model", model, "outcome", outcome)
		}
	}
	for _, p := range cfg.Providers {
		seed(c.budget.labels(cfg, p.Name, ""))
	}
	for _, m := range cfg.Models {
		if provider, api, err := config.SplitModelRef(m.Model); err == nil {
			seed(c.budget.labels(cfg, provider, api))
		}
	}
}

// Handler serves GET (and so HEAD) /metrics and answers 404 for every other
// path. token is read on every request: empty leaves the endpoint open,
// anything else must come as a bearer credential, checked before anything is
// read. Past that a scrape is held to maxScrapesInFlight at once, each within
// scrapeTimeout; a scrape that outlives its timeout keeps its place until it
// returns, so a stuck reader cannot pile up goroutines.
func (c *Collector) Handler(token func() string) http.Handler {
	inflight := make(chan struct{}, maxScrapesInFlight)
	scrape := http.TimeoutHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		select {
		case inflight <- struct{}{}:
			defer func() { <-inflight }()
		default:
			http.Error(w, "too many scrapes in flight", http.StatusServiceUnavailable)
			return
		}
		body := c.exposition()
		w.Header().Set("Content-Type", expositionType)
		_, _ = w.Write(body)
	}), scrapeTimeout, "scrape timed out\n")
	mux := http.NewServeMux()
	mux.HandleFunc("GET /metrics", func(w http.ResponseWriter, r *http.Request) {
		if want := token(); want != "" && !bearerIs(r, want) {
			w.Header().Set("WWW-Authenticate", `Bearer realm="coddy-metrics"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		scrape.ServeHTTP(w, r)
	})
	return mux
}

// exposition is one scrape: the instrument families, the state of the process
// from one status snapshot, and the library's Go and process figures.
func (c *Collector) exposition() []byte {
	var b bytes.Buffer
	for _, f := range c.families {
		f.writeTo(&b)
	}
	writeState(&b, c.src.Status())
	writeLibrary(&b)
	return b.Bytes()
}

// bearerIs compares the request's bearer credential with want in constant
// time. The scheme is case-insensitive and the credential trimmed, as for the
// HTTP API (external/httpserver is built under another tag, so the few lines
// are repeated here).
func bearerIs(r *http.Request, want string) bool {
	const scheme = "Bearer "
	h := r.Header.Get("Authorization")
	if len(h) < len(scheme) || !strings.EqualFold(h[:len(scheme)], scheme) {
		return false
	}
	got := strings.TrimSpace(h[len(scheme):])
	return got != "" && subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}
