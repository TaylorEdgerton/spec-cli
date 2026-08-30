package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/TaylorEdgerton/spec-cli/internal/discovery"
	"github.com/TaylorEdgerton/spec-cli/internal/evidence"
	"github.com/TaylorEdgerton/spec-cli/internal/gitutil"
	"github.com/TaylorEdgerton/spec-cli/internal/review"
	"github.com/TaylorEdgerton/spec-cli/internal/state"
	"github.com/charmbracelet/x/ansi"
)

const reviewBaseline = "a1b2c3d4e5f6"

func reviewPlanFixture() *state.StoredChangePlan {
	return &state.StoredChangePlan{
		SchemaVersion: state.ArtifactSchemaVersion, Source: state.PlanSourcePaste, Submitter: "Codex",
		Plan: state.ChangePlan{
			Summary: "Disable automatic indexing",
			Files: []state.PlannedFile{
				{Path: "config/config.go", Action: state.PlanFileModify, Reason: "add AutoIndexEnabled"},
				{Path: "indexer/indexer.go", Action: state.PlanFileModify, Reason: "respect the setting"},
				{Path: "old_indexer.go", Action: state.PlanFileDelete, Reason: "replaced by indexer"},
			},
			IntegrationPoints: []state.PlannedIntegration{
				{ExistingSymbol: "runIndexCommand", PlannedChange: "manual path remains unchanged", Relationship: "unchanged manual path"},
			},
			Verification:  []state.PlannedVerification{{Behaviour: "manual indexing still works", LikelyLocation: "indexer/indexer_test.go"}},
			Uncertainties: []string{"CLI flag location"},
		},
	}
}

func reviewChangesFixture() []gitutil.FileChange {
	return []gitutil.FileChange{
		{Path: "config/config.go", Kind: gitutil.ChangeModified, Additions: 12, Deletions: 2},
		{Path: "indexer/indexer.go", Kind: gitutil.ChangeModified, Additions: 20, Deletions: 4},
		{Path: "cmd/spec/config.go", Kind: gitutil.ChangeModified, Additions: 6},
		{Path: "assets/logo.png", Kind: gitutil.ChangeAdded, Binary: true},
	}
}

func reviewDiscoveriesFixture() []discovery.Result {
	return []discovery.Result{{Path: "indexer/indexer.go", Symbols: []discovery.Symbol{{
		Name: "ensureIndex", Line: 84, Capability: discovery.CapabilityPrecise,
		Related: []discovery.RelatedSymbol{
			{Name: "Config.AutoIndexEnabled", Path: "config/config.go", Line: 22, Relation: "reads", Capability: discovery.CapabilityPrecise},
			{Name: "runIndexCommand", Path: "cmd/spec/root.go", Line: 141, Relation: "calls", Capability: discovery.CapabilityStructural},
		},
	}}}}
}

