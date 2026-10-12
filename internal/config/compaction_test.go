package config

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestCompactionDefaults(t *testing.T) {
	var c Compaction
	c.ApplyDefaults()

	if !c.IsEnabled() {
		t.Fatal("compaction must be enabled by default")
	}
	if !c.IsAutoEnabled() {
		t.Fatal("automatic compaction must be enabled by default")
	}
	if c.ThresholdPercent != CompactionDefaultThresholdPercent {
		t.Fatalf("threshold = %d, want %d", c.ThresholdPercent, CompactionDefaultThresholdPercent)
	}
	if got := c.EffectiveKeepRecentTurns(); got != CompactionDefaultKeepRecentTurns {
		t.Fatalf("keep_recent_turns = %d, want %d", got, CompactionDefaultKeepRecentTurns)
	}
	if (&Compaction{}).EffectiveThresholdPercent() != CompactionDefaultThresholdPercent {
		t.Fatal("EffectiveThresholdPercent must default without ApplyDefaults")
	}
}

func TestCompactionCanDisableOnlyAutomation(t *testing.T) {
	off := false
	c := Compaction{AutoEnabled: &off}
	if !c.IsEnabled() || c.IsAutoEnabled() {
		t.Fatalf("manual and automatic switches are not independent: %+v", c)
	}
}

func TestCompactionExplicitValuesSurviveDefaults(t *testing.T) {
	off := false
	zero := 0
	c := Compaction{Enabled: &off, ThresholdPercent: 55, KeepRecentTurns: &zero, Model: " fake/model "}
	c.ApplyDefaults()
	c.Normalize()

	if c.IsEnabled() {
		t.Fatal("explicit enabled=false must survive defaults")
	}
	if c.ThresholdPercent != 55 {
		t.Fatalf("threshold = %d, want 55", c.ThresholdPercent)
	}
	if got := c.EffectiveKeepRecentTurns(); got != 0 {
		t.Fatalf("keep_recent_turns = %d, want explicit 0", got)
	}
	if c.Model != "fake/model" {
		t.Fatalf("model = %q, want trimmed", c.Model)
	}
}

func TestCompactionValidate(t *testing.T) {
	neg := -1
	cases := []struct {
		name    string
		c       Compaction
		wantErr string
	}{
		{name: "defaults are valid", c: Compaction{}},
		{name: "threshold over 100", c: Compaction{ThresholdPercent: 101}, wantErr: "threshold_percent"},
		{name: "threshold negative", c: Compaction{ThresholdPercent: -5}, wantErr: "threshold_percent"},
		{name: "keep negative", c: Compaction{KeepRecentTurns: &neg}, wantErr: "keep_recent_turns"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := tc.c
			c.ApplyDefaults()
			err := c.Validate()
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %v, want mention of %q", err, tc.wantErr)
			}
		})
	}
}

func TestCompactionYAMLSectionParsed(t *testing.T) {
	yaml := `
providers:
  - name: fake
    type: openai
    api_key: k
models:
  - model: fake/m
agent:
  model: fake/m
compaction:
  enable: true
  auto_enable: false
  threshold_percent: 70
  keep_recent_turns: 3
  model: fake/m
`
	cfg, err := parseValidateYAMLBytes(yaml, Paths{})
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Compaction.IsEnabled() || cfg.Compaction.IsAutoEnabled() {
		t.Fatal("yaml auto_enable: false ignored or manual compaction disabled")
	}
	if cfg.Compaction.ThresholdPercent != 70 {
		t.Fatalf("threshold = %d", cfg.Compaction.ThresholdPercent)
	}
	if got := cfg.Compaction.EffectiveKeepRecentTurns(); got != 3 {
		t.Fatalf("keep = %d", got)
	}
	if cfg.Compaction.Model != "fake/m" {
		t.Fatalf("model = %q", cfg.Compaction.Model)
	}
}

func TestResultEvictionDefaults(t *testing.T) {
	var r ResultEviction
	if !r.IsEnabled() {
		t.Fatal("result eviction must be enabled by default")
	}
	if got := r.EffectiveKeepRecent(); got != ResultEvictionDefaultKeepRecent {
		t.Fatalf("keep_recent = %d, want %d", got, ResultEvictionDefaultKeepRecent)
	}
	if got := r.EffectiveMinResultBytes(); got != ResultEvictionDefaultMinResultBytes {
		t.Fatalf("min_result_bytes = %d, want %d", got, ResultEvictionDefaultMinResultBytes)
	}
}

