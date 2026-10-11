package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

// ToolCall.Incomplete is what a provider said about the end of a call: the
// answer ended inside it. These tests replay the event sequences the two APIs
// document for a response the output limit ended, through the real adapters:
//
//   - Anthropic Messages streaming sends content_block_start, content_block_delta*
//     and content_block_stop for EVERY block, the one max_tokens cut included, then
//     message_delta with stop_reason "max_tokens" and message_stop. A closed block
//     says nothing about a cut, so the adapter leaves Incomplete false and the
//     arguments show the cut.
//   - The OpenAI Responses API (the Codex backend) closes a function_call item the
//     limit cut with response.output_item.done, the arguments written so far and
//     the status "incomplete", then ends the response with response.incomplete and
//     the reason "max_output_tokens".

const (
	cutWriteArguments  = `{"path":"a.go","content":"pack`
	wholeReadArguments = `{"path":"a.go"}`
)

// streamAnthropicEvents streams one Messages answer through the Anthropic SDK
// client the adapter uses and returns what the caller of Stream sees.
func streamAnthropicEvents(t *testing.T, events []string) (*Response, *streamedCalls) {
	t.Helper()
	p, done := anthropicStreamStub(t, anthropicSSE(t, events...))
	defer done()
	seen, onChunk := collectToolCalls()
	resp, err := p.Stream(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil, onChunk)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	return resp, seen
}

// Anthropic closes every content block, the one the output limit cut included:
// the stream does not mark a closed block, whatever its arguments look like.
func TestAnthropicStreamToolUseClosedAtTheOutputLimitIsNotMarkedIncomplete(t *testing.T) {
	for _, tc := range []struct {
		name      string
		blocks    [][]string
		stop      string
		wantCalls []ToolCall
	}{
		{
			name: "cut inside the arguments, after text",
			blocks: [][]string{
				anthropicTextBlock(0, "Writing the file."),
				anthropicToolUseBlock(1, "toolu_w", "write", `{"path":"a.go",`, `"content":"pack`),
			},
			stop:      "max_tokens",
			wantCalls: []ToolCall{{ID: "toolu_w", Name: "write", InputJSON: cutWriteArguments}},
		},
		{
			name:      "cut inside the arguments, as the first block",
			blocks:    [][]string{anthropicToolUseBlock(0, "toolu_w", "write", cutWriteArguments)},
			stop:      "max_tokens",
			wantCalls: []ToolCall{{ID: "toolu_w", Name: "write", InputJSON: cutWriteArguments}},
		},
		{
			name:      "cut before the first argument",
			blocks:    [][]string{anthropicTextBlock(0, "Writing."), anthropicToolUseBlock(1, "toolu_w", "write")},
			stop:      "max_tokens",
			wantCalls: []ToolCall{{ID: "toolu_w", Name: "write", InputJSON: ""}},
		},
		{
			name:      "whole call without arguments and no input_json_delta",
			blocks:    [][]string{anthropicToolUseBlock(0, "toolu_n", "coddy_todo_plan_read")},
			stop:      "max_tokens",
			wantCalls: []ToolCall{{ID: "toolu_n", Name: "coddy_todo_plan_read", InputJSON: ""}},
		},
		{
			name:      "whole call without arguments and an empty input_json_delta",
			blocks:    [][]string{anthropicTextBlock(0, "Reading the plan."), anthropicToolUseBlock(1, "toolu_n", "coddy_todo_plan_read", "")},
			stop:      "max_tokens",
			wantCalls: []ToolCall{{ID: "toolu_n", Name: "coddy_todo_plan_read", InputJSON: ""}},
		},
		{
			name:      "whole call with arguments at the limit",
			blocks:    [][]string{anthropicToolUseBlock(0, "toolu_r", "read", wholeReadArguments)},
			stop:      "max_tokens",
			wantCalls: []ToolCall{{ID: "toolu_r", Name: "read", InputJSON: wholeReadArguments}},
		},
		{
			name: "text then a call, both closed, found behind the text",
			blocks: [][]string{
				anthropicTextBlock(0, "Let me look."),
				anthropicToolUseBlock(1, "toolu_r", "read", wholeReadArguments),
			},
			stop:      "tool_use",
			wantCalls: []ToolCall{{ID: "toolu_r", Name: "read", InputJSON: wholeReadArguments}},
		},
		{
			name: "thinking, text, a whole call and a cut one",
			blocks: [][]string{
				anthropicThinkingBlock(0, "Read first.", "sig"),
				anthropicTextBlock(1, "Reading, then writing."),
				anthropicToolUseBlock(2, "toolu_r", "read", wholeReadArguments),
				anthropicToolUseBlock(3, "toolu_w", "write", cutWriteArguments),
			},
			stop: "max_tokens",
			wantCalls: []ToolCall{
				{ID: "toolu_r", Name: "read", InputJSON: wholeReadArguments},
				{ID: "toolu_w", Name: "write", InputJSON: cutWriteArguments},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// message_start; per block content_block_start, content_block_delta*,
			// content_block_stop; message_delta with the stop reason; message_stop.
			resp, seen := streamAnthropicEvents(t, append(anthropicEvents(tc.blocks...), anthropicMessageEnd(tc.stop)...))
			if !reflect.DeepEqual(resp.ToolCalls, tc.wantCalls) {
				t.Errorf("tool calls = %+v, want %+v (Incomplete false: Anthropic closed every block)", resp.ToolCalls, tc.wantCalls)
			}
			if resp.StopReason != tc.stop {
				t.Errorf("stop reason = %q, want %q", resp.StopReason, tc.stop)
			}
			if !reflect.DeepEqual(seen.completed, tc.wantCalls) {
				t.Errorf("completed chunks = %+v, want %+v", seen.completed, tc.wantCalls)
			}
		})
	}
}