func reviewEvidenceFixture() evidence.Report {
	baseline := time.Date(2026, 8, 30, 9, 0, 0, 0, time.UTC)
	return evidence.Classify([]state.EvidenceRun{
		{
			ID: "run-pre", Phase: evidence.PhasePreChange, Command: "go test ./...", BaselineSHA: reviewBaseline,
			WorktreeFingerprint: "fresh", StartedAt: baseline, FinishedAt: baseline.Add(time.Minute),
			Tests: []state.EvidenceTest{{ID: "TestAutoIndexCanBeDisabled", Name: "TestAutoIndexCanBeDisabled", Path: "indexer/indexer_test.go", SourceDigest: "digest-a", Status: "failed"}},
		},
		{
			ID: "run-post", Phase: evidence.PhaseImplementation, Command: "go test ./...", BaselineSHA: reviewBaseline,
			WorktreeFingerprint: "fresh", StartedAt: baseline.Add(2 * time.Hour), FinishedAt: baseline.Add(2*time.Hour + time.Minute),
			Tests: []state.EvidenceTest{
				{ID: "TestAutoIndexCanBeDisabled", Name: "TestAutoIndexCanBeDisabled", Path: "indexer/indexer_test.go", SourceDigest: "digest-a", Status: "passed"},
				{ID: "TestConfigAutoIndexFalse", Name: "TestConfigAutoIndexFalse", Path: "config/config_test.go", SourceDigest: "digest-b", Status: "passed"},
			},
		},
		{
			ID: "run-stale", Phase: evidence.PhaseImplementation, Command: "go test ./legacy", BaselineSHA: reviewBaseline,
			WorktreeFingerprint: "stale", StartedAt: baseline.Add(3 * time.Hour), FinishedAt: baseline.Add(3*time.Hour + time.Minute),
			Tests: []state.EvidenceTest{{ID: "TestLegacyIndex", Name: "TestLegacyIndex", SourceDigest: "digest-c", Status: "passed"}},
		},
		{
			ID: "run-manual", Phase: evidence.PhaseImplementation, Manual: true, Command: "checked the CLI by hand",
			WorktreeFingerprint: "fresh", StartedAt: baseline.Add(4 * time.Hour), FinishedAt: baseline.Add(4 * time.Hour),
		},
	}, "fresh")
}

func reviewSnapshotFixture(plan *state.StoredChangePlan) reviewSnapshot {
	changes := reviewChangesFixture()
	return reviewSnapshot{
		SpecID: "SPEC-014", Title: "Disable automatic indexing", Baseline: reviewBaseline,
		Intent: "Add an option to disable automatic indexing", Scope: "Preserve manual indexing",
		Plan:       plan,
		Projection: review.Project(plan, changes, reviewDiscoveriesFixture()),
		Evidence:   reviewEvidenceFixture(),
		Hunks: map[string][]review.HunkReview{
			"indexer/indexer.go": {
				{Symbol: "ensureIndex", Hunk: gitutil.DiffHunk{Header: "@@ -80,4 +80,7 @@", OldStart: 80, OldCount: 4, NewStart: 80, NewCount: 7, Lines: []gitutil.DiffLine{
					{Kind: gitutil.DiffContext, Text: "func ensureIndex(cfg Config) error {", OldLine: 80, NewLine: 80},
					{Kind: gitutil.DiffAddition, Text: "\tif !cfg.AutoIndexEnabled {", NewLine: 81},
					{Kind: gitutil.DiffDeletion, Text: "\treturn createIndex(cfg) // " + strings.Repeat("very long trailing comment ", 20), OldLine: 81},
				}}},
				{Symbol: "createIndex", Hunk: gitutil.DiffHunk{Header: "@@ -120,2 +123,3 @@", OldStart: 120, NewStart: 123, Lines: []gitutil.DiffLine{
					{Kind: gitutil.DiffAddition, Text: "\treturn nil", NewLine: 123},
				}}},
			},
			"config/config.go": {{Symbol: "Config", Hunk: gitutil.DiffHunk{Header: "@@ -20,1 +20,2 @@", OldStart: 20, NewStart: 20, Lines: []gitutil.DiffLine{
				{Kind: gitutil.DiffAddition, Text: "\tAutoIndexEnabled bool", NewLine: 22},
			}}}},
		},
		RefreshedAt: time.Date(2026, 8, 30, 13, 0, 0, 0, time.UTC),
	}
}

func newReviewFixtureModel(t *testing.T, plan *state.StoredChangePlan) *reviewModel {
	t.Helper()
	model := newReviewModel(t.TempDir(), reviewSnapshotFixture(plan))
	model.Update(tea.WindowSizeMsg{Width: 120, Height: 34})
	return model
}

func reviewPlain(model *reviewModel) string { return ansi.Strip(model.View().Content) }

