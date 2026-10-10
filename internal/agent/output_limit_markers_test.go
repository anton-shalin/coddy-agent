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

// The placement markers of shared files (<coddy_file id="..."/>) are for the web
// UI. A text the output limit cut off and the step that goes on from it are one
// text, joined with nothing between, so the markers a truncated message carries
// stand in front of its text and are never streamed after it: either would land
// in the middle of a word as often as not.

// agentChunks is the text of every message chunk a client was sent, in order.
func agentChunks(s *outputLimitSender) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for _, u := range s.updates {
		if c, ok := u.(acp.MessageChunkUpdate); ok && c.SessionUpdate == acp.UpdateTypeAgentMessageChunk {
			out = append(out, c.Content.Text)
		}
	}
	return out
}

// shareNotes is a call of share_file on the notes file of cutCallAgent.
var shareNotes = llm.ToolCall{ID: "call_share", Name: "share_file", InputJSON: `{"path":"notes.txt"}`}

// textMessages lists the assistant messages of a transcript that hold text.
func textMessages(msgs []llm.Message) []llm.Message {
	var out []llm.Message
	for _, m := range msgs {
		if m.Role == llm.RoleAssistant && strings.TrimSpace(m.Content) != "" {
			out = append(out, m)
		}
	}
	return out
}

// TestNoMarkerChunkIsStreamedForATruncatedMessage: a message the output limit cut
// off carries the markers of the files shared before it in front of its text
// when it is stored, and nothing is streamed for them after it - a chunk there
// would land in the middle of the text that goes on. The surface reads them
// from the stored message. The message that goes on from it places nothing: the
// file is placed already. A reply that is not cut off closes with its markers
// streamed, as before.
func TestNoMarkerChunkIsStreamedForATruncatedMessage(t *testing.T) {
	const (
		head = "The notes file is the butt"
		rest = "on labelled notes."
	)
	p := &outputLimitProvider{steps: []outputLimitStep{
		{calls: []llm.ToolCall{shareNotes}, stop: "tool_use"},
		{text: head, stop: "max_tokens"},
		{text: rest, stop: "end_turn"},
		{text: "ok", stop: "end_turn"},
	}}
	ag, st, sender := cutCallAgent(t, p, nil)
	if stop, err := runOutputLimitTurn(ag); err != nil || stop != string(acp.StopReasonEndTurn) || p.calls != 3 {
		t.Fatalf("stop = %q, err = %v after %d calls; want end_turn after 3", stop, err, p.calls)
	}

	said := textMessages(st.GetMessages())
	if len(said) != 2 || !said[0].Truncated || said[1].Truncated {
		t.Fatalf("the transcript holds %+v, want the cut text and its continuation", said)
	}
	if len(said[0].Artifacts) != 1 {
		t.Fatalf("the cut text carries artifacts %+v, want the shared file", said[0].Artifacts)
	}
	markers := session.ArtifactMarkers(said[0].Artifacts)
	if markers == "" {
		t.Fatal("the shared file has no marker; the test proves nothing")
	}
	if want := markers + "\n\n" + head; said[0].Content != want {
		t.Fatalf("the cut text is stored as %q, want the markers in front: %q", said[0].Content, want)
	}
	if said[1].Content != rest || len(said[1].Artifacts) != 0 {
		t.Fatalf("the continuation is stored as %+v, want its text alone", said[1])
	}

	chunks := agentChunks(sender)
	for _, c := range chunks {
		if strings.Contains(c, "<coddy_file") {
			t.Fatalf("a marker was streamed after a truncated message: chunks %q", chunks)
		}
	}
	if got := strings.Join(chunks, ""); got != head+rest {
		t.Fatalf("the client was streamed %q, want the text as the model wrote it: %q", got, head+rest)
	}

	// The next turn sends the text as it was written, whole.
	if _, err := ag.Run(context.Background(), []acp.ContentBlock{{Type: "text", Text: "next"}}); err != nil {
		t.Fatal(err)
	}
	requireNoAssistantInARow(t, p.seen[len(p.seen)-1])
	var sent []string
	for _, m := range textMessages(p.seen[len(p.seen)-1]) {
		sent = append(sent, m.Content)
	}
	if len(sent) != 1 || sent[0] != head+rest {
		t.Fatalf("the next turn replays %q, want the one text %q", sent, head+rest)
	}
}

