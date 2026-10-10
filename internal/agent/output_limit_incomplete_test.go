package agent

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// What the provider says about the end of a tool call (llm.ToolCall.Incomplete)
// decides, together with the arguments, whether the output limit cut the call
// off. The unit table pins the rule; the wire tests below drive the real
// Anthropic and Codex adapters with the event sequences the two APIs document
// for a response the output limit ended:
//
//   - Anthropic Messages streaming: message_start; per content block
//     content_block_start, content_block_delta*, content_block_stop - for every
//     block, the one max_tokens cut included; message_delta with the stop reason
//     "max_tokens"; message_stop.
//   - OpenAI Responses API (the Codex backend): response.created;
//     response.output_item.added (function_call, in_progress);
//     response.function_call_arguments.delta*; response.output_item.done (status
//     "incomplete" and the arguments written so far for the item the limit cut);
//     response.incomplete with the reason "max_output_tokens".

// TestCutOffToolCallProviderWord: the last call of a step that stopped at the
// output limit is cut off when its provider said so (Incomplete) or when its
// arguments are there and are not a whole JSON document; nothing else counts.
func TestCutOffToolCallProviderWord(t *testing.T) {
	call := func(id, args string, incomplete bool) llm.ToolCall {
		return llm.ToolCall{ID: id, Name: "write", InputJSON: args, Incomplete: incomplete}
	}
	limit := func(calls ...llm.ToolCall) *llm.Response {
		return &llm.Response{StopReason: "max_tokens", ToolCalls: calls}
	}
	for _, tc := range []struct {
		name     string
		response *llm.Response
		wantCut  bool
		wantID   string
	}{
		{"provider said cut, arguments parse", limit(call("c1", `{"a":1}`, true)), true, "c1"},
		{"provider said cut, no arguments", limit(call("c1", "", true)), true, "c1"},
		{"provider said cut, blank arguments", limit(call("c1", " \n", true)), true, "c1"},
		{"provider said cut, arguments stop partway", limit(call("c1", `{"a":`, true)), true, "c1"},
		{"provider said nothing, no arguments", limit(call("c1", "", false)), false, ""},
		{"provider said nothing, blank arguments", limit(call("c1", " \n", false)), false, ""},
		{"provider said nothing, arguments parse", limit(call("c1", `{"a":1}`, false)), false, ""},
		{"provider said nothing, arguments stop partway", limit(call("c1", `{"a":`, false)), true, "c1"},
		{"another stop reason, provider said cut", &llm.Response{StopReason: "tool_use", ToolCalls: []llm.ToolCall{call("c1", `{}`, true)}}, false, ""},
		{"no stop reason, provider said cut", &llm.Response{ToolCalls: []llm.ToolCall{call("c1", `{}`, true)}}, false, ""},
		{"end of turn, provider said cut", &llm.Response{StopReason: "end_turn", ToolCalls: []llm.ToolCall{call("c1", "", true)}}, false, ""},
		{"only the last call counts: an earlier one cut, the last whole", limit(call("c1", `{"a":`, true), call("c2", `{}`, false)), false, ""},
		{"only the last call counts: an earlier one cut, the last said cut", limit(call("c1", `{"a":`, true), call("c2", "", true)), true, "c2"},
		{"only the last call counts: an earlier one whole, the last said cut", limit(call("c1", `{}`, false), call("c2", `{}`, true)), true, "c2"},
		{"only the last call counts: an earlier one said cut, the last stops partway", limit(call("c1", `{}`, true), call("c2", `{"b":`, false)), true, "c2"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, cut := cutOffToolCall(tc.response)
			if cut != tc.wantCut || (cut && got.ID != tc.wantID) {
				t.Fatalf("cutOffToolCall = %+v, %v; want cut %v by call %q", got, cut, tc.wantCut, tc.wantID)
			}
		})
	}
}

// sseFrame is one server-sent event of a scripted provider answer.
type sseFrame struct {
	event string
	data  map[string]any
}

