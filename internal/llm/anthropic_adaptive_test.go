package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/tidwall/gjson"
)

func TestAnthropicAdaptiveRequestJSON(t *testing.T) {
	for _, tc := range []struct {
		name, model, effort, thinking, outputEffort string
		budget                                      int64
	}{
		{name: "opus high", model: "claude-opus-5-5", effort: "high", thinking: "adaptive", outputEffort: "high"},
		{name: "opus default", model: "claude-opus-5-5", thinking: "adaptive", outputEffort: "medium"},
		{name: "opus low", model: "claude-opus-5-5", effort: "low", thinking: "adaptive", outputEffort: "low"},
		{name: "opus medium", model: "claude-opus-5-5", effort: "medium", thinking: "adaptive", outputEffort: "medium"},
		{name: "opus max", model: "claude-opus-5-5", effort: "max", thinking: "adaptive", outputEffort: "max"},
		{name: "opus xhigh", model: "claude-opus-5-5", effort: "xhigh", thinking: "adaptive", outputEffort: "xhigh"},
		{name: "opus custom unchanged", model: "claude-opus-5-5", effort: "custom", thinking: "adaptive", outputEffort: "custom"},
		{name: "legacy high", model: "claude-sonnet-4-5", effort: "high", thinking: "enabled", budget: 6144},
		{name: "legacy default", model: "claude-sonnet-4-5"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := newAnthropicProvider(tc.model, "", "", nil, 8192, 0.5, tc.effort)
			raw, err := json.Marshal(p.buildParams("", nil, nil))
			if err != nil {
				t.Fatal(err)
			}
			if got := gjson.GetBytes(raw, "thinking.type").String(); got != tc.thinking {
				t.Errorf("thinking.type = %q, want %q", got, tc.thinking)
			}
			budget := gjson.GetBytes(raw, "thinking.budget_tokens")
			if budget.Int() != tc.budget || budget.Exists() != (tc.budget != 0) {
				t.Errorf("thinking.budget_tokens = %s, want %d (omitted when zero)", budget.Raw, tc.budget)
			}
			if got := gjson.GetBytes(raw, "output_config.effort").String(); got != tc.outputEffort {
				t.Errorf("output_config.effort = %q, want %q", got, tc.outputEffort)
			}
			if got := gjson.GetBytes(raw, "max_tokens").Int(); got != 8192 {
				t.Errorf("max_tokens = %d, want 8192", got)
			}
			if got := gjson.GetBytes(raw, "temperature").Exists(); got != (tc.thinking == "") {
				t.Errorf("temperature present = %v, want %v", got, tc.thinking == "")
			}
		})
	}
}

func TestAnthropicAdaptiveRejectsOffBeforeRequest(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	p := newAnthropicProvider("claude-opus-5-5", "test", srv.URL, srv.Client(), 8192, 0, "off")
	messages := []Message{{Role: RoleUser, Content: "hi"}}
	if _, err := p.Complete(context.Background(), messages, nil); err == nil || !strings.Contains(err.Error(), "thinking is always enabled") {
		t.Errorf("Complete error = %v, want unsupported off error", err)
	}
	if _, err := p.Stream(context.Background(), messages, nil, func(StreamChunk) {}); err == nil || !strings.Contains(err.Error(), "thinking is always enabled") {
		t.Errorf("Stream error = %v, want unsupported off error", err)
	}
	if got := calls.Load(); got != 0 {
		t.Errorf("provider calls = %d, want zero", got)
	}
}

func TestAnthropicAdaptiveReplaysSignedReasoning(t *testing.T) {
	for _, effort := range []string{"high", ""} {
		t.Run("effort="+effort, func(t *testing.T) {
			p := newAnthropicProvider("claude-opus-5-5", "", "", nil, 8192, 0, effort)
			_, messages, err := p.splitMessages([]Message{
				{Role: RoleUser, Content: "read"},
				{Role: RoleAssistant, Reasoning: "exact reasoning\n", ReasoningSignature: "signed", ToolCalls: []ToolCall{{ID: "call_1", Name: "read", InputJSON: "{}"}}},
				{Role: RoleTool, ToolCallID: "call_1", Content: "result"},
			})
			if err != nil {
				t.Fatal(err)
			}
			raw, err := json.Marshal(p.buildParams("", messages, nil))
			if err != nil {
				t.Fatal(err)
			}
			for path, want := range map[string]string{
				"messages.1.content.0.type":      "thinking",
				"messages.1.content.0.thinking":  "exact reasoning\n",
				"messages.1.content.0.signature": "signed",
				"messages.1.content.1.type":      "tool_use",
				"messages.2.content.0.type":      "tool_result",
			} {
				if got := gjson.GetBytes(raw, path).String(); got != want {
					t.Errorf("%s = %q, want %q", path, got, want)
				}
			}
		})
	}
}
