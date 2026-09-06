package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/TaylorEdgerton/spec-cli/internal/change"
	"github.com/TaylorEdgerton/spec-cli/internal/evidence"
	"github.com/TaylorEdgerton/spec-cli/internal/review"
	"github.com/TaylorEdgerton/spec-cli/internal/state"
	"github.com/charmbracelet/x/ansi"
)

func disableAutoIndexPlan() state.ChangePlan {
	return state.ChangePlan{
		Summary: "Disable automatic indexing while preserving manual indexing",
		Files: []state.PlannedFile{
			{Path: "config/config.go", Action: state.PlanFileModify, Reason: "add the disable_auto_index setting"},
			{Path: "indexer/indexer.go", Action: state.PlanFileModify, Reason: "respect the setting in automatic indexing"},
			{Path: "cmd/spec/config.go", Action: state.PlanFileModify, Reason: "expose configuration input"},
		},
		IntegrationPoints: []state.PlannedIntegration{{
			ExistingSymbol: "ensureIndex", PlannedChange: "check disable_auto_index", Relationship: "controls automatic indexing",
		}},
		Verification: []state.PlannedVerification{{Behaviour: "manual indexing remains available", LikelyLocation: "indexer/indexer_test.go"}},
	}
}

func prepareIdleWorkflowRepository(t *testing.T, withIndexingCode bool) string {
	t.Helper()
	root, workspace, _ := definitionRepository(t, false)
	if err := workspace.Abandon(); err != nil {
		t.Fatal(err)
	}
	if withIndexingCode {
		files := map[string]string{
			"config/config.go":   "package config\n\ntype Config struct{}\n",
			"indexer/indexer.go": "package indexer\n\nfunc ensureIndex() error { return nil }\nfunc runManualIndex() error { return nil }\n",
			"cmd/spec/config.go": "package spec\n\nfunc bindConfig() {}\n",
		}
		for name, content := range files {
			path := filepath.Join(root, filepath.FromSlash(name))
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		runDefinitionGit(t, root, "add", "config/config.go", "indexer/indexer.go", "cmd/spec/config.go")
		runDefinitionGit(t, root, "-c", "user.name=Spec Test", "-c", "user.email=spec@example.invalid", "commit", "-m", "indexing baseline")
	}
	return root
}

func createDefinitionThroughApp(t *testing.T, app *workflowApp, setup state.Setup) {
	t.Helper()
	app.Update(key(tea.KeyEnter, ""))
	definition, ok := app.active.(*definitionModel)
	if !ok || app.screen != screenDefinition {
		t.Fatalf("Home did not open Definition: screen=%q active=%T", app.screen, app.active)
	}
	definition.setup = setup
	definition.created = true
	app.Update(workflowNavigateMsg{Action: actionOverview})
}

func writeDisableAutoIndexChanges(t *testing.T, root string) {
	t.Helper()
	updates := map[string]string{
		"config/config.go":   "package config\n\ntype Config struct { DisableAutoIndex bool }\n",
		"indexer/indexer.go": "package indexer\n\nfunc ensureIndex(disabled bool) error { if disabled { return nil }; return nil }\nfunc runManualIndex() error { return nil }\n",
		"cmd/spec/config.go": "package spec\n\nfunc bindConfig() string { return \"disable_auto_index\" }\n",
		"docs/auto-index.md": "# Automatic indexing\n\nSet `disable_auto_index` to disable automatic indexing.\n",
	}
	for name, content := range updates {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestDisableAutoIndexWorkflowFromHomeThroughHistory(t *testing.T) {
	root := prepareIdleWorkflowRepository(t, true)
	app := newWorkflowApp(root, screenHome)
	app.output = io.Discard
	app.Update(tea.WindowSizeMsg{Width: 120, Height: 34})
	if home := ansi.Strip(app.View().Content); !strings.Contains(home, "No active change") || !strings.Contains(home, "Create a change") {
		t.Fatalf("workflow did not enter framed Home:\n%s", home)
	}

	setup := state.Setup{
		Title:    "Add an option to disable automatic indexing",
		Outcome:  "Automatic indexing can be disabled while manual indexing remains available",
		Criteria: []state.SetupCriterion{{Text: "Automatic indexing can be disabled", Included: true}},
	}
	createDefinitionThroughApp(t, app, setup)
	workspace, err := state.Load(root)
	if err != nil || workspace.Setup != nil || workspace.BaseSHA == "" {
		t.Fatalf("Definition did not persist a baseline-backed Spec: %+v, %v", workspace.Metadata, err)
	}
	if app.screen != shellScreen(actionContextReview) || len(app.context) == 0 {
		t.Fatalf("Definition did not expose discovered implementation context: screen=%q context=%+v", app.screen, app.context)
	}
	app.Update(key(tea.KeyEnter, ""))
	if app.screen != screenOverview {
		t.Fatalf("context continuation opened %q", app.screen)
	}

	app.Update(workflowNavigateMsg{Action: actionPlanCapture})
	capture := app.active.(*planCaptureModel)
	planPromptCopied := false
	capture.copyPlanPrompt = func() error { planPromptCopied = true; return nil }
	app.Update(key('p', "p"))
	plan := disableAutoIndexPlan()
	canonical, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	capture.readClipboard = func() (string, error) { return "```spec-plan\n" + string(canonical) + "\n```", nil }
	app.Update(key(tea.KeyEnter, ""))
	app.Update(key(tea.KeyEnter, ""))
	if !planPromptCopied || app.screen != screenPlan {
		t.Fatalf("clipboard plan path did not reach accepted Plan: copied=%v screen=%q", planPromptCopied, app.screen)
	}
	app.Update(key('c', "c"))
	overview := app.active.(*overviewModel)
	implementationPromptCopied := false
	overview.copyPrompt = func(string) error { implementationPromptCopied = true; return nil }
	app.Update(key(tea.KeyEnter, ""))
	if !implementationPromptCopied {
		t.Fatal("accepted plan was not followed by the distinct implementation prompt action")
	}

	writeDisableAutoIndexChanges(t, root)
	workspace, _ = state.Load(root)
	now := time.Now().UTC()
	for _, run := range []state.EvidenceRun{
		{ID: "before", Phase: evidence.PhaseExisting, Command: "go test ./...", BaselineSHA: workspace.BaseSHA, StartedAt: now, FinishedAt: now.Add(time.Second), Tests: []state.EvidenceTest{{ID: "TestManualIndex", Name: "TestManualIndex", SourceDigest: "manual-v1", Status: "passed"}}},
		{ID: "after", Phase: evidence.PhaseImplementation, Command: "go test ./...", BaselineSHA: workspace.BaseSHA, StartedAt: now.Add(time.Minute), FinishedAt: now.Add(time.Minute + time.Second), Tests: []state.EvidenceTest{{ID: "TestManualIndex", Name: "TestManualIndex", SourceDigest: "manual-v1", Status: "passed"}, {ID: "TestDisableAutoIndex", Name: "TestDisableAutoIndex", SourceDigest: "disable-v1", Status: "passed"}}},
	} {
		if err := workspace.AppendEvidence(run); err != nil {
			t.Fatal(err)
		}
	}
	app.Update(key('r', "r"))
	reviewModel, ok := app.active.(*reviewModel)
	if !ok || app.screen != screenReview {
		t.Fatalf("explicit refresh did not open Review: screen=%q active=%T", app.screen, app.active)
	}
	if drift := reviewModel.snap.Projection.Drift; drift.Matched != 3 || drift.Additional != 1 || drift.Untouched != 0 {
		t.Fatalf("plan vs actual = %+v, want 3 matched / 1 additional", drift)
	}
	if summary := reviewModel.snap.Evidence.Summary; summary.Existing != 1 || summary.NewTests != 1 {
		t.Fatalf("evidence summary = %+v", summary)
	}
	setReviewCursorByID(t, reviewModel, "review.complete")
	app.Update(key(tea.KeyEnter, ""))
	if app.screen != screenComplete {
		t.Fatalf("Review completion action opened %q", app.screen)
	}
	app.Update(key(tea.KeyEnter, ""))
	if app.screen != screenHistory {
		t.Fatalf("completion did not open History: %q", app.screen)
	}
	history := app.active.(*historyModel)
	record, ok := history.selected()
	if !ok || record.PlanDrift.Matched != 3 || record.PlanDrift.Additional != 1 || record.Stats.Files != 4 || record.EvidenceSummary.NewTests != 1 || !record.CompletionAcknowledged {
		t.Fatalf("History did not retain completed review facts: %+v", record)
	}
	app.Update(key(tea.KeyEnter, ""))
	if archive := ansi.Strip(app.View().Content); !strings.Contains(archive, setup.Title) {
		t.Fatalf("completed Spec did not reopen read-only from History:\n%s", archive)
	}
}

func planProjectionForPath(t *testing.T, source state.PlanSource) review.Projection {
	t.Helper()
	root, workspace, _ := definitionRepository(t, false)
	setup := *workspace.Setup
	setup.Title = "Add an option to disable automatic indexing"
	setup.Outcome = "Keep manual indexing available"
	if _, err := saveDefinitionContract(root, setup, io.Discard); err != nil {
		t.Fatal(err)
	}
	plan := disableAutoIndexPlan()
	if source == state.PlanSourceCLI {
		body, _ := json.Marshal(plan)
		if err := runPlanCommand(root, []string{"submit", "--stdin"}, bytes.NewReader(body), io.Discard, time.Now()); err != nil {
			t.Fatal(err)
		}
	} else {
		body, _ := json.Marshal(plan)
		if _, stored, err := capturePlanDecision(root, "```spec-plan\n"+string(body)+"\n```", planAccept, "human paste", time.Now()); err != nil || stored == nil {
			t.Fatalf("paste plan = %+v, %v", stored, err)
		}
	}
	writeDisableAutoIndexChanges(t, root)
	snapshot, err := loadReviewSnapshot(root)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot.Projection
}

func TestCLIPlanSubmissionProducesTheSameReviewProjectionAsPaste(t *testing.T) {
	pasted := planProjectionForPath(t, state.PlanSourcePaste)
	cli := planProjectionForPath(t, state.PlanSourceCLI)
	if !reflect.DeepEqual(pasted, cli) {
		t.Fatalf("plan submission paths produced different review projections:\npaste=%+v\ncli=%+v", pasted, cli)
	}
}

func TestOptionalWorkflowCompletesAtNarrowWidthWithoutContextPlanOrEvidence(t *testing.T) {
	root := prepareIdleWorkflowRepository(t, false)
	app := newWorkflowApp(root, screenHome)
	app.output = io.Discard
	app.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	createDefinitionThroughApp(t, app, state.Setup{Title: "Quasar zephyr fallback"})
	if app.screen != shellScreen(actionContextReview) || len(app.context) != 0 {
		t.Fatalf("fallback scenario unexpectedly discovered context: screen=%q context=%+v", app.screen, app.context)
	}
	app.Update(key(tea.KeyEnter, ""))
	app.Update(workflowNavigateMsg{Action: actionPlanCapture})
	capture := app.active.(*planCaptureModel)
	capture.cursor = 2
	app.Update(key(tea.KeyEnter, ""))
	if app.screen != screenOverview {
		t.Fatalf("optional plan skip opened %q", app.screen)
	}
	if err := os.WriteFile(filepath.Join(root, "untracked.txt"), []byte("fallback\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	app.Update(key('r', "r"))
	reviewModel := app.active.(*reviewModel)
	if reviewModel.snap.Plan != nil || len(reviewModel.snap.Evidence.Items) != 0 || reviewModel.snap.Projection.Stats.Files != 1 {
		t.Fatalf("fallback Review invented optional facts: plan=%+v evidence=%+v stats=%+v", reviewModel.snap.Plan, reviewModel.snap.Evidence, reviewModel.snap.Projection.Stats)
	}
	setReviewCursorByID(t, reviewModel, "review.complete")
	app.Update(key(tea.KeyEnter, ""))
	app.Update(key(tea.KeyEnter, ""))
	history := app.active.(*historyModel)
	record, ok := history.selected()
	if !ok || record.Plan != nil || len(record.Evidence) != 0 || record.Stats.Files != 1 || !record.CompletionAcknowledged {
		t.Fatalf("fallback completion facts = %+v", record)
	}
}

func TestEndToEndHelpersDoNotWriteOutsideTheirTemporaryRepositories(t *testing.T) {
	// This tiny guard keeps helper changes explicit: all scenario file writes must
	// remain rooted in the test repository passed by the caller.
	root := t.TempDir()
	writeDisableAutoIndexChanges(t, root)
	for _, path := range []string{"config/config.go", "indexer/indexer.go", "cmd/spec/config.go", "docs/auto-index.md"} {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(path))); err != nil {
			t.Fatalf("missing helper output %s: %v", path, err)
		}
	}
	if _, err := os.Stat(filepath.Join(root, change.ActiveFilename)); !os.IsNotExist(err) {
		t.Fatalf("scenario helper unexpectedly wrote active Spec: %v", err)
	}
}
