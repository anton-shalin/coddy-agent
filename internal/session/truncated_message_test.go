package session_test

import (
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// An assistant message the output limit cut off, and the message that goes on
// from it, are stored as they streamed at the join: the whitespace there is part
// of the text, and trimming it ("and then" + "the second half") would run the
// words together wherever the two are joined. Every other assistant message keeps
// being stored without the whitespace around it, an answer a provider failure cut
// off included: it is not marked truncated, since the step run again may start it
// over instead of going on from its last character.

func assistantContents(st *session.State) []string {
	var out []string
	for _, m := range st.GetMessages() {
		if m.Role == llm.RoleAssistant {
			out = append(out, m.Content)
		}
	}
	return out
}

func TestAddMessageStoresTheJoinOfATruncatedMessageAsStreamed(t *testing.T) {
	assistant := func(content string, truncated bool) llm.Message {
		return llm.Message{Role: llm.RoleAssistant, Content: content, Truncated: truncated}
	}
	for _, tc := range []struct {
		name string
		in   []llm.Message
		want []string
	}{
		{
			name: "the end of the cut text and the start of the continuation",
			in:   []llm.Message{assistant("  The module and then ", true), assistant(" the second half.\n", false)},
			want: []string{"The module and then ", " the second half."},
		},
		{
			name: "a chain",
			in:   []llm.Message{assistant("a ", true), assistant(" b ", true), assistant(" c\n", false)},
			want: []string{"a ", " b ", " c"},
		},
		{
			name: "a message after one that was not cut",
			in:   []llm.Message{assistant("  a  ", false), assistant("  b  ", false)},
			want: []string{"a", "b"},
		},
		{
			name: "an answer a provider failure cut off and the step run again after it",
			in:   []llm.Message{assistant("The module starts with ", false), assistant(" an import block.", false)},
			want: []string{"The module starts with", "an import block."},
		},
		{
			name: "a placement marker is still taken out",
			in:   []llm.Message{assistant("a <coddy_file id=\"x\"/>", true), assistant(" b<coddy_file id=\"y\"/>", false)},
			want: []string{"a ", " b"},
		},
		{
			name: "an assistant message after a user message is trimmed at its start",
			in:   []llm.Message{assistant("a ", true), {Role: llm.RoleUser, Content: "go"}, assistant("  b", false)},
			want: []string{"a ", "b"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := &session.State{ID: "sess_truncated"}
			for _, m := range tc.in {
				st.AddMessage(m)
			}
			got := assistantContents(st)
			if strings.Join(got, "|") != strings.Join(tc.want, "|") {
				t.Fatalf("stored %q, want %q", got, tc.want)
			}
		})
	}
}

// TestSessionLoadReplaysATruncatedMessageAndItsContinuationAsStreamed: a client
// that loads the session draws the chunks one after the other, so the replay
// sends the two as they were stored, without trimming the join.
func TestSessionLoadReplaysATruncatedMessageAndItsContinuationAsStreamed(t *testing.T) {
	for _, tc := range []struct {
		name string
		said []llm.Message
		want string
	}{
		{
			name: "a text the output limit cut off",
			said: []llm.Message{
				{Role: llm.RoleAssistant, Content: "The module and then ", Truncated: true},
				{Role: llm.RoleAssistant, Content: " the second half."},
			},
			want: "The module and then | the second half.",
		},
		{
			// Not marked truncated: the step run again may have started over, so
			// the two are drawn as the two messages they are, each trimmed.
			name: "an answer a provider failure cut off",
			said: []llm.Message{
				{Role: llm.RoleAssistant, Content: "The module starts with "},
				{Role: llm.RoleAssistant, Content: " an import block."},
			},
			want: "The module starts with|an import block.",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			cfg := testConfig()
			store := &session.FileStore{Root: t.TempDir()}
			root := t.TempDir()
			runner := func(_ context.Context, st *session.State, _ []acp.ContentBlock, _ acp.UpdateSender) (string, error) {
				st.AddMessage(llm.Message{Role: llm.RoleUser, Content: "write it"})
				for _, m := range tc.said {
					st.AddMessage(m)
				}
				return string(acp.StopReasonEndTurn), nil
			}
			m1 := session.NewManager(cfg, noopSender{}, runner, slog.Default(), root, store)
			res, err := m1.HandleSessionNew(ctx, acp.SessionNewParams{CWD: root})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := m1.HandleSessionPrompt(ctx, acp.SessionPromptParams{
				SessionID: res.SessionID,
				Prompt:    []acp.ContentBlock{{Type: acp.ContentTypeText, Text: "write it"}},
			}); err != nil {
				t.Fatal(err)
			}
			snd := &captureSender{}
			m2 := session.NewManager(cfg, snd, noopRunner, slog.Default(), root, store)
			if _, err := m2.HandleSessionLoad(ctx, acp.SessionLoadParams{SessionID: res.SessionID, CWD: root}); err != nil {
				t.Fatal(err)
			}
			snd.mu.Lock()
			defer snd.mu.Unlock()
			var chunks []string
			for _, u := range snd.ups {
				if chunk, ok := u.(acp.MessageChunkUpdate); ok && chunk.SessionUpdate == "agent_message_chunk" {
					chunks = append(chunks, chunk.Content.Text)
				}
			}
			if strings.Join(chunks, "|") != tc.want {
				t.Fatalf("replayed chunks = %q, want %q", chunks, tc.want)
			}
		})
	}
}