func TestReviewTabOrderMatchesKeyboardOrderAndSelectionPersistsPerTab(t *testing.T) {
	model := newReviewFixtureModel(t, reviewPlanFixture())
	for index, label := range reviewTabLabels {
		if int(model.tab) != index {
			t.Fatalf("tab %d = %d", index, model.tab)
		}
		plain := reviewPlain(model)
		if model.tab == tabStats {
			if !strings.Contains(plain, "Change Summary") {
				t.Fatalf("summary state not rendered:\n%s", plain)
			}
		} else if !strings.Contains(plain, "["+label+"]") {
			t.Fatalf("active tab %q not rendered:\n%s", label, plain)
		}
		model.Update(key(tea.KeyTab, ""))
	}
	if model.tab != tabOverview {
		t.Fatalf("tab cycle did not wrap: %d", model.tab)
	}

	model.tab = tabFiles
	model.Update(key(tea.KeyDown, ""))
	model.Update(key(tea.KeyDown, ""))
	filesCursor := model.cursors[tabFiles]
	if filesCursor != 2 {
		t.Fatalf("files cursor = %d", filesCursor)
	}
	model.tab = tabIntegration
	model.Update(key(tea.KeyDown, ""))
	model.tab = tabFiles
	if model.cursors[tabFiles] != filesCursor {
		t.Fatalf("files cursor was reset to %d", model.cursors[tabFiles])
	}
	if model.cursors[tabIntegration] != 1 {
		t.Fatalf("integration cursor = %d", model.cursors[tabIntegration])
	}
}

func TestReviewFiltersAndSelectionNeverMutateSavedPlanOrEvidence(t *testing.T) {
	plan := reviewPlanFixture()
	model := newReviewFixtureModel(t, plan)
	originalPlan, originalEvidence := *plan, reflect.DeepEqual(model.snap.Evidence, reviewEvidenceFixture())
	if !originalEvidence {
		t.Fatal("evidence fixture is not reproducible")
	}
	model.tab = tabFiles
	for range 5 {
		model.Update(key('f', "f"))
		model.Update(key(tea.KeyDown, ""))
	}
	model.tab = tabEvidence
	model.Update(key(tea.KeyDown, ""))
	if !reflect.DeepEqual(*plan, originalPlan) {
		t.Fatalf("filtering mutated the plan: %+v", *plan)
	}
	if !reflect.DeepEqual(model.snap.Evidence, reviewEvidenceFixture()) {
		t.Fatalf("filtering mutated evidence: %+v", model.snap.Evidence)
	}
}

func TestReviewTabsRenderTheirOwnContract(t *testing.T) {
	tests := []struct {
		tab      reviewTab
		expected []string
	}{
		{tabOverview, []string{"Add an option to disable automatic indexing", "Disable automatic indexing", "Changed files", "Evidence"}},
		{tabFiles, []string{"Matched", "Additional", "untouched", "config/config.go", "cmd/spec/config.go", "old_indexer.go"}},
		{tabIntegration, []string{"Existing code interaction", "ensureIndex", "Config.AutoIndexEnabled", "precise", "structural", "planned"}},
		{tabEvidence, []string{"TestAutoIndexCanBeDisabled", "TestConfigAutoIndexFalse", "baseline", "manual", "stale"}},
		{tabDiff, []string{"indexer/indexer.go", "files", "Existing symbol"}},
		{tabStats, []string{"Files", "Lines", "Reviewability", "Complete Spec", "Request Changes"}},
	}
	for _, test := range tests {
		t.Run(reviewTabLabels[test.tab], func(t *testing.T) {
			model := newReviewFixtureModel(t, reviewPlanFixture())
			model.tab = test.tab
			plain := reviewPlain(model)
			for _, expected := range test.expected {
				if !strings.Contains(plain, expected) {
					t.Fatalf("%s tab missing %q:\n%s", reviewTabLabels[test.tab], expected, plain)
				}
			}
			for _, forbidden := range []string{"ERROR", "WRONG", "VIOLATION"} {
				if strings.Contains(plain, forbidden) {
					t.Fatalf("%s tab used judgemental wording %q:\n%s", reviewTabLabels[test.tab], forbidden, plain)
				}
			}
		})
	}
}

