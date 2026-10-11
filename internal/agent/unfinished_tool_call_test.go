package agent

import (
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// codexWireResponseEndedBy is a Responses API answer the backend ended short
// of a whole answer for reason (response.incomplete), as codexWireResponse is
// for the output limit.
func codexWireResponseEndedBy(reason string, items ...[]sseFrame) string {
	frames := []sseFrame{
		{"response.created", map[string]any{"response": map[string]any{"id": "resp_fixture", "status": "in_progress"}}},
		{"response.in_progress", map[string]any{"response": map[string]any{"id": "resp_fixture", "status": "in_progress"}}},
	}
	for _, i := range items {
		frames = append(frames, i...)
	}
	frames = append(frames, sseFrame{"response.incomplete", map[string]any{"response": map[string]any{
		"id": "resp_fixture", "status": "incomplete", "incomplete_details": map[string]any{"reason": reason},
		"usage": map[string]any{"input_tokens": 9, "output_tokens": 4},
	}}})
	return writeSSEFrames(frames)
}

// codexWireText is a message output item holding text.
func codexWireText(index int, text string) []sseFrame {
	itemID := "msg_text"
	return []sseFrame{
		{"response.output_item.added", map[string]any{"output_index": index, "item": map[string]any{"id": itemID, "type": "message", "status": "in_progress", "role": "assistant", "content": []any{}}}},
		{"response.output_text.delta", map[string]any{"item_id": itemID, "output_index": index, "delta": text}},
		{"response.output_item.done", map[string]any{"output_index": index, "item": map[string]any{
			"id": itemID, "type": "message", "status": "completed", "role": "assistant",
			"content": []any{map[string]any{"type": "output_text", "text": text, "annotations": []any{}}},
		}}},
	}
}

// TestUnfinishedToolCallIsNeverRunWhateverStoppedTheStep: a Codex response the
// content filter ended closes the function_call item it was writing with the
// status "incomplete" and the arguments written so far. That call never runs,
// though the step did not stop at the output limit: its row and its record
// close as failed with the reason, the transcript does not keep it, and no
// message asks the model to make it again - that is for the output limit only.
// A call the step wrote whole before it runs as usual.
func TestUnfinishedToolCallIsNeverRunWhateverStoppedTheStep(t *testing.T) {
	const cutID, tool = "call_filtered", "write"
	const wholeID, wholeTool = "call_plan", "coddy_todo_plan_read"
	wholeWrite := `{"path":"` + cutWritePath + `","content":"package module // CUTMARK"}`
	for _, tc := range []struct {
		name     string
		answer   string
		requests int // the step, and the one after it when a whole call ran
	}{
		{
			name: "text, then a call the content filter cut",
			answer: codexWireResponseEndedBy("content_filter",
				codexWireText(0, "Writing the module."),
				codexWireCall(1, cutID, tool, "incomplete", wholeWrite, wholeWrite)),
			requests: 1,
		},
		{
			name: "a whole call, then a call the content filter cut",
			answer: codexWireResponseEndedBy("content_filter",
				codexWireCall(0, wholeID, wholeTool, "completed", `{}`, `{}`),
				codexWireCall(1, cutID, tool, "incomplete", `{"path":"`+cutWritePath+`",`, `{"path":"`+cutWritePath+`",`)),
			requests: 2,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wire, p := wireProvider(t, "codex", tc.answer, wireDoneAnswer("codex"))
			ag, st, sender := cutCallAgent(t, p, nil)

			stop, err := runOutputLimitTurn(ag)
			if err != nil || stop != string(acp.StopReasonEndTurn) {
				t.Fatalf("stop = %q, err = %v; want end_turn", stop, err)
			}
			bodies := wire.requests()
			if len(bodies) != tc.requests {
				t.Fatalf("provider requests = %d, want %d", len(bodies), tc.requests)
			}
			if !toolRowAnnounced(sender, cutID) {
				t.Fatal("the call was never announced as a pending row; the test proves nothing")
			}
			if got := sender.toolStatuses(cutID); len(got) == 0 || got[len(got)-1] != "failed" {
				t.Fatalf("the client was told %q about the row of the unfinished call, want it closed as failed", got)
			}
			dir := st.GetPersistedSessionDir()
			if meta, err := session.ReadToolCallMeta(dir, cutID); err != nil || meta.Status != "failed" {
				t.Fatalf("tool-call record = %+v, %v; want status failed", meta, err)
			}
			if res, err := session.ReadToolCallResult(dir, cutID); err != nil || strings.TrimSpace(res) != unfinishedToolCallResult {
				t.Fatalf("tool-call result = %q, %v; want %q", res, err, unfinishedToolCallResult)
			}
			requireNoFile(t, st, cutWritePath)
			if n := sender.permissionRequests(); n != 0 {
				t.Fatalf("the client was asked %d times to approve a call that never ran", n)
			}
			stored := st.GetMessages()
			for _, id := range callIDs(stored) {
				if id == cutID {
					t.Fatalf("the transcript keeps the unfinished call: %v", callIDs(stored))
				}
			}
			requireWholePairing(t, stored)
			for _, body := range bodies[1:] {
				second := wireStrings(t, body)
				if countString(second, cutID) != 0 || strings.Contains(body, "CUTMARK") {
					t.Fatal("the unfinished call reached the next request")
				}
				if countString(second, cutOffToolCallNudge(tool)) != 0 {
					t.Fatal("the next request asks for the call again, which only the output limit does")
				}
				if countString(second, wholeID) < 2 {
					t.Fatal("the whole call and its result are not in the next request")
				}
			}
		})
	}
}

