package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

// localMessages are the messages the loop puts at the end of a request and
// never stores: the recoveries', the loop guard's and the provider recovery's.
var localMessages = map[string]bool{
	outputLimitNudge:                true,
	outputLimitContinueNudge:        true,
	emptyAssistantContinuationNudge: true,
	providerRecoveryNudge:           true,
	streamLoopNudge:                 true,
	reasoningLoopNudge:              true,
}

// standingAtTheEnd is what a request ends with after its last stored message:
// the local messages that stand there, in order.
func standingAtTheEnd(msgs []llm.Message) []string {
	hist := requestHistory(msgs)
	i := len(hist)
	for i > 0 && strings.HasPrefix(hist[i-1], "user: ") && localMessages[strings.TrimPrefix(hist[i-1], "user: ")] {
		i--
	}
	return hist[i:]
}

// lastIsAssistant reports whether a request ends on an assistant message, below
// the turn context block: what a provider reads as a prefill.
func lastIsAssistant(msgs []llm.Message) bool {
	hist := requestHistory(msgs)
	return len(hist) > 0 && strings.HasPrefix(hist[len(hist)-1], "assistant: ")
}

// TestPromptCacheRebuiltRequestEndsWithTheStandingMessagesOfTheWorkingOne: the
// same chain of recoveries is run twice, once with a compaction before the last
// request and once without. The request a compaction rebuilt from the
// transcript must end with the same local messages, in the same order, as the
// request the turn built step by step - the round-1 chain of two empty answers
// and a text the output limit cut off included, and a provider failure that
// kept part of an answer, whose request to go on used to be lost to a
// compaction right after it.
func TestPromptCacheRebuiltRequestEndsWithTheStandingMessagesOfTheWorkingOne(t *testing.T) {
	keptStart := outputLimitStep{text: "The notes hold", err: errConnectionReset}
	for _, tc := range []struct {
		name  string
		steps []outputLimitStep // the history is compacted during the last of them, before the request after it
	}{
		{"a text cut off", []outputLimitStep{partialStep}},
		{"a text cut off twice", []outputLimitStep{partialStep, partialStep}},
		{"thinking, then a text", []outputLimitStep{cutOff, partialStep}},
		{"a text, then thinking", []outputLimitStep{partialStep, cutOff}},
		{"two empty answers, then a text", []outputLimitStep{signedEmptyStep, signedEmptyStep, partialStep}},
		{"two unsigned empty answers, then a text", []outputLimitStep{unsignedEmptyStep, unsignedEmptyStep, partialStep}},
		{"two empty answers, then thinking cut off", []outputLimitStep{signedEmptyStep, signedEmptyStep, cutOff}},
		{"a provider failure that kept part of an answer", []outputLimitStep{keptStart}},
		{"a text cut off, then a provider failure in its continuation", []outputLimitStep{partialStep, keptStart}},
		{"two empty answers, a text, then a provider failure", []outputLimitStep{signedEmptyStep, signedEmptyStep, partialStep, keptStart}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			run := func(compact bool) []llm.Message {
				t.Helper()
				enabled, keep := false, 1
				steps := append([]outputLimitStep(nil), tc.steps...)
				if compact {
					last := &steps[len(steps)-1]
					last.during = func() { enabled = true }
				}
				steps = append(steps, answered)
				p := &outputLimitProvider{steps: steps}
				ag, st := outputLimitAgent(t, p, func(c *config.Config) {
					c.Agent.LLMRetryBaseMS = 1
					c.Compaction = config.Compaction{Enabled: &enabled, ThresholdPercent: 1, KeepRecentTurns: &keep}
					c.Models[0].MaxContextTokens = 10000
				})
				st.AddMessage(llm.Message{Role: llm.RoleUser, Content: "Earlier question."})
				st.AddMessage(llm.Message{Role: llm.RoleAssistant, Content: "Earlier answer."})
				stop, err := runOutputLimitTurn(ag)
				if err != nil || stop != string(acp.StopReasonEndTurn) || p.calls != len(steps) {
					t.Fatalf("compact=%v: stop = %q, err = %v after %d calls; want end_turn after %d", compact, stop, err, p.calls, len(steps))
				}
				last := p.seen[len(p.seen)-1]
				var summarized bool
				for _, m := range last {
					if strings.Contains(m.Content, "Earlier turn summary.") {
						summarized = true
					}
				}
				if summarized != compact {
					t.Fatalf("compact=%v: the last request was compacted = %v; the test proves nothing", compact, summarized)
				}
				return last
			}
			working, rebuilt := run(false), run(true)
			want, got := standingAtTheEnd(working), standingAtTheEnd(rebuilt)
			if len(want) == 0 {
				t.Fatalf("the working request ends with no local message; the chain does not exercise a recovery:\n%s", tailLines(strings.Join(requestHistory(working), "\n"), 4))
			}
			if strings.Join(got, "\n") != strings.Join(want, "\n") {
				t.Fatalf("the rebuilt request ends with %q, the working one with %q", got, want)
			}
			if lastIsAssistant(rebuilt) {
				t.Fatalf("the rebuilt request ends on an assistant message:\n%s", tailLines(strings.Join(requestHistory(rebuilt), "\n"), 3))
			}
		})
	}
}

