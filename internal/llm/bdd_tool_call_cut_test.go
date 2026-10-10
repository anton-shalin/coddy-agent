package llm

// Godog harness for features/tool_call_cut_by_provider.feature: the real Codex
// and Anthropic providers against a stub server that streams the events each API
// documents for an answer the output limit ended inside a tool call. The event
// builders are the ones of tool_call_completeness_test.go.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cucumber/godog"
)

type toolCallCutState struct {
	t *testing.T
	// provider is "Codex" or "Anthropic".
	provider string
	name     string
	// fragments are the argument fragments the call streams.
	fragments []string
	// cut says the output limit stopped the answer inside the call.
	cut  bool
	resp *Response
}

func (s *toolCallCutState) reset() {
	s.provider, s.name, s.fragments, s.cut, s.resp = "", "", nil, false, nil
}

func (s *toolCallCutState) answerStartsCall(provider, name string) error {
	s.provider, s.name = provider, name
	return nil
}

func (s *toolCallCutState) callArgumentsStream(arguments string) error {
	s.fragments = append(s.fragments, arguments)
	return nil
}

func (s *toolCallCutState) limitStopsInsideCall() error {
	s.cut = true
	return nil
}

func (s *toolCallCutState) callFinishesBeforeLimit() error {
	s.cut = false
	return nil
}

func (s *toolCallCutState) streamAnswer() error {
	arguments := ""
	for _, f := range s.fragments {
		arguments += f
	}
	var resp *Response
	var err error
	switch s.provider {
	case "Codex":
		call := codexFunctionCall{id: "call_1", name: s.name, status: "completed", fragments: s.fragments, arguments: arguments}
		if s.cut {
			call.status = "incomplete"
		}
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			writeCodexResponse(w, true, call)
		}))
		defer srv.Close()
		resp, err = newCodexTestProvider(s.t, srv.URL).Stream(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil, func(StreamChunk) {})
	case "Anthropic":
		// Anthropic closes every block, the one the limit cut included, so the
		// two cases differ in the arguments only.
		events := append(anthropicEvents(anthropicToolUseBlock(0, "toolu_1", s.name, s.fragments...)), anthropicMessageEnd("max_tokens")...)
		sse := anthropicSSE(s.t, events...)
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, sse)
		}))
		defer srv.Close()
		resp, err = newAnthropicProvider("test-model", "key", srv.URL, nil, 0, 0, "").Stream(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil, func(StreamChunk) {})
	default:
		return fmt.Errorf("unknown provider %q", s.provider)
	}
	if err != nil {
		return fmt.Errorf("stream failed: %w", err)
	}
	s.resp = resp
	return nil
}

func (s *toolCallCutState) onlyCall(name string) (ToolCall, error) {
	if len(s.resp.ToolCalls) != 1 {
		return ToolCall{}, fmt.Errorf("tool calls = %+v, want one", s.resp.ToolCalls)
	}
	call := s.resp.ToolCalls[0]
	if call.Name != name {
		return ToolCall{}, fmt.Errorf("tool call = %q, want %q", call.Name, name)
	}
	return call, nil
}

func (s *toolCallCutState) answerStopsAt(reason string) error {
	if s.resp.StopReason != reason {
		return fmt.Errorf("stop reason = %q, want %q", s.resp.StopReason, reason)
	}
	return nil
}

func (s *toolCallCutState) callIsReportedCut(name string) error {
	call, err := s.onlyCall(name)
	if err != nil {
		return err
	}
	if !call.Incomplete {
		return fmt.Errorf("call %+v is not reported cut off", call)
	}
	return nil
}

func (s *toolCallCutState) callIsNotReportedCut(name string) error {
	call, err := s.onlyCall(name)
	if err != nil {
		return err
	}
	if call.Incomplete {
		return fmt.Errorf("call %+v is reported cut off", call)
	}
	return nil
}

func (s *toolCallCutState) callKeepsArguments(arguments string) error {
	if got := s.resp.ToolCalls[0].InputJSON; got != arguments {
		return fmt.Errorf("arguments = %q, want %q", got, arguments)
	}
	return nil
}

func (s *toolCallCutState) callKeepsNoArguments() error {
	return s.callKeepsArguments("")
}

func (s *toolCallCutState) argumentsAreNotWhole() error {
	if got := s.resp.ToolCalls[0].InputJSON; got == "" || json.Valid([]byte(got)) {
		return fmt.Errorf("arguments %q are a whole JSON document or empty", got)
	}
	return nil
}

func TestToolCallCutByProviderFeature(t *testing.T) {
	s := &toolCallCutState{t: t}
	suite := godog.TestSuite{
		Name: "tool-call-cut-by-provider",
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
				s.reset()
				return ctx, nil
			})
			sc.Step(`^an? (Codex|Anthropic) answer that starts a call to "([^"]*)"$`, s.answerStartsCall)
			sc.Step(`^the call's arguments stream as (.+)$`, s.callArgumentsStream)
			sc.Step(`^the output limit stops the answer inside the call$`, s.limitStopsInsideCall)
			sc.Step(`^the call finishes before the output limit stops the answer$`, s.callFinishesBeforeLimit)
			sc.Step(`^Coddy streams the answer$`, s.streamAnswer)
			sc.Step(`^the answer stops at "([^"]*)"$`, s.answerStopsAt)
			sc.Step(`^the call to "([^"]*)" is reported cut off$`, s.callIsReportedCut)
			sc.Step(`^the call to "([^"]*)" is not reported cut off$`, s.callIsNotReportedCut)
			sc.Step(`^the call keeps the arguments (.+)$`, s.callKeepsArguments)
			sc.Step(`^the call keeps no arguments$`, s.callKeepsNoArguments)
			sc.Step(`^the arguments are not a whole JSON document$`, s.argumentsAreNotWhole)
		},
		Options: &godog.Options{Format: "pretty", Paths: []string{"../../features/tool_call_cut_by_provider.feature"}, TestingT: t, Strict: true},
	}
	if suite.Run() != 0 {
		t.Fatal("tool call cut by provider feature suite failed")
	}
}
