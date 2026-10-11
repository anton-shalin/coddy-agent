package session

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

// An answer the output limit cut off and the model continued is several
// assistant messages of the transcript; an export holds it as the one answer it
// is, as the web UI draws it: one entry, the parts joined as they streamed, so a
// code fence cut in two does not swallow the heading of a second section.
func TestBuildExportDocumentJoinsAContinuedAnswer(t *testing.T) {
	msgs := []llm.Message{
		{Role: llm.RoleUser, Content: "write it"},
		{Role: llm.RoleAssistant, Content: "Here is the module:\n\n```go\nfunc Run() {", Truncated: true, Model: "m1"},
		{Role: llm.RoleAssistant},
		{Role: llm.RoleAssistant, Content: "\n}\n```\n\nThat is all.", Model: "m1"},
		{Role: llm.RoleUser, Content: "and the test?"},
		{Role: llm.RoleAssistant, Content: "A test ", Truncated: true},
	}
	doc := BuildExportDocument(ExportInput{SessionID: "s", Messages: msgs})
	var answers []ExportEntry
	for _, e := range doc.Entries {
		if e.Type == ExportEntryAssistant {
			answers = append(answers, e)
		}
	}
	if len(answers) != 2 {
		t.Fatalf("assistant entries = %+v, want the continued answer as one and the answer of the second turn", answers)
	}
	if want := "Here is the module:\n\n```go\nfunc Run() {\n}\n```\n\nThat is all."; answers[0].Text != want || answers[0].Truncated {
		t.Fatalf("the continued answer exports as %+v, want %q, not truncated", answers[0], want)
	}
	// An answer the turn ended on while it was cut off says so.
	if !answers[1].Truncated || answers[1].Text != "A test " {
		t.Fatalf("an answer that stops partway exports as %+v, want it marked truncated", answers[1])
	}

	md, err := RenderExport(doc, ExportFormatMarkdown)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(md), "## Assistant"); n != 2 {
		t.Fatalf("the markdown export has %d assistant sections, want 2:\n%s", n, md)
	}
	raw, err := RenderExport(doc, ExportFormatJSON)
	if err != nil {
		t.Fatal(err)
	}
	var back ExportDocument
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	var flagged int
	for _, e := range back.Entries {
		if e.Truncated {
			flagged++
		}
	}
	if flagged != 1 {
		t.Fatalf("the JSON export marks %d entries truncated, want the one that stops partway:\n%s", flagged, raw)
	}
}

// The parts of a continued answer stay apart in an export where the web UI
// keeps them apart: the continuation thought first, which is an entry of its
// own between them.
func TestBuildExportDocumentKeepsAContinuationThatThoughtApart(t *testing.T) {
	msgs := []llm.Message{
		{Role: llm.RoleUser, Content: "write it"},
		{Role: llm.RoleAssistant, Content: "and then ", Truncated: true},
		{Role: llm.RoleAssistant, Content: "the second half.", Reasoning: "Where was I."},
	}
	doc := BuildExportDocument(ExportInput{SessionID: "s", Messages: msgs})
	var texts []string
	for _, e := range doc.Entries {
		if e.Type == ExportEntryAssistant {
			texts = append(texts, e.Text)
		}
	}
	if len(texts) != 2 || texts[0] != "and then " || texts[1] != "the second half." {
		t.Fatalf("assistant entries = %q, want the two parts apart", texts)
	}
}
