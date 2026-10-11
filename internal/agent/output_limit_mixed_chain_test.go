package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// TestProviderFailureInsideAContinuedAnswerKeepsItOneText: the output limit cuts
// an answer off, the step that goes on from it fails at the provider after
// streaming more of it, and the step run again writes the rest. The part the
// failure left is a part of the continued answer - the step was asked to go on
// from the cut text, and the step run again is asked to go on from it - so it is
// stored truncated, and everything that reads the answer reads all of it: the
// report a parent gets, the next turn's history, a replay to a client.
func TestProviderFailureInsideAContinuedAnswerKeepsItOneText(t *testing.T) {
	const whole = "REPORT: the cell is a button labelled Save."
	p := &outputLimitProvider{steps: []outputLimitStep{
		{text: "REPORT: the cell is a butt", stop: "max_tokens"},
		{text: "on lab", err: errConnectionReset},
		{text: "elled Save.", stop: "end_turn"},
		{text: "ok", stop: "end_turn"},
	}}
	ag, st := recoveringAgent(t, p)
	stop, err := runOutputLimitTurn(ag)
	if err != nil || stop != string(acp.StopReasonEndTurn) || p.calls != 3 {
		t.Fatalf("stop = %q, err = %v after %d calls; want end_turn after 3", stop, err, p.calls)
	}

	stored := assistantMessages(st.GetMessages())
	if len(stored) != 3 || !stored[0].Truncated || !stored[1].Truncated || stored[2].Truncated {
		t.Fatalf("the transcript stores %+v, want the cut text and the part the failure left truncated, the rest not", stored)
	}
	msgs := st.GetMessages()
	if got := lastAssistantPlainText(msgs); got != whole {
		t.Fatalf("the report of the run is %q, want %q", got, whole)
	}
	var replayed strings.Builder
	for i, m := range msgs {
		if m.Role == llm.RoleAssistant {
			replayed.WriteString(session.ReplayedAssistantText(msgs[:i], m))
		}
	}
	if replayed.String() != whole {
		t.Fatalf("a replay draws %q, want %q", replayed.String(), whole)
	}

	if _, err := ag.Run(context.Background(), []acp.ContentBlock{{Type: "text", Text: "next"}}); err != nil {
		t.Fatal(err)
	}
	next := p.seen[len(p.seen)-1]
	requireNoAssistantInARow(t, next)
	if got := assistantMessages(next); len(got) != 1 || got[0].Content != whole {
		t.Fatalf("the next turn replays %+v, want one message holding %q", got, whole)
	}
}

// TestProviderFailureOutsideAContinuedAnswerStaysApart: an answer of its own a
// provider failure cut off is not a part of anything; the step run again may
// start it over, so it is not marked truncated and the next turn sends it apart
// from the answer after it, with a blank line between.
func TestProviderFailureOutsideAContinuedAnswerStaysApart(t *testing.T) {
	p := &outputLimitProvider{steps: []outputLimitStep{
		{text: "Here is the pla", err: errConnectionReset},
		{text: "Here is the plan.", stop: "end_turn"},
		{text: "ok", stop: "end_turn"},
	}}
	ag, st := recoveringAgent(t, p)
	if _, err := runOutputLimitTurn(ag); err != nil {
		t.Fatal(err)
	}
	stored := assistantMessages(st.GetMessages())
	if len(stored) != 2 || stored[0].Truncated {
		t.Fatalf("the transcript stores %+v, want the kept start not truncated", stored)
	}
	if got := lastAssistantPlainText(st.GetMessages()); got != "Here is the plan." {
		t.Fatalf("the report of the run is %q, want the answer the step run again wrote", got)
	}
	if _, err := ag.Run(context.Background(), []acp.ContentBlock{{Type: "text", Text: "next"}}); err != nil {
		t.Fatal(err)
	}
	if got := assistantMessages(p.seen[len(p.seen)-1]); len(got) != 1 || got[0].Content != "Here is the pla\n\nHere is the plan." {
		t.Fatalf("the next turn replays %+v, want the two apart with a blank line", got)
	}
}
