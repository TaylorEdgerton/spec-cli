package main

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/TaylorEdgerton/spec-cli/internal/change"
	"github.com/TaylorEdgerton/spec-cli/internal/state"
	"github.com/charmbracelet/x/ansi"
)

func TestShellRoutesEveryForwardActionAndReturnsWithBack(t *testing.T) {
	for action, want := range map[string]shellScreen{
		actionDefinition:    screenDefinition,
		actionOverview:      screenOverview,
		actionPlan:          screenPlan,
		actionReview:        screenReview,
		actionSummary:       screenReview,
		actionReviewChanges: screenReview,
		actionIntegration:   screenReview,
		actionDiff:          screenReview,
		actionEvidence:      screenReview,
		actionHistory:       screenHistory,
		actionComplete:      screenHistory,
		actionCompletion:    screenComplete,
		actionChanges:       screenBack,
		actionBack:          screenBack,
		actionQuit:          screenExit,
		actionNone:          screenStay,
		"unknown":           screenStay,
	} {
		if got := screenForAction(action); got != want {
			t.Fatalf("screenForAction(%q) = %q, want %q", action, got, want)
		}
	}
}

func TestShellScreensShareExitBackAndHelpKeys(t *testing.T) {
	newScreens := func(t *testing.T) map[string]interface {
		Update(tea.Msg) (tea.Model, tea.Cmd)
		View() tea.View
	} {
		return map[string]interface {
			Update(tea.Msg) (tea.Model, tea.Cmd)
			View() tea.View
		}{
			"definition": newDefinitionModel(state.Setup{Title: "Intent"}, "clean"),
			"overview":   newOverviewModel(overviewData{Title: "Intent", SpecID: "SPEC-001", Now: time.Now()}),
			"plan":       newPlanModel(t.TempDir(), reviewPlanFixture()),
			"review":     newReviewModel(t.TempDir(), reviewSnapshotFixture(reviewPlanFixture())),
			"complete":   newCompletionModel(reviewSnapshotFixture(reviewPlanFixture())),
			"history":    newHistoryModel(t.TempDir(), t.TempDir(), historyFixture(), false),
		}
	}
	navigationOf := func(model any) string {
		return reflect.ValueOf(model).Elem().FieldByName("nav").String()
	}

	for name, model := range newScreens(t) {
		model.Update(tea.WindowSizeMsg{Width: 120, Height: 34})
		before := ansi.Strip(model.View().Content)
		model.Update(key('?', "?"))
		help := ansi.Strip(model.View().Content)
		if help == before || !strings.Contains(help, "Keys") {
			t.Fatalf("%s did not show help for ?:\n%s", name, help)
		}
		model.Update(key('?', "?"))
		if ansi.Strip(model.View().Content) != before {
			t.Fatalf("%s did not toggle help off", name)
		}
		if navigation := navigationOf(model); navigation != actionNone {
			t.Fatalf("%s help toggle navigated to %q", name, navigation)
		}
	}

	for name, model := range newScreens(t) {
		model.Update(tea.WindowSizeMsg{Width: 120, Height: 34})
		if plan, ok := model.(*planModel); ok {
			plan.received = true
		}
		model.Update(key(tea.KeyEsc, ""))
		if navigation := navigationOf(model); navigation != actionBack {
			t.Fatalf("%s esc navigation = %q, want %q", name, navigation, actionBack)
		}
	}

	for name, model := range newScreens(t) {
		model.Update(tea.WindowSizeMsg{Width: 120, Height: 34})
		model.Update(key('q', "q"))
		if navigation := navigationOf(model); navigation != actionNone {
			t.Fatalf("%s q navigation = %q, want no direct quit", name, navigation)
		}
		if strings.Contains(ansi.Strip(model.View().Content), "q exit") || strings.Contains(ansi.Strip(model.View().Content), "q cancel") {
			t.Fatalf("%s advertises q outside Home", name)
		}
	}

	for name, model := range newScreens(t) {
		model.Update(tea.WindowSizeMsg{Width: 120, Height: 34})
		for _, message := range []tea.Msg{key(tea.KeyDown, ""), key('j', "j"), key(tea.KeyUp, ""), key('k', "k")} {
			model.Update(message)
		}
		if navigation := navigationOf(model); navigation != actionNone {
			t.Fatalf("%s movement navigated to %q", name, navigation)
		}
	}
}

func TestOverviewOpensPlanReviewEvidenceAndHistoryOnAdvertisedKeys(t *testing.T) {
	data := overviewData{Root: t.TempDir(), Title: "Disable automatic indexing", SpecID: "SPEC-001", Now: time.Now()}
	for keystroke, want := range map[rune]string{'r': actionReview, 'e': actionEvidence, 'h': actionHistory} {
		model := newOverviewModel(data)
		updateModel(model, key(keystroke, string(keystroke)))
		if model.nav != want {
			t.Fatalf("%q navigation = %q, want %q", keystroke, model.nav, want)
		}
	}

	planned := newOverviewModel(overviewData{Title: "Intent", SpecID: "SPEC-001", Now: time.Now(), Facts: overviewFacts{PlanAvailable: true}})
	updateModel(planned, key('l', "l"))
	if planned.nav != actionPlan {
		t.Fatalf("plan navigation = %q", planned.nav)
	}

	staged := newOverviewModel(overviewData{Title: "Intent", SpecID: "SPEC-001", Now: time.Now(), Facts: overviewFacts{SetupActive: true}})
	updateModel(staged, key(tea.KeyEnter, ""))
	if staged.nav != actionDefinition {
		t.Fatalf("enter on the intent stage = %q", staged.nav)
	}

	copied := ""
	prompt := newOverviewModel(data)
	prompt.copyPrompt = func(root string) error {
		copied = root
		return nil
	}
	updateModel(prompt, key('p', "p"))
	if prompt.nav != actionNone || copied == "" {
		t.Fatalf("p copied=%q navigation=%q", copied, prompt.nav)
	}
	if !strings.Contains(ansi.Strip(prompt.View().Content), "clipboard") {
		t.Fatalf("prompt copy is not reported:\n%s", ansi.Strip(prompt.View().Content))
	}
}

