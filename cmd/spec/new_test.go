package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/TaylorEdgerton/spec-cli/internal/change"
	"github.com/TaylorEdgerton/spec-cli/internal/discovery"
	"github.com/TaylorEdgerton/spec-cli/internal/state"
)

func TestLineEditorSupportsCursorEditing(t *testing.T) {
	editor := newLineEditor("Application restart is not required")
	editor.key(key(tea.KeyHome, ""))
	editor.insert("An ")
	if editor.value != "An Application restart is not required" {
		t.Fatalf("value = %q", editor.value)
	}
	editor.key(key(tea.KeyEnd, ""))
	editor.key(key(tea.KeyBackspace, ""))
	if !strings.HasSuffix(editor.value, "require") {
		t.Fatalf("backspace value = %q", editor.value)
	}
}

func TestSetupCriteriaSupportsToggleAddEditDeleteAndAccept(t *testing.T) {
	model := &setupCriteriaModel{
		criteria: []state.SetupCriterion{
			{Text: "Reconnect automatically", Included: true},
			{Text: "No restart required", Included: true},
		},
		editing: -1,
	}
	updateModel(model, key(tea.KeySpace, " "))
	if model.criteria[0].Included {
		t.Fatal("space did not exclude criterion")
	}
	updateModel(model, key('a', "a"))
	updateModel(model, tea.PasteMsg{Content: "Health checks stay responsive"})
	updateModel(model, key(tea.KeyEnter, ""))
	if len(model.criteria) != 3 {
		t.Fatalf("criteria = %+v", model.criteria)
	}
	updateModel(model, key('e', "e"))
	updateModel(model, key(tea.KeyHome, ""))
	updateModel(model, tea.PasteMsg{Content: "All "})
	updateModel(model, key(tea.KeyEnter, ""))
	if model.criteria[2].Text != "All Health checks stay responsive" {
		t.Fatalf("edited criterion = %q", model.criteria[2].Text)
	}
	updateModel(model, key('d', "d"))
	if len(model.criteria) != 2 {
		t.Fatalf("delete criteria = %+v", model.criteria)
	}
	model.cursor = len(model.criteria) + 2
	updateModel(model, key(tea.KeyEnter, ""))
	if !model.done || model.interrupted || model.back {
		t.Fatalf("criteria model = %+v", model)
	}
}

func TestSetupCriteriaCanMoveBack(t *testing.T) {
	model := &setupCriteriaModel{criteria: []state.SetupCriterion{{Text: "One", Included: true}}, editing: -1}
	model.cursor = len(model.criteria) + 1
	updateModel(model, key(tea.KeyEnter, ""))
	if !model.done || !model.back || model.interrupted {
		t.Fatalf("criteria model = %+v", model)
	}
}

func TestTextPromptCanMoveBackWithoutDiscardingInput(t *testing.T) {
	model := &textPromptModel{canBack: true, editor: newLineEditor("Updated outcome")}
	updateModel(model, tea.KeyPressMsg(tea.Key{Code: tea.KeyTab, Mod: tea.ModShift}))
	if !model.done || !model.back || model.interrupted || model.editor.value != "Updated outcome" {
		t.Fatalf("text prompt model = %+v", model)
	}
}

func TestVerificationPromptIsBoundedToSetup(t *testing.T) {
	setup := state.Setup{
		Title:   "Fix reconnect handling",
		Outcome: "Reconnect automatically",
		Limits:  "Do not change the database library",
		Criteria: []state.SetupCriterion{
			{Text: "Reconnect after an outage", Included: true},
			{Text: "Discarded", Included: false},
		},
	}
	prompt := verificationPrompt(t.TempDir(), setup, []string{"go test ./..."})
	for _, expected := range []string{setup.Title, setup.Outcome, setup.Limits, "Reconnect after an outage", "go test ./...", "Do not implement the product change"} {
		if !strings.Contains(prompt, expected) {
			t.Fatalf("prompt missing %q:\n%s", expected, prompt)
		}
	}
	if strings.Contains(prompt, "Discarded") {
		t.Fatalf("prompt includes excluded criterion:\n%s", prompt)
	}
}

func TestSetupReviewUsesBoundedSummary(t *testing.T) {
	setup := state.Setup{
		Title:   "Fix reconnect handling " + strings.Repeat("safely ", 20),
		Outcome: "Reconnect automatically",
		Criteria: []state.SetupCriterion{
			{Text: strings.Repeat("criterion ", 100), Included: true},
			{Text: "Excluded", Included: false},
		},
	}
	summary := formatSetupSummary(setup)
	for _, expected := range []string{"Change: Fix reconnect handling", setup.Outcome, "Limits: none", "Success criteria: 1"} {
		if !strings.Contains(summary, expected) {
			t.Fatalf("summary missing %q: %s", expected, summary)
		}
	}
	if strings.Contains(summary, "criterion criterion") || !strings.Contains(summary, "…") || len(strings.Split(summary, "\n")) != 4 {
		t.Fatalf("summary is not bounded: %s", summary)
	}
}