// TestProviderRecoveryRunsAgainWithTheStandingMessagesOfTheFailedRequest: a
// provider failure rebuilds the history from the transcript (#246) and the
// request run again ends with the local messages the failed request ended with,
// in the same order, followed by the answer the failed call kept and the request
// to go on when it kept any. The round-1 chain (two empty answers, a text the
// output limit cut off, a provider failure) is among them.
func TestProviderRecoveryRunsAgainWithTheStandingMessagesOfTheFailedRequest(t *testing.T) {
	silent := outputLimitStep{err: errConnectionReset}
	keptStart := outputLimitStep{text: " in", err: errConnectionReset}
	for _, tc := range []struct {
		name  string
		steps []outputLimitStep // the last one fails
		kept  string
	}{
		{"a text cut off, then a silent failure", []outputLimitStep{partialStep, silent}, ""},
		{"a text cut off, then a failure in its continuation", []outputLimitStep{partialStep, keptStart}, " in"},
		{"two empty answers, a text, then a silent failure", []outputLimitStep{signedEmptyStep, signedEmptyStep, partialStep, silent}, ""},
		{"two empty answers, a text, then a failure in its continuation", []outputLimitStep{signedEmptyStep, signedEmptyStep, partialStep, keptStart}, " in"},
		{"two unsigned empty answers, a text, then a failure", []outputLimitStep{unsignedEmptyStep, unsignedEmptyStep, partialStep, keptStart}, " in"},
		{"two empty answers, then thinking cut off, then a failure", []outputLimitStep{signedEmptyStep, signedEmptyStep, cutOff, silent}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			steps := append(append([]outputLimitStep(nil), tc.steps...), answered)
			p := &outputLimitProvider{steps: steps}
			ag, _ := recoveringAgent(t, p)
			stop, err := runOutputLimitTurn(ag)
			if err != nil || stop != string(acp.StopReasonEndTurn) || p.calls != len(steps) {
				t.Fatalf("stop = %q, err = %v after %d calls; want end_turn after %d", stop, err, p.calls, len(steps))
			}
			failed, rerun := p.seen[len(p.seen)-2], p.seen[len(p.seen)-1]
			want := standingAtTheEnd(failed)
			if len(want) == 0 {
				t.Fatalf("the failed request ends with no local message; the chain does not exercise a recovery")
			}
			hist := requestHistory(rerun)
			if tc.kept != "" {
				n := len(hist)
				if n < 2 || hist[n-2] != "assistant: "+tc.kept || hist[n-1] != "user: "+providerRecoveryNudge {
					t.Fatalf("the request run again does not end with the kept answer and the request to go on:\n%s", tailLines(strings.Join(hist, "\n"), 4))
				}
				hist = hist[:n-2]
			}
			n := len(want)
			if len(hist) < n || strings.Join(hist[len(hist)-n:], "\n") != strings.Join(want, "\n") {
				t.Fatalf("the request run again does not carry the failed request's standing messages %q before what the failure left:\n%s", want, tailLines(strings.Join(requestHistory(rerun), "\n"), n+3))
			}
		})
	}
}

// loopThenCompactProvider is bddLoopProvider with a hook run on its first call,
// which turns compaction on, and a summary for the compaction to write.
type loopThenCompactProvider struct {
	*bddLoopProvider
	onFirst func()
}

func (p *loopThenCompactProvider) Complete(context.Context, []llm.Message, []llm.ToolDefinition) (*llm.Response, error) {
	return &llm.Response{Content: "Earlier turn summary."}, nil
}

func (p *loopThenCompactProvider) Stream(ctx context.Context, msgs []llm.Message, defs []llm.ToolDefinition, onChunk func(llm.StreamChunk)) (*llm.Response, error) {
	if p.calls == 0 && p.onFirst != nil {
		p.onFirst()
	}
	return p.bddLoopProvider.Stream(ctx, msgs, defs, onChunk)
}

// TestPromptCacheCompactionAfterTheLoopGuardKeepsItsNudge: the loop guard cuts
// a response that repeats itself and sends a nudge after the part it kept. A
// compaction before the next request rebuilds the history from the transcript,
// which holds that part and not the nudge; the nudge stands at the end of the
// working history and is put back, so the request does not end on the step the
// guard cut.
func TestPromptCacheCompactionAfterTheLoopGuardKeepsItsNudge(t *testing.T) {
	run := func(compact bool) []llm.Message {
		t.Helper()
		enabled, keep := false, 1
		p := &loopThenCompactProvider{
			bddLoopProvider: &bddLoopProvider{channel: loopAbortText, recoverAfter: 1, realAnswer: "Validation is in place."},
			onFirst: func() {
				if compact {
					enabled = true
				}
			},
		}
		ag, st := outputLimitAgent(t, p, func(c *config.Config) {
			c.Compaction = config.Compaction{Enabled: &enabled, ThresholdPercent: 1, KeepRecentTurns: &keep}
			c.Models[0].MaxContextTokens = 10000
		})
		st.AddMessage(llm.Message{Role: llm.RoleUser, Content: "Earlier question."})
		st.AddMessage(llm.Message{Role: llm.RoleAssistant, Content: "Earlier answer."})
		stop, err := runOutputLimitTurn(ag)
		if err != nil || stop != string(acp.StopReasonEndTurn) || p.calls != 2 {
			t.Fatalf("compact=%v: stop = %q, err = %v after %d calls; want end_turn after 2", compact, stop, err, p.calls)
		}
		last := p.seen[len(p.seen)-1]
		var summarized bool
		for _, m := range last {
			if strings.Contains(m.Content, "Earlier turn summary.") {
				summarized = true
			}
		}
		if summarized != compact {
			t.Fatalf("compact=%v: the last request was compacted = %v; the test proves nothing", compact, summarized)
		}
		return last
	}
	working, rebuilt := run(false), run(true)
	want := []string{"user: " + streamLoopNudge}
	if got := standingAtTheEnd(working); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("the working request ends with %q, want the loop guard's nudge", got)
	}
	if got := standingAtTheEnd(rebuilt); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("the request after the compaction ends with %q, want the loop guard's nudge:\n%s", got, tailLines(strings.Join(requestHistory(rebuilt), "\n"), 3))
	}
}
