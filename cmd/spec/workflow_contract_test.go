package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/TaylorEdgerton/spec-cli/internal/discovery"
	"github.com/TaylorEdgerton/spec-cli/internal/state"
	"github.com/charmbracelet/x/ansi"
)

func TestWorkflowScreensExposeCanonicalVisualAndNavigationOrder(t *testing.T) {
	definition := newDefinitionModel(state.Setup{Title: "Intent"}, "clean").screen()
	if got, want := definition.selectableItemIDs(), []string{definitionIntentID, definitionScopeID, definitionAcceptanceID, definitionCreateID}; !reflect.DeepEqual(got, want) {
		t.Fatalf("definition canonical order = %v, want %v", got, want)
	}
	overview := newOverviewModel(overviewData{Facts: overviewFacts{BaselineReady: true}}).screen()
	if got := overview.selectableItemIDs(); len(got) != 7 || got[0] != "overview.stage.intent" || got[6] != "overview.stage.complete" {
		t.Fatalf("overview canonical stages = %v", got)
	}
	plan := newPlanModel(t.TempDir(), reviewPlanFixture()).screen()
	if got, want := plan.selectableItemIDs(), []string{"plan.file.0", "plan.file.1", "plan.file.2", "plan.integration.0"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("plan canonical order = %v, want %v", got, want)
	}
	reviewModel := newReviewModel(t.TempDir(), reviewSnapshotFixture(reviewPlanFixture()))
	reviewModel.tab = tabFiles
	if got := reviewModel.screen().selectableItemIDs(); len(got) == 0 || !strings.HasPrefix(got[0], "review.file.") {
		t.Fatalf("review canonical rows = %v", got)
	}
	history := newHistoryModel(t.TempDir(), historyFixture(), false).screen()
	if got := history.selectableItemIDs(); len(got) != len(historyFixture()) || !strings.HasPrefix(got[0], "history.") {
		t.Fatalf("history canonical rows = %v", got)
	}
}

func TestViewportKeepsCanonicalSelectionVisibleWithoutResizeClamp(t *testing.T) {
	lines := []string{"zero", "one", "two", "three", "four", "five", "six", "selected", "eight"}
	visible, offset := (screenViewport{Height: 4}).visible(lines, 7)
	if offset != 5 || !strings.Contains(strings.Join(visible, "\n"), "selected") {
		t.Fatalf("offset=%d visible=%v", offset, visible)
	}
	if strings.Contains(strings.Join(visible, "\n"), "resize to see") {
		t.Fatalf("viewport used an unreachable resize clamp: %v", visible)
	}
}

func TestShellPreservesWireframeHeaderRows(t *testing.T) {
	plain := ansi.Strip(uiAppShell(80, 24, "SPEC-014 · Disable automatic indexing  OPEN\nGit: main · baseline a1b2c3d  12 min", "Intent\nAdd an option", "enter open stage  q exit"))
	lines := strings.Split(plain, "\n")
	first, second := -1, -1
	for index, line := range lines {
		if strings.Contains(line, "SPEC-014") {
			first = index
		}
		if strings.Contains(line, "Git: main") {
			second = index
		}
	}
	if first < 0 || second != first+1 {
		t.Fatalf("header rows were not preserved together:\n%s", plain)
	}
}

func TestContextReviewShowsDiscoveryProvenanceAndExplicitContinuation(t *testing.T) {
	model := newContextReviewModel([]discovery.Result{{Path: "indexer/indexer.go", Symbols: []discovery.Symbol{{Name: "ensureIndex", Capability: discovery.CapabilityPrecise}}}})
	model.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	plain := ansi.Strip(model.View().Content)
	for _, expected := range []string{"Likely implementation context", "indexer/indexer.go", "ensureIndex", "precise", "Continue", "Skip"} {
		if !strings.Contains(plain, expected) {
			t.Fatalf("context review missing %q:\n%s", expected, plain)
		}
	}
	model.Update(key(tea.KeyEnter, ""))
	if model.nav != actionContinue {
		t.Fatalf("context continue action = %q", model.nav)
	}
}