// TestAMarkerChunkClosesAReplyThatWasNotCutOff is the other half of the above:
// the markers of a reply that is not cut off are still streamed after it.
func TestAMarkerChunkClosesAReplyThatWasNotCutOff(t *testing.T) {
	p := &outputLimitProvider{steps: []outputLimitStep{
		{calls: []llm.ToolCall{shareNotes}, stop: "tool_use"},
		{text: "Here is the notes file.", stop: "end_turn"},
	}}
	ag, st, sender := cutCallAgent(t, p, nil)
	if stop, err := runOutputLimitTurn(ag); err != nil || stop != string(acp.StopReasonEndTurn) {
		t.Fatalf("stop = %q, err = %v", stop, err)
	}
	said := textMessages(st.GetMessages())
	if len(said) != 1 || said[0].Truncated || len(said[0].Artifacts) != 1 {
		t.Fatalf("the transcript holds %+v, want one reply with the shared file", said)
	}
	markers := session.ArtifactMarkers(said[0].Artifacts)
	chunks := agentChunks(sender)
	if len(chunks) == 0 || chunks[len(chunks)-1] != "\n\n"+markers {
		t.Fatalf("chunks = %q, want the reply closed with %q", chunks, "\n\n"+markers)
	}
	if want := "Here is the notes file.\n\n" + markers; said[0].Content != want {
		t.Fatalf("the reply is stored as %q, want %q", said[0].Content, want)
	}
}

// TestBuildMessagesJoinsATruncatedMessageWithItsMarkersInFront: the history built
// from a transcript in which a text the output limit cut off carries the markers
// of two shared files in front of it sends that text and its continuation as one
// message, "butt" and "on labelled" with nothing between them and no marker
// anywhere in what the model reads.
func TestBuildMessagesJoinsATruncatedMessageWithItsMarkersInFront(t *testing.T) {
	st := &session.State{ID: "sess_markers_join", CWD: t.TempDir(), Mode: session.ModeAgent}
	share := func(id string) llm.ToolCall {
		return llm.ToolCall{ID: id, Name: "share_file", InputJSON: `{"path":"` + id + `.txt"}`}
	}
	for _, m := range []llm.Message{
		{Role: llm.RoleUser, Content: "share the notes"},
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{share("one"), share("two")}},
		{Role: llm.RoleTool, ToolCallID: "one", Content: "{}", Artifacts: []llm.Artifact{{ID: "artifact-one", Name: "one.txt"}}},
		{Role: llm.RoleTool, ToolCallID: "two", Content: "{}", Artifacts: []llm.Artifact{{ID: "artifact-two", Name: "two.txt"}}},
		{Role: llm.RoleAssistant, Content: "Both files are ready, see the butt", Truncated: true},
		{Role: llm.RoleAssistant, Content: "on labelled Open."},
	} {
		st.AddMessage(m)
	}
	stored := st.GetMessages()
	head := stored[4]
	if len(head.Artifacts) != 2 || !strings.HasPrefix(head.Content, `<coddy_file id="artifact-one"/>`+"\n"+`<coddy_file id="artifact-two"/>`+"\n\n") {
		t.Fatalf("the cut text is stored as %+v, want both markers in front of it", head)
	}
	if rest := stored[5]; rest.Content != "on labelled Open." || len(rest.Artifacts) != 0 {
		t.Fatalf("the continuation is stored as %+v, want its text alone", rest)
	}

	ag := NewAgent(&config.Config{}, st, resumePermissionSender{}, nil)
	var said []string
	for _, m := range ag.buildMessages("system") {
		if m.Role == llm.RoleAssistant && len(m.ToolCalls) == 0 {
			said = append(said, m.Content)
		}
	}
	if want := "Both files are ready, see the button labelled Open."; len(said) != 1 || said[0] != want {
		t.Fatalf("the history carries %q, want one message holding %q", said, want)
	}
}
