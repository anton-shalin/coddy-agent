//go:build http && ui

package ui

import (
	"testing"

	"github.com/cucumber/godog"
)

// The search of the Settings drawer is behaviour of the drawer, so each step
// runs the Vitest test that drives it against a stubbed server. A test inside
// a describe block is named by the block and its own name, joined by a space.
func TestWebUISettingsSearchFeature(t *testing.T) {
	const file = "src/ui/settings/Settings.test.tsx"
	steps := []struct{ step, name string }{
		{`^slash puts the cursor in the settings search unless a field has it or a dialog is open$`,
			"settings search slash puts the cursor in the settings search unless a field has it or a dialog is open"},
		{`^typing a query shows the matching settings of every tab in place of the sections, with the words marked$`,
			"settings search typing a query shows the matching settings of every tab in place of the sections, with the words marked"},
		{`^choosing a field in another tab opens that tab, rings the setting and puts the cursor in it$`,
			"settings search choosing a field in another tab opens that tab, rings the setting and puts the cursor in it"},
		{`^searching and choosing a result leave unsaved edits as they were$`,
			"settings search searching and choosing a result leave unsaved edits as they were"},
		{`^choosing a field of a provider row opens that row and unfolds the block it stands in$`,
			"settings search choosing a field of a provider row opens that row and unfolds the block it stands in"},
		{`^choosing a field of another row of the open list reveals it in that row, not in the one that was open$`,
			"settings search choosing a field of another row of the open list reveals it in that row, not in the one that was open"},
		{`^on a fine pointer the focus goes to the control named by the primary name \(Proxy URL's input, a model's id input\), never a switch beside it, a disabled input or the \(i\)$`,
			"settings search on a fine pointer the focus goes to the control named by the primary name (Proxy URL's input, a model's id input), never a switch beside it, a disabled input or the (i)"},
		{`^on a touch-only device the focus goes to the setting's named box, so no keyboard opens$`,
			"settings search on a touch-only device the focus goes to the setting's named box, so no keyboard opens"},
		{`^Escape with results showing clears the query wherever the focus is$`,
			"settings search Escape with results showing clears the query wherever the focus is"},
		{`^the Telegram back button clears shown results first, then goes back from a tab to the tiles$`,
			"settings search the Telegram back button clears shown results first, then goes back from a tab to the tiles"},
		{`^the query stays after a choice, and the field brings the results back with the chosen one highlighted$`,
			"settings search the query stays after a choice, and the field brings the results back with the chosen one highlighted"},
		{`^a Russian page finds a setting by its Russian name and by its config key$`,
			"settings search a Russian page finds a setting by its Russian name and by its config key"},
		{`^a relay's settings are searched too$`,
			"settings search a relay's settings are searched too"},
	}
	suite := godog.TestSuite{
		Name: "web_ui_settings_search",
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			for _, s := range steps {
				name := s.name
				sc.Step(s.step, func() error { return runVitestScenario(file, name) })
			}
		},
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/web_ui_settings_search.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("web UI settings search feature failed")
	}
}
