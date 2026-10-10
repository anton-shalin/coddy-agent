package agent

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// A step the output limit stopped in the middle of visible text is not the end
// of the turn either: the text stays, because the user read it, and the model
// is asked to go on from its last word, inside the bound the empty step has
// (outputLimitRecoveries in react.go). The happy path is
// features/turn_end_reasons.feature; these are its edges.

const partialText = "Here is the first half of the module and then"

var (
	// partialStep is a step that wrote visible text and ran out of output
	// budget in the middle of it, after some thinking.
	partialStep = outputLimitStep{reasoning: "Plan.", text: partialText, stop: "max_tokens"}
	// restOfText finishes what partialStep began.
	restOfText = outputLimitStep{text: " the second half.", stop: "end_turn"}
)

// outputLimitSender is a client that records what a turn sent it: the session
// updates and the permission requests.
type outputLimitSender struct {
	mu      sync.Mutex
	updates []interface{}
	asked   []acp.PermissionRequestParams
}

func (s *outputLimitSender) SendSessionUpdate(_ string, u interface{}) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.updates = append(s.updates, u)
	return nil
}

func (s *outputLimitSender) RequestPermission(_ context.Context, p acp.PermissionRequestParams) (*acp.PermissionResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.asked = append(s.asked, p)
	return &acp.PermissionResult{Outcome: "allow", OptionID: "allow"}, nil
}

func (*outputLimitSender) RequestQuestion(context.Context, acp.QuestionRequestParams) (*acp.QuestionResult, error) {
	return &acp.QuestionResult{}, nil
}

// permissionRequests is how many times the client was asked to approve a call.
func (s *outputLimitSender) permissionRequests() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.asked)
}

// toolStatuses lists, in order, the statuses the client was told for a tool call.
func (s *outputLimitSender) toolStatuses(id string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for _, u := range s.updates {
		if st, ok := u.(acp.ToolCallStatusUpdate); ok && st.ToolCallID == id {
			out = append(out, st.Status)
		}
	}
	return out
}

// assistantTexts is the text of every assistant message of a transcript, in order.
func assistantTexts(msgs []llm.Message) []string {
	var out []string
	for _, m := range msgs {
		if m.Role == llm.RoleAssistant && strings.TrimSpace(m.Content) != "" {
			out = append(out, m.Content)
		}
	}
	return out
}

// TestPromptCacheOutputLimitContinuationIsLocalAndAppendOnly: the message that
// asks the model to go on reaches the model only. The request after the cut-off
// step is the one before it with the step's text and that message added at the
// end, nothing in between rewritten; the transcript keeps the text and the
// continuation as the two assistant messages they are, and never the message.
func TestPromptCacheOutputLimitContinuationIsLocalAndAppendOnly(t *testing.T) {
	p := &outputLimitProvider{steps: []outputLimitStep{partialStep, restOfText}}
	ag, st := outputLimitAgent(t, p, nil)

	stop, err := runOutputLimitTurn(ag)
	if err != nil || stop != string(acp.StopReasonEndTurn) || p.calls != 2 {
		t.Fatalf("stop = %q, err = %v after %d calls; want end_turn after 2", stop, err, p.calls)
	}
	first, second := requestHistory(p.seen[0]), requestHistory(p.seen[1])
	if len(second) != len(first)+2 {
		t.Fatalf("history went from %d to %d messages, want two more (the text and the message):\n%s", len(first), len(second), strings.Join(second, "\n"))
	}
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("message %d of the cached history changed:\nbefore: %s\nafter:  %s", i, first[i], second[i])
		}
	}
	if got, want := second[len(second)-2], "assistant: "+partialText; got != want {
		t.Fatalf("the text the user read is not replayed as it was: %q, want %q", got, want)
	}
	if got, want := second[len(second)-1], "user: "+outputLimitContinueNudge; got != want {
		t.Fatalf("the last message is %q, want the message that asks the model to continue", got)
	}

	stored := st.GetMessages()
	for _, m := range stored {
		if m.Content == outputLimitContinueNudge {
			t.Fatal("the continuation message was written to the transcript")
		}
	}
	// The text and its continuation are stored as they streamed at the join: the
	// continuation keeps the space it starts with.
	if got := assistantTexts(stored); len(got) != 2 || got[0] != partialText || got[1] != restOfText.text {
		t.Fatalf("assistant messages of the transcript = %q, want the text and its continuation", got)
	}
	if notice := st.TakeTurnStopNotice(); notice != "" {
		t.Fatalf("a continued turn left a stop notice: %q", notice)
	}
}