// An Anthropic-compatible gateway may end a tool_use block without its
// content_block_stop. That is NOT how Anthropic itself behaves (it sends the
// stop for every block); it is how a gateway or a proxy that cuts the stream
// shows its end, and the one thing the adapter can tell from the events.
func TestAnthropicCompatibleStreamToolUseWithoutContentBlockStopIsIncomplete(t *testing.T) {
	for _, tc := range []struct {
		name      string
		blocks    [][]string
		wantCalls []ToolCall
	}{
		{
			name:      "arguments that stop partway",
			blocks:    [][]string{anthropicTextBlock(0, "Writing."), anthropicToolUseStart(1, "toolu_w", "write", `{"path":"a.go",`, `"content":"pack`)},
			wantCalls: []ToolCall{{ID: "toolu_w", Name: "write", InputJSON: cutWriteArguments, Incomplete: true}},
		},
		{
			name:      "arguments that happen to parse",
			blocks:    [][]string{anthropicTextBlock(0, "Reading."), anthropicToolUseStart(1, "toolu_r", "read", wholeReadArguments)},
			wantCalls: []ToolCall{{ID: "toolu_r", Name: "read", InputJSON: wholeReadArguments, Incomplete: true}},
		},
		{
			name:      "no arguments at all",
			blocks:    [][]string{anthropicTextBlock(0, "Reading."), anthropicToolUseStart(1, "toolu_n", "coddy_todo_plan_read")},
			wantCalls: []ToolCall{{ID: "toolu_n", Name: "coddy_todo_plan_read", InputJSON: "", Incomplete: true}},
		},
		{
			name: "only the block left open",
			blocks: [][]string{
				anthropicToolUseBlock(0, "toolu_r", "read", wholeReadArguments),
				anthropicToolUseStart(1, "toolu_w", "write", cutWriteArguments),
			},
			wantCalls: []ToolCall{
				{ID: "toolu_r", Name: "read", InputJSON: wholeReadArguments},
				{ID: "toolu_w", Name: "write", InputJSON: cutWriteArguments, Incomplete: true},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp, seen := streamAnthropicEvents(t, append(anthropicEvents(tc.blocks...), anthropicMessageEnd("max_tokens")...))
			if !reflect.DeepEqual(resp.ToolCalls, tc.wantCalls) {
				t.Errorf("tool calls = %+v, want %+v", resp.ToolCalls, tc.wantCalls)
			}
			if resp.StopReason != "max_tokens" {
				t.Errorf("stop reason = %q, want max_tokens", resp.StopReason)
			}
			if !reflect.DeepEqual(seen.completed, tc.wantCalls) {
				t.Errorf("completed chunks = %+v, want %+v", seen.completed, tc.wantCalls)
			}
		})
	}
}

// codexFunctionCall is one function_call output item of a scripted Responses
// API answer.
type codexFunctionCall struct {
	id        string
	name      string
	status    string   // status of the item in output_item.done
	fragments []string // response.function_call_arguments.delta, in order
	arguments string   // arguments of the item in output_item.done
}

// writeCodexFunctionCall writes the events of one function_call output item the
// way the Responses API streams it: output_item.added (in_progress, no
// arguments yet), the argument deltas, and output_item.done with the final item.
// A call that finished is also closed by response.function_call_arguments.done;
// the item the output limit cut is closed by output_item.done alone.
func writeCodexFunctionCall(w io.Writer, index int, c codexFunctionCall) {
	itemID := "fc_" + c.id
	sse(w, "response.output_item.added", map[string]any{
		"output_index": index,
		"item": map[string]any{
			"id": itemID, "type": "function_call", "status": "in_progress",
			"call_id": c.id, "name": c.name, "arguments": "",
		},
	})
	for _, f := range c.fragments {
		sse(w, "response.function_call_arguments.delta", map[string]any{"item_id": itemID, "output_index": index, "delta": f})
	}
	if c.status == "completed" {
		sse(w, "response.function_call_arguments.done", map[string]any{"item_id": itemID, "output_index": index, "arguments": c.arguments})
	}
	item := map[string]any{
		"id": itemID, "type": "function_call", "call_id": c.id, "name": c.name, "arguments": c.arguments,
	}
	if c.status != "" {
		item["status"] = c.status
	}
	sse(w, "response.output_item.done", map[string]any{"output_index": index, "item": item})
}