func TestReviewOverviewFollowsTheInformationHierarchy(t *testing.T) {
	model := newReviewFixtureModel(t, reviewPlanFixture())
	assertTextOrder(t, reviewPlain(model),
		"Original intent", "Add an option to disable automatic indexing",
		"Implementation plan", "Disable automatic indexing",
		"Actual change", "Changed files",
		"Plan vs actual", "Matched",
		"Existing code integrations", "ensureIndex",
		"Implementation", "cmd/spec/config.go",
		"Evidence", "Diff",
	)

	withoutPlan := newReviewFixtureModel(t, nil)
	plain := reviewPlain(withoutPlan)
	for _, absent := range []string{"Implementation plan", "Plan vs actual"} {
		if strings.Contains(plain, absent) {
			t.Fatalf("absent plan still rendered %q:\n%s", absent, plain)
		}
	}
	for _, expected := range []string{"Original intent", "Actual change", "Changed files", "Evidence"} {
		if !strings.Contains(plain, expected) {
			t.Fatalf("plan-free overview missing %q:\n%s", expected, plain)
		}
	}
}

func TestReviewOnlyExplicitRefreshReReadsGitAndRecordsIt(t *testing.T) {
	model := newReviewFixtureModel(t, reviewPlanFixture())
	refreshes := 0
	var recorded []state.TimelineEvent
	model.refresh = func(string) (reviewSnapshot, error) {
		refreshes++
		snapshot := reviewSnapshotFixture(reviewPlanFixture())
		snapshot.RefreshedAt = time.Date(2026, 8, 30, 14, 0, 0, 0, time.UTC)
		return snapshot, nil
	}
	model.record = func(_ string, event state.TimelineEvent) error {
		recorded = append(recorded, event)
		return nil
	}

	model.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	for _, keystroke := range []tea.KeyPressMsg{key(tea.KeyTab, ""), key(tea.KeyDown, ""), key('f', "f"), key(tea.KeyUp, "")} {
		model.Update(keystroke)
	}
	reviewPlain(model)
	if refreshes != 0 || len(recorded) != 0 {
		t.Fatalf("read-only interaction wrote state: refreshes=%d recorded=%v", refreshes, recorded)
	}

	model.Update(key('r', "r"))
	if refreshes != 1 || len(recorded) != 1 || recorded[0].Type != state.TimelineActualRefreshed {
		t.Fatalf("refresh = %d recorded=%+v", refreshes, recorded)
	}
	if !model.snap.RefreshedAt.Equal(time.Date(2026, 8, 30, 14, 0, 0, 0, time.UTC)) {
		t.Fatalf("refresh did not replace the snapshot: %v", model.snap.RefreshedAt)
	}

	model.refresh = func(string) (reviewSnapshot, error) { return reviewSnapshot{}, errors.New("git is unavailable") }
	model.Update(key('r', "r"))
	if !strings.Contains(reviewPlain(model), "git is unavailable") {
		t.Fatalf("refresh failure is not visible:\n%s", reviewPlain(model))
	}
}