// TestOutputLimitContinuationIsBoundedAndTheNoticeSaysSo: a model that is cut
// off in the middle of its text again and again is asked to go on
// maxOutputLimitRecoveries times, and the turn then ends with the notice that
// says the model was already asked.
func TestOutputLimitContinuationIsBoundedAndTheNoticeSaysSo(t *testing.T) {
	p := &outputLimitProvider{steps: []outputLimitStep{partialStep}}
	ag, st := outputLimitAgent(t, p, nil)

	stop, err := runOutputLimitTurn(ag)
	if err != nil || stop != string(acp.StopReasonMaxTokens) {
		t.Fatalf("stop = %q, err = %v; want max_tokens", stop, err)
	}
	if want := 1 + maxOutputLimitRecoveries; p.calls != want {
		t.Fatalf("provider called %d times, want %d (the step and its continuations)", p.calls, want)
	}
	last := p.seen[len(p.seen)-1]
	if got := countNudges(last, outputLimitContinueNudge); got != maxOutputLimitRecoveries {
		t.Fatalf("the last request carries %d continuation messages, want %d", got, maxOutputLimitRecoveries)
	}
	if got := len(assistantTexts(last)); got != maxOutputLimitRecoveries {
		t.Fatalf("the last request replays %d texts, want %d (every text but the one it asks for)", got, maxOutputLimitRecoveries)
	}
	if got := len(assistantTexts(st.GetMessages())); got != 1+maxOutputLimitRecoveries {
		t.Fatalf("the transcript holds %d texts, want %d", got, 1+maxOutputLimitRecoveries)
	}
	notice := st.TakeTurnStopNotice()
	for _, want := range []string{"max_tokens", "2 times", "keep its steps short", "Send a message"} {
		if !strings.Contains(notice, want) {
			t.Fatalf("notice %q does not say %q", notice, want)
		}
	}
}

// TestOutputLimitContinuationHonoursLLMRetryMax follows issue #226: the
// continuation takes a slot of agent.llm_retry_max like every recovery, so 0
// turns it off and 1 allows one.
func TestOutputLimitContinuationHonoursLLMRetryMax(t *testing.T) {
	for _, tc := range []struct {
		name       string
		retryMax   int
		wantCalls  int
		wantNotice string
	}{
		{"off", 0, 1, plainOutputLimitNotice},
		{"one slot", 1, 2, "after the agent had already asked the model once"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &outputLimitProvider{steps: []outputLimitStep{partialStep}}
			ag, st := outputLimitAgent(t, p, func(c *config.Config) { c.Agent.LLMRetryMax = &tc.retryMax })
			stop, err := runOutputLimitTurn(ag)
			if err != nil || stop != string(acp.StopReasonMaxTokens) {
				t.Fatalf("stop = %q, err = %v; want max_tokens", stop, err)
			}
			if p.calls != tc.wantCalls {
				t.Fatalf("provider called %d times, want %d", p.calls, tc.wantCalls)
			}
			if notice := st.TakeTurnStopNotice(); !strings.Contains(notice, tc.wantNotice) {
				t.Fatalf("notice = %q, want it to contain %q", notice, tc.wantNotice)
			}
		})
	}
}

// TestOutputLimitContinuationAtTheStepCapEndsOnTheOutputLimit: the message needs
// an iteration to be read in. On the last allowed step the turn ends on the
// output limit, with that notice, rather than on the step limit.
func TestOutputLimitContinuationAtTheStepCapEndsOnTheOutputLimit(t *testing.T) {
	p := &outputLimitProvider{steps: []outputLimitStep{partialStep, restOfText}}
	ag, st := outputLimitAgent(t, p, func(c *config.Config) { c.Agent.MaxTurns = 1 })
	stop, err := runOutputLimitTurn(ag)
	if err != nil || stop != string(acp.StopReasonMaxTokens) || p.calls != 1 {
		t.Fatalf("stop = %q, err = %v after %d calls; want max_tokens after 1", stop, err, p.calls)
	}
	if notice := st.TakeTurnStopNotice(); notice != plainOutputLimitNotice {
		t.Fatalf("notice = %q, want the plain output-limit notice", notice)
	}
}

// TestOutputLimitContinuationLeavesAStoppedTurnAlone: a Stop pressed during the
// cut-off step ends the turn as a stop, not as the output limit; the model is
// not asked to go on behind the user's back.
func TestOutputLimitContinuationLeavesAStoppedTurnAlone(t *testing.T) {
	var st *session.State
	stopped := partialStep
	stopped.during = func() { st.SetUserCancelledTurn() }
	p := &outputLimitProvider{steps: []outputLimitStep{stopped, restOfText}}
	var ag *Agent
	ag, st = outputLimitAgent(t, p, nil)
	stop, _ := runOutputLimitTurn(ag)
	if p.calls != 1 {
		t.Fatalf("provider called %d times after a Stop, want 1", p.calls)
	}
	if stop != string(acp.StopReasonCancelled) {
		t.Fatalf("stop = %q after a Stop, want cancelled", stop)
	}
	if notice := st.TakeTurnStopNotice(); notice != "" {
		t.Fatalf("a stopped turn left a stop notice: %q", notice)
	}
}

