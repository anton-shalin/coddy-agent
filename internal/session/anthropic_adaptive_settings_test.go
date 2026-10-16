package session_test

import (
	"context"
	"log/slog"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"testing/quick"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

func adaptiveSettingsManager(t *testing.T) (*session.Manager, *session.State, string) {
	t.Helper()
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	t.Cleanup(backend.Close)
	cfg := &config.Config{
		Providers: []config.ProviderConfig{{Name: "reviewer", Type: "anthropic", APIBase: backend.URL, APIKey: "test", Proxy: "none"}},
		Models: []config.ModelEntry{
			{Model: "reviewer/claude-opus-5-5", ReasoningDefault: "high", AllowReasoningOff: true},
			{Model: "reviewer/claude-sonnet-4-5", ReasoningDefault: "high", AllowReasoningOff: true},
		},
		Agent: config.Agent{Model: "reviewer/claude-opus-5-5"},
	}
	mgr := session.NewManager(cfg, noopSender{}, noopRunner, slog.Default(), t.TempDir(), nil)
	st, err := mgr.EnsureHTTPSession(context.Background(), "sess-adaptive-owner", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return mgr, st, "sess-adaptive-owner"
}

func TestAdaptiveSettingsCombinedTurnCommandsRemainValid(t *testing.T) {
	mgr, st, id := adaptiveSettingsManager(t)
	st.SetSelectedModelID("reviewer/claude-sonnet-4-5")
	st.SetSelectedReasoning("off")
	prompt := []acp.ContentBlock{{Type: acp.ContentTypeText, Text: "/model reviewer/claude-opus-5-5 --once /reasoning high --count=2"}}
	if _, err := mgr.TakeSettingsCommands(context.Background(), id, prompt, "command"); err != nil {
		t.Fatalf("valid combined overrides rejected: %v", err)
	}
	if len(st.TurnOverrides()) != 2 {
		t.Fatal("both overrides must be armed")
	}
	if _, err := mgr.HandleSessionPromptWithSender(context.Background(), acp.SessionPromptParams{SessionID: id, Prompt: []acp.ContentBlock{{Type: acp.ContentTypeText, Text: "hi"}}}, noopSender{}, nil); err != nil {
		t.Fatalf("valid combined overrides failed on use: %v", err)
	}
}

func TestAdaptiveSettingsActiveTurnAdmission(t *testing.T) {
	mgr, st, id := adaptiveSettingsManager(t)
	st.SetSelectedModelID("reviewer/claude-sonnet-4-5")
	st.SetTurnSetting(session.SettingModel, "reviewer/claude-opus-5-5")
	off := "off"
	before := st.TurnOverrides()
	if _, err := mgr.ApplyTurnSettings(context.Background(), id, session.SettingsChange{Reasoning: &off}); err == nil {
		t.Fatal("active Opus/off accepted")
	}
	if !reflect.DeepEqual(before, st.TurnOverrides()) {
		t.Fatal("rejected active-turn change mutated overrides")
	}
	st.SetSelectedModelID("reviewer/claude-opus-5-5")
	st.SetTurnSetting(session.SettingModel, "reviewer/claude-sonnet-4-5")
	if _, err := mgr.ApplyTurnSettings(context.Background(), id, session.SettingsChange{Reasoning: &off}); err != nil {
		t.Fatalf("legacy active-turn off rejected: %v", err)
	}
}

func TestAdaptiveSessionSettingsPreserveActiveTurnAdmission(t *testing.T) {
	mgr, st, id := adaptiveSettingsManager(t)
	st.SetSelectedModelID("reviewer/claude-sonnet-4-5")
	st.SetSelectedReasoning("high")
	st.SetTurnSetting(session.SettingModel, "reviewer/claude-opus-5-5")
	before, version := st.TurnOverrides(), st.PublishedSettingsVersion()
	off := "off"
	if _, err := mgr.ApplySessionSettings(context.Background(), id, session.SettingsChange{Reasoning: &off}); err == nil {
		t.Error("session change left unsupported active Opus/off")
	}
	if st.GetSelectedReasoning() != "high" || !reflect.DeepEqual(before, st.TurnOverrides()) || st.PublishedSettingsVersion() != version {
		t.Error("rejected session change mutated active or session settings")
	}
}

func TestAdaptiveConcurrentSessionSettingsAdmission(t *testing.T) {
	mgr, st, id := adaptiveSettingsManager(t)
	model, off := "reviewer/claude-opus-5-5", "off"
	for attempt := 0; attempt < 100; attempt++ {
		st.SetSelectedModelID("reviewer/claude-sonnet-4-5")
		st.SetSelectedReasoning("high")
		start, results := make(chan struct{}), make(chan error, 2)
		for _, change := range []session.SettingsChange{{Model: &model}, {Reasoning: &off}} {
			go func(ch session.SettingsChange) {
				<-start
				_, err := mgr.ApplySessionSettings(context.Background(), id, ch)
				results <- err
			}(change)
		}
		close(start)
		first, second := <-results, <-results
		if (first == nil) == (second == nil) || st.EffectiveModelID(mgr.Cfg()) == model && st.EffectiveReasoning(mgr.Cfg()) == off {
			t.Fatalf("attempt %d: concurrent incompatible changes were not serialized: %v / %v", attempt, first, second)
		}
	}
}

func TestAdaptiveSettingsOverrideLifetimeInvariant(t *testing.T) {
	mgr, _, _ := adaptiveSettingsManager(t)
	property := func(modelCount, reasoningCount uint8) bool {
		modelTurns := int(modelCount)%session.MaxOverrideTurns + 1
		reasoningTurns := int(reasoningCount)%session.MaxOverrideTurns + 1
		st := &session.State{}
		st.ArmTurnOverride(session.SettingModel, "reviewer/claude-sonnet-4-5", modelTurns)
		st.ArmTurnOverride(session.SettingReasoning, "off", reasoningTurns)
		before := st.TurnOverrides()
		err := mgr.ValidateOperatorSettings(st, session.SettingsChange{}, "hi")
		return (err == nil) == (modelTurns >= reasoningTurns) && reflect.DeepEqual(before, st.TurnOverrides()) && st.GetSelectedModelID() == "" && st.GetSelectedReasoning() == ""
	}
	if err := quick.Check(property, &quick.Config{MaxCount: 100, Rand: rand.New(rand.NewSource(55))}); err != nil {
		t.Fatal(err)
	}
}

func TestAdaptiveSettingsCommandsRejectBeforeMutation(t *testing.T) {
	for _, prompt := range []string{
		"/reasoning off hi",
		"/model reviewer/claude-opus-5-5 /reasoning off --once hi",
		"/model reviewer/claude-sonnet-4-5 --once /reasoning off --count=2 hi",
	} {
		t.Run(prompt, func(t *testing.T) {
			mgr, st, id := adaptiveSettingsManager(t)
			before := st.PublishedSettingsVersion()
			_, err := mgr.TakeSettingsCommands(context.Background(), id, []acp.ContentBlock{{Type: acp.ContentTypeText, Text: prompt}}, "command")
			if err == nil {
				t.Fatal("unsupported combined settings were accepted")
			}
			if st.GetSelectedModelID() != "" || st.GetSelectedReasoning() != "" || len(st.TurnOverrides()) != 0 || st.PublishedSettingsVersion() != before {
				t.Fatal("rejected commands changed session settings")
			}
		})
	}
}