func TestShellOpensDefinitionAsAnEditOfTheActiveSpec(t *testing.T) {
	root, workspace, _ := definitionRepository(t, false)
	setup := *workspace.Setup
	setup.Title = "Disable automatic indexing"
	if err := workspace.SaveSetup(setup); err != nil {
		t.Fatal(err)
	}
	if _, err := completeDefinition(root, setup, &bytes.Buffer{}, definitionServices{
		BuildPrompt: func(string) (string, error) { return "prompt", nil },
		CopyPrompt:  func(string) error { return nil },
	}); err != nil {
		t.Fatal(err)
	}

	if err := editDefinition(root); err != nil {
		t.Fatal(err)
	}
	edited, err := state.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if edited.Setup == nil || !edited.Setup.Editing || edited.Setup.Title != setup.Title {
		t.Fatalf("definition did not reopen as an edit: %+v", edited.Setup)
	}
	resumed, err := change.BeginSetup(root, "", time.Now())
	if err != nil {
		t.Fatalf("reopening Define = %v", err)
	}
	if !resumed.Editing {
		t.Fatalf("resumed setup = %+v", resumed)
	}
	if err := editDefinition(root); err != nil {
		t.Fatalf("editing an already-open draft = %v", err)
	}
}

func TestNonInteractiveCommandsKeepTheirContractAfterTheMigration(t *testing.T) {
	root, workspace, _ := definitionRepository(t, false)
	setup := *workspace.Setup
	setup.Title = "Disable automatic indexing"
	if err := workspace.SaveSetup(setup); err != nil {
		t.Fatal(err)
	}
	if _, err := completeDefinition(root, setup, &bytes.Buffer{}, definitionServices{
		BuildPrompt: func(string) (string, error) { return "prompt", nil },
		CopyPrompt:  func(string) error { return nil },
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "added.go"), []byte("package base\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	if err := runDone(root, nil, strings.NewReader(""), &output, false); err != nil {
		t.Fatalf("spec done = %v\n%s", err, output.String())
	}
	if !strings.Contains(output.String(), "no verification result") {
		t.Fatalf("missing evidence was not reported:\n%s", output.String())
	}
	records, err := workspace.HistoryRecords()
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || !records[0].CompletionAcknowledged || records[0].Stats.Files == 0 {
		t.Fatalf("archived record = %+v", records)
	}

	output.Reset()
	if err := runDone(root, nil, strings.NewReader(""), &output, false); err == nil || !strings.Contains(err.Error(), "no active change") {
		t.Fatalf("second spec done = %v", err)
	}
	if err := runHome(strings.NewReader(""), &output, false); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"definitely-not-a-command"}); err == nil || !strings.Contains(err.Error(), "unknown command") {
		t.Fatalf("unknown command = %v", err)
	}
}

func TestReviewDecisionsAreExplicitAndSurviveUnresolvedFacts(t *testing.T) {
	snapshot := reviewSnapshotFixture(nil)
	snapshot.Criteria = []change.Criterion{{Text: "Automatic indexing can be disabled", Checked: false}}
	model := newReviewModel(t.TempDir(), snapshot)
	model.record = func(string, state.TimelineEvent) error { return nil }
	model.Update(tea.WindowSizeMsg{Width: 120, Height: 34})
	model.tab = tabSummary
	plain := reviewPlain(model)
	for _, expected := range []string{
		"No implementation plan was submitted",
		"1 acceptance criterion has not been reviewed",
		"recorded against a different worktree",
		"Manual 1",
	} {
		if !strings.Contains(plain, expected) {
			t.Fatalf("summary does not expose %q:\n%s", expected, plain)
		}
	}

	if model.nav != actionNone || model.decision != decisionNone {
		t.Fatalf("opening the summary decided %q/%q", model.nav, model.decision)
	}
	setReviewCursorByID(t, model, "review.complete")
	model.Update(key(tea.KeyEnter, ""))
	if model.decision != decisionNone || model.nav != actionCompletion {
		t.Fatalf("complete = %q/%q", model.decision, model.nav)
	}

	changes := newReviewModel(t.TempDir(), snapshot)
	changes.record = func(string, state.TimelineEvent) error { return nil }
	changes.tab = tabSummary
	setReviewCursorByID(t, changes, "review.request_changes")
	changes.Update(key(tea.KeyEnter, ""))
	if changes.decision != decisionChanges || changes.nav != actionChanges {
		t.Fatalf("request changes = %q/%q", changes.decision, changes.nav)
	}
}