// writeCodexResponse streams a whole Responses API answer: response.created and
// response.in_progress, the calls, and the terminal event. A response the
// output limit ended is closed by response.incomplete with the reason
// "max_output_tokens"; any other is response.completed.
func writeCodexResponse(w io.Writer, outputLimit bool, calls ...codexFunctionCall) {
	sse(w, "response.created", map[string]any{"response": map[string]any{"id": "resp_1", "status": "in_progress"}})
	sse(w, "response.in_progress", map[string]any{"response": map[string]any{"id": "resp_1", "status": "in_progress"}})
	for i, c := range calls {
		writeCodexFunctionCall(w, i, c)
	}
	usage := map[string]any{"input_tokens": 9, "output_tokens": 4}
	if outputLimit {
		sse(w, "response.incomplete", map[string]any{"response": map[string]any{
			"id": "resp_1", "status": "incomplete",
			"incomplete_details": map[string]any{"reason": "max_output_tokens"},
			"usage":              usage,
		}})
		return
	}
	sse(w, "response.completed", map[string]any{"response": map[string]any{"id": "resp_1", "status": "completed", "usage": usage}})
}

// streamCodexResponse streams the scripted answer through the real adapter.
func streamCodexResponse(t *testing.T, outputLimit bool, calls ...codexFunctionCall) (*Response, *streamedCalls) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		writeCodexResponse(w, outputLimit, calls...)
	}))
	defer srv.Close()
	seen, onChunk := collectToolCalls()
	resp, err := newCodexTestProvider(t, srv.URL).Stream(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil, onChunk)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	return resp, seen
}

// The Codex backend closes a function_call item the output limit cut with the
// status "incomplete" and the arguments written so far, which may be empty or
// happen to parse: the status is the only word on it.
func TestCodexStreamFunctionCallStatusMarksAnIncompleteItem(t *testing.T) {
	for _, tc := range []struct {
		name        string
		outputLimit bool
		call        codexFunctionCall
		wantCall    ToolCall
		wantStop    string
	}{
		{
			name:        "cut inside the arguments",
			outputLimit: true,
			call:        codexFunctionCall{id: "call_w", name: "write", status: "incomplete", fragments: []string{`{"path":"a.go",`, `"content":"pack`}, arguments: cutWriteArguments},
			wantCall:    ToolCall{ID: "call_w", Name: "write", InputJSON: cutWriteArguments, Incomplete: true},
			wantStop:    "max_tokens",
		},
		{
			name:        "cut after arguments that parse",
			outputLimit: true,
			call: codexFunctionCall{
				id: "call_w", name: "write", status: "incomplete",
				fragments: []string{`{"path":"a.go","content":"package a"}`}, arguments: `{"path":"a.go","content":"package a"}`,
			},
			wantCall: ToolCall{ID: "call_w", Name: "write", InputJSON: `{"path":"a.go","content":"package a"}`, Incomplete: true},
			wantStop: "max_tokens",
		},
		{
			name:        "cut before the first argument",
			outputLimit: true,
			call:        codexFunctionCall{id: "call_w", name: "write", status: "incomplete", arguments: ""},
			wantCall:    ToolCall{ID: "call_w", Name: "write", InputJSON: "", Incomplete: true},
			wantStop:    "max_tokens",
		},
		{
			name:        "finished before the limit",
			outputLimit: true,
			call:        codexFunctionCall{id: "call_r", name: "read", status: "completed", fragments: []string{wholeReadArguments}, arguments: wholeReadArguments},
			wantCall:    ToolCall{ID: "call_r", Name: "read", InputJSON: wholeReadArguments},
			wantStop:    "max_tokens",
		},
		{
			name:        "finished before the limit, no arguments",
			outputLimit: true,
			call:        codexFunctionCall{id: "call_n", name: "coddy_todo_plan_read", status: "completed", fragments: []string{`{}`}, arguments: `{}`},
			wantCall:    ToolCall{ID: "call_n", Name: "coddy_todo_plan_read", InputJSON: `{}`},
			wantStop:    "max_tokens",
		},
		{
			name:        "finished before the limit, arguments left empty",
			outputLimit: true,
			call:        codexFunctionCall{id: "call_n", name: "coddy_todo_plan_read", status: "completed", arguments: ""},
			wantCall:    ToolCall{ID: "call_n", Name: "coddy_todo_plan_read", InputJSON: ""},
			wantStop:    "max_tokens",
		},
		{
			name:        "finished and the response completed",
			outputLimit: false,
			call:        codexFunctionCall{id: "call_r", name: "read", status: "completed", fragments: []string{wholeReadArguments}, arguments: wholeReadArguments},
			wantCall:    ToolCall{ID: "call_r", Name: "read", InputJSON: wholeReadArguments},
			wantStop:    "tool_use",
		},
		{
			name:        "item without a status",
			outputLimit: false,
			call:        codexFunctionCall{id: "call_r", name: "read", fragments: []string{wholeReadArguments}, arguments: wholeReadArguments},
			wantCall:    ToolCall{ID: "call_r", Name: "read", InputJSON: wholeReadArguments},
			wantStop:    "tool_use",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp, seen := streamCodexResponse(t, tc.outputLimit, tc.call)
			if want := []ToolCall{tc.wantCall}; !reflect.DeepEqual(resp.ToolCalls, want) {
				t.Errorf("tool calls = %+v, want %+v", resp.ToolCalls, want)
			}
			if resp.StopReason != tc.wantStop {
				t.Errorf("stop reason = %q, want %q", resp.StopReason, tc.wantStop)
			}
			// The call the loop acts on is the one delivered as a chunk too.
			if want := []ToolCall{tc.wantCall}; !reflect.DeepEqual(seen.completed, want) {
				t.Errorf("completed chunks = %+v, want %+v", seen.completed, want)
			}
		})
	}
}

