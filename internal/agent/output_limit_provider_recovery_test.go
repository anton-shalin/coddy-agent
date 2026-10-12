package agent

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

var errConnectionReset = errors.New("read: connection reset by peer")

// recoveringAgent is outputLimitAgent with the pause before a provider recovery
// cut to a few milliseconds.
func recoveringAgent(t *testing.T, p llm.Provider) (*Agent, *session.State) {
	t.Helper()
	return outputLimitAgent(t, p, func(c *config.Config) { c.Agent.LLMRetryBaseMS = 1 })
}

// TestPromptCacheProviderRecoveryKeepsThePendingRecoveries: a provider recovery
// runs the step again as the request that failed (issue #520). The transcript
// still holds the steps that came back empty and none of the local-only nudges,
// so a history rebuilt from it would differ; the request run again must be the
// one that failed - the empty steps left out, the nudges the recovery has earned
// at the end - not a history that ends on the cut-off step.
func TestPromptCacheProviderRecoveryKeepsThePendingRecoveries(t *testing.T) {
	signedEmpty := outputLimitStep{reasoning: "Hmm.", signature: "sig", stop: "end_turn"}
	unsignedEmpty := outputLimitStep{reasoning: "Hmm.", stop: "end_turn"}
	failing := outputLimitStep{err: errConnectionReset}
	for _, tc := range []struct {
		name   string
		steps  []outputLimitStep
		nudges map[string]int // text -> how many times the failed request carried it
	}{
		{
			// Unsigned thinking is what Anthropic cannot replay as the final turn.
			name:   "output limit",
			steps:  []outputLimitStep{cutOff, failing, answered},
			nudges: map[string]int{outputLimitNudge: 1},
		},
		{
			name:   "output limit with signed thinking",
			steps:  []outputLimitStep{{reasoning: "Thinking.", signature: "sig", stop: "max_tokens"}, failing, answered},
			nudges: map[string]int{outputLimitNudge: 1},
		},
		{
			name:   "output limit twice",
			steps:  []outputLimitStep{cutOff, cutOff, failing, answered},
			nudges: map[string]int{outputLimitNudge: 2},
		},
		{
			name:   "empty re-issue",
			steps:  []outputLimitStep{signedEmpty, failing, answered},
			nudges: map[string]int{},
		},
		{
			name:   "empty nudge",
			steps:  []outputLimitStep{signedEmpty, signedEmpty, failing, answered},
			nudges: map[string]int{emptyAssistantContinuationNudge: 1},
		},
		{
			name:   "output limit then empty nudge",
			steps:  []outputLimitStep{cutOff, unsignedEmpty, unsignedEmpty, failing, answered},
			nudges: map[string]int{outputLimitNudge: 1, emptyAssistantContinuationNudge: 1},
		},
		{
			// The text the user read stays in the rebuilt history; the message that
			// asks the model to go on follows it.
			name:   "output limit in the middle of the text",
			steps:  []outputLimitStep{partialStep, failing, answered},
			nudges: map[string]int{outputLimitContinueNudge: 1},
		},
		{
			name:   "output limit in the middle of the text then while thinking",
			steps:  []outputLimitStep{partialStep, cutOff, failing, answered},
			nudges: map[string]int{outputLimitContinueNudge: 1, outputLimitNudge: 1},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &outputLimitProvider{steps: tc.steps}
			ag, st := recoveringAgent(t, p)
			stop, err := runOutputLimitTurn(ag)
			if err != nil || stop != string(acp.StopReasonEndTurn) || p.calls != len(tc.steps) {
				t.Fatalf("stop = %q, err = %v after %d calls; want end_turn after %d", stop, err, p.calls, len(tc.steps))
			}
			failed, rerun := p.seen[len(p.seen)-2], p.seen[len(p.seen)-1]
			if got, want := strings.Join(requestHistory(rerun), "\n"), strings.Join(requestHistory(failed), "\n"); got != want {
				t.Fatalf("the request run again is not the one that failed:\nfailed: %s\nagain:  %s", want, got)
			}
			if hasEmptyAssistant(rerun) {
				t.Fatalf("the run-again request ends on a step that came back empty: %+v", rerun)
			}
			for text, want := range tc.nudges {
				if got := countNudges(rerun, text); got != want {
					t.Fatalf("the run-again request carries %q %d times, want %d", text, got, want)
				}
			}
			if notice := st.TakeTurnStopNotice(); notice != "" {
				t.Fatalf("a recovered turn left a stop notice: %q", notice)
			}
		})
	}
}

