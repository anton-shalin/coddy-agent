//go:build http

package httpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
	"github.com/tidwall/gjson"
)

func TestChatCompletionsAdaptiveSmallCap(t *testing.T) {
	var mu sync.Mutex
	var requests []string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		mu.Lock()
		requests = append(requests, string(body))
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"msg_1","type":"message","role":"assistant","model":"claude-opus-5-5","content":[{"type":"text","text":"done"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
	defer backend.Close()
	root := t.TempDir()
	cfg := &config.Config{
		Paths:     config.Paths{Home: t.TempDir(), CWD: root},
		Providers: []config.ProviderConfig{{Name: "reviewer", Type: "anthropic", APIBase: backend.URL, APIKey: "test", Proxy: "none"}},
		Models: []config.ModelEntry{
			{Model: "reviewer/claude-opus-5-5", MaxTokens: 8192, ReasoningDefault: "high", AllowReasoningOff: true},
			{Model: "reviewer/claude-sonnet-4-5", MaxTokens: 8192, ReasoningDefault: "high"},
		},
		Agent: config.Agent{Model: "reviewer/claude-opus-5-5"},
	}
	mgr := session.NewManager(cfg, noopSender{}, nil, slog.Default(), root, &session.FileStore{Root: t.TempDir()})
	srv := New(cfg, mgr, slog.Default(), root)
	defer srv.Drain()
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	for _, field := range []string{"max_tokens", "max_completion_tokens"} {
		for _, model := range []string{"claude-opus-5-5", "claude-sonnet-4-5"} {
			body := fmt.Sprintf(`{"model":"reviewer/%s","messages":[{"role":"user","content":"hi"}],"%s":512,"stream":false}`, model, field)
			res, err := http.Post(ts.URL+"/v1/chat/completions", "application/json", strings.NewReader(body))
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := ioReadAllClose(res.Body)
			wantStatus := http.StatusOK
			if model == "claude-sonnet-4-5" {
				wantStatus = http.StatusBadRequest
			}
			if res.StatusCode != wantStatus {
				t.Fatalf("%s/%s: status %d, want %d: %s", model, field, res.StatusCode, wantStatus, raw)
			}
		}
	}
	for _, stream := range []bool{false, true} {
		body := fmt.Sprintf(`{"model":"reviewer/claude-opus-5-5","messages":[{"role":"user","content":"hi"}],"reasoning_effort":"off","stream":%v}`, stream)
		res, err := http.Post(ts.URL+"/v1/chat/completions", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := ioReadAllClose(res.Body)
		if res.StatusCode != http.StatusBadRequest || !strings.Contains(gjson.GetBytes(raw, "error.message").String(), "thinking is always enabled") {
			t.Fatalf("off (stream %v): status %d, want 400 unsupported off: %s", stream, res.StatusCode, raw)
		}
		if sid := res.Header.Get("X-Coddy-Session-ID"); sid != "" {
			t.Errorf("off request created session %s", sid)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(requests) != 2 {
		t.Fatalf("provider requests = %d, want two adaptive requests", len(requests))
	}
	for _, raw := range requests {
		if gjson.Get(raw, "max_tokens").Int() != 512 || gjson.Get(raw, "thinking.type").String() != "adaptive" || gjson.Get(raw, "thinking.budget_tokens").Exists() || gjson.Get(raw, "output_config.effort").String() != "high" {
			t.Errorf("adaptive cap or effort changed: %s", raw)
		}
	}
}

func adaptiveSettingsServer(t *testing.T, model, reasoning string) (*Server, *session.State, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		http.Error(w, "unexpected provider call", http.StatusInternalServerError)
	}))
	t.Cleanup(backend.Close)
	root := t.TempDir()
	cfg := &config.Config{
		Paths:     config.Paths{Home: t.TempDir(), CWD: root},
		Providers: []config.ProviderConfig{{Name: "reviewer", Type: "anthropic", APIBase: backend.URL, APIKey: "test", Proxy: "none"}},
		Models: []config.ModelEntry{
			{Model: "reviewer/claude-opus-5-5", ReasoningDefault: "high", AllowReasoningOff: true},
			{Model: "reviewer/claude-sonnet-4-5", ReasoningDefault: "high", AllowReasoningOff: true},
		},
		Agent: config.Agent{Model: "reviewer/claude-opus-5-5"},
	}
	runner := func(_ context.Context, st *session.State, _ []acp.ContentBlock, _ acp.UpdateSender) (string, error) {
		calls.Add(1)
		st.AddMessage(llm.Message{Role: llm.RoleAssistant, Content: "done"})
		return string(acp.StopReasonEndTurn), nil
	}
	mgr := session.NewManager(cfg, noopSender{}, runner, slog.Default(), root, &session.FileStore{Root: t.TempDir()})
	st, err := mgr.EnsureHTTPSession(context.Background(), "sess-adaptive-settings", root)
	if err != nil {
		t.Fatal(err)
	}
	st.SetSelectedModelID("reviewer/" + model)
	st.SetSelectedReasoning(reasoning)
	st.AddMessage(llm.Message{Role: llm.RoleUser, Content: "existing"})
	mgr.PublishSessionSettings("sess-adaptive-settings", st, "", "web")
	mgr.PublishSessionSettings("sess-adaptive-settings", st, "", "web")
	srv := New(cfg, mgr, slog.Default(), root)
	t.Cleanup(srv.Drain)
	return srv, st, &calls
}

func TestAdaptiveProfileSettingsAdmission(t *testing.T) {
	for _, path := range []string{"/v1/chat/completions", "/v1/responses"} {
		for _, stream := range []bool{false, true} {
			for _, tc := range []struct {
				name, model, reasoning, metadata string
				allowed                          bool
			}{
				{"explicit off", "claude-opus-5-5", "high", `{"reasoning":"off"}`, false},
				{"model and off", "claude-sonnet-4-5", "high", `{"model":"reviewer/claude-opus-5-5","reasoning":"off"}`, false},
				{"persisted off", "claude-opus-5-5", "off", `{}`, false},
				{"stale off", "claude-opus-5-5", "off", `{"settingsVersion":1,"reasoning":"high"}`, false},
				{"model carrying off", "claude-sonnet-4-5", "off", `{"model":"reviewer/claude-opus-5-5"}`, false},
				{"slash off", "claude-opus-5-5", "high", `{}`, false},
				{"slash combined off", "claude-sonnet-4-5", "high", `{}`, false},
				{"slash legacy off", "claude-sonnet-4-5", "high", `{}`, true},
				{"pending off", "claude-sonnet-4-5", "high", `{}`, false},
				{"stale pending off", "claude-sonnet-4-5", "high", `{"settingsVersion":1}`, false},
				{"high", "claude-sonnet-4-5", "off", `{"model":"reviewer/claude-opus-5-5","reasoning":"high"}`, true},
				{"default clears off", "claude-opus-5-5", "off", `{"reasoning":null}`, true},
				{"legacy off", "claude-sonnet-4-5", "high", `{"reasoning":"off"}`, true},
			} {
				t.Run(fmt.Sprintf("%s/stream=%v/%s", path, stream, tc.name), func(t *testing.T) {
					srv, st, calls := adaptiveSettingsServer(t, tc.model, tc.reasoning)
					if strings.Contains(tc.name, "pending") {
						st.ArmTurnOverride(session.SettingModel, "reviewer/claude-opus-5-5", 1)
						st.ArmTurnOverride(session.SettingReasoning, "off", 1)
					}
					beforeOverrides := st.TurnOverrides()
					beforeModel, beforeReasoning, beforeMode := st.GetSelectedModelID(), st.GetSelectedReasoning(), st.GetMode()
					beforeVersion, beforeMessages := st.PublishedSettingsVersion(), st.GetMessages()
					prompt := "hi"
					if strings.HasPrefix(tc.name, "slash") {
						prompt = "/reasoning off hi"
					}
					if tc.name == "slash combined off" {
						prompt = "/model reviewer/claude-opus-5-5 /reasoning off --once hi"
					}
					body := fmt.Sprintf(`{"model":"ask","messages":[{"role":"user","content":%q}],"input":%q,"stream":%v,"metadata":%s}`, prompt, prompt, stream, tc.metadata)
					req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
					req.Header.Set("X-Coddy-Session-ID", "sess-adaptive-settings")
					rec := httptest.NewRecorder()
					srv.Handler().ServeHTTP(rec, req)
					if tc.allowed {
						if rec.Code != http.StatusOK || calls.Load() != 1 {
							t.Fatalf("valid profile: status=%d calls=%d body=%s", rec.Code, calls.Load(), rec.Body.String())
						}
						return
					}
					if rec.Code != http.StatusBadRequest || strings.Contains(rec.Header().Get("Content-Type"), "text/event-stream") {
						t.Errorf("status=%d content-type=%q, want JSON400 before SSE: %s", rec.Code, rec.Header().Get("Content-Type"), rec.Body.String())
					}
					if calls.Load() != 0 || st.GetSelectedModelID() != beforeModel || st.GetSelectedReasoning() != beforeReasoning || st.GetMode() != beforeMode || st.PublishedSettingsVersion() != beforeVersion || !reflect.DeepEqual(st.GetMessages(), beforeMessages) || !reflect.DeepEqual(st.TurnOverrides(), beforeOverrides) {
						t.Error("rejected profile called provider/runner or changed session state")
					}
				})
			}
		}
	}
}

func TestAdaptiveArmedSettingsAdmission(t *testing.T) {
	srv, st, calls := adaptiveSettingsServer(t, "claude-sonnet-4-5", "high")
	code, body := patchSessionJSON(t, srv, "sess-adaptive-settings", map[string]interface{}{"selectedModelId": "reviewer/claude-opus-5-5", "selectedReasoning": "high", "turns": 1})
	if code != http.StatusOK {
		t.Fatalf("arm valid Opus/high: %d %v", code, body)
	}
	before, version := st.TurnOverrides(), st.PublishedSettingsVersion()
	code, body = patchSessionJSON(t, srv, "sess-adaptive-settings", map[string]interface{}{"selectedReasoning": "off", "turns": 1})
	if code != http.StatusBadRequest {
		t.Errorf("merged pending Opus/off: %d %v, want400", code, body)
	}
	if !reflect.DeepEqual(st.TurnOverrides(), before) || st.PublishedSettingsVersion() != version || calls.Load() != 0 {
		t.Error("rejected override changed settings or called runner/provider")
	}
}

func TestAdaptiveSessionPatchAdmission(t *testing.T) {
	for _, tc := range []struct {
		name, model, reasoning string
		patch                  map[string]interface{}
		allowed                bool
	}{
		{"reasoning off", "claude-opus-5-5", "high", map[string]interface{}{"selectedReasoning": "off"}, false},
		{"active Opus off", "claude-sonnet-4-5", "high", map[string]interface{}{"selectedReasoning": "off"}, false},
		{"model and off", "claude-sonnet-4-5", "high", map[string]interface{}{"selectedModelId": "reviewer/claude-opus-5-5", "selectedReasoning": "off"}, false},
		{"model carrying off", "claude-sonnet-4-5", "off", map[string]interface{}{"selectedModelId": "reviewer/claude-opus-5-5"}, false},
		{"clear model carrying off", "claude-sonnet-4-5", "off", map[string]interface{}{"selectedModelId": ""}, false},
		{"high", "claude-opus-5-5", "off", map[string]interface{}{"selectedReasoning": "high"}, true},
		{"default", "claude-opus-5-5", "off", map[string]interface{}{"selectedReasoning": ""}, true},
		{"legacy off", "claude-sonnet-4-5", "high", map[string]interface{}{"selectedReasoning": "off"}, true},
		{"unrelated tags", "claude-opus-5-5", "off", map[string]interface{}{"tags": []string{"review"}}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, st, calls := adaptiveSettingsServer(t, tc.model, tc.reasoning)
			if tc.name == "active Opus off" {
				st.SetTurnSetting(session.SettingModel, "reviewer/claude-opus-5-5")
			}
			beforeOverrides := st.TurnOverrides()
			beforeModel, beforeReasoning := st.GetSelectedModelID(), st.GetSelectedReasoning()
			beforeVersion, beforeMessages := st.PublishedSettingsVersion(), st.GetMessages()
			code, body := patchSessionJSON(t, srv, "sess-adaptive-settings", tc.patch)
			want := http.StatusBadRequest
			if tc.allowed {
				want = http.StatusOK
			}
			if code != want {
				t.Errorf("PATCH status=%d, want %d: %v", code, want, body)
			}
			if calls.Load() != 0 || !reflect.DeepEqual(st.GetMessages(), beforeMessages) {
				t.Error("settings PATCH called provider/runner or changed transcript")
			}
			if !tc.allowed && (st.GetSelectedModelID() != beforeModel || st.GetSelectedReasoning() != beforeReasoning || st.PublishedSettingsVersion() != beforeVersion || !reflect.DeepEqual(st.TurnOverrides(), beforeOverrides)) {
				t.Error("rejected PATCH changed session settings")
			}
		})
	}
}
