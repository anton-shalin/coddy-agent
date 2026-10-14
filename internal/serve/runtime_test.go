package serve

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/agent"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/status"
)

type recordingBroker struct {
	requests []agent.DetachedPermissionRequest
}

func (b *recordingBroker) RequestDetachedPermission(_ context.Context, req agent.DetachedPermissionRequest) (*acp.PermissionResult, error) {
	b.requests = append(b.requests, req)
	return &acp.PermissionResult{Outcome: "selected", OptionID: "allow"}, nil
}

type decliningBroker struct{ err error }

func (b decliningBroker) RequestDetachedPermission(context.Context, agent.DetachedPermissionRequest) (*acp.PermissionResult, error) {
	return nil, b.err
}

// waitingBroker shows the prompt and waits until the runtime withdraws it.
type waitingBroker struct {
	asked     chan struct{}
	withdrawn chan struct{}
}

func newWaitingBroker() *waitingBroker {
	return &waitingBroker{asked: make(chan struct{}, 1), withdrawn: make(chan struct{}, 1)}
}

func (b *waitingBroker) RequestDetachedPermission(ctx context.Context, _ agent.DetachedPermissionRequest) (*acp.PermissionResult, error) {
	b.asked <- struct{}{}
	<-ctx.Done()
	b.withdrawn <- struct{}{}
	return nil, nil
}

var detachedReq = agent.DetachedPermissionRequest{ParentSessionID: "sess_parent", ChildSessionID: "sess_child", TaskID: "bg_1", AgentName: "writer"}

// Every turn on the shared manager is handed the runtime itself as its broker,
// long before a surface that can show a prompt is up - and after it went away.
// No surface at all is "nobody can be asked", which the relay turns into a
// refusal with a reason; an offered surface gets the request unchanged.
func TestRuntimeDetachedPermissionSurfaces(t *testing.T) {
	rt := &Runtime{}

	if _, err := rt.RequestDetachedPermission(context.Background(), detachedReq); !errors.Is(err, agent.ErrNoDetachedApprover) {
		t.Fatalf("no surface answered %v, want ErrNoDetachedApprover", err)
	}

	broker := &recordingBroker{}
	withdraw := rt.AddDetachedPermissionApprover(broker)
	res, err := rt.RequestDetachedPermission(context.Background(), detachedReq)
	if err != nil || res == nil || res.OptionID != "allow" {
		t.Fatalf("offered surface answered %+v, %v; want its allow", res, err)
	}
	if len(broker.requests) != 1 || broker.requests[0].ChildSessionID != "sess_child" || broker.requests[0].TaskID != "bg_1" {
		t.Fatalf("surface saw %+v", broker.requests)
	}

	withdraw()
	if _, err := rt.RequestDetachedPermission(context.Background(), detachedReq); !errors.Is(err, agent.ErrNoDetachedApprover) {
		t.Fatalf("withdrawn surface answered %v, want ErrNoDetachedApprover", err)
	}
}

// A surface restarted under a settings change can come up before the old one
// has finished stopping, so withdrawing the old offer must not take the new one
// with it.
func TestRuntimeWithdrawingAnOfferLeavesTheOthers(t *testing.T) {
	rt := &Runtime{}
	withdrawOld := rt.AddDetachedPermissionApprover(decliningBroker{err: agent.ErrNoDetachedApprover})
	fresh := &recordingBroker{}
	rt.AddDetachedPermissionApprover(fresh)

	withdrawOld()
	withdrawOld()

	res, err := rt.RequestDetachedPermission(context.Background(), detachedReq)
	if err != nil || res == nil || res.OptionID != "allow" {
		t.Fatalf("after withdrawing the old offer got %+v, %v; want the fresh surface's allow", res, err)
	}
	if rt.AddDetachedPermissionApprover(nil) == nil {
		t.Fatal("offering nil must still return a callable withdraw")
	}
}