func TestReviewFilesTabCountsSelectsAndHandsOffToDiff(t *testing.T) {
	model := newReviewFixtureModel(t, reviewPlanFixture())
	model.tab = tabFiles
	plain := reviewPlain(model)
	for _, expected := range []string{"Matched 2", "Additional 2", "untouched 1", "planned and changed", "config/config.go", "add AutoIndexEnabled"} {
		if !strings.Contains(plain, expected) {
			t.Fatalf("files tab missing %q:\n%s", expected, plain)
		}
	}

	model.Update(key('f', "f"))
	filtered := reviewPlain(model)
	if strings.Count(filtered, "additional") == strings.Count(plain, "additional") && strings.Contains(filtered, "old_indexer.go") {
		t.Fatalf("filter did not narrow the list:\n%s", filtered)
	}
	model.filter = ""

	model.cursors[tabFiles] = 0
	selected, ok := model.selectedRow()
	if !ok || !strings.Contains(selected.ID, "config/config.go") {
		t.Fatalf("selected row = %+v ok=%v", selected, ok)
	}
	model.Update(key('d', "d"))
	if model.tab != tabDiff {
		t.Fatalf("d did not hand off to diff: tab=%d", model.tab)
	}
	if got := model.diffFile(); got != "config/config.go" {
		t.Fatalf("diff opened %q instead of the selected file", got)
	}

	empty := newReviewModel(t.TempDir(), reviewSnapshot{})
	empty.Update(tea.WindowSizeMsg{Width: 120, Height: 34})
	empty.tab = tabFiles
	if plain := reviewPlain(empty); !strings.Contains(plain, "No file changes") {
		t.Fatalf("empty files tab did not explain itself:\n%s", plain)
	}
}

func TestReviewIntegrationTabLabelsPrecisionPreviewsAndHandsOff(t *testing.T) {
	model := newReviewFixtureModel(t, reviewPlanFixture())
	model.tab = tabIntegration
	opened := 0
	model.open = func(string, discovery.Result) error {
		opened++
		return errors.New("code is unavailable")
	}
	plain := reviewPlain(model)
	for _, expected := range []string{"runIndexCommand", "manual path remains unchanged", "planned", "ensureIndex", "precise", "config/config.go:22", "structural"} {
		if !strings.Contains(plain, expected) {
			t.Fatalf("integration tab missing %q:\n%s", expected, plain)
		}
	}

	model.cursors[tabIntegration] = 1
	if plain := reviewPlain(model); !strings.Contains(plain, "Preview") {
		t.Fatalf("integration tab has no code preview:\n%s", plain)
	}
	model.Update(key('o', "o"))
	if opened != 1 || !strings.Contains(reviewPlain(model), "code is unavailable") {
		t.Fatalf("VS Code hand-off = %d:\n%s", opened, reviewPlain(model))
	}
}

func TestReviewEvidenceTabExposesProvenanceAndActions(t *testing.T) {
	model := newReviewFixtureModel(t, reviewPlanFixture())
	model.tab = tabEvidence
	runs := 0
	model.runTests = func(string) error {
		runs++
		return nil
	}
	plain := reviewPlain(model)
	for _, expected := range []string{
		"Pre-change reproduction", "TestAutoIndexCanBeDisabled", "failed against baseline a1b2c3d",
		"test modified after baseline: NO", "Added during implementation", "TestConfigAutoIndexFalse",
		"manual", "checked the CLI by hand", "stale", "Fail → pass", "1",
	} {
		if !strings.Contains(plain, expected) {
			t.Fatalf("evidence tab missing %q:\n%s", expected, plain)
		}
	}
	model.Update(key('t', "t"))
	if runs != 1 {
		t.Fatalf("t did not run tests: %d", runs)
	}
	model.Update(key('d', "d"))
	if model.tab != tabDiff {
		t.Fatalf("d did not hand off to the test diff: tab=%d", model.tab)
	}
}