// writeSSEFrames renders frames as the wire carries them. The event name is the
// "type" of the data, as both APIs write it.
func writeSSEFrames(frames []sseFrame) string {
	var b strings.Builder
	for _, f := range frames {
		f.data["type"] = f.event
		raw, err := json.Marshal(f.data)
		if err != nil {
			panic(err)
		}
		fmt.Fprintf(&b, "event: %s\ndata: %s\n\n", f.event, raw)
	}
	return b.String()
}

// anthropicWireBlock is one content block of a Messages answer, closed the way
// Anthropic closes every block.
func anthropicWireBlock(index int, start map[string]any, deltas ...map[string]any) []sseFrame {
	frames := []sseFrame{{"content_block_start", map[string]any{"index": index, "content_block": start}}}
	for _, d := range deltas {
		frames = append(frames, sseFrame{"content_block_delta", map[string]any{"index": index, "delta": d}})
	}
	return append(frames, sseFrame{"content_block_stop", map[string]any{"index": index}})
}

func anthropicWireText(index int, text string) []sseFrame {
	return anthropicWireBlock(index, map[string]any{"type": "text", "text": ""}, map[string]any{"type": "text_delta", "text": text})
}

// anthropicWireToolUse is a client tool_use block: its input streams as the
// given input_json_delta fragments.
func anthropicWireToolUse(index int, id, name string, fragments ...string) []sseFrame {
	var deltas []map[string]any
	for _, f := range fragments {
		deltas = append(deltas, map[string]any{"type": "input_json_delta", "partial_json": f})
	}
	return anthropicWireBlock(index, map[string]any{"type": "tool_use", "id": id, "name": name, "input": map[string]any{}}, deltas...)
}

// anthropicWireMessage is a whole streamed Messages answer: message_start, the
// blocks, message_delta with the stop reason, message_stop.
func anthropicWireMessage(stop string, blocks ...[]sseFrame) string {
	frames := []sseFrame{{"message_start", map[string]any{"message": map[string]any{
		"id": "msg_fixture", "type": "message", "role": "assistant", "model": "test-model", "content": []any{},
		"stop_reason": nil, "stop_sequence": nil, "usage": map[string]any{"input_tokens": 3, "output_tokens": 0},
	}}}}
	for _, b := range blocks {
		frames = append(frames, b...)
	}
	frames = append(frames,
		sseFrame{"message_delta", map[string]any{"delta": map[string]any{"stop_reason": stop, "stop_sequence": nil}, "usage": map[string]any{"output_tokens": 5}}},
		sseFrame{"message_stop", map[string]any{}},
	)
	return writeSSEFrames(frames)
}

// codexWireCall is one function_call output item of a Responses API answer: it
// is announced in_progress, its arguments stream, and the closing item carries
// the final arguments and the status.
func codexWireCall(index int, id, name, status, finalArguments string, fragments ...string) []sseFrame {
	itemID := "fc_" + id
	frames := []sseFrame{{"response.output_item.added", map[string]any{"output_index": index, "item": map[string]any{
		"id": itemID, "type": "function_call", "status": "in_progress", "call_id": id, "name": name, "arguments": "",
	}}}}
	for _, f := range fragments {
		frames = append(frames, sseFrame{"response.function_call_arguments.delta", map[string]any{"item_id": itemID, "output_index": index, "delta": f}})
	}
	if status == "completed" {
		frames = append(frames, sseFrame{"response.function_call_arguments.done", map[string]any{"item_id": itemID, "output_index": index, "arguments": finalArguments}})
	}
	return append(frames, sseFrame{"response.output_item.done", map[string]any{"output_index": index, "item": map[string]any{
		"id": itemID, "type": "function_call", "status": status, "call_id": id, "name": name, "arguments": finalArguments,
	}}})
}

// codexWireResponse is a whole streamed Responses answer. A response the output
// limit ended closes with response.incomplete and the reason "max_output_tokens".
func codexWireResponse(outputLimit bool, items ...[]sseFrame) string {
	created := map[string]any{"response": map[string]any{"id": "resp_fixture", "status": "in_progress"}}
	frames := []sseFrame{{"response.created", created}, {"response.in_progress", map[string]any{"response": map[string]any{"id": "resp_fixture", "status": "in_progress"}}}}
	for _, i := range items {
		frames = append(frames, i...)
	}
	usage := map[string]any{"input_tokens": 9, "output_tokens": 4}
	if outputLimit {
		frames = append(frames, sseFrame{"response.incomplete", map[string]any{"response": map[string]any{
			"id": "resp_fixture", "status": "incomplete", "incomplete_details": map[string]any{"reason": "max_output_tokens"}, "usage": usage,
		}}})
	} else {
		frames = append(frames, sseFrame{"response.completed", map[string]any{"response": map[string]any{"id": "resp_fixture", "status": "completed", "usage": usage}}})
	}
	return writeSSEFrames(frames)
}