// TestProviderRecoveryKeepsThePendingNudgesBeforeTheKeptAnswer: when the failed
// call had already streamed text, the transcript holds it and the rebuilt
// history ends with it; the nudges the step earned stay where they were sent,
// before the answer they led to, and the continue message follows.
func TestProviderRecoveryKeepsThePendingNudgesBeforeTheKeptAnswer(t *testing.T) {
	p := &outputLimitProvider{steps: []outputLimitStep{cutOff, {text: "The module starts with", err: errConnectionReset}, answered}}
	ag, _ := recoveringAgent(t, p)
	stop, err := runOutputLimitTurn(ag)
	if err != nil || stop != string(acp.StopReasonEndTurn) || p.calls != 3 {
		t.Fatalf("stop = %q, err = %v after %d calls; want end_turn after 3", stop, err, p.calls)
	}
	hist := requestHistory(p.seen[2])
	nudgeAt, keptAt, continueAt := -1, -1, -1
	for i, line := range hist {
		switch {
		case strings.HasSuffix(line, outputLimitNudge):
			nudgeAt = i
		case strings.HasSuffix(line, "The module starts with"):
			keptAt = i
		case strings.HasSuffix(line, providerRecoveryNudge):
			continueAt = i
		}
	}
	if nudgeAt < 0 || keptAt != nudgeAt+1 || continueAt != keptAt+1 || continueAt != len(hist)-1 {
		t.Fatalf("want the nudge, the kept answer and the continue message in that order at the end, got %q", hist)
	}
	if hasEmptyAssistant(p.seen[2]) {
		t.Fatalf("the cut-off step is in the rebuilt history: %+v", p.seen[2])
	}
}

// TestProviderRecoveryKeepsTheContinuationBeforeTheKeptAnswer: the same for a
// text the output limit cut off. The transcript holds that text and the answer
// the failed call had begun; the rebuilt history has the text, the message that
// asks the model to go on, the answer kept after it, and the provider's own
// continue message last.
func TestProviderRecoveryKeepsTheContinuationBeforeTheKeptAnswer(t *testing.T) {
	p := &outputLimitProvider{steps: []outputLimitStep{partialStep, {text: " the second half starts with", err: errConnectionReset}, answered}}
	ag, _ := recoveringAgent(t, p)
	stop, err := runOutputLimitTurn(ag)
	if err != nil || stop != string(acp.StopReasonEndTurn) || p.calls != 3 {
		t.Fatalf("stop = %q, err = %v after %d calls; want end_turn after 3", stop, err, p.calls)
	}
	hist := requestHistory(p.seen[2])
	n := len(hist)
	// The kept answer goes on from the text, so it is stored as it streamed, with
	// the space it starts with.
	if n < 4 || hist[n-4] != "assistant: "+partialText || hist[n-3] != "user: "+outputLimitContinueNudge ||
		hist[n-2] != "assistant:  the second half starts with" || hist[n-1] != "user: "+providerRecoveryNudge {
		t.Fatalf("want the text, the continuation message, the kept answer and the provider's message in that order at the end, got %q", hist[max(n-5, 0):])
	}
}

// TestProviderRecoveryDuringAContinuationKeepsTheMessagesInOrder: a text the
// output limit cut off is answered with the message that asks for the rest, and
// the call that was to write the rest fails after streaming the start of it. The
// request run again carries the cut text, that message, the kept start of the rest
// and the provider's own message, in that order and as four messages: the kept
// answer is not folded into the text it goes on from within the turn, and the
// message that asked for it does not move in front of the text. The kept start is
// a part of the continued answer and is stored truncated, so the next turn's
// history holds one assistant message: the cut text, the kept start and what the
// step run again wrote, joined with nothing between, as they streamed.
func TestProviderRecoveryDuringAContinuationKeepsTheMessagesInOrder(t *testing.T) {
	const (
		cutText = "Here is the first half of the module and then " // the whitespace at the end is part of the text
		kept    = " abc"                                           // and so is the whitespace the continuation starts with
	)
	p := &outputLimitProvider{steps: []outputLimitStep{
		{text: cutText, stop: "max_tokens"},
		{text: kept, err: errConnectionReset},
		answered,
		{text: "ok", stop: "end_turn"},
	}}
	ag, st := recoveringAgent(t, p)
	stop, err := runOutputLimitTurn(ag)
	if err != nil || stop != string(acp.StopReasonEndTurn) || p.calls != 3 {
		t.Fatalf("stop = %q, err = %v after %d calls; want end_turn after 3", stop, err, p.calls)
	}

	rerun := p.seen[2]
	tail := []string{"assistant: " + cutText, "user: " + outputLimitContinueNudge, "assistant: " + kept, "user: " + providerRecoveryNudge}
	hist := requestHistory(rerun)
	if n := len(tail); len(hist) < n || strings.Join(hist[len(hist)-n:], "\n") != strings.Join(tail, "\n") {
		t.Fatalf("the request run again does not end with the cut text, the message that asked for the rest, the kept start of it and the provider's message:\n%s", tailLines(strings.Join(hist, "\n"), len(tail)+2))
	}
	if got := countNudges(rerun, outputLimitContinueNudge); got != 1 {
		t.Fatalf("the request run again carries the message that asks for the rest %d times, want 1", got)
	}

	stored := assistantMessages(st.GetMessages())
	if len(stored) != 3 || !stored[0].Truncated || stored[0].Content != cutText ||
		!stored[1].Truncated || stored[1].Content != kept || stored[2].Truncated || stored[2].Content != "Done." {
		t.Fatalf("the transcript stores %+v, want the cut text and the kept start (both truncated, as they streamed) and the answer", stored)
	}

	if _, err := ag.Run(context.Background(), []acp.ContentBlock{{Type: "text", Text: "next"}}); err != nil {
		t.Fatal(err)
	}
	next := p.seen[len(p.seen)-1]
	requireNoAssistantInARow(t, next)
	if got, want := assistantMessages(next), cutText+kept+"Done."; len(got) != 1 || got[0].Content != want {
		t.Fatalf("the next turn replays %+v, want one message holding %q", got, want)
	}
}