// Only the call the limit cut carries the status: calls written before it were
// closed whole.
func TestCodexStreamOnlyTheItemTheOutputLimitCutIsIncomplete(t *testing.T) {
	resp, _ := streamCodexResponse(t, true,
		codexFunctionCall{id: "call_r", name: "read", status: "completed", fragments: []string{wholeReadArguments}, arguments: wholeReadArguments},
		codexFunctionCall{id: "call_w", name: "write", status: "incomplete", fragments: []string{cutWriteArguments}, arguments: cutWriteArguments},
	)
	want := []ToolCall{
		{ID: "call_r", Name: "read", InputJSON: wholeReadArguments},
		{ID: "call_w", Name: "write", InputJSON: cutWriteArguments, Incomplete: true},
	}
	if !reflect.DeepEqual(resp.ToolCalls, want) {
		t.Errorf("tool calls = %+v, want %+v", resp.ToolCalls, want)
	}
	if resp.StopReason != "max_tokens" {
		t.Errorf("stop reason = %q, want max_tokens", resp.StopReason)
	}
}

// Incomplete is the stream's word for the agent, read right after the answer
// arrives: it is never stored in a transcript or sent back to a provider.
func TestToolCallIncompleteIsNeverSerialized(t *testing.T) {
	raw, err := json.Marshal(ToolCall{ID: "call_1", Name: "write", InputJSON: cutWriteArguments, Incomplete: true})
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"id":"call_1","name":"write","input":"{\"path\":\"a.go\",\"content\":\"pack"}`; string(raw) != want {
		t.Errorf("serialized call = %s, want %s", raw, want)
	}
	var back ToolCall
	if err := json.Unmarshal([]byte(`{"id":"call_1","name":"write","input":"{}","Incomplete":true,"incomplete":true}`), &back); err != nil {
		t.Fatal(err)
	}
	if back.Incomplete {
		t.Errorf("a stored call came back marked incomplete: %+v", back)
	}
}

// TestAnthropicCompatibleStreamWithoutAnyContentBlockStopMarksNothing: a server
// that speaks Anthropic's Messages API but sends no content_block_stop at all
// says nothing about where a block ended, so no call it streams is marked
// Incomplete - at the output limit the arguments alone decide, and a step that
// stopped to call its tools has every call run, as before the flag existed.
func TestAnthropicCompatibleStreamWithoutAnyContentBlockStopMarksNothing(t *testing.T) {
	for _, stop := range []string{"tool_use", "max_tokens", "end_turn"} {
		t.Run(stop, func(t *testing.T) {
			events := append(anthropicEvents(
				anthropicToolUseStart(0, "toolu_r", "read", wholeReadArguments),
				anthropicToolUseStart(1, "toolu_n", "coddy_todo_plan_read"),
			), anthropicMessageEnd(stop)...)
			resp, _ := streamAnthropicEvents(t, events)
			want := []ToolCall{
				{ID: "toolu_r", Name: "read", InputJSON: wholeReadArguments},
				{ID: "toolu_n", Name: "coddy_todo_plan_read", InputJSON: ""},
			}
			if !reflect.DeepEqual(resp.ToolCalls, want) {
				t.Errorf("tool calls = %+v, want %+v", resp.ToolCalls, want)
			}
		})
	}
}