// cutCallWire is a provider API on a test server: it answers its n-th request
// with answers[n] (the last repeats) and keeps the request bodies.
type cutCallWire struct {
	mu     sync.Mutex
	bodies []string
}

func (w *cutCallWire) requests() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]string(nil), w.bodies...)
}

// wireProvider starts the server and returns the real adapter of kind
// ("anthropic" or "codex") pointed at it, as llm.NewProvider builds it for a
// configured row.
func wireProvider(t *testing.T, kind string, answers ...string) (*cutCallWire, llm.Provider) {
	t.Helper()
	wire := &cutCallWire{}
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request: %v", err)
			return
		}
		wire.mu.Lock()
		n := len(wire.bodies)
		wire.bodies = append(wire.bodies, string(body))
		wire.mu.Unlock()
		rw.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(rw, answers[min(n, len(answers)-1)])
	}))
	t.Cleanup(srv.Close)

	input := llm.ProviderInput{Type: kind, Model: "test-model", APIKey: "test-key", BaseURL: srv.URL, MaxTokens: 4096, RetryDisabled: true}
	if kind == "codex" {
		authPath := filepath.Join(t.TempDir(), "auth.json")
		payload := base64.RawURLEncoding.EncodeToString([]byte(`{"exp":` + strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10) + `}`))
		auth := fmt.Sprintf(`{"auth_mode":"chatgpt","tokens":{"access_token":"eyJhbGciOiJub25lIn0.%s.sig","refresh_token":"refresh","account_id":"account"}}`, payload)
		if err := os.WriteFile(authPath, []byte(auth), 0o600); err != nil {
			t.Fatal(err)
		}
		input.AuthPath = authPath
		input.NoCLILogin = true
		t.Setenv(llm.EnvCodexBaseURL, srv.URL)
	}
	p, err := llm.NewProvider(input)
	if err != nil {
		t.Fatalf("NewProvider(%s): %v", kind, err)
	}
	return wire, p
}

// wireStrings is every string of a request body, keys aside: the texts of the
// messages and the ids of the calls and results, whatever the API calls them.
func wireStrings(t *testing.T, body string) []string {
	t.Helper()
	var doc any
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("request body is not JSON: %v", err)
	}
	var out []string
	var walk func(v any)
	walk = func(v any) {
		switch v := v.(type) {
		case string:
			out = append(out, v)
		case []any:
			for _, e := range v {
				walk(e)
			}
		case map[string]any:
			for _, e := range v {
				walk(e)
			}
		}
	}
	walk(doc)
	return out
}

func countString(strs []string, want string) int {
	var n int
	for _, s := range strs {
		if s == want {
			n++
		}
	}
	return n
}

// wireDoneAnswer is the answer that ends the turn: a sentence and the normal end
// of the response.
func wireDoneAnswer(kind string) string {
	if kind == "anthropic" {
		return anthropicWireMessage("end_turn", anthropicWireText(0, "Done."))
	}
	return writeSSEFrames([]sseFrame{
		{"response.created", map[string]any{"response": map[string]any{"id": "resp_done", "status": "in_progress"}}},
		{"response.output_text.delta", map[string]any{"delta": "Done."}},
		{"response.completed", map[string]any{"response": map[string]any{"id": "resp_done", "status": "completed", "usage": map[string]any{"input_tokens": 9, "output_tokens": 1}}}},
	})
}

// toolRowAnnounced says whether the client was shown a pending row for a call.
func toolRowAnnounced(sender *outputLimitSender, id string) bool {
	sender.mu.Lock()
	defer sender.mu.Unlock()
	for _, u := range sender.updates {
		if row, ok := u.(acp.ToolCallUpdate); ok && row.ToolCallID == id {
			return true
		}
	}
	return false
}