func TestPlanCapturePastePreviewEditSkipAndAcceptAreReachable(t *testing.T) {
	raw := "```spec-plan\n" + validPlanJSON + "\n```"
	model := newPlanCaptureModel("")
	model.Update(tea.PasteMsg{Content: raw})
	model.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter, Mod: tea.ModCtrl}))
	if model.mode != planCapturePreview || model.plan.Summary == "" {
		t.Fatalf("paste was not validated into preview: %+v", model)
	}
	plain := ansi.Strip(model.View().Content)
	for _, expected := range []string{"Plan preview", "Accept", "Edit", "Skip", "config/config.go"} {
		if !strings.Contains(plain, expected) {
			t.Fatalf("plan capture missing %q:\n%s", expected, plain)
		}
	}
	model.cursor = 1
	model.Update(key(tea.KeyEnter, ""))
	if model.decision != planEdit || model.mode != planCapturePaste {
		t.Fatalf("edit result = %+v", model)
	}
	model.mode, model.cursor = planCapturePreview, 2
	model.Update(key(tea.KeyEnter, ""))
	if model.decision != planSkip {
		t.Fatalf("skip decision = %q", model.decision)
	}
	model.mode, model.cursor, model.decision = planCapturePreview, 0, ""
	model.Update(key(tea.KeyEnter, ""))
	if model.decision != planAccept {
		t.Fatalf("accept decision = %q", model.decision)
	}
}

func TestPlanWireframeAdvertisedActionsAreImplemented(t *testing.T) {
	model := newPlanModel(t.TempDir(), reviewPlanFixture())
	model.Update(tea.WindowSizeMsg{Width: 120, Height: 34})
	plain := ansi.Strip(model.View().Content)
	for _, expected := range []string{"enter inspect", "e edit plan", "c continue", "b back"} {
		if !strings.Contains(plain, expected) {
			t.Fatalf("plan footer missing %q:\n%s", expected, plain)
		}
	}
	model.Update(key('c', "c"))
	if model.nav != actionContinue {
		t.Fatalf("continue action = %q", model.nav)
	}
	edit := newPlanModel(t.TempDir(), reviewPlanFixture())
	edit.Update(key('e', "e"))
	if edit.nav != actionPlanCapture {
		t.Fatalf("edit action = %q", edit.nav)
	}
}

func TestPersistentWorkflowRootTransitionsWithoutQuittingTeaProgram(t *testing.T) {
	app := newWorkflowApp(t.TempDir(), screenOverview)
	app.Update(tea.WindowSizeMsg{Width: 120, Height: 34})
	if app.screen != screenOverview || strings.TrimSpace(app.View().Content) == "" {
		t.Fatalf("initial workflow app = %+v", app)
	}
	app.Update(workflowNavigateMsg{Action: actionPlanCapture})
	if app.screen != shellScreen(actionPlanCapture) || app.done {
		t.Fatalf("plan capture transition = screen:%q done:%v", app.screen, app.done)
	}
	app.Update(workflowNavigateMsg{Action: actionQuit})
	if !app.done {
		t.Fatal("quit did not end the root app")
	}
}

func TestPersistentWorkflowCanSkipAPlanWithoutPersistingOne(t *testing.T) {
	root, workspace, _ := definitionRepository(t, false)
	setup := *workspace.Setup
	setup.Title = "Continue without a plan"
	if err := workspace.SaveSetup(setup); err != nil {
		t.Fatal(err)
	}
	if _, err := saveDefinitionContract(root, setup, io.Discard); err != nil {
		t.Fatal(err)
	}

	app := newWorkflowApp(root, shellScreen(actionPlanCapture))
	raw := "```spec-plan\n" + validPlanJSON + "\n```"
	app.Update(tea.PasteMsg{Content: raw})
	app.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter, Mod: tea.ModCtrl}))
	capture := app.active.(*planCaptureModel)
	capture.cursor = 2
	app.Update(key(tea.KeyEnter, ""))
	if app.screen != screenOverview {
		t.Fatalf("skip returned to %q, want Overview", app.screen)
	}
	stored, err := workspace.Plan()
	if err != nil || stored != nil {
		t.Fatalf("skipped plan persisted: %+v, %v", stored, err)
	}
}

