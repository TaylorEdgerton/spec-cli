package main

import (
	"bytes"
	"errors"
	"fmt"
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

func samePlainLine(text, left, right string) bool {
	for _, line := range strings.Split(text, "\n") {
		if strings.Contains(line, left) && strings.Contains(line, right) {
			return true
		}
	}
	return false
}

func setReviewCursorByID(t *testing.T, model *reviewModel, id string) {
	t.Helper()
	for index, item := range model.screen().selectableItems() {
		if item.ID == id {
			model.cursors[model.tab] = index
			return
		}
	}
	t.Fatalf("review item %q is unavailable: %v", id, model.screen().selectableItemIDs())
}

func TestReviewUsesExactlyFiveViewsWithSummaryFirst(t *testing.T) {
	want := []string{"Summary", "Changes", "Integration", "Evidence", "Diff"}
	if !reflect.DeepEqual(reviewTabLabels, want) {
		t.Fatalf("review views = %v, want %v", reviewTabLabels, want)
	}
	model := newReviewFixtureModel(t, reviewPlanFixture())
	if model.tab != tabSummary {
		t.Fatalf("initial review view = %v, want Summary", model.tab)
	}
	original := model.snap
	for _, label := range want {
		plain := reviewPlain(model)
		if !strings.Contains(plain, "["+label+"]") {
			t.Fatalf("active view %q not rendered:\n%s", label, plain)
		}
		model.Update(key(tea.KeyTab, ""))
	}
	if model.tab != tabSummary || !reflect.DeepEqual(model.snap, original) {
		t.Fatalf("view cycling changed refreshed snapshot or did not wrap: tab=%v", model.tab)
	}
}

func TestReviewAttentionProjectionIsDeterministicAndRoutesToOwningViews(t *testing.T) {
	snapshot := reviewSnapshotFixture(reviewPlanFixture())
	snapshot.Projection.Stats.Files = 13
	snapshot.Evidence.Items = append(snapshot.Evidence.Items,
		evidence.Item{ID: "modified", Name: "TestModified", Category: evidence.CategoryModifiedExisting, Automated: true, Passing: true, Fresh: true},
		evidence.Item{ID: "failing", Name: "TestFailing", Category: evidence.CategoryExisting, Automated: true, Passing: false, Fresh: true},
	)
	items := projectReviewAttention(snapshot)
	wantIDs := []string{"additional", "untouched", "integration", "modified-tests", "failing-evidence", "stale-evidence", "large-baseline-drift"}
	for _, id := range wantIDs {
		found := false
		for _, item := range items {
			if item.ID == id {
				found = true
				if id == "additional" && item.Kind != attentionNeutral {
					t.Fatalf("additional files were framed as %q, want neutral", item.Kind)
				}
			}
		}
		if !found {
			t.Fatalf("attention projection missing %q: %+v", id, items)
		}
	}
	if again := projectReviewAttention(snapshot); !reflect.DeepEqual(items, again) {
		t.Fatalf("attention projection is not deterministic:\n%+v\n%+v", items, again)
	}
	for index, attention := range items {
		model := newReviewModel(t.TempDir(), snapshot)
		model.record = func(string, state.TimelineEvent) error { return nil }
		model.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
		model.cursors[tabSummary] = index
		if plain := reviewPlain(model); !strings.Contains(plain, attention.Label) || !strings.Contains(plain, "Complete Spec") {
			t.Fatalf("compact Summary hid selected attention %q or decisions:\n%s", attention.ID, plain)
		}
		model.Update(key(tea.KeyEnter, ""))
		if model.tab != attention.Target {
			t.Fatalf("attention %q routed to %s, want %s", attention.ID, reviewTabLabels[model.tab], reviewTabLabels[attention.Target])
		}
	}

	model := newReviewModel(t.TempDir(), snapshot)
	model.Update(tea.WindowSizeMsg{Width: 120, Height: 34})
	writes := 0
	model.record = func(string, state.TimelineEvent) error { writes++; return nil }
	model.Update(key(tea.KeyDown, ""))
	if writes != 0 {
		t.Fatalf("attention selection wrote %d events", writes)
	}
	model.cursors[tabSummary] = 0
	model.Update(key(tea.KeyEnter, ""))
	if model.tab != tabChanges || writes != 0 {
		t.Fatalf("additional attention routed to tab=%v with %d writes", model.tab, writes)
	}
}

func TestReviewSummaryShowsHierarchyAndEmptyViewsExplainMissingFacts(t *testing.T) {
	model := newReviewFixtureModel(t, reviewPlanFixture())
	plain := reviewPlain(model)
	assertTextOrder(t, plain, "Original intent", "Actual change", "Original plan vs actual", "Review attention", "Evidence", "Complete Spec", "Request Changes")
	for _, expected := range []string{"Add an option to disable automatic indexing", "4", "+38", "-6", "Matched 2", "Additional 2", "Existing tests", "New tests"} {
		if !strings.Contains(plain, expected) {
			t.Fatalf("summary missing %q:\n%s", expected, plain)
		}
	}
	for _, detail := range []string{"config/config.go", "ensureIndex", "TestAutoIndexCanBeDisabled"} {
		if strings.Contains(plain, detail) {
			t.Fatalf("summary dumped detail %q instead of progressively disclosing it:\n%s", detail, plain)
		}
	}

	empty := newReviewModel(t.TempDir(), reviewSnapshot{RefreshedAt: time.Now()})
	empty.Update(tea.WindowSizeMsg{Width: 120, Height: 34})
	if summary := reviewPlain(empty); !strings.Contains(summary, "No implementation plan was submitted") || !strings.Contains(summary, "No evidence has been recorded") {
		t.Fatalf("empty summary is not explanatory:\n%s", summary)
	}
	for tab, expected := range map[reviewTab]string{
		tabChanges:     "No file changes",
		tabIntegration: "No declared or discovered relationships",
		tabEvidence:    "No evidence has been recorded",
		tabDiff:        "No diff is available",
	} {
		empty.tab = tab
		if plain := reviewPlain(empty); !strings.Contains(plain, expected) {
			t.Fatalf("%s empty view missing %q:\n%s", reviewTabLabels[tab], expected, plain)
		}
	}
}

func TestReviewTabOrderMatchesKeyboardOrderAndSelectionPersistsPerTab(t *testing.T) {
	model := newReviewFixtureModel(t, reviewPlanFixture())
	for index, label := range reviewTabLabels {
		if int(model.tab) != index {
			t.Fatalf("tab %d = %d", index, model.tab)
		}
		plain := reviewPlain(model)
		if !strings.Contains(plain, "["+label+"]") {
			t.Fatalf("active tab %q not rendered:\n%s", label, plain)
		}
		model.Update(key(tea.KeyTab, ""))
	}
	if model.tab != tabSummary {
		t.Fatalf("tab cycle did not wrap: %d", model.tab)
	}

	model.tab = tabChanges
	model.Update(key(tea.KeyDown, ""))
	model.Update(key(tea.KeyDown, ""))
	filesCursor := model.cursors[tabChanges]
	if filesCursor != 2 {
		t.Fatalf("files cursor = %d", filesCursor)
	}
	model.tab = tabIntegration
	model.Update(key(tea.KeyDown, ""))
	model.tab = tabChanges
	if model.cursors[tabChanges] != filesCursor {
		t.Fatalf("changes cursor was reset to %d", model.cursors[tabChanges])
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
	model.tab = tabChanges
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
		{tabSummary, []string{"Add an option to disable automatic indexing", "Disable automatic indexing", "Actual change", "Review attention", "Evidence"}},
		{tabChanges, []string{"Matched", "Additional", "untouched", "config/config.go", "cmd/spec/config.go", "old_indexer.go"}},
		{tabIntegration, []string{"Existing-code boundaries", "ensureIndex", "precise", "structural", "planned"}},
		{tabEvidence, []string{"TestAutoIndexCanBeDisabled", "TestConfigAutoIndexFalse", "starting state", "manual", "stale"}},
		{tabDiff, []string{"indexer/indexer.go", "files", "Focused hunk", "Symbol"}},
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

func TestReviewSummaryFollowsTheInformationHierarchy(t *testing.T) {
	model := newReviewFixtureModel(t, reviewPlanFixture())
	assertTextOrder(t, reviewPlain(model),
		"Original intent", "Add an option to disable automatic indexing",
		"Implementation plan", "Disable automatic indexing",
		"Actual change", "Files",
		"Original plan vs actual", "Matched",
		"Review attention", "Evidence", "Complete Spec", "Request Changes",
	)

	withoutPlan := newReviewFixtureModel(t, nil)
	plain := reviewPlain(withoutPlan)
	if strings.Contains(plain, "Original plan vs actual") {
		t.Fatalf("absent plan still rendered drift:\n%s", plain)
	}
	for _, expected := range []string{"Original intent", "No implementation plan was submitted", "Actual change", "Evidence"} {
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
	model.tab = tabChanges
	plain := reviewPlain(model)
	for _, expected := range []string{"Matched (2)", "Additional (2)", "Planned but untouched (1)", "planned and changed", "config/config.go", "add AutoIndexEnabled"} {
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

	model.cursors[tabChanges] = 0
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
	empty.tab = tabChanges
	if plain := reviewPlain(empty); !strings.Contains(plain, "No file changes") {
		t.Fatalf("empty files tab did not explain itself:\n%s", plain)
	}
}

func TestReviewChangesGroupsFilesAndShowsCompleteSelectedDetail(t *testing.T) {
	snapshot := reviewSnapshotFixture(reviewPlanFixture())
	longPath := "internal/presentation/components/review_change_detail_renderer.go"
	snapshot.Projection.Files[0].Path = longPath
	snapshot.Projection.Files[0].Change.Path = longPath
	snapshot.Hunks[longPath] = snapshot.Hunks["config/config.go"]
	delete(snapshot.Hunks, "config/config.go")
	model := newReviewModel(t.TempDir(), snapshot)
	model.tab = tabChanges
	model.Update(tea.WindowSizeMsg{Width: 120, Height: 34})
	plain := reviewPlain(model)
	assertTextOrder(t, plain, "Matched (2)", "Additional (2)", "Planned but untouched (1)")
	for _, expected := range []string{
		"Change detail", longPath, "Planned", "modify", "Actual", "modified", "Lines", "+12 -2",
		"Reason", "add AutoIndexEnabled", "Changed symbols", "Config", "[ View diff ]", "[ Open VS Code ]",
	} {
		if !strings.Contains(plain, expected) {
			t.Fatalf("Changes detail missing %q:\n%s", expected, plain)
		}
	}
	if strings.Count(plain, longPath) != 1 {
		t.Fatalf("long path should be truncated in the list and complete only in detail:\n%s", plain)
	}
	if strings.Contains(plain, "File                           Plan") {
		t.Fatalf("Changes retained the wide status table:\n%s", plain)
	}
}

func TestReviewChangesUsesWideSplitAndNarrowStackWithoutChangingCanonicalSelection(t *testing.T) {
	model := newReviewModel(t.TempDir(), reviewSnapshotFixture(reviewPlanFixture()))
	model.tab = tabChanges
	model.cursors[tabChanges] = 0
	wantIDs := append([]string(nil), model.screen().selectableItemIDs()...)
	for _, test := range []struct {
		width, height int
		labels        []string
		split         bool
	}{
		{120, 34, []string{"Files", "Change detail"}, true},
		{96, 34, []string{"Files", "Change detail"}, true},
		{80, 24, []string{"Files", "Selected change", "Changed symbols", "[ View diff ]", "[ Open VS Code ]"}, false},
	} {
		model.Update(tea.WindowSizeMsg{Width: test.width, Height: test.height})
		plain := reviewPlain(model)
		for _, label := range test.labels {
			if !strings.Contains(plain, label) {
				t.Fatalf("Changes at %dx%d missing %q:\n%s", test.width, test.height, label, plain)
			}
		}
		if got := model.screen().selectableItemIDs(); !reflect.DeepEqual(got, wantIDs) || model.cursors[tabChanges] != 0 {
			t.Fatalf("resize changed canonical items or selection: ids=%v cursor=%d", got, model.cursors[tabChanges])
		}
		if test.split && !samePlainLine(plain, "Files", "Change detail") {
			t.Fatalf("Changes did not retain split panes at shell content width %d:\n%s", test.width, plain)
		}
		if lipgloss.Width(model.View().Content) > test.width || lipgloss.Height(model.View().Content) > test.height {
			t.Fatalf("Changes exceeded %dx%d", test.width, test.height)
		}
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
	for _, expected := range []string{"runIndexCommand", "unchanged manual path", "planned", "ensureIndex", "precise", "config/config.go:22", "structural"} {
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

func TestReviewIntegrationRendersOneDirectionalRelationshipWithHonestProvenance(t *testing.T) {
	model := newReviewFixtureModel(t, reviewPlanFixture())
	model.tab = tabIntegration
	plain := reviewPlain(model)
	for _, expected := range []string{
		"planned · AI-declared", "precise · compiler-backed", "structural · parser-derived",
		"Code / relationship detail", "[ View diff ]", "[ Open VS Code ]",
	} {
		if !strings.Contains(plain, expected) {
			t.Fatalf("Integration missing %q:\n%s", expected, plain)
		}
	}
	wantRelationships := []string{
		"runIndexCommand → unchanged manual path → manual path remains unchanged",
		"ensureIndex → reads → Config.AutoIndexEnabled",
		"ensureIndex → calls → runIndexCommand",
	}
	for index, want := range wantRelationships {
		if got := integrationLabel(model.snap.Projection.Integrations[index]); got != want {
			t.Fatalf("relationship %d = %q, want %q", index, got, want)
		}
	}
	for _, item := range model.snap.Projection.Integrations {
		line := ansi.Strip(integrationLabel(item))
		target := item.Symbol
		if item.Parent == "" {
			target = item.Change
		}
		if target != "" && strings.Count(line, target) != 1 {
			t.Fatalf("relationship duplicated target %q in %q", target, line)
		}
	}
	if strings.Contains(plain, "planned · compiler-backed") || strings.Contains(plain, "planned · parser-derived") {
		t.Fatalf("planned relationship capability was inflated:\n%s", plain)
	}
	model.Update(tea.WindowSizeMsg{Width: 96, Height: 34})
	if shellWidth := reviewPlain(model); !samePlainLine(shellWidth, "Existing-code boundaries", "Code / relationship detail") {
		t.Fatalf("Integration did not retain split panes at shell content width:\n%s", shellWidth)
	}
}

func TestReviewIntegrationAndDiffHandOffTheSelectedFileIdentity(t *testing.T) {
	model := newReviewFixtureModel(t, reviewPlanFixture())
	model.tab = tabIntegration
	setReviewCursorByID(t, model, "review.integration.1")
	model.Update(key('d', "d"))
	if model.tab != tabDiff || model.diffFile() != "config/config.go" {
		t.Fatalf("Integration handed off tab=%v file=%q", model.tab, model.diffFile())
	}
	model.Update(key('i', "i"))
	integration, ok := model.selectedIntegration()
	if model.tab != tabIntegration || !ok || integration.Path != "config/config.go" {
		t.Fatalf("Diff handed back to integration tab=%v item=%+v ok=%v", model.tab, integration, ok)
	}

	model.tab = tabChanges
	model.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	setReviewCursorByID(t, model, "review.file.old_indexer.go")
	model.Update(key('d', "d"))
	if model.tab != tabChanges || !strings.Contains(reviewPlain(model), "No actual diff") || !strings.Contains(reviewPlain(model), "[Changes]") {
		t.Fatalf("untouched plan item opened an unrelated diff: tab=%v\n%s", model.tab, reviewPlain(model))
	}

	model.tab = tabIntegration
	setReviewCursorByID(t, model, "review.integration.0")
	model.Update(key('d', "d"))
	if model.tab != tabIntegration || !strings.Contains(reviewPlain(model), "no changed file") {
		t.Fatalf("location-free planned relationship opened an unrelated diff: tab=%v\n%s", model.tab, reviewPlain(model))
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
		"Behaviour reproduced", "TestAutoIndexCanBeDisabled", "failed against starting state a1b2c3d",
		"test modified after starting state: NO", "Added during implementation", "TestConfigAutoIndexFalse",
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

func TestReviewEvidenceUsesSignOffHierarchyWithoutInflatingProof(t *testing.T) {
	model := newReviewFixtureModel(t, reviewPlanFixture())
	model.tab = tabEvidence
	model.snap.Evidence.Items = append(model.snap.Evidence.Items, evidence.Item{
		ID: "command:broken", Name: "go test ./...", Command: "go test ./...",
		Category: evidence.CategoryExisting, Status: "parser_error", Fresh: false,
		Reason: "structured output could not be parsed",
	}, evidence.Item{
		ID: "modified", Name: "TestModifiedDuringChange", Category: evidence.CategoryModifiedExisting,
		Status: "passed", Passing: true, Fresh: true,
	})
	plain := reviewPlain(model)
	for _, expected := range []string{
		"Before implementation", "Behaviour reproduced", "Added during implementation",
		"Needs attention", "Manual", "supporting coverage; not independent proof",
		"modified during implementation; not independent proof", "stale",
		"command-level fallback", "structured output could not be parsed",
	} {
		if !strings.Contains(plain, expected) {
			t.Fatalf("evidence sign-off hierarchy missing %q:\n%s", expected, plain)
		}
	}
	if strings.Contains(strings.ToLower(plain), "evidence score") {
		t.Fatalf("evidence invented a synthetic score:\n%s", plain)
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
	words := strings.Join(strings.Fields(plain), " ")
	for _, expected := range []string{"ensureIndex", "+", "-"} {
		if !strings.Contains(plain, expected) {
			t.Fatalf("diff tab missing %q:\n%s", expected, plain)
		}
	}
	for _, expected := range []string{"Planned ✓", "Integration changed ✓"} {
		if !strings.Contains(words, expected) {
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

func TestReviewDiffUsesFileNavigatorAndFocusedHunkAtWideAndNarrowWidths(t *testing.T) {
	model := newReviewFixtureModel(t, reviewPlanFixture())
	model.tab = tabDiff
	for model.diffFile() != "indexer/indexer.go" {
		model.Update(key(tea.KeyDown, ""))
	}
	wantIDs := append([]string(nil), model.screen().selectableItemIDs()...)
	for _, size := range []struct {
		width, height int
		split         bool
	}{{120, 34, true}, {96, 34, true}, {80, 24, false}} {
		model.Update(tea.WindowSizeMsg{Width: size.width, Height: size.height})
		plain := reviewPlain(model)
		words := strings.Join(strings.Fields(plain), " ")
		assertTextOrder(t, plain, "Files", "Focused hunk")
		for _, expected := range []string{"indexer/indexer.go", "Symbol", "ensureIndex", "[ Open VS Code ]", "[ Integration ]"} {
			if !strings.Contains(plain, expected) {
				t.Fatalf("Diff at %dx%d missing %q:\n%s", size.width, size.height, expected, plain)
			}
		}
		for _, expected := range []string{"Planned ✓", "Integration changed ✓"} {
			if !strings.Contains(words, expected) {
				t.Fatalf("Diff at %dx%d missing %q:\n%s", size.width, size.height, expected, plain)
			}
		}
		if !reflect.DeepEqual(model.screen().selectableItemIDs(), wantIDs) || model.diffFile() != "indexer/indexer.go" {
			t.Fatalf("Diff resize changed file identity: ids=%v file=%q", model.screen().selectableItemIDs(), model.diffFile())
		}
		if size.split && !samePlainLine(plain, "Files", "Focused hunk") {
			t.Fatalf("Diff did not retain split panes at shell content width %d:\n%s", size.width, plain)
		}
		for _, line := range strings.Split(plain, "\n") {
			if ansi.StringWidth(line) > size.width {
				t.Fatalf("Diff line overflow at %dx%d: %q", size.width, size.height, line)
			}
		}
	}
}

func TestReviewDiffFocusScrollsCodeAndEscapeReturnsToFiles(t *testing.T) {
	model := newReviewFixtureModel(t, reviewPlanFixture())
	model.tab = tabDiff
	model.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	for model.diffFile() != "indexer/indexer.go" {
		model.Update(key(tea.KeyDown, ""))
	}
	lines := make([]gitutil.DiffLine, 30)
	for index := range lines {
		lines[index] = gitutil.DiffLine{Kind: gitutil.DiffAddition, Text: fmt.Sprintf("scroll marker %02d", index), NewLine: 80 + index}
	}
	hunk := model.snap.Hunks["indexer/indexer.go"][0]
	hunk.Hunk.Lines = lines
	model.snap.Hunks["indexer/indexer.go"][0] = hunk

	model.Update(key(tea.KeyEnter, ""))
	if !model.diffFocused || model.diffScroll != 0 {
		t.Fatalf("enter did not focus the hunk: focused=%v scroll=%d", model.diffFocused, model.diffScroll)
	}
	before := reviewPlain(model)
	if !strings.Contains(before, "FOCUSED") || !strings.Contains(before, "scroll marker 00") || strings.Contains(before, "scroll marker 29") {
		t.Fatalf("initial focused viewport is not bounded:\n%s", before)
	}
	for range 18 {
		model.Update(key(tea.KeyDown, ""))
	}
	after := reviewPlain(model)
	if model.diffFile() != "indexer/indexer.go" || model.diffScroll == 0 || strings.Contains(after, "scroll marker 00") || !strings.Contains(after, "scroll marker 18") {
		t.Fatalf("focused hunk did not scroll independently: file=%q scroll=%d\n%s", model.diffFile(), model.diffScroll, after)
	}
	model.Update(key(tea.KeyEscape, ""))
	if model.diffFocused || model.done || model.tab != tabDiff {
		t.Fatalf("escape did not return to file navigation: focused=%v done=%v tab=%d", model.diffFocused, model.done, model.tab)
	}
}

func TestReviewSummaryShowsTotalsAttentionAndExplicitDecisions(t *testing.T) {
	model := newReviewFixtureModel(t, reviewPlanFixture())
	model.tab = tabSummary
	var recorded []state.TimelineEvent
	model.record = func(_ string, event state.TimelineEvent) error {
		recorded = append(recorded, event)
		return nil
	}
	plain := reviewPlain(model)
	for _, expected := range []string{"4", "+38", "-6", "GOOD", "Review attention", "2 additional files changed outside the accepted plan", "Complete Spec", "Request Changes"} {
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

	setReviewCursorByID(t, model, "review.request_changes")
	model.Update(key(tea.KeyEnter, ""))
	if model.decision != decisionChanges || len(recorded) != 1 || recorded[0].Type != state.TimelineChangesRequested {
		t.Fatalf("request changes = %q recorded=%+v", model.decision, recorded)
	}
	if model.snap.Plan == nil || len(model.snap.Evidence.Items) == 0 {
		t.Fatal("request changes discarded plan or evidence facts")
	}
	model.done, model.nav = false, actionNone
	setReviewCursorByID(t, model, "review.complete")
	model.Update(key(tea.KeyEnter, ""))
	if model.decision != decisionChanges || model.nav != actionCompletion || len(recorded) != 1 {
		t.Fatalf("completion handoff = %q/%q recorded=%+v", model.decision, model.nav, recorded)
	}
}

func TestReviewRendersInsideSupportedWindowsAndExplainsSmallerOnes(t *testing.T) {
	for _, size := range []struct{ width, height int }{{80, 24}, {120, 34}} {
		model := newReviewModel(t.TempDir(), reviewSnapshotFixture(reviewPlanFixture()))
		model.Update(tea.WindowSizeMsg{Width: size.width, Height: size.height})
		for tab := range reviewTabLabels {
			model.tab = reviewTab(tab)
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
	if small.tab != tabChanges {
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