// TestToolCallsOfAServerWithoutBlockStopsRun: an Anthropic-compatible server
// that sends no content_block_stop at all says nothing about where a block
// ended. A step that stopped to call its tools has them run, as before the
// provider could report a call unfinished.
func TestToolCallsOfAServerWithoutBlockStopsRun(t *testing.T) {
	const callID, tool = "call_plan", "coddy_todo_plan_read"
	open := func(block []sseFrame) []sseFrame { return block[:len(block)-1] } // without its content_block_stop
	answer := anthropicWireMessage("tool_use", open(anthropicWireText(0, "Reading the plan.")), open(anthropicWireToolUse(1, callID, tool, `{}`)))
	wire, p := wireProvider(t, "anthropic", answer, wireDoneAnswer("anthropic"))
	ag, st, sender := cutCallAgent(t, p, nil)

	stop, err := runOutputLimitTurn(ag)
	if err != nil || stop != string(acp.StopReasonEndTurn) {
		t.Fatalf("stop = %q, err = %v; want end_turn", stop, err)
	}
	bodies := wire.requests()
	if len(bodies) != 2 {
		t.Fatalf("provider requests = %d, want 2: the call runs and its result goes back", len(bodies))
	}
	if n := countString(wireStrings(t, bodies[1]), callID); n < 2 {
		t.Fatalf("the second request carries the call id %d times, want the call and its result", n)
	}
	if got := sender.toolStatuses(callID); len(got) == 0 || got[len(got)-1] != "completed" {
		t.Fatalf("statuses of the call = %q, want it completed", got)
	}
	requireWholePairing(t, st.GetMessages())
}

// TestALoneUnfinishedCallEndsTheTurnWithoutAnEmptyAnswerRecovery: a Codex
// response the content filter ended while its only output was a function_call
// item closes that item as "incomplete". The call is dropped and its row closes
// as failed, and the turn ends there: the step did answer, so it is not an empty
// answer to replay or to nudge with a message saying it held no tool call.
func TestALoneUnfinishedCallEndsTheTurnWithoutAnEmptyAnswerRecovery(t *testing.T) {
	const callID, tool = "call_filtered", "write"
	answer := codexWireResponseEndedBy("content_filter",
		codexWireCall(0, callID, tool, "incomplete", `{"path":"`+cutWritePath+`",`, `{"path":"`+cutWritePath+`",`))
	wire, p := wireProvider(t, "codex", answer, wireDoneAnswer("codex"))
	ag, st, sender := cutCallAgent(t, p, nil)

	stop, err := runOutputLimitTurn(ag)
	if err != nil || stop != string(acp.StopReasonEndTurn) {
		t.Fatalf("stop = %q, err = %v; want end_turn", stop, err)
	}
	if n := len(wire.requests()); n != 1 {
		t.Fatalf("provider requests = %d, want 1: no replay and no nudge after a step that only wrote a dropped call", n)
	}
	if got := sender.toolStatuses(callID); len(got) == 0 || got[len(got)-1] != "failed" {
		t.Fatalf("the client was told %q about the row of the unfinished call, want it closed as failed", got)
	}
	requireNoFile(t, st, cutWritePath)
	requireWholePairing(t, st.GetMessages())
}