func TestChangeSummaryIsASeparateExplicitDecisionState(t *testing.T) {
	model := newReviewModel(t.TempDir(), reviewSnapshotFixture(reviewPlanFixture()))
	model.tab = tabStats
	plain := ansi.Strip(model.View().Content)
	if !strings.Contains(plain, "Change Summary") || !strings.Contains(plain, "Complete Spec") || !strings.Contains(plain, "Request Changes") {
		t.Fatalf("summary contract missing:\n%s", plain)
	}
	model.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	plain = ansi.Strip(model.View().Content)
	assertTextOrder(t, plain, "Files", "Lines", "Tests", "Reviewability", "What changed", "Review attention", "Evidence", "Complete Spec", "Request Changes")
	if !sameRenderedLine(plain, "Complete Spec", "Request Changes") {
		t.Fatalf("summary decisions are not presented together:\n%s", plain)
	}
	if model.snap.RefreshedAt.IsZero() || model.snap.RefreshedAt.After(time.Now().Add(24*time.Hour)) {
		t.Fatalf("fixture refresh boundary invalid: %v", model.snap.RefreshedAt)
	}
}

func TestPersistentWorkflowFollowsDefinitionPlanRefreshReviewDecisionAndHistory(t *testing.T) {
	root, workspace, _ := definitionRepository(t, false)
	setup := *workspace.Setup
	setup.Title = "Disable automatic indexing"
	setup.Outcome = "Preserve manual indexing"
	if err := workspace.SaveSetup(setup); err != nil {
		t.Fatal(err)
	}

	app := newWorkflowApp(root, screenDefinition)
	app.Update(tea.WindowSizeMsg{Width: 120, Height: 34})
	app.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter, Mod: tea.ModCtrl}))
	if app.screen != shellScreen(actionContextReview) {
		t.Fatalf("after create screen = %q, want context review", app.screen)
	}
	app.Update(key(tea.KeyEnter, ""))
	if app.screen != screenOverview {
		t.Fatalf("after context screen = %q, want Overview", app.screen)
	}

	app.Update(workflowNavigateMsg{Action: actionPlanCapture})
	raw := "```spec-plan\n" + validPlanJSON + "\n```"
	app.Update(tea.PasteMsg{Content: raw})
	app.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter, Mod: tea.ModCtrl}))
	app.Update(key(tea.KeyEnter, ""))
	if app.screen != screenPlan {
		t.Fatalf("accepted plan screen = %q", app.screen)
	}
	stored, err := workspace.Plan()
	if err != nil || stored == nil || stored.Plan.Summary == "" {
		t.Fatalf("stored pasted plan = %+v, %v", stored, err)
	}
	app.Update(key('c', "c"))
	if app.screen != screenOverview {
		t.Fatalf("continue from plan = %q", app.screen)
	}

	if err := os.WriteFile(filepath.Join(root, "implementation.go"), []byte("package base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if overview := app.active.(*overviewModel); overview.data.Stats.Files != 0 {
		t.Fatalf("Overview silently refreshed actual state: %+v", overview.data.Stats)
	}
	app.Update(workflowNavigateMsg{Action: actionReview})
	reviewed, ok := app.active.(*reviewModel)
	if !ok || app.screen != screenReview || reviewed.snap.Projection.Stats.Files == 0 {
		t.Fatalf("explicit review refresh = screen:%q model:%T", app.screen, app.active)
	}
	reviewed.tab = tabEvidence
	app.Update(key('s', "s"))
	if app.screen != screenSummary {
		t.Fatalf("evidence to summary = %q", app.screen)
	}
	summary := app.active.(*reviewModel)
	summary.cursors[tabStats] = 1
	app.Update(key(tea.KeyEnter, ""))
	if app.screen != screenOverview {
		t.Fatalf("Request Changes returned to %q", app.screen)
	}

	app.Update(workflowNavigateMsg{Action: actionReview})
	app.Update(workflowNavigateMsg{Action: actionSummary})
	app.Update(key(tea.KeyEnter, ""))
	if app.screen != screenHistory {
		t.Fatalf("Complete Spec opened %q", app.screen)
	}
	records, err := workspace.HistoryRecords()
	if err != nil || len(records) != 1 || records[0].BaseSHA == "" || records[0].Stats.Files == 0 {
		t.Fatalf("archived history = %+v, %v", records, err)
	}
}