// TestOutputLimitWholeCallWithoutArgumentsRunsOnTheWire: a call that takes no
// arguments and was written whole before the step stopped at the output limit
// is not a cut call. Anthropic hands it over with an empty input, the Codex
// backend with "{}" or, from a lenient server, an empty string; it runs, its
// result is in the next request, and nobody is asked to make it again.
func TestOutputLimitWholeCallWithoutArgumentsRunsOnTheWire(t *testing.T) {
	const callID, tool = "call_plan", "coddy_todo_plan_read"
	for _, tc := range []struct {
		name   string
		kind   string
		answer string
	}{
		{
			name: "Anthropic, content_block_stop and an empty input_json_delta",
			kind: "anthropic",
			answer: anthropicWireMessage("max_tokens",
				anthropicWireText(0, "Reading the plan."),
				anthropicWireToolUse(1, callID, tool, "")),
		},
		{
			name:   "Codex, completed item with {} arguments",
			kind:   "codex",
			answer: codexWireResponse(true, codexWireCall(0, callID, tool, "completed", `{}`, `{}`)),
		},
		{
			name:   "Codex, completed item with empty arguments",
			kind:   "codex",
			answer: codexWireResponse(true, codexWireCall(0, callID, tool, "completed", "")),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wire, p := wireProvider(t, tc.kind, tc.answer, wireDoneAnswer(tc.kind))
			ag, st, sender := cutCallAgent(t, p, nil)

			stop, err := runOutputLimitTurn(ag)
			if err != nil || stop != string(acp.StopReasonEndTurn) {
				t.Fatalf("stop = %q, err = %v; want end_turn", stop, err)
			}
			bodies := wire.requests()
			if len(bodies) != 2 {
				t.Fatalf("provider requests = %d, want 2: the call runs and its result goes back", len(bodies))
			}
			second := wireStrings(t, bodies[1])
			if n := countString(second, callID); n < 2 {
				t.Fatalf("the second request carries the call id %d times, want the call and its result", n)
			}
			for _, nudge := range []string{cutOffToolCallNudge(tool), outputLimitNudge, outputLimitContinueNudge} {
				if countString(second, nudge) != 0 {
					t.Fatalf("the second request carries a message for a step that was cut: %q", nudge)
				}
			}
			statuses := sender.toolStatuses(callID)
			if len(statuses) == 0 || statuses[len(statuses)-1] != "completed" {
				t.Fatalf("statuses of the call = %q, want it completed", statuses)
			}
			meta, err := session.ReadToolCallMeta(st.GetPersistedSessionDir(), callID)
			if err != nil || meta.Status == "failed" {
				t.Fatalf("tool-call record = %+v, %v; want a call that ran", meta, err)
			}
			if got, err := session.ReadToolCallResult(st.GetPersistedSessionDir(), callID); err != nil || strings.Contains(got, "not run") {
				t.Fatalf("tool-call result = %q, %v; want the tool's own result", got, err)
			}
			requireWholePairing(t, st.GetMessages())
			if notice := st.TakeTurnStopNotice(); notice != "" {
				t.Fatalf("a turn whose call ran left a stop notice: %q", notice)
			}
		})
	}
}

