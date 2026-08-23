package main

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestNewSpecChoiceSupportsKeyboardNavigation(t *testing.T) {
	model := newNewSpecModel("Fix reconnect handling")
	updateModel(model, key(tea.KeyDown, ""))
	updateModel(model, key(tea.KeyEnter, ""))
	if !model.done || model.action != newSpecAskAI {
		t.Fatalf("model = %+v", model)
	}
}

func TestNewSpecGuidedFlowBuildsEditableCriteria(t *testing.T) {
	model := newNewSpecModel("Fix reconnect handling")
	updateModel(model, key(tea.KeyEnter, ""))
	answers := []string{
		"Connections do not recover after an outage.",
		"The application reconnects automatically.",
		"Health checks remain responsive.",
		"Do not change the database library.",
		"src/db/database.go, tests/test_database.go",
	}
	for _, answer := range answers {
		updateModel(model, tea.PasteMsg{Content: answer})
		updateModel(model, key(tea.KeyEnter, ""))
	}
	if model.stage != newSpecCriteria || len(model.criteria) != 2 {
		t.Fatalf("stage = %v, criteria = %+v", model.stage, model.criteria)
	}

	updateModel(model, key(tea.KeySpace, " "))
	if model.criteria[0].included {
		t.Fatal("space did not uncheck the selected criterion")
	}
	updateModel(model, key('a', "a"))
	updateModel(model, tea.PasteMsg{Content: "Application restart is not required"})
	updateModel(model, key(tea.KeyEnter, ""))
	if len(model.criteria) != 3 || model.criteria[2].text != "Application restart is not required" {
		t.Fatalf("criterion was not added: %+v", model.criteria)
	}

	updateModel(model, key('e', "e"))
	updateModel(model, key(tea.KeyHome, ""))
	updateModel(model, tea.PasteMsg{Content: "An "})
	updateModel(model, key(tea.KeyEnter, ""))
	if got := model.criteria[2].text; got != "An Application restart is not required" {
		t.Fatalf("edited criterion = %q", got)
	}

	model.cursor = len(model.criteria) + 1
	updateModel(model, key(tea.KeyEnter, ""))
	if !model.done || model.action != newSpecGuided {
		t.Fatalf("model did not accept criteria: %+v", model)
	}
	guided := model.guidedSpec()
	if len(guided.AcceptanceCriteria) != 2 {
		t.Fatalf("included criteria = %v", guided.AcceptanceCriteria)
	}
	if got := strings.Join(guided.RelevantFiles, "|"); got != "src/db/database.go|tests/test_database.go" {
		t.Fatalf("relevant files = %q", got)
	}
}

func TestNewSpecGuidedFlowAsksForMissingTitle(t *testing.T) {
	model := newNewSpecModel("")
	updateModel(model, key(tea.KeyEnter, ""))
	if !strings.Contains(model.questionView(), "What are you changing?") {
		t.Fatalf("question view = %q", model.questionView())
	}
	updateModel(model, tea.PasteMsg{Content: "Fix reconnect handling"})
	updateModel(model, key(tea.KeyEnter, ""))
	if model.title != "Fix reconnect handling" || model.question != 1 {
		t.Fatalf("title = %q, question = %d", model.title, model.question)
	}
	if got := model.answer(0); got != "" {
		t.Fatalf("first detail answer should still be empty, got %q", got)
	}
}

func TestNewSpecCriteriaCanBeDeleted(t *testing.T) {
	model := newNewSpecModel("Change")
	model.stage = newSpecCriteria
	model.criteria = []criterion{{text: "One", included: true}, {text: "Two", included: true}}
	updateModel(model, key('d', "d"))
	if len(model.criteria) != 1 || model.criteria[0].text != "Two" {
		t.Fatalf("criteria = %+v", model.criteria)
	}
}

func updateModel(model *newSpecModel, message tea.Msg) {
	model.Update(message)
}

func key(code rune, text string) tea.KeyPressMsg {
	return tea.KeyPressMsg(tea.Key{Code: code, Text: text})
}
