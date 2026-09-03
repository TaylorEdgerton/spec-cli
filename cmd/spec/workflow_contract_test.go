package main

import (
	"bytes"
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
	if got, want := overview.selectableItemIDs(), []string{"overview.next.prompt", "overview.next.plan.capture", "overview.next.review"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("overview canonical NEXT actions = %v, want %v", got, want)
	}
	plan := newPlanModel(t.TempDir(), reviewPlanFixture()).screen()
	if got, want := plan.selectableItemIDs(), []string{"plan.file.0", "plan.file.1", "plan.file.2", "plan.integration.0"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("plan canonical order = %v, want %v", got, want)
	}
	reviewModel := newReviewModel(t.TempDir(), reviewSnapshotFixture(reviewPlanFixture()))
	reviewModel.tab = tabChanges
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

func TestPlanCaptureStartsWithChoiceAndClipboardPreviewPersistsOnlyOnAccept(t *testing.T) {
	raw := "```spec-plan\n" + validPlanJSON + "\n```"
	model := newPlanCaptureModel("")
	if model.mode != planCaptureChoice || strings.Contains(ansi.Strip(model.View().Content), "Paste one response containing") {
		t.Fatalf("plan capture did not start at the guided choice: %+v\n%s", model, ansi.Strip(model.View().Content))
	}
	plain := ansi.Strip(model.View().Content)
	for _, expected := range []string{"copy plan prompt", "Import plan from clipboard", "Wait for `spec plan submit --stdin`", "Skip plan for this change", "never required"} {
		if !strings.Contains(plain, expected) {
			t.Fatalf("plan choice missing %q:\n%s", expected, plain)
		}
	}
	model.readClipboard = func() (string, error) { return raw, nil }
	model.Update(key(tea.KeyEnter, ""))
	if model.mode != planCaptureClipboardPreview || model.plan.Summary == "" || model.decision != "" {
		t.Fatalf("clipboard was not validated without persistence: %+v", model)
	}
	plain = ansi.Strip(model.View().Content)
	for _, expected := range []string{"spec-plan detected", "Accept Plan", "Paste different response", "Inspect/Edit", "Skip", "config/config.go"} {
		if !strings.Contains(plain, expected) {
			t.Fatalf("plan capture missing %q:\n%s", expected, plain)
		}
	}
	model.cursor = 2
	model.Update(key(tea.KeyEnter, ""))
	if model.decision != planEdit || model.mode != planCapturePaste {
		t.Fatalf("edit result = %+v", model)
	}
	model.mode, model.cursor = planCaptureClipboardPreview, 3
	model.Update(key(tea.KeyEnter, ""))
	if model.decision != planSkip {
		t.Fatalf("skip decision = %q", model.decision)
	}
	model.mode, model.cursor, model.decision = planCaptureClipboardPreview, 0, ""
	model.Update(key(tea.KeyEnter, ""))
	if model.decision != planAccept {
		t.Fatalf("accept decision = %q", model.decision)
	}
}

func TestPlanCaptureClipboardErrorFallsBackToManualPaste(t *testing.T) {
	model := newPlanCaptureModel("")
	model.readClipboard = func() (string, error) { return "not a plan", nil }
	model.Update(key(tea.KeyEnter, ""))
	if model.mode != planCaptureClipboardError || !strings.Contains(model.status, "no fenced spec-plan") {
		t.Fatalf("invalid clipboard state = %+v", model)
	}
	if strings.Contains(ansi.Strip(model.View().Content), "Ctrl+Enter preview") {
		t.Fatal("manual editor was shown before the explicit fallback action")
	}
	model.Update(key(tea.KeyEnter, ""))
	if model.mode != planCapturePaste {
		t.Fatalf("manual paste fallback not opened: %+v", model)
	}
}

func TestPlanCaptureCLIWaitRefreshFindsPersistedValidatedPlan(t *testing.T) {
	model := newPlanCaptureModel("")
	model.cursor = 1
	model.reloadPlan = func() (*state.StoredChangePlan, error) { return nil, nil }
	model.Update(key(tea.KeyEnter, ""))
	if model.mode != planCaptureCLIWait || !strings.Contains(ansi.Strip(model.View().Content), "spec plan submit --stdin") {
		t.Fatalf("CLI wait state = %+v\n%s", model, ansi.Strip(model.View().Content))
	}
	plan, err := validateChangePlan([]byte(validPlanJSON))
	if err != nil {
		t.Fatal(err)
	}
	model.reloadPlan = func() (*state.StoredChangePlan, error) {
		return &state.StoredChangePlan{Source: state.PlanSourceCLI, Plan: plan}, nil
	}
	model.Update(key('r', "r"))
	if model.mode != planCaptureClipboardPreview || !model.acceptedPersisted || model.plan.Summary == "" {
		t.Fatalf("CLI refresh did not load persisted plan: %+v", model)
	}
}

func TestPlanCaptureCopiesPlanPromptInPlaceAndReportsFailureHonestly(t *testing.T) {
	model := newPlanCaptureModel("")
	copied := 0
	model.copyPlanPrompt = func() error { copied++; return nil }
	model.Update(key('p', "p"))
	if copied != 1 || model.mode != planCaptureChoice || model.nav != actionNone || !strings.Contains(model.status, "copied") {
		t.Fatalf("copy action state=%+v copied=%d", model, copied)
	}
	model.copyPlanPrompt = func() error { return fmt.Errorf("no clipboard") }
	model.Update(key('p', "p"))
	if !strings.Contains(model.status, "Clipboard unavailable") || strings.Contains(model.status, "copied") {
		t.Fatalf("failed copy status=%q", model.status)
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
	app.Update(key('q', "q"))
	if app.done || !app.quitConfirm || app.quitCursor != 0 {
		t.Fatal("q outside Home did not open a safe confirmation")
	}
	app.Update(key(tea.KeyEnter, ""))
	if app.done || app.quitConfirm {
		t.Fatal("default quit confirmation did not stay in Spec")
	}
}

func TestRootNavigationBackHomeQuitAndEmergencyExitContract(t *testing.T) {
	root, workspace, _ := definitionRepository(t, false)
	before, err := os.ReadFile(filepath.Join(workspace.Dir, "metadata.json"))
	if err != nil {
		t.Fatal(err)
	}

	app := newWorkflowApp(root, screenOverview)
	app.Update(key(tea.KeyEsc, ""))
	if app.done || app.screen != screenHome {
		t.Fatalf("back from root child = screen:%q done:%v", app.screen, app.done)
	}
	app.Update(key(tea.KeyDown, ""))
	afterCursor, err := os.ReadFile(filepath.Join(workspace.Dir, "metadata.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, afterCursor) {
		t.Fatal("Home cursor movement mutated persisted workflow state")
	}
	app.Update(key(tea.KeyEsc, ""))
	if app.done || app.screen != screenHome {
		t.Fatalf("back from Home = screen:%q done:%v", app.screen, app.done)
	}

	app.Update(workflowNavigateMsg{Action: actionResume})
	if app.screen == screenHome {
		t.Fatal("Resume did not leave Home")
	}
	app.Update(key('g', "g"))
	after, err := os.ReadFile(filepath.Join(workspace.Dir, "metadata.json"))
	if err != nil {
		t.Fatal(err)
	}
	if app.screen != screenHome || !bytes.Equal(before, after) {
		t.Fatalf("g Home mutated workflow state or missed Home: screen=%q", app.screen)
	}

	app.Update(key('q', "q"))
	if !app.done {
		t.Fatal("q from Home did not exit")
	}

	emergency := newWorkflowApp(root, screenHome)
	emergency.Update(tea.KeyPressMsg(tea.Key{Code: 'c', Mod: tea.ModCtrl}))
	if !emergency.done {
		t.Fatal("Ctrl+C did not emergency-exit")
	}
}

func TestRootHomeDestinationsStayInsideOneApplication(t *testing.T) {
	root, _, _ := definitionRepository(t, false)
	for action, want := range map[string]shellScreen{
		actionResume:    screenDefinition,
		actionExplore:   screenExplore,
		actionRecent:    screenHistory,
		actionDocuments: screenDocuments,
	} {
		app := newWorkflowApp(root, screenHome)
		app.Update(workflowNavigateMsg{Action: action})
		if app.done || app.screen != want || app.active == nil {
			t.Fatalf("%s = screen:%q active:%T done:%v, want %q", action, app.screen, app.active, app.done, want)
		}
		app.Update(workflowNavigateMsg{Action: actionBack})
		if app.done || app.screen != screenHome {
			t.Fatalf("%s back = screen:%q done:%v", action, app.screen, app.done)
		}
	}
}

func TestIdleHomeStartsDefinitionInsideRootAndBackReturnsHome(t *testing.T) {
	root, workspace, _ := definitionRepository(t, false)
	if err := workspace.Abandon(); err != nil {
		t.Fatal(err)
	}
	app := newWorkflowApp(root, screenHome)
	app.Update(workflowNavigateMsg{Action: actionNew})
	if app.done || app.screen != screenDefinition {
		t.Fatalf("new change = screen:%q done:%v", app.screen, app.done)
	}
	loaded, err := state.Load(root)
	if err != nil || !loaded.Active || loaded.Setup == nil {
		t.Fatalf("new change setup = %+v, %v", loaded.Metadata, err)
	}
	app.Update(workflowNavigateMsg{Action: actionBack})
	if app.done || app.screen != screenHome {
		t.Fatalf("new change back = screen:%q done:%v", app.screen, app.done)
	}
}

func TestRootResponsiveNavigationRailOverlayAndMinimumSize(t *testing.T) {
	root, _, _ := definitionRepository(t, false)
	wide := newWorkflowApp(root, screenOverview)
	wide.Update(tea.WindowSizeMsg{Width: 120, Height: 34})
	wideView := wide.View().Content
	widePlain := ansi.Strip(wideView)
	for _, expected := range []string{"CHANGE", "Intent & Scope", "REVIEW", "Explore", "History", "Home"} {
		if !strings.Contains(widePlain, expected) {
			t.Fatalf("wide navigation missing %q:\n%s", expected, widePlain)
		}
	}
	if lipgloss.Width(wideView) > 120 || lipgloss.Height(wideView) > 34 {
		t.Fatalf("wide root bounds = %dx%d", lipgloss.Width(wideView), lipgloss.Height(wideView))
	}

	narrow := newWorkflowApp(root, screenOverview)
	narrow.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	narrow.Update(key('n', "n"))
	narrowPlain := ansi.Strip(narrow.View().Content)
	if !strings.Contains(narrowPlain, "Navigate") || !strings.Contains(narrowPlain, "Home") {
		t.Fatalf("narrow navigation overlay missing:\n%s", narrowPlain)
	}

	small := newWorkflowApp(root, screenOverview)
	small.Update(tea.WindowSizeMsg{Width: 50, Height: 12})
	if plain := ansi.Strip(small.View().Content); !strings.Contains(plain, "Terminal is too small") {
		t.Fatalf("small terminal state missing:\n%s", plain)
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
	capture.cursor = 3
	app.Update(key(tea.KeyEnter, ""))
	if app.screen != screenOverview {
		t.Fatalf("skip returned to %q, want Overview", app.screen)
	}
	stored, err := workspace.Plan()
	if err != nil || stored != nil {
		t.Fatalf("skipped plan persisted: %+v, %v", stored, err)
	}
}

func TestChangeSummaryIsTheFirstReviewViewAndExplicitDecisionState(t *testing.T) {
	model := newReviewModel(t.TempDir(), reviewSnapshotFixture(reviewPlanFixture()))
	model.tab = tabSummary
	plain := ansi.Strip(model.View().Content)
	if !strings.Contains(plain, "Review · Summary") || !strings.Contains(plain, "Complete Spec") || !strings.Contains(plain, "Request Changes") {
		t.Fatalf("summary contract missing:\n%s", plain)
	}
	model.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	plain = ansi.Strip(model.View().Content)
	assertTextOrder(t, plain, "Original intent", "Implementation plan", "Actual change", "Files", "Lines", "Tests", "Reviewability", "Plan vs actual", "Review attention", "Evidence", "Complete Spec", "Request Changes")
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
	if !ok || app.screen != screenReview || reviewed.tab != tabSummary || reviewed.snap.Projection.Stats.Files == 0 {
		t.Fatalf("explicit review refresh = screen:%q model:%T", app.screen, app.active)
	}
	app.Update(workflowNavigateMsg{Action: actionBack})
	overviewAfterRefresh, ok := app.active.(*overviewModel)
	if !ok || !overviewAfterRefresh.data.StatsRefreshed || overviewAfterRefresh.data.RefreshedAt.IsZero() || overviewAfterRefresh.data.Stats.Files == 0 {
		t.Fatalf("Overview did not receive cached explicit refresh: screen:%q model:%T data:%+v", app.screen, app.active, overviewAfterRefresh)
	}
	app.Update(workflowNavigateMsg{Action: actionReview})
	reviewed = app.active.(*reviewModel)
	reviewed.tab = tabEvidence
	app.Update(key('s', "s"))
	if app.screen != screenSummary {
		t.Fatalf("evidence to summary = %q", app.screen)
	}
	summary := app.active.(*reviewModel)
	setReviewCursorByID(t, summary, "review.request_changes")
	app.Update(key(tea.KeyEnter, ""))
	if app.screen != screenOverview {
		t.Fatalf("Request Changes returned to %q", app.screen)
	}

	app.Update(workflowNavigateMsg{Action: actionReview})
	app.Update(workflowNavigateMsg{Action: actionSummary})
	setReviewCursorByID(t, app.active.(*reviewModel), "review.complete")
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
		}, []string{"SPEC-014", "main", "Intent", "NEXT", "Change lifecycle", "Since baseline"}},
		{"plan", func() contractModel { return newPlanModel(t.TempDir(), reviewPlanFixture()) }, []string{"Implementation Plan", "Summary", "Planned changes", "Existing integration points", "enter inspect"}},
		{"review-changes", func() contractModel {
			model := newReviewModel(t.TempDir(), reviewSnapshotFixture(reviewPlanFixture()))
			model.tab = tabChanges
			return model
		}, []string{"Review · Changes", "[Changes]", "Matched", "Additional"}},
		{"integration", func() contractModel {
			model := newReviewModel(t.TempDir(), reviewSnapshotFixture(reviewPlanFixture()))
			model.tab = tabIntegration
			return model
		}, []string{"Review · Integration", "Existing-code boundaries", "runIndexCommand", "planned"}},
		{"evidence", func() contractModel {
			model := newReviewModel(t.TempDir(), reviewSnapshotFixture(reviewPlanFixture()))
			model.tab = tabEvidence
			return model
		}, []string{"Review · Evidence", "TestAutoIndexCanBeDisabled", "baseline", "run tests"}},
		{"summary", func() contractModel {
			model := newReviewModel(t.TempDir(), reviewSnapshotFixture(reviewPlanFixture()))
			model.tab = tabSummary
			return model
		}, []string{"Review · Summary", "[Summary]", "Original intent", "Actual change", "Files", "Lines", "Tests", "Reviewability", "Review attention", "Evidence", "Complete Spec", "Request Changes"}},
		{"diff", func() contractModel {
			model := newReviewModel(t.TempDir(), reviewSnapshotFixture(reviewPlanFixture()))
			model.tab = tabDiff
			return model
		}, []string{"Review · Diff", "Files", "Focused hunk", "Symbol"}},
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
