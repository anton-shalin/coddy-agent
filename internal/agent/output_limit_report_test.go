package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/hooks"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

// An answer the output limit cut off and the model went on from is several
// assistant messages of the transcript. What a turn hands over as its answer -
// the report of a subagent, the last_assistant_message of a Stop hook - is all
// of it, not the part after the last cut.

// shareFileCall is a call of share_file on a file of the workspace.
func shareFileCall(id, path string) llm.ToolCall {
	return llm.ToolCall{ID: id, Name: "share_file", InputJSON: `{"path":"` + path + `"}`}
}

// cutTextStep is a scripted step that streams text and stops at the output limit
// in the middle of it.
func cutTextStep(text string) scriptStep {
	return func(_ []llm.Message, _ []llm.ToolDefinition, onChunk func(llm.StreamChunk)) *llm.Response {
		onChunk(llm.StreamChunk{TextDelta: text})
		return &llm.Response{Content: text, StopReason: "max_tokens"}
	}
}

func TestLastAssistantPlainTextIsTheWholeContinuedAnswer(t *testing.T) {
	user := llm.Message{Role: llm.RoleUser, Content: "go"}
	cut := func(s string) llm.Message { return llm.Message{Role: llm.RoleAssistant, Content: s, Truncated: true} }
	text := func(s string) llm.Message { return llm.Message{Role: llm.RoleAssistant, Content: s} }
	empty := llm.Message{Role: llm.RoleAssistant, Reasoning: "Hmm."}
	call := llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "c1", Name: "read"}}}
	tool := llm.Message{Role: llm.RoleTool, ToolCallID: "c1", Content: "ok"}
	for _, tc := range []struct {
		name string
		msgs []llm.Message
		want string
	}{
		{"no assistant message", []llm.Message{user}, ""},
		{"a plain answer", []llm.Message{user, text("Done.")}, "Done."},
		{"a text and its continuation", []llm.Message{user, cut("REPORT: the cell is a butt"), text("on labelled Save.")}, "REPORT: the cell is a button labelled Save."},
		{"a chain of three", []llm.Message{user, cut("a "), cut("b "), text("c")}, "a b c"},
		{"an empty step in the chain", []llm.Message{user, cut("a "), empty, text("b")}, "a b"},
		{"an empty last step is passed over", []llm.Message{user, cut("a "), text("b"), empty}, "a b"},
		{"an empty last step after a plain answer", []llm.Message{user, text("a"), text("b"), empty}, "b"},
		{"a continuation after a tool step", []llm.Message{user, call, tool, cut("a "), text("b")}, "a b"},
		{"the chain stops at the tool step", []llm.Message{user, cut("a "), call, tool, text("b")}, "b"},
		{"an answer after a plain one is its own", []llm.Message{user, text("first"), text("second")}, "second"},
		{"a cut text that nothing went on from", []llm.Message{user, cut("and then ")}, "and then"},
		// The placement markers of shared files are for the web UI: in front of
		// a truncated text, at the end of any other; a report goes to a model.
		{"markers stay out of the report", []llm.Message{user, cut("<coddy_file id=\"a\"/>\n\nREPORT: the cell is a butt"), text("on labelled Save.\n\n<coddy_file id=\"b\"/>")}, "REPORT: the cell is a button labelled Save."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := lastAssistantPlainText(tc.msgs); got != tc.want {
				t.Fatalf("lastAssistantPlainText = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestSubagentReportIsTheWholeContinuedAnswer: a child whose report was cut off at
// the output limit and went on hands its parent all of it. A report that ended
// "on labelled Save." would leave the parent model without the half of what the
// child found.
func TestSubagentReportIsTheWholeContinuedAnswer(t *testing.T) {
	s := &subagentsFeatureState{}
	if err := s.reset(); err != nil {
		t.Fatal(err)
	}
	defer s.close()
	for _, step := range []func() error{
		func() error { return s.workspaceDefinition("reviewer") },
		s.parentSession,
		func() error { return s.approveDefinition("reviewer") },
	} {
		if err := step(); err != nil {
			t.Fatal(err)
		}
	}
	s.mu.Lock()
	s.childSteps = func() []scriptStep {
		return []scriptStep{
			cutTextStep("REPORT: the cell is a butt"),
			answerStep("on labelled Save."),
		}
	}
	s.mu.Unlock()
	if err := s.spawnWith("reviewer", false); err != nil {
		t.Fatal(err)
	}
	res, err := s.lastSpawnResult()
	if err != nil {
		t.Fatal(err)
	}
	if want := "REPORT: the cell is a button labelled Save."; !strings.Contains(res, want) {
		t.Fatalf("the spawn_agent result is %q, want it to hold the whole report %q", res, want)
	}
	// The child was asked to go on once, which is how the answer became two
	// messages of its transcript.
	child := s.childProviderByName("reviewer")
	if child == nil {
		t.Fatal("no child ran as reviewer")
	}
	child.mu.Lock()
	defer child.mu.Unlock()
	if len(child.requests) != 2 || countNudges(child.requests[1], outputLimitContinueNudge) != 1 {
		t.Fatalf("the child made %d requests, want the cut-off step and one that asks it to go on", len(child.requests))
	}
}

// TestStopHookIsHandedTheWholeContinuedAnswer: the last_assistant_message of a Stop
// hook is the answer the turn ends with in full, and without the placement
// markers of shared files, which are for the web UI only.
func TestStopHookIsHandedTheWholeContinuedAnswer(t *testing.T) {
	for _, tc := range []struct {
		name  string
		share bool
		steps []scriptStep
		want  string
	}{
		{
			name:  "a text and its continuation",
			steps: []scriptStep{cutTextStep("The notes hold"), answerStep(" forty numbered lines.")},
			want:  "The notes hold forty numbered lines.",
		},
		{
			name:  "a chain of three",
			steps: []scriptStep{cutTextStep("The notes "), cutTextStep("hold forty "), answerStep("numbered lines.")},
			want:  "The notes hold forty numbered lines.",
		},
		{
			name:  "an answer that was not cut off",
			steps: []scriptStep{answerStep("The notes hold forty numbered lines.")},
			want:  "The notes hold forty numbered lines.",
		},
		{
			// The markers of the file shared before the answer stand in front of its
			// first part; the hook is not handed them.
			name:  "a file shared before the cut",
			share: true,
			steps: []scriptStep{
				toolStep(shareFileCall("call-share", "notes.txt")),
				cutTextStep("The file is the butt"),
				answerStep("on labelled notes."),
			},
			want: "The file is the button labelled notes.",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &hooksFeatureState{}
			if err := s.reset(); err != nil {
				t.Fatal(err)
			}
			defer s.close()
			if err := s.hookRecordsAny(hooks.EventStop); err != nil {
				t.Fatal(err)
			}
			if err := s.agentSession(); err != nil {
				t.Fatal(err)
			}
			if tc.share {
				if err := os.WriteFile(filepath.Join(s.cwd, "notes.txt"), []byte("forty numbered lines\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if err := s.runPrompt("go", tc.steps...); err != nil {
				t.Fatal(err)
			}
			if s.turnErr != nil || s.stopReason != "end_turn" {
				t.Fatalf("the turn ended with err = %v, stop = %q; want end_turn", s.turnErr, s.stopReason)
			}
			payload, err := s.recordedPayload()
			if err != nil {
				t.Fatal(err)
			}
			if payload["hook_event_name"] != hooks.EventStop {
				t.Fatalf("recorded event = %v, want %s", payload["hook_event_name"], hooks.EventStop)
			}
			if got, _ := payload["last_assistant_message"].(string); got != tc.want {
				t.Fatalf("last_assistant_message = %q, want %q", got, tc.want)
			}
		})
	}
}