func TestResultEvictionExplicitValues(t *testing.T) {
	off := false
	zero := 0
	r := ResultEviction{Enabled: &off, KeepRecent: &zero, MinResultBytes: &zero}
	if r.IsEnabled() {
		t.Fatal("explicit enable:false must disable")
	}
	if r.EffectiveKeepRecent() != 0 {
		t.Fatalf("keep_recent = %d, want 0", r.EffectiveKeepRecent())
	}
	if r.EffectiveMinResultBytes() != 0 {
		t.Fatalf("min_result_bytes = %d, want 0", r.EffectiveMinResultBytes())
	}
}

func TestResultEvictionValidate(t *testing.T) {
	neg := -1
	if err := (&ResultEviction{KeepRecent: &neg}).Validate(); err == nil {
		t.Fatal("expected error for negative keep_recent")
	}
	if err := (&ResultEviction{MinResultBytes: &neg}).Validate(); err == nil {
		t.Fatal("expected error for negative min_result_bytes")
	}
	if err := (&Compaction{ResultEviction: ResultEviction{KeepRecent: &neg}}).Validate(); err == nil {
		t.Fatal("Compaction.Validate must surface result_eviction errors")
	}
}

// compaction.in_turn has one key, its switch: the fold of the turn being
// answered and the recovery from a refused request are on unless the operator
// turns them off, with or without ApplyDefaults.
func TestInTurnDefaults(t *testing.T) {
	var c Compaction
	c.ApplyDefaults()
	if !c.InTurn.IsEnabled() {
		t.Fatal("in-turn compaction must be on by default")
	}
	if c.InTurn.Enabled != nil {
		t.Fatalf("ApplyDefaults must leave the switch unset, got %v", *c.InTurn.Enabled)
	}
	if !(&InTurn{}).IsEnabled() {
		t.Fatal("the zero value must mean the default (on)")
	}
}

func TestInTurnExplicitValues(t *testing.T) {
	off, on := false, true
	if (&InTurn{Enabled: &off}).IsEnabled() {
		t.Fatal("enable: false must switch the fold off")
	}
	if !(&InTurn{Enabled: &on}).IsEnabled() {
		t.Fatal("enable: true must keep the fold on")
	}
	c := Compaction{InTurn: InTurn{Enabled: &off}}
	c.ApplyDefaults()
	if c.InTurn.IsEnabled() {
		t.Fatal("explicit enable: false must survive defaults")
	}
}

// An absent key stays nil (the default) and an explicit enable: false is read
// back as written.
func TestInTurnYAML(t *testing.T) {
	load := func(extra string) (*Config, error) {
		return parseValidateYAMLBytes(`
providers:
  - name: fake
    type: openai
    api_key: k
models:
  - model: fake/m
agent:
  model: fake/m
compaction:
`+extra, Paths{})
	}

	absent, err := load("  threshold_percent: 70\n")
	if err != nil {
		t.Fatal(err)
	}
	if absent.Compaction.InTurn.Enabled != nil {
		t.Fatalf("an absent key must stay nil, got %v", *absent.Compaction.InTurn.Enabled)
	}
	if !absent.Compaction.InTurn.IsEnabled() {
		t.Fatal("an absent key must mean the default (on)")
	}

	set, err := load("  in_turn:\n    enable: false\n")
	if err != nil {
		t.Fatal(err)
	}
	if set.Compaction.InTurn.Enabled == nil || set.Compaction.InTurn.IsEnabled() {
		t.Fatalf("enable: false lost: %+v", set.Compaction.InTurn)
	}
}

