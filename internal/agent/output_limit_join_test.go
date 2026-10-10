package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// A text the output limit cut off and the step that goes on from it are two
// assistant messages of the transcript, with a local message between them in
// the requests of the turn. A history built from the transcript for a later turn
// has no such message to separate them, and chat templates that insist on
// alternation refuse two assistant messages in a row, every later turn, for the
// rest of the session. So that history joins them: the text with nothing between
// it and its continuation, stored and replayed as streamed; two messages that
// are not one text with a blank line. Signed reasoning keeps its message.

func assistantMessages(msgs []llm.Message) []llm.Message {
	var out []llm.Message
	for _, m := range msgs {
		if m.Role == llm.RoleAssistant {
			out = append(out, m)
		}
	}
	return out
}

func requireNoAssistantInARow(t *testing.T, msgs []llm.Message) {
	t.Helper()
	for i := 1; i < len(msgs); i++ {
		if msgs[i].Role == llm.RoleAssistant && msgs[i-1].Role == llm.RoleAssistant {
			t.Fatalf("two assistant messages in a row at %d:\n%s", i, tailLines(joinHistory(msgs), 6))
		}
	}
}

func joinHistory(msgs []llm.Message) string {
	var out string
	for i, line := range requestHistory(msgs) {
		if i > 0 {
			out += "\n"
		}
		out += line
	}
	return out
}

// TestPromptCacheNextTurnAfterAContinuationSendsOneAssistantMessage: the
// continued answer goes into the next turn's request as the one text it is, byte
// for byte as it streamed, whitespace at the join and a code fence included, and
// the requests of the turn that continued it are what they were.
func TestPromptCacheNextTurnAfterAContinuationSendsOneAssistantMessage(t *testing.T) {
	for _, tc := range []struct {
		name, first, rest string
	}{
		{"a space at the end of the text", "Here is the first half of the module and then ", "the second half."},
		{"a space at the start of the continuation", "Here is the first half of the module and then", " the second half."},
		{"a code fence on a line of its own", "Here is the module:\n```go\nfunc Run() {", "\n}\n```\nThat is all."},
		{"trailing blank lines", "First paragraph.\n\n", "Second paragraph."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &outputLimitProvider{steps: []outputLimitStep{
				{text: tc.first, stop: "max_tokens"},
				{text: tc.rest, stop: "end_turn"},
				{text: "Tested.", stop: "end_turn"},
			}}
			ag, st := outputLimitAgent(t, p, nil)
			if stop, err := runOutputLimitTurn(ag); err != nil || stop != string(acp.StopReasonEndTurn) {
				t.Fatalf("first turn: stop = %q, err = %v", stop, err)
			}
			// The turn itself saw the two as two, with its message between.
			inTurn := p.seen[1]
			if got := assistantMessages(inTurn); len(got) != 1 || got[0].Content != tc.first {
				t.Fatalf("the request of the turn replays %+v, want the text as it streamed", got)
			}
			// What the transcript stores is what streamed, at the join.
			stored := assistantMessages(st.GetMessages())
			if len(stored) != 2 || stored[0].Content != tc.first || stored[1].Content != tc.rest || !stored[0].Truncated {
				t.Fatalf("the transcript stores %+v, want the text (truncated) and its continuation exactly as streamed", stored)
			}

			if stop, err := ag.Run(context.Background(), []acp.ContentBlock{{Type: "text", Text: "now test it"}}); err != nil || stop != string(acp.StopReasonEndTurn) {
				t.Fatalf("second turn: stop = %q, err = %v", stop, err)
			}
			next := p.seen[len(p.seen)-1]
			requireNoAssistantInARow(t, next)
			if got := assistantMessages(next); len(got) != 1 || got[0].Content != tc.first+tc.rest {
				t.Fatalf("the next turn replays %+v, want one message holding %q", got, tc.first+tc.rest)
			}
		})
	}
}

// TestPromptCacheNextTurnAfterACutCallWithTextSendsOneAssistantMessage: the
// text before a cut-off call and the step that answered the message about it
// are two assistant messages, and a step with a call joins the text before it.
func TestPromptCacheNextTurnAfterACutCallWithTextSendsOneAssistantMessage(t *testing.T) {
	read := llm.ToolCall{ID: "r1", Name: "coddy_todo_plan_read", InputJSON: "{}"}
	p := &outputLimitProvider{steps: []outputLimitStep{
		{text: "I will write it.", calls: []llm.ToolCall{cutWrite("c1")}, stop: "max_tokens"},
		{calls: []llm.ToolCall{read}, stop: "tool_use"},
		{text: "Done.", stop: "end_turn"},
		{text: "ok", stop: "end_turn"},
	}}
	ag, _, _ := cutCallAgent(t, p, nil)
	if _, err := runOutputLimitTurn(ag); err != nil {
		t.Fatal(err)
	}
	if _, err := ag.Run(context.Background(), []acp.ContentBlock{{Type: "text", Text: "next"}}); err != nil {
		t.Fatal(err)
	}
	next := p.seen[len(p.seen)-1]
	requireNoAssistantInARow(t, next)
	var withCall *llm.Message
	for i := range next {
		if len(next[i].ToolCalls) == 1 && next[i].ToolCalls[0].ID == "r1" {
			withCall = &next[i]
		}
	}
	if withCall == nil || withCall.Content != "I will write it." {
		t.Fatalf("the step with the call does not carry the text before it: %+v", withCall)
	}
}