// TestOutputLimitCutCallIsNotRunOnTheWire: a call the provider's events show was
// cut off is not run, whatever its arguments look like. Its pending row closes
// as failed with the reason, the tool-call record says it was not run, the call
// is nowhere in the next request, and that request ends with the message that
// names the tool.
func TestOutputLimitCutCallIsNotRunOnTheWire(t *testing.T) {
	const callID, tool = "call_cut", "write"
	// If the call ran, this file would be written: the permission client says yes.
	const wholeWrite = `{"path":"` + cutWritePath + `","content":"package module // CUTMARK"}`
	for _, tc := range []struct {
		name   string
		kind   string
		answer string
	}{
		{
			// The documented Anthropic stream: the cut block is closed like every
			// block, so the arguments are all that shows the cut.
			name: "Anthropic, content_block_stop and arguments that stop partway",
			kind: "anthropic",
			answer: anthropicWireMessage("max_tokens",
				anthropicWireText(0, "Writing the module."),
				anthropicWireToolUse(1, callID, tool, `{"path":"`+cutWritePath+`",`, `"content":"package module // CUTMARK\n\nfunc Run() {`)),
		},
		{
			// Not Anthropic's own behaviour: a gateway that cuts the stream leaves the
			// block without its content_block_stop, and arguments that parse say
			// nothing against that.
			name: "Anthropic-compatible gateway, no content_block_stop and arguments that parse",
			kind: "anthropic",
			answer: func() string {
				block := anthropicWireToolUse(1, callID, tool, wholeWrite)
				return anthropicWireMessage("max_tokens", anthropicWireText(0, "Writing the module."), block[:len(block)-1])
			}(),
		},
		{
			name: "Codex, status incomplete and arguments that parse",
			kind: "codex",
			answer: codexWireResponse(true,
				codexWireCall(0, callID, tool, "incomplete", wholeWrite, wholeWrite)),
		},
		{
			name: "Codex, status incomplete and arguments that stop partway",
			kind: "codex",
			answer: codexWireResponse(true,
				codexWireCall(0, callID, tool, "incomplete", `{"path":"`+cutWritePath+`","content":"package module // CUTMARK`, `{"path":"`+cutWritePath+`",`, `"content":"package module // CUTMARK`)),
		},
		{
			name:   "Codex, status incomplete and no arguments yet",
			kind:   "codex",
			answer: codexWireResponse(true, codexWireCall(0, callID, tool, "incomplete", "")),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wire, p := wireProvider(t, tc.kind, tc.answer, wireDoneAnswer(tc.kind))
			ag, st, sender := cutCallAgent(t, p, nil)

			stop, err := runOutputLimitTurn(ag)
			if err != nil || stop != string(acp.StopReasonEndTurn) {
				t.Fatalf("stop = %q, err = %v; want end_turn", stop, err)
			}
			bodies := wire.requests()
			if len(bodies) != 2 {
				t.Fatalf("provider requests = %d, want 2: the step, then the request that asks for smaller calls", len(bodies))
			}
			second := wireStrings(t, bodies[1])
			if countString(second, cutOffToolCallNudge(tool)) != 1 {
				t.Fatalf("the second request does not carry the message for a cut call naming %q", tool)
			}
			if n := countString(second, callID); n != 0 {
				t.Fatalf("the second request carries the cut call or a result for it (%d times)", n)
			}
			if strings.Contains(bodies[1], "CUTMARK") {
				t.Fatalf("the half-written arguments reached the second request")
			}

			if !toolRowAnnounced(sender, callID) {
				t.Fatal("the call was never announced as a pending row; the test proves nothing")
			}
			// The row stays pending while the arguments stream, then it is over:
			// closed as failed, never running or completed.
			got := sender.toolStatuses(callID)
			if len(got) == 0 || got[len(got)-1] != "failed" {
				t.Fatalf("the client was told %q about the row of the cut-off call, want it closed as failed", got)
			}
			for _, status := range got[:len(got)-1] {
				if status != "pending" {
					t.Fatalf("the client was told %q about the row of the cut-off call, want pending and then failed", got)
				}
			}
			dir := st.GetPersistedSessionDir()
			meta, err := session.ReadToolCallMeta(dir, callID)
			if err != nil || meta.Status != "failed" {
				t.Fatalf("tool-call record = %+v, %v; want status failed", meta, err)
			}
			if res, err := session.ReadToolCallResult(dir, callID); err != nil || !strings.HasPrefix(res, "not run:") {
				t.Fatalf("tool-call result = %q, %v; want %q", res, err, cutOffToolCallResult)
			}
			requireNoFile(t, st, cutWritePath)
			if n := sender.permissionRequests(); n != 0 {
				t.Fatalf("the client was asked %d times to approve a call that never ran", n)
			}
			stored := st.GetMessages()
			if ids := callIDs(stored); len(ids) != 0 {
				t.Fatalf("the transcript keeps the cut-off call: %v", ids)
			}
			requireWholePairing(t, stored)
			if notice := st.TakeTurnStopNotice(); notice != "" {
				t.Fatalf("a recovered turn left a stop notice: %q", notice)
			}
		})
	}
}