// TestKeptAnswerOf: the answer a provider failure kept goes after the messages
// that stood at the end of the request that failed, as a request carries an
// assistant message: the markers of shared files taken out, the whitespace it
// starts with kept only where it goes on from a text the output limit cut off.
func TestKeptAnswerOf(t *testing.T) {
	user := llm.Message{Role: llm.RoleUser, Content: "go"}
	cut := llm.Message{Role: llm.RoleAssistant, Content: "Press the butt", Truncated: true}
	empty := llm.Message{Role: llm.RoleAssistant}
	reply := func(s string) llm.Message { return llm.Message{Role: llm.RoleAssistant, Content: s} }
	for _, tc := range []struct {
		name       string
		stored     []llm.Message
		wantAnswer string
	}{
		{"markers are taken out", []llm.Message{user, reply("Here it is.\n\n<coddy_file id=\"a\"/>")}, "Here it is."},
		{"whitespace at the start goes after a message that was not cut off", []llm.Message{user, reply("  Here it is. ")}, "Here it is."},
		{"whitespace at the start stays after a text the output limit cut off", []llm.Message{user, cut, reply(" on labelled Save. ")}, " on labelled Save."},
		{"an empty step between does not make it a new answer", []llm.Message{user, cut, empty, reply(" on")}, " on"},
		{"a user message between ends the join", []llm.Message{cut, user, reply(" on")}, "on"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			answer := keptAnswerOf(tc.stored)
			if answer.Role != llm.RoleAssistant || answer.Content != tc.wantAnswer {
				t.Fatalf("answer = %+v, want an assistant message holding %q", answer, tc.wantAnswer)
			}
		})
	}
}

// signedEmptyStep is an answer with signed thinking and nothing else, the kind
// of empty step that stays in the transcript and in a history rebuilt from it.
var signedEmptyStep = outputLimitStep{reasoning: "Hmm.", signature: "sig", stop: "end_turn"}

// unsignedEmptyStep is the same with unsigned thinking.
var unsignedEmptyStep = outputLimitStep{reasoning: "Hmm.", stop: "end_turn"}

// countEmptyAssistants is how many assistant messages of a request say nothing:
// no text and no tool call.
func countEmptyAssistants(msgs []llm.Message) int {
	var n int
	for _, m := range msgs {
		if m.Role == llm.RoleAssistant && strings.TrimSpace(m.Content) == "" && len(m.ToolCalls) == 0 {
			n++
		}
	}
	return n
}

// TestProviderRecoveryRunsTheFailedRequestAgain: after a provider failure the
// step runs again as the request that failed, byte for byte (issue #520), not a
// history rebuilt from the transcript. Whatever the recoveries before the
// failure did to the working history - texts the output limit cut off, the
// messages that asked for a shorter step or for the rest, empty steps left out,
// signed or not - the request run again carries exactly what the failed one
// carried, in the same order.
func TestProviderRecoveryRunsTheFailedRequestAgain(t *testing.T) {
	failing := outputLimitStep{err: errConnectionReset}
	for _, tc := range []struct {
		name  string
		steps []outputLimitStep
	}{
		{"a text cut off twice", []outputLimitStep{partialStep, partialStep, failing, answered}},
		{"thinking, then a text", []outputLimitStep{cutOff, partialStep, failing, answered}},
		{"a text, then thinking", []outputLimitStep{partialStep, cutOff, failing, answered}},
		{"two empty answers, then a text", []outputLimitStep{signedEmptyStep, signedEmptyStep, partialStep, failing, answered}},
		{"two unsigned empty answers, then a text", []outputLimitStep{unsignedEmptyStep, unsignedEmptyStep, partialStep, failing, answered}},
		{"an empty answer, then a text, then thinking", []outputLimitStep{signedEmptyStep, partialStep, cutOff, failing, answered}},
		{"two empty answers, then thinking cut off", []outputLimitStep{signedEmptyStep, signedEmptyStep, cutOff, failing, answered}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &outputLimitProvider{steps: tc.steps}
			ag, _ := recoveringAgent(t, p)
			stop, err := runOutputLimitTurn(ag)
			if err != nil || stop != string(acp.StopReasonEndTurn) || p.calls != len(tc.steps) {
				t.Fatalf("stop = %q, err = %v after %d calls; want end_turn after %d", stop, err, p.calls, len(tc.steps))
			}
			failed, rerun := p.seen[len(p.seen)-2], p.seen[len(p.seen)-1]
			if !reflect.DeepEqual(withoutTurnContext(failed), withoutTurnContext(rerun)) {
				t.Fatalf("the request run again is not the one that failed:\nfailed: %s\nagain:  %s",
					strings.Join(requestHistory(failed), "\n"), strings.Join(requestHistory(rerun), "\n"))
			}
		})
	}
}