func TestASCIIWireframeContractsAtSupportedWidths(t *testing.T) {
	type contractModel interface {
		Update(tea.Msg) (tea.Model, tea.Cmd)
		View() tea.View
	}
	tests := []struct {
		name     string
		model    func() contractModel
		expected []string
	}{
		{"definition", func() contractModel {
			return newDefinitionModel(state.Setup{Title: "Disable automatic indexing", Outcome: "Preserve manual indexing"}, "clean")
		}, []string{"Spec · New Change", "Define Change", "Intent", "Scope / expected behaviour", "Acceptance", "Create Spec"}},
		{"overview", func() contractModel {
			return newOverviewModel(overviewData{Title: "Disable automatic indexing", SpecID: "SPEC-014", Branch: "main", Baseline: reviewBaseline, Intent: "Disable automatic indexing", Scope: "Preserve manual indexing", StartedAt: time.Now().Add(-12 * time.Minute), Now: time.Now(), Facts: overviewFacts{BaselineReady: true}})
		}, []string{"SPEC-014", "Git: main", "Intent", "Progress", "Implementation"}},
		{"plan", func() contractModel { return newPlanModel(t.TempDir(), reviewPlanFixture()) }, []string{"Implementation Plan", "Summary", "Planned files", "Existing integration points", "enter inspect"}},
		{"review-files", func() contractModel {
			model := newReviewModel(t.TempDir(), reviewSnapshotFixture(reviewPlanFixture()))
			model.tab = tabFiles
			return model
		}, []string{"Review · Files", "[Files]", "Matched", "Additional", "config/config.go"}},
		{"integration", func() contractModel {
			model := newReviewModel(t.TempDir(), reviewSnapshotFixture(reviewPlanFixture()))
			model.tab = tabIntegration
			return model
		}, []string{"Review · Integration", "Existing code interaction", "ensureIndex", "precise"}},
		{"evidence", func() contractModel {
			model := newReviewModel(t.TempDir(), reviewSnapshotFixture(reviewPlanFixture()))
			model.tab = tabEvidence
			return model
		}, []string{"Review · Evidence", "TestAutoIndexCanBeDisabled", "baseline", "run tests"}},
		{"summary", func() contractModel {
			model := newReviewModel(t.TempDir(), reviewSnapshotFixture(reviewPlanFixture()))
			model.tab = tabStats
			return model
		}, []string{"Change Summary", "Files", "Lines", "Tests", "Reviewability", "What changed", "Review attention", "Evidence", "Complete Spec", "Request Changes"}},
		{"diff", func() contractModel {
			model := newReviewModel(t.TempDir(), reviewSnapshotFixture(reviewPlanFixture()))
			model.tab = tabDiff
			return model
		}, []string{"Review · Diff", "Diff ·", "Existing symbol", "hunk"}},
		{"history", func() contractModel { return newHistoryModel(t.TempDir(), historyFixture(), true) }, []string{"Spec history", "Date", "Spec", "Status", "Selected", "Files", "duration"}},
		{"timeline", func() contractModel {
			model := newHistoryModel(t.TempDir(), historyFixture(), false)
			model.timeline = true
			return model
		}, []string{"SPEC-002 · Timeline", "Timeline", "Spec created"}},
	}
	for _, test := range tests {
		for _, size := range []struct{ width, height int }{{80, 24}, {120, 34}} {
			t.Run(test.name+fmt.Sprintf("-%dx%d", size.width, size.height), func(t *testing.T) {
				model := test.model()
				model.Update(tea.WindowSizeMsg{Width: size.width, Height: size.height})
				content := model.View().Content
				plain := ansi.Strip(content)
				for _, expected := range test.expected {
					if !strings.Contains(plain, expected) {
						t.Fatalf("wireframe missing %q:\n%s", expected, plain)
					}
				}
				assertTextOrder(t, plain, test.expected...)
				if strings.Contains(plain, "resize to see") {
					t.Fatalf("wireframe retained hard clamp:\n%s", plain)
				}
				if lipgloss.Width(content) > size.width || lipgloss.Height(content) > size.height {
					t.Fatalf("wireframe bounds = %dx%d, want <= %dx%d", lipgloss.Width(content), lipgloss.Height(content), size.width, size.height)
				}
			})
		}
	}
}

func sameRenderedLine(rendered string, fragments ...string) bool {
	for _, line := range strings.Split(rendered, "\n") {
		matched := true
		for _, fragment := range fragments {
			matched = matched && strings.Contains(line, fragment)
		}
		if matched {
			return true
		}
	}
	return false
}