// The agent stages compaction.in_turn.enable through config_set, which edits the
// YAML tree: a switch turned off has to reach the file and read back off.
func TestCommitUCICommandsInTurnSwitchReachesTheFile(t *testing.T) {
	paths := testPathConfig(t, "compaction:\n  threshold_percent: 70\n")

	if _, err := CommitUCICommands(paths, mustParseUCI(t, "set compaction.in_turn.enable=false")); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadWithPaths(paths)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Compaction.InTurn.Enabled == nil || cfg.Compaction.InTurn.IsEnabled() {
		t.Fatalf("in_turn = %+v, want an explicit enable: false", cfg.Compaction.InTurn)
	}
	raw, err := os.ReadFile(paths.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "in_turn:") || !strings.Contains(string(raw), "enable: false") {
		t.Fatalf("the switch did not reach the file:\n%s", raw)
	}
}

// A save of a config that never named the section does not materialize it, and
// an explicit enable: false is kept.
func TestMarshalConfigYAMLKeepsInTurnOnlyWhenSet(t *testing.T) {
	cfg := loadForRewrite(t, sparseConfig)
	cfg.Agent.MaxTurns = 42
	saved := rewrite(t, cfg, sparseConfig)
	if strings.Contains(saved, "in_turn") {
		t.Errorf("saved config materializes in_turn the file never had:\n%s", saved)
	}

	const withSwitch = sparseConfig + `compaction:
  in_turn:
    enable: false
`
	cfg = loadForRewrite(t, withSwitch)
	cfg.Agent.MaxTurns = 42
	saved = rewrite(t, cfg, withSwitch)
	if !strings.Contains(saved, "enable: false") || !strings.Contains(saved, "in_turn:") {
		t.Errorf("saved config dropped the explicit in_turn switch:\n%s", saved)
	}
}

// descendant walks the properties of a JSON-schema-shaped document by name.
func descendant(t *testing.T, node map[string]interface{}, path ...string) map[string]interface{} {
	t.Helper()
	for _, name := range path {
		props, ok := node["properties"].(map[string]interface{})
		if !ok {
			t.Fatalf("no properties before %q", name)
		}
		next, ok := props[name].(map[string]interface{})
		if !ok {
			t.Fatalf("no property %q", name)
		}
		node = next
	}
	return node
}

// describedSchemas returns the two documents a person reads a key by: the
// published config.schema.json and the Settings form's UISchemaMap.
func describedSchemas(t *testing.T) map[string]map[string]interface{} {
	t.Helper()
	var schema map[string]interface{}
	if err := json.Unmarshal(ConfigSchemaJSON(), &schema); err != nil {
		t.Fatal(err)
	}
	return map[string]map[string]interface{}{"config.schema.json": schema, "UISchemaMap": UISchemaMap()}
}

// compaction.in_turn.enable does nothing while compaction.auto_enable is false:
// the fold and the recovery from a refused request both need the automatic
// trigger. Every description a person reads the key by says so (the web UI
// dictionaries, the Russian descriptions and the bundled skill carry the same
// sentence and are held by docs-check and by review).
func TestInTurnDescriptionsSayTheyNeedTheAutomaticTrigger(t *testing.T) {
	for name, root := range describedSchemas(t) {
		section := descendant(t, root, "compaction", "in_turn")
		enable := descendant(t, section, "enable")
		for label, node := range map[string]map[string]interface{}{"compaction.in_turn": section, "compaction.in_turn.enable": enable} {
			desc, _ := node["description"].(string)
			if !strings.Contains(desc, "auto_enable") {
				t.Errorf("%s: the description of %s does not say it needs compaction.auto_enable: %q", name, label, desc)
			}
		}
	}
}

// result_eviction also collapses the listings of a fixed set of tools, so the
// section no longer calls itself read/grep only and names each listing tool it
// takes.
func TestResultEvictionDescriptionsNameTheListingTools(t *testing.T) {
	for name, root := range describedSchemas(t) {
		section := descendant(t, root, "compaction", "result_eviction")
		title, _ := section["title"].(string)
		desc, _ := section["description"].(string)
		if strings.Contains(strings.ToLower(title), "read/grep") {
			t.Errorf("%s: the section still calls itself read/grep only: %q", name, title)
		}
		for _, tool := range []string{"glob", "print_tree", "websearch", "webfetch"} {
			if !strings.Contains(desc, tool) {
				t.Errorf("%s: the description of compaction.result_eviction does not name %s: %q", name, tool, desc)
			}
		}
	}
}