// TestOutputLimitContinuationSurvivesCompaction: a compaction between the
// cut-off step and the request that asks the model to go on rebuilds the history
// from the transcript, which holds the text and not the message; the rebuilt
// request ends with the text and the message once.
func TestOutputLimitContinuationSurvivesCompaction(t *testing.T) {
	enabled, keep := false, 1
	cut := partialStep
	cut.during = func() { enabled = true }
	p := &outputLimitProvider{steps: []outputLimitStep{cut, restOfText}}
	ag, st := outputLimitAgent(t, p, func(c *config.Config) {
		c.Compaction = config.Compaction{Enabled: &enabled, ThresholdPercent: 1, KeepRecentTurns: &keep}
		c.Models[0].MaxContextTokens = 10000
	})
	st.AddMessage(llm.Message{Role: llm.RoleUser, Content: "Earlier question."})
	st.AddMessage(llm.Message{Role: llm.RoleAssistant, Content: "Earlier answer."})

	stop, err := runOutputLimitTurn(ag)
	if err != nil || stop != string(acp.StopReasonEndTurn) || p.calls != 2 {
		t.Fatalf("stop = %q, err = %v after %d calls; want end_turn after 2", stop, err, p.calls)
	}
	hist := requestHistory(p.seen[1])
	if got := countNudges(p.seen[1], outputLimitContinueNudge); got != 1 {
		t.Fatalf("the request after the compaction carries the message %d times, want 1:\n%s", got, strings.Join(hist, "\n"))
	}
	if n := len(hist); n < 2 || hist[n-2] != "assistant: "+partialText || hist[n-1] != "user: "+outputLimitContinueNudge {
		t.Fatalf("the rebuilt request does not end with the text and the message:\n%s", strings.Join(hist, "\n"))
	}
	var summarized bool
	for _, m := range p.seen[1] {
		if strings.Contains(m.Content, "Earlier turn summary.") {
			summarized = true
		}
	}
	if !summarized {
		t.Fatal("the history was not compacted between the two requests; the test proves nothing")
	}
}

// TestOutputLimitContinuationInASubagent: a child has nobody to type "continue",
// so the continuation is what keeps its run going.
func TestOutputLimitContinuationInASubagent(t *testing.T) {
	p := &outputLimitProvider{steps: []outputLimitStep{partialStep, restOfText}}
	ag, st := outputLimitAgent(t, p, nil)
	ag.subagent = &session.SubagentMeta{Name: "general"}
	stop, err := runOutputLimitTurn(ag)
	if err != nil || stop != string(acp.StopReasonEndTurn) || p.calls != 2 {
		t.Fatalf("subagent: stop = %q, err = %v after %d calls; want end_turn after 2", stop, err, p.calls)
	}
	if notice := st.TakeTurnStopNotice(); notice != "" {
		t.Fatalf("a continued subagent left a notice: %q", notice)
	}
}

// TestOutputLimitContinuationThenCutOffStepShareTheBound: a text cut off, then
// a step cut off while it only thought, are two recoveries of one chain: the
// request after them carries both messages, in the order they were earned, and
// the third cut-off ends the turn.
func TestOutputLimitContinuationThenCutOffStepShareTheBound(t *testing.T) {
	p := &outputLimitProvider{steps: []outputLimitStep{partialStep, cutOff, cutOff}}
	ag, st := outputLimitAgent(t, p, nil)
	stop, err := runOutputLimitTurn(ag)
	if err != nil || stop != string(acp.StopReasonMaxTokens) || p.calls != 3 {
		t.Fatalf("stop = %q, err = %v after %d calls; want max_tokens after 3", stop, err, p.calls)
	}
	hist := requestHistory(p.seen[2])
	n := len(hist)
	if n < 4 || hist[n-3] != "assistant: "+partialText || hist[n-2] != "user: "+outputLimitContinueNudge || hist[n-1] != "user: "+outputLimitNudge {
		t.Fatalf("the last request does not carry the two messages in order after the text:\n%s", strings.Join(hist, "\n"))
	}
	if hasEmptyAssistant(p.seen[2]) {
		t.Fatal("the step that only thought was replayed")
	}
	if notice := st.TakeTurnStopNotice(); !strings.Contains(notice, "2 times") {
		t.Fatalf("notice = %q, want it to count both recoveries", notice)
	}
}