// tailLines is the last n lines of a history joined with newlines, for a
// failure message that has to stay readable under a system prompt.
func tailLines(joined string, n int) string {
	lines := strings.Split(joined, "\n")
	return strings.Join(lines[max(len(lines)-n, 0):], "\n")
}

// TestCompactionKeepsOnlyTheNudgesThatStillStandAtTheEnd: a compaction rebuilds
// the history from the transcript, and what it puts back after the history is
// what stood at the end of the working history: the messages sent since the
// last step that left text or a tool call there. A message sent before such a
// step was answered by it, and would come after text it is not about.
func TestCompactionKeepsOnlyTheNudgesThatStillStandAtTheEnd(t *testing.T) {
	text, thinking, goOn := "assistant: "+partialText, "user: "+outputLimitNudge, "user: "+outputLimitContinueNudge
	wording := "user: " + emptyAssistantContinuationNudge
	for _, tc := range []struct {
		name  string
		steps []outputLimitStep // the last of them is the one after which the history is compacted
		// tail is the end of the history of the request after the compaction.
		tail []string
		// absent is a message that request must not carry at all.
		absent string
	}{
		{"a text cut off", []outputLimitStep{partialStep}, []string{text, goOn}, ""},
		{"thinking, then a text", []outputLimitStep{cutOff, partialStep}, []string{text, goOn}, thinking},
		{"a text, then thinking", []outputLimitStep{partialStep, cutOff}, []string{text, goOn, thinking}, ""},
		{"thinking cut off twice", []outputLimitStep{cutOff, cutOff}, []string{thinking, thinking}, ""},
		{"two empty answers, then a text", []outputLimitStep{signedEmptyStep, signedEmptyStep, partialStep}, []string{text, goOn}, wording},
		{"two empty answers, then thinking cut off", []outputLimitStep{signedEmptyStep, signedEmptyStep, cutOff}, []string{wording, thinking}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			enabled, keep := false, 1
			steps := append([]outputLimitStep(nil), tc.steps...)
			last := &steps[len(steps)-1]
			last.during = func() { enabled = true }
			steps = append(steps, answered)
			p := &outputLimitProvider{steps: steps}
			ag, st := outputLimitAgent(t, p, func(c *config.Config) {
				c.Compaction = config.Compaction{Enabled: &enabled, ThresholdPercent: 1, KeepRecentTurns: &keep}
				c.Models[0].MaxContextTokens = 10000
			})
			st.AddMessage(llm.Message{Role: llm.RoleUser, Content: "Earlier question."})
			st.AddMessage(llm.Message{Role: llm.RoleAssistant, Content: "Earlier answer."})

			stop, err := runOutputLimitTurn(ag)
			if err != nil || stop != string(acp.StopReasonEndTurn) || p.calls != len(steps) {
				t.Fatalf("stop = %q, err = %v after %d calls; want end_turn after %d", stop, err, p.calls, len(steps))
			}
			after := p.seen[len(p.seen)-1]
			var summarized bool
			for _, m := range after {
				if strings.Contains(m.Content, "Earlier turn summary.") {
					summarized = true
				}
			}
			if !summarized {
				t.Fatal("the history was not compacted before the last request; the test proves nothing")
			}
			hist := requestHistory(after)
			n := len(tc.tail)
			if len(hist) < n || strings.Join(hist[len(hist)-n:], "\n") != strings.Join(tc.tail, "\n") {
				t.Fatalf("the request after the compaction does not end with %q:\n%s", tc.tail, tailLines(strings.Join(hist, "\n"), n+3))
			}
			for _, line := range hist {
				if tc.absent != "" && line == tc.absent {
					t.Fatalf("the request after the compaction carries %q, which a later step had answered", tc.absent)
				}
			}
		})
	}
}