func TestReviewDiffTabMovesFilesAndHunksAndAnnotatesThem(t *testing.T) {
	model := newReviewFixtureModel(t, reviewPlanFixture())
	model.tab = tabDiff
	plain := reviewPlain(model)
	if !strings.Contains(plain, "1 / 4 files") {
		t.Fatalf("diff file counter missing:\n%s", plain)
	}

	model.Update(key(tea.KeyDown, ""))
	if got := model.diffFile(); got != "cmd/spec/config.go" {
		t.Fatalf("file movement = %q", got)
	}
	model.cursors[tabDiff] = 0
	for model.diffFile() != "indexer/indexer.go" {
		model.Update(key(tea.KeyDown, ""))
	}
	plain = reviewPlain(model)
	for _, expected := range []string{"ensureIndex", "+", "-", "Planned: yes", "Integration changed: yes"} {
		if !strings.Contains(plain, expected) {
			t.Fatalf("diff tab missing %q:\n%s", expected, plain)
		}
	}
	if strings.Contains(plain, strings.Repeat("very long trailing comment ", 4)) {
		t.Fatalf("long diff line was not truncated:\n%s", plain)
	}

	if model.hunk != 0 {
		t.Fatalf("hunk cursor = %d", model.hunk)
	}
	model.Update(key('n', "n"))
	if model.hunk != 1 || !strings.Contains(reviewPlain(model), "createIndex") {
		t.Fatalf("n did not advance the hunk: %d", model.hunk)
	}
	model.Update(key('p', "p"))
	if model.hunk != 0 {
		t.Fatalf("p did not return to the previous hunk: %d", model.hunk)
	}

	for model.diffFile() != "assets/logo.png" {
		model.Update(key(tea.KeyDown, ""))
	}
	if plain := reviewPlain(model); !strings.Contains(plain, "Binary file") {
		t.Fatalf("binary diff message missing:\n%s", plain)
	}
	empty := newReviewModel(t.TempDir(), reviewSnapshot{})
	empty.Update(tea.WindowSizeMsg{Width: 120, Height: 34})
	empty.tab = tabDiff
	if plain := reviewPlain(empty); !strings.Contains(plain, "No diff") {
		t.Fatalf("empty diff message missing:\n%s", plain)
	}
}

func TestReviewStatsTabShowsTotalsThresholdsAndExplicitDecisions(t *testing.T) {
	model := newReviewFixtureModel(t, reviewPlanFixture())
	model.tab = tabStats
	var recorded []state.TimelineEvent
	model.record = func(_ string, event state.TimelineEvent) error {
		recorded = append(recorded, event)
		return nil
	}
	plain := reviewPlain(model)
	for _, expected := range []string{"4", "+38", "-6", "GOOD", "6 files", "300", "Review attention", "2 files changed outside the original plan", "Complete Spec", "Request Changes"} {
		if !strings.Contains(plain, expected) {
			t.Fatalf("stats tab missing %q:\n%s", expected, plain)
		}
	}

	if model.decision != decisionNone {
		t.Fatalf("decision before any action = %q", model.decision)
	}
	model.Update(key(tea.KeyDown, ""))
	reviewPlain(model)
	if model.decision != decisionNone || len(recorded) != 0 {
		t.Fatalf("selection recorded a decision: %q %+v", model.decision, recorded)
	}

	model.cursors[tabStats] = 1
	model.Update(key(tea.KeyEnter, ""))
	if model.decision != decisionChanges || len(recorded) != 1 || recorded[0].Type != state.TimelineChangesRequested {
		t.Fatalf("request changes = %q recorded=%+v", model.decision, recorded)
	}
	if model.snap.Plan == nil || len(model.snap.Evidence.Items) == 0 {
		t.Fatal("request changes discarded plan or evidence facts")
	}
	model.cursors[tabStats] = 0
	model.Update(key(tea.KeyEnter, ""))
	if model.decision != decisionComplete || len(recorded) != 2 || recorded[1].Type != state.TimelineReviewDecision {
		t.Fatalf("complete = %q recorded=%+v", model.decision, recorded)
	}
}