// When every surface says it cannot show this prompt - or failed to - nobody was
// asked, and the subagent must hear exactly that rather than "unanswered".
func TestRuntimeDetachedPermissionEverySurfaceDeclines(t *testing.T) {
	rt := &Runtime{}
	rt.AddDetachedPermissionApprover(decliningBroker{err: agent.ErrNoDetachedApprover})
	rt.AddDetachedPermissionApprover(decliningBroker{err: errors.New("telegram: send failed")})

	if _, err := rt.RequestDetachedPermission(context.Background(), detachedReq); !errors.Is(err, agent.ErrNoDetachedApprover) {
		t.Fatalf("every surface declined but got %v, want ErrNoDetachedApprover", err)
	}
}

// The wait ends with the run: a stopped or timed-out child cancels the context,
// and every surface that shows the prompt must be told so it can take it down.
func TestRuntimeDetachedPermissionCancelReachesEverySurface(t *testing.T) {
	rt := &Runtime{}
	web, chat := newWaitingBroker(), newWaitingBroker()
	rt.AddDetachedPermissionApprover(web)
	rt.AddDetachedPermissionApprover(chat)

	ctx, cancel := context.WithCancel(context.Background())
	type outcome struct {
		res *acp.PermissionResult
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		res, err := rt.RequestDetachedPermission(ctx, detachedReq)
		done <- outcome{res, err}
	}()
	for _, b := range []*waitingBroker{web, chat} {
		select {
		case <-b.asked:
		case <-time.After(5 * time.Second):
			t.Fatal("a surface was never asked")
		}
	}
	cancel()
	for _, b := range []*waitingBroker{web, chat} {
		select {
		case <-b.withdrawn:
		case <-time.After(5 * time.Second):
			t.Fatal("a surface kept the prompt after the run ended")
		}
	}
	select {
	case out := <-done:
		if out.res != nil || out.err != nil {
			t.Fatalf("cancelled wait answered %+v, %v; want nil, nil", out.res, out.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the runtime never returned after the run ended")
	}
}

// A relay alone opens no session store, so there is no manager to hold the live
// configuration. The runtime holds it instead, and a reload goes through the
// same three questions a manager answers - what is live, replace it, who is
// watching - so a relay's settings take effect the way an agent's do (issue
// #401).
func TestRuntimeWithoutAManagerHoldsTheLiveConfiguration(t *testing.T) {
	first := &config.Config{}
	first.Swarm.Name = "office"
	rt := &Runtime{}
	if err := rt.Init(Options{Cfg: first}); err != nil {
		t.Fatal(err)
	}
	if rt.Mgr != nil {
		t.Fatal("a process that runs no turns opened a session manager")
	}
	if rt.Cfg() != first {
		t.Fatal("the runtime does not serve the configuration it started with")
	}
	var seen []*config.Config
	remove := rt.AddConfigObserver(func(c *config.Config) { seen = append(seen, c) })
	next := &config.Config{}
	next.Swarm.Name = "office-2"
	rt.ReplaceConfig(next)
	if rt.Cfg() != next || len(seen) != 1 || seen[0] != next {
		t.Fatalf("replace: live=%p seen=%v", rt.Cfg(), seen)
	}
	remove()
	rt.ReplaceConfig(first)
	if len(seen) != 1 {
		t.Fatal("a removed observer still hears replacements")
	}
	rt.ReplaceConfig(nil)
	if rt.Cfg() != first {
		t.Fatal("a nil configuration replaced the live one")
	}
}

// A relay alone holds its configuration in the runtime, and two overlapping
// replacements reach the supervisor in the order they were stored, as they do
// through a manager: the other order left it rebuilding the relay on the older
// one.
func TestRuntimeReplacementsReachObserversInTheOrderTheyWereStored(t *testing.T) {
	rt := &Runtime{}
	if err := rt.Init(Options{Cfg: &config.Config{}}); err != nil {
		t.Fatal(err)
	}
	first, second := &config.Config{}, &config.Config{}
	entered, release := make(chan struct{}), make(chan struct{})
	var mu sync.Mutex
	var last *config.Config
	remove := rt.AddConfigObserver(func(c *config.Config) {
		if c == first {
			close(entered)
			<-release
		}
		mu.Lock()
		last = c
		mu.Unlock()
	})
	defer remove()
	firstDone, secondDone := make(chan struct{}), make(chan struct{})
	go func() { rt.ReplaceConfig(first); close(firstDone) }()
	<-entered
	go func() { rt.ReplaceConfig(second); close(secondDone) }()
	// Give a second replacement that is not held back the time to publish
	// before the first one's publication resumes.
	select {
	case <-secondDone:
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	for _, done := range []chan struct{}{firstDone, secondDone} {
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("a replacement never finished")
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if last != rt.Cfg() {
		t.Fatalf("the observer was left on %p while the runtime holds %p", last, rt.Cfg())
	}
}

// TestRuntimeConnectsANewSessionsServersInTheBackground: a session coddy serve
// opens - the first message of a web chat, a messenger chat - returns before
// its MCP servers answer, and its first turn says it was taken before it waits
// for them (issue #357). A cold stdio server held the first frame of a chat for
// sixteen seconds.
func TestRuntimeConnectsANewSessionsServersInTheBackground(t *testing.T) {
	cfg := &config.Config{}
	cfg.Paths.Home = t.TempDir()
	rt := &Runtime{}
	if err := rt.Init(Options{Cfg: cfg, NeedsSessions: true, SessionsRoot: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	if rt.Mgr == nil {
		t.Fatal("a process that runs turns opened no session manager")
	}
	if !rt.Mgr.NewSessionsBackgroundMCP() {
		t.Fatal("coddy serve connects a new session's MCP servers before session/new returns")
	}
	if rt.Mgr.BackgroundMCPConnect() {
		t.Fatal("coddy serve starts a stored session's MCP servers when it is loaded to be read")
	}
}

// launched starts a supervisor over the harness descriptors and waits until it
// has applied the configuration it started with, the point from which
// Status(that configuration) reads "applied".
func launched(t *testing.T, s *serveFeatureState) {
	t.Helper()
	if err := s.launch(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.stopSupervisor)
	if err := waitFor(func() bool {
		s.sup.mu.Lock()
		defer s.sup.mu.Unlock()
		return s.sup.applied == s.cfg
	}); err != nil {
		t.Fatalf("the supervisor never applied its starting configuration: %v", err)
	}
}

func statusRow(t *testing.T, rows []status.Subsystem, kind Kind) status.Subsystem {
	t.Helper()
	for _, row := range rows {
		if row.Kind == string(kind) {
			return row
		}
	}
	t.Fatalf("no row for %s in %+v", kind, rows)
	return status.Subsystem{}
}

// A listener's new address is pending as soon as the configuration says so:
// a read right after a save sees it before the supervisor has been handed the
// configuration, and moving the address back clears it. Whether anyone has to
// restart the process depends on a dispatcher being behind it.
func TestSupervisorStatusPendingBeforeApply(t *testing.T) {
	for _, tc := range []struct {
		name         string
		restartable  bool
		wantRequired bool
	}{
		{"a foreground process waits for a manual restart", false, true},
		{"a process under a dispatcher restarts by itself", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &serveFeatureState{}
			s.reset()
			s.restartable = tc.restartable
			launched(t, s)

			moved := *s.cfg
			moved.HTTPServer.Port = 23456
			rows, required, refused := s.sup.Status(&moved)
			row := statusRow(t, rows, KindHTTP)
			if row.State != status.StateRunning || row.Address != "12345" || row.PendingAddress != "23456" {
				t.Errorf("http row = %+v, want running on 12345 with 23456 pending", row)
			}
			if required != tc.wantRequired || refused != "" {
				t.Errorf("restartRequired = %v, refused = %q; want %v and none", required, refused, tc.wantRequired)
			}

			rows, required, _ = s.sup.Status(s.cfg)
			if row := statusRow(t, rows, KindHTTP); row.PendingAddress != "" || required {
				t.Errorf("moving the address back left %+v, restartRequired = %v", row, required)
			}
		})
	}
}

// A dispatcher restarts the process only into a configuration its pre-flight
// accepts. When it would not, the change waits for a manual restart that would
// fail the same way, and the read says why.
func TestSupervisorStatusRestartRefused(t *testing.T) {
	s := &serveFeatureState{}
	s.reset()
	s.subs = s.describe(false) // no gateway in this build
	s.restartable = true
	launched(t, s)

	unrunnable := *s.cfg
	unrunnable.Gateways.Telegram.Enabled = true
	unrunnable.HTTPServer.Port = 23456
	_, required, refused := s.sup.Status(&unrunnable)
	if !required || !strings.Contains(refused, "gateways.telegram.enable") || !strings.Contains(refused, "-tags gateway") {
		t.Errorf("restartRequired = %v, refused = %q; want the pre-flight's refusal naming the key and the tag", required, refused)
	}

	// With no address to move there is nothing to restart for, runnable or not.
	unrunnable.HTTPServer.Port = 0
	if _, required, refused := s.sup.Status(&unrunnable); required || refused != "" {
		t.Errorf("without a pending address: restartRequired = %v, refused = %q", required, refused)
	}

	// Without a dispatcher the change just waits, whatever the pre-flight says.
	s.sup.Restartable = false
	unrunnable.HTTPServer.Port = 23456
	if _, required, refused := s.sup.Status(&unrunnable); !required || refused != "" {
		t.Errorf("foreground: restartRequired = %v, refused = %q; want a manual restart and no refusal", required, refused)
	}
}

// Before the supervisor has applied a configuration every surface it enables
// is starting, never stopped; once it has, an enabled one that is not running
// is stopped.
func TestSupervisorStatusStartingBeforeLaunch(t *testing.T) {
	s := &serveFeatureState{}
	s.reset()
	s.cfg.Gateways.Telegram.Enabled = true
	idle := NewSupervisor(nil, s.subs)
	rows, required, _ := idle.Status(s.cfg)
	if len(rows) != 2 || required {
		t.Fatalf("an unstarted supervisor reads %+v, restartRequired = %v; want the two enabled surfaces", rows, required)
	}
	for i, kind := range []Kind{KindHTTP, KindGateway} {
		if rows[i].Kind != string(kind) || rows[i].State != status.StateStarting || rows[i].Address != "" {
			t.Errorf("row %d = %+v, want %s starting with no address, in declaration order", i, rows[i], kind)
		}
	}

	s.cfg.Gateways.Telegram.Enabled = false
	launched(t, s)
	wider := *s.cfg
	wider.Scheduler.Enabled = true
	rows, _, _ = s.sup.Status(&wider)
	if got := statusRow(t, rows, KindScheduler).State; got != status.StateStarting {
		t.Errorf("a surface the configuration enabled, before the supervisor saw it: %q, want starting", got)
	}
	s.reloads <- &wider
	if err := s.settle(&wider); err != nil {
		t.Fatal(err)
	}
	rows, _, _ = s.sup.Status(&wider)
	if got := statusRow(t, rows, KindScheduler).State; got != status.StateRunning {
		t.Errorf("after the supervisor applied it: %q, want running", got)
	}
}

// An enabled surface the supervisor applied and did not start (this binary
// has no support for it) is stopped, and it has no address.
func TestSupervisorStatusStoppedWhenAppliedButNotRunning(t *testing.T) {
	s := &serveFeatureState{}
	s.reset()
	s.subs = s.describe(false)
	launched(t, s)

	next := *s.cfg
	next.Gateways.Telegram.Enabled = true
	s.reloads <- &next
	if err := s.settle(&next); err != nil {
		t.Fatal(err)
	}
	rows, required, refused := s.sup.Status(&next)
	if row := statusRow(t, rows, KindGateway); row.State != status.StateStopped || row.Address != "" {
		t.Errorf("gateway row = %+v, want stopped with no address", row)
	}
	if required || refused != "" {
		t.Errorf("restartRequired = %v, refused = %q; want neither", required, refused)
	}
}

// A listener the configuration turns off is retained until the supervisor
// stops it, and it may simply stop: a new address for it is no reason to
// restart anything.
func TestSupervisorStatusDisabledListenerHasNoPending(t *testing.T) {
	s := &serveFeatureState{}
	s.reset()
	s.restartable = false
	launched(t, s)

	off := false
	disabled := *s.cfg
	disabled.HTTPServer.Enabled = &off
	disabled.HTTPServer.Port = 23456
	rows, required, refused := s.sup.Status(&disabled)
	row := statusRow(t, rows, KindHTTP)
	if row.State != status.StateRetained || row.Address != "12345" || row.PendingAddress != "" {
		t.Errorf("http row = %+v, want retained on 12345 with nothing pending", row)
	}
	if required || refused != "" {
		t.Errorf("restartRequired = %v, refused = %q; want neither", required, refused)
	}
}
