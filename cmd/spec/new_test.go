package main

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/TaylorEdgerton/spec-cli/internal/change"
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