// TestPromptCacheNextTurnAfterAKeptProviderAnswerSendsOneAssistantMessage: the
// answer a provider failure cut off and the step run again after it are one
// assistant message in the next turn's history, but not one text: the stream
// ended wherever the failure hit it and the step run again may go on from the last
// character or start the answer over, so the answer is stored as any other
// (not truncated, trimmed) and the history separates the two with a blank line,
// which reads right either way.
func TestPromptCacheNextTurnAfterAKeptProviderAnswerSendsOneAssistantMessage(t *testing.T) {
	for _, tc := range []struct {
		name, kept, rest, want string
	}{
		{"the step goes on from the last character", "The module starts with ", "an import block.", "The module starts with\n\nan import block."},
		{"the step starts the answer over", "The module sta", "The module starts with an import block.", "The module sta\n\nThe module starts with an import block."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &outputLimitProvider{steps: []outputLimitStep{
				{text: tc.kept, err: errConnectionReset},
				{text: tc.rest, stop: "end_turn"},
				{text: "ok", stop: "end_turn"},
			}}
			ag, st := recoveringAgent(t, p)
			if stop, err := runOutputLimitTurn(ag); err != nil || stop != string(acp.StopReasonEndTurn) {
				t.Fatalf("first turn: stop = %q, err = %v", stop, err)
			}
			stored := assistantMessages(st.GetMessages())
			if len(stored) != 2 || stored[0].Truncated || stored[0].Content != strings.TrimSpace(tc.kept) || stored[1].Content != tc.rest {
				t.Fatalf("the transcript stores %+v, want the kept answer, not truncated, and the step run again", stored)
			}
			if _, err := ag.Run(context.Background(), []acp.ContentBlock{{Type: "text", Text: "next"}}); err != nil {
				t.Fatal(err)
			}
			next := p.seen[len(p.seen)-1]
			requireNoAssistantInARow(t, next)
			if got := assistantMessages(next); len(got) != 1 || got[0].Content != tc.want {
				t.Fatalf("the next turn replays %+v, want one message holding %q", got, tc.want)
			}
		})
	}
}

// TestFoldSplitAnswers pins what is joined and what is not.
func TestFoldSplitAnswers(t *testing.T) {
	user := llm.Message{Role: llm.RoleUser, Content: "go"}
	text := func(s string) llm.Message { return llm.Message{Role: llm.RoleAssistant, Content: s} }
	cut := func(s string) llm.Message { return llm.Message{Role: llm.RoleAssistant, Content: s, Truncated: true} }
	signed := func(m llm.Message) llm.Message { m.ReasoningSignature = "sig"; return m }
	withCall := func(m llm.Message) llm.Message {
		m.ToolCalls = []llm.ToolCall{{ID: "c1", Name: "read", InputJSON: "{}"}}
		return m
	}
	for _, tc := range []struct {
		name string
		in   []llm.Message
		want []llm.Message
	}{
		{"a text and its continuation", []llm.Message{user, cut("and then "), text("the rest.")}, []llm.Message{user, text("and then the rest.")}},
		{"a chain of continuations", []llm.Message{user, cut("a"), cut("b"), text("c")}, []llm.Message{user, text("abc")}},
		{"the merged message is cut when its continuation is", []llm.Message{user, cut("a"), cut("b")}, []llm.Message{user, cut("ab")}},
		{"two texts that are not one", []llm.Message{user, text("I will write it."), text("Done.")}, []llm.Message{user, text("I will write it.\n\nDone.")}},
		{"a text before a step with a call", []llm.Message{user, text("I will read."), withCall(text(""))}, []llm.Message{user, withCall(text("I will read."))}},
		{"a text before a step with text and a call", []llm.Message{user, text("I will read."), withCall(text("Reading."))}, []llm.Message{user, withCall(text("I will read.\n\nReading."))}},
		{"a step with a call is not joined to what follows", []llm.Message{user, withCall(text("a")), text("b")}, []llm.Message{user, withCall(text("a")), text("b")}},
		{"signed reasoning before", []llm.Message{user, signed(text("a")), text("b")}, []llm.Message{user, signed(text("a")), text("b")}},
		{"signed reasoning after", []llm.Message{user, text("a"), signed(text("b"))}, []llm.Message{user, text("a"), signed(text("b"))}},
		{"a user message between", []llm.Message{text("a"), user, text("b")}, []llm.Message{text("a"), user, text("b")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := foldSplitAnswers(tc.in)
			if len(got) != len(tc.want) {
				t.Fatalf("got %d messages, want %d: %+v", len(got), len(tc.want), got)
			}
			for i := range got {
				if got[i].Role != tc.want[i].Role || got[i].Content != tc.want[i].Content || got[i].Truncated != tc.want[i].Truncated ||
					len(got[i].ToolCalls) != len(tc.want[i].ToolCalls) || got[i].ReasoningSignature != tc.want[i].ReasoningSignature {
					t.Fatalf("message %d = %+v, want %+v", i, got[i], tc.want[i])
				}
			}
		})
	}
}

// TestBuildMessagesLeavesASignedStepItsMessage: a step with signed reasoning,
// which Anthropic, Codex and Devin replay, is not folded into the text next to
// it, whichever side it is on.
func TestBuildMessagesLeavesASignedStepItsMessage(t *testing.T) {
	st := &session.State{ID: "sess_fold", CWD: t.TempDir(), Mode: session.ModeAgent}
	for _, m := range []llm.Message{
		{Role: llm.RoleUser, Content: "go"},
		{Role: llm.RoleAssistant, Content: "a cut text", Truncated: true},
		{Role: llm.RoleAssistant, Content: "its continuation", Reasoning: "signed", ReasoningSignature: "sig"},
	} {
		st.AddMessage(m)
	}
	ag := NewAgent(&config.Config{Agent: config.Agent{Model: "fake/model"}}, st, resumePermissionSender{}, nil)
	if got := assistantMessages(ag.buildMessages("SYSTEM")); len(got) != 2 {
		t.Fatalf("a signed step was folded: %+v", got)
	}
}