func TestDiscoverySummaryIsBoundedAndExplainsResults(t *testing.T) {
	root := t.TempDir()
	results := []discovery.Result{
		{Path: "src/core/health.py", Line: 12, Column: 5, Preview: "def check_database_health():", Reasons: []string{"strong intent match to \"database health\""}},
		{Path: "tests/test_health.py", Line: 8, Column: 3, Preview: "check_database_health()", Reasons: []string{"related test file"}},
	}
	for _, result := range results {
		path := filepath.Join(root, filepath.FromSlash(result.Path))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(result.Preview+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	summary := formatDiscovery(results)
	for _, expected := range []string{"src/core/health.py:12:5", "strong intent match", "def check_database_health", "tests/test_health.py:8:3"} {
		if !strings.Contains(summary, expected) {
			t.Fatalf("summary missing %q:\n%s", expected, summary)
		}
	}
	if !strings.Contains(summary, "\x1b[92;4m") {
		t.Fatalf("summary locations are not highlighted:\n%q", summary)
	}
}

func TestDiscoverySummaryShowsSymbolsAndOpenChoicesUseTheirLocations(t *testing.T) {
	results := []discovery.Result{
		{
			Path: "internal/discovery/discovery.go",
			Symbols: []discovery.Symbol{
				{Name: "addFieldSignals()", Kind: "function", Line: 448, Column: 6, Reasons: []string{"uses kindWord"}},
				{Name: "kindWord", Kind: "constant", Line: 62, Column: 2, Reasons: []string{"matched discovery intent"}},
			},
		},
	}

	summary := formatDiscovery(results)
	for _, expected := range []string{"internal/discovery/discovery.go:448:6", "addFieldSignals() :448", "uses kindWord", "kindWord :62"} {
		if !strings.Contains(summary, expected) {
			t.Fatalf("summary missing %q:\n%s", expected, summary)
		}
	}
	choices := discoveryOpenChoices(results)
	if len(choices) != 2 || choices[0].Result.Line != 448 || choices[0].Result.Column != 6 ||
		!strings.Contains(choices[0].Label, "discovery.go · addFieldSignals() :448") || choices[1].Result.Line != 62 {
		t.Fatalf("open choices = %+v", choices)
	}

	context := exploredSymbol{Path: results[0].Path, Symbol: discovery.Symbol{
		Name: "addFieldSignals()", Line: 448, Column: 6,
		Related: []discovery.RelatedSymbol{
			{Name: "kindWord", Line: 62, Column: 2, Relation: "uses kindWord"},
			{Name: "addSignal()", Line: 470, Column: 6, Relation: "calls addSignal()"},
			{Name: "significantWords()", Line: 490, Column: 6, Relation: "receives words from significantWords()"},
		},
	}}
	tree := formatExploreTree(context)
	for _, expected := range []string{"internal/discovery/discovery.go:448:6", "addFieldSignals()", "├─ uses kindWord", "├─ calls addSignal()", "└─ receives words from significantWords()"} {
		if !strings.Contains(tree, expected) {
			t.Fatalf("context tree missing %q:\n%s", expected, tree)
		}
	}
	exploreItems := exploreChoices(context)
	if len(exploreItems) != 5 || exploreItems[0] != "addFieldSignals() :448" || exploreItems[1] != "kindWord :62" || exploreItems[4] != "Back" {
		t.Fatalf("explore choices = %+v", exploreItems)
	}
}

func TestConsoleSectionsHaveLabelledSeparators(t *testing.T) {
	var output bytes.Buffer
	printConsoleSection(&output, "Explore Context", "Choose a location.")
	if !strings.Contains(output.String(), "── Explore Context ──") || !strings.Contains(output.String(), "Choose a location.") {
		t.Fatalf("section = %q", output.String())
	}
}

func TestChoiceMenusLeaveMouseEventsToTheTerminal(t *testing.T) {
	model := &choiceModel{items: []string{"Explore context", "Continue"}, selected: -1}
	view := model.View()
	if view.MouseMode != tea.MouseModeNone || view.OnMouse != nil {
		t.Fatal("choice menu captures terminal mouse events")
	}
}

func TestDiscoveryQueryUsesOnlyIncludedCriteria(t *testing.T) {
	query := discoveryQuery(state.Setup{
		Title:   "Database health",
		Outcome: "Health is reported",
		Limits:  "Keep compatibility",
		Criteria: []state.SetupCriterion{
			{Text: "Report failures", Included: true},
			{Text: "Discarded criterion", Included: false},
		},
	})
	if len(query.Criteria) != 1 || query.Criteria[0] != "Report failures" {
		t.Fatalf("query = %+v", query)
	}
	if query.Intent != "Database health" || query.Outcome != "Health is reported" {
		t.Fatalf("query fields = %+v", query)
	}
}

func TestCriteriaReviewRequiresEveryItem(t *testing.T) {
	model := &reviewCriteriaModel{criteria: []change.Criterion{{Text: "One"}, {Text: "Two"}}}
	model.cursor = len(model.criteria)
	updateModel(model, key(tea.KeyEnter, ""))
	if model.done {
		t.Fatal("review completed with unchecked criteria")
	}
	model.cursor = 0
	updateModel(model, key(tea.KeySpace, " "))
	updateModel(model, key(tea.KeyDown, ""))
	updateModel(model, key(tea.KeySpace, " "))
	updateModel(model, key(tea.KeyDown, ""))
	updateModel(model, key(tea.KeyEnter, ""))
	if !model.done {
		t.Fatal("review did not complete after every criterion was checked")
	}
}

func updateModel(model tea.Model, message tea.Msg) {
	model.Update(message)
}

func key(code rune, text string) tea.KeyPressMsg {
	return tea.KeyPressMsg(tea.Key{Code: code, Text: text})
}