func TestReviewRendersInsideSupportedWindowsAndExplainsSmallerOnes(t *testing.T) {
	for _, size := range []struct{ width, height int }{{80, 24}, {120, 34}} {
		model := newReviewModel(t.TempDir(), reviewSnapshotFixture(reviewPlanFixture()))
		model.Update(tea.WindowSizeMsg{Width: size.width, Height: size.height})
		for tab := tabOverview; tab <= tabStats; tab++ {
			model.tab = tab
			content := model.View().Content
			if got := lipgloss.Width(content); got > size.width {
				t.Fatalf("%s at %dx%d overflowed to width %d", reviewTabLabels[tab], size.width, size.height, got)
			}
			if got := lipgloss.Height(content); got > size.height {
				t.Fatalf("%s at %dx%d overflowed to height %d", reviewTabLabels[tab], size.width, size.height, got)
			}
		}
	}

	small := newReviewModel(t.TempDir(), reviewSnapshotFixture(reviewPlanFixture()))
	small.Update(tea.WindowSizeMsg{Width: 60, Height: 20})
	plain := ansi.Strip(small.View().Content)
	if !strings.Contains(plain, "80") || !strings.Contains(strings.ToLower(plain), "terminal") {
		t.Fatalf("undersized terminal message missing:\n%s", plain)
	}
	small.Update(key(tea.KeyTab, ""))
	if small.tab != tabFiles {
		t.Fatalf("navigation was corrupted in an undersized terminal: tab=%d", small.tab)
	}
}

func TestLoadReviewSnapshotReadsGitStateDiscoveryAndRecordsRefreshEvents(t *testing.T) {
	root, workspace, _ := definitionRepository(t, false)
	setup := *workspace.Setup
	setup.Title = "Disable automatic indexing"
	setup.Outcome = "Preserve manual indexing"
	if err := workspace.SaveSetup(setup); err != nil {
		t.Fatal(err)
	}
	if _, err := completeDefinition(root, setup, &bytes.Buffer{}, definitionServices{
		BuildPrompt: func(string) (string, error) { return "prompt", nil },
		CopyPrompt:  func(string) error { return nil },
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "base.go"), []byte("package base\n\nfunc ensureIndex() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "added.go"), []byte("package base\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	queried := 0
	original := findReviewContext
	t.Cleanup(func() { findReviewContext = original })
	findReviewContext = func(gotRoot string, query discovery.Query) ([]discovery.Result, error) {
		queried++
		if gotRoot != root || query.Intent != setup.Title {
			t.Fatalf("discovery query = %q %+v", gotRoot, query)
		}
		return []discovery.Result{{Path: "base.go", Symbols: []discovery.Symbol{{
			Name: "ensureIndex", Line: 1, Capability: discovery.CapabilityStructural,
			Related: []discovery.RelatedSymbol{{Name: "createIndex", Path: "base.go", Line: 3, Relation: "calls", Capability: discovery.CapabilityStructural}},
		}}}}, nil
	}

	snapshot, err := loadReviewSnapshot(root)
	if err != nil {
		t.Fatal(err)
	}
	if queried != 1 {
		t.Fatalf("discovery was consulted %d times", queried)
	}
	if snapshot.Intent != setup.Title || snapshot.Baseline == "" {
		t.Fatalf("snapshot identity = %+v", snapshot)
	}
	if snapshot.Projection.Stats.Files != 2 {
		t.Fatalf("changed files = %+v", snapshot.Projection.Files)
	}
	if len(snapshot.Projection.Integrations) == 0 || snapshot.Projection.Integrations[0].Parent != "ensureIndex" {
		t.Fatalf("integrations = %+v", snapshot.Projection.Integrations)
	}
	hunks := snapshot.Hunks["base.go"]
	if len(hunks) == 0 || hunks[0].Symbol != "ensureIndex" {
		t.Fatalf("hunks = %+v", hunks)
	}

	event := state.TimelineEvent{ID: "SPEC-001:refresh:1", Type: state.TimelineActualRefreshed, Actor: "human", Source: "review", OccurredAt: time.Now().UTC()}
	if err := recordReviewEvent(root, event); err != nil {
		t.Fatal(err)
	}
	events, err := workspace.TimelineEvents()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, recorded := range events {
		found = found || recorded.ID == event.ID
	}
	if !found {
		t.Fatalf("refresh event was not recorded: %+v", events)
	}
}
