package remote

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
)

// A session loaded from a remote server replays an answer the output limit cut
// off and the model continued as the local console replays it: the rows come
// as the server's messages route sends them, the first marked truncated, and
// their chunks join into the text the stream drew, the whitespace at the join
// included, while every other assistant row is replayed trimmed.
func TestReplayJoinsAContinuedAnswerAsItStreamed(t *testing.T) {
	const body = `{"messages":[
		{"role":"user","content":"write it"},
		{"role":"assistant","content":"and then ","truncated":true},
		{"role":"assistant","content":""},
		{"role":"assistant","content":"the second half.  "},
		{"role":"user","content":"thanks"},
		{"role":"assistant","content":"  You are welcome.  "}
	]}`
	var res messagesResponse
	if err := json.Unmarshal([]byte(body), &res); err != nil {
		t.Fatal(err)
	}
	h, err := NewHandler(Options{BaseURL: "http://127.0.0.1:1"})
	if err != nil {
		t.Fatal(err)
	}
	sender := &collectSender{}
	h.SetServer(sender)
	h.replayMessages("sess_1", res.Messages)

	sender.mu.Lock()
	defer sender.mu.Unlock()
	var chunks []string
	for _, u := range sender.updates {
		if v, ok := u.(acp.MessageChunkUpdate); ok && v.SessionUpdate == acp.UpdateTypeAgentMessageChunk {
			chunks = append(chunks, v.Content.Text)
		}
	}
	if len(chunks) != 3 {
		t.Fatalf("agent chunks = %q, want the cut text, its continuation and the next answer", chunks)
	}
	if got := chunks[0] + chunks[1]; got != "and then the second half." {
		t.Fatalf("the continued answer replays as %q, want %q", got, "and then the second half.")
	}
	if chunks[2] != "You are welcome." || strings.TrimSpace(chunks[2]) != chunks[2] {
		t.Fatalf("an answer that was not cut replays as %q, want it trimmed", chunks[2])
	}
}
