package main

import (
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/TaylorEdgerton/spec-cli/internal/state"
	"github.com/charmbracelet/x/ansi"
)

func TestOverviewRendersOrderedOrientationWithoutPercentageProgress(t *testing.T) {
	started := time.Date(2026, 8, 30, 1, 0, 0, 0, time.UTC)
	data := overviewData{
		Title: "Disable automatic indexing", SpecID: "SPEC-014", Branch: "feature/indexing", Baseline: "a1b2c3d4",
		Intent: "Add an option to disable automatic indexing", Scope: "Preserve manual indexing", StartedAt: started, Now: started.Add(12 * time.Minute),
		Stats: state.ChangeStats{Files: 6, Additions: 184, Deletions: 31, TestsAdded: 4}, StatsRefreshed: true,
		RefreshedAt: started.Add(11 * time.Minute), Facts: overviewFacts{BaselineReady: true, WorkspaceDirty: true},
	}
	for _, size := range []struct{ width, height int }{{80, 24}, {120, 34}} {
		model := newOverviewModel(data)
		model.Update(tea.WindowSizeMsg{Width: size.width, Height: size.height})
		view := model.View().Content
		plain := ansi.Strip(view)
		assertTextOrder(t, plain, "Intent", "Add an option", "Expected behaviour", "Preserve manual", "NEXT", "Change lifecycle", "Since starting state")
		for _, forbidden := range []string{"Progress", "%", "█", "░"} {
			if strings.Contains(plain, forbidden) {
				t.Fatalf("overview retained %q at %dx%d:\n%s", forbidden, size.width, size.height, plain)
			}
		}
		for _, expected := range []string{"SPEC-014", "Disable automatic indexing", "OPEN", "feature/indexing", "a1b2c3d", "12m ago", "Files", "6", "Lines +184 -31", "Tests changed 4", "explicit refresh"} {
			if !strings.Contains(plain, expected) {
				t.Fatalf("overview missing %q at %dx%d:\n%s", expected, size.width, size.height, plain)
			}
		}
		if lipgloss.Width(view) > size.width || lipgloss.Height(view) > size.height {
			t.Fatalf("overview bounds = %dx%d, want <= %dx%d", lipgloss.Width(view), lipgloss.Height(view), size.width, size.height)
		}
	}
	model := newOverviewModel(data)
	if current, ok := model.currentStage(); !ok || current.ID != "implementation" {
		t.Fatalf("current stage = %+v, %v", current, ok)
	}
}

func TestOverviewWrapsMultilineIntentAndScopeAtSupportedWidth(t *testing.T) {
	model := newOverviewModel(overviewData{
		SpecID: "SPEC-101", Title: "Readable prose", Baseline: "abcdef123456", Branch: "main", Now: time.Now(),
		Intent: "Replace the first implementation detail with a reusable component.\n\nKeep the human-facing workflow understandable at narrow terminal widths.",
		Scope:  "The existing navigation remains selectable, while very long explanatory prose wraps instead of being silently truncated at the shell boundary.",
		Facts:  overviewFacts{BaselineReady: true},
	})
	model.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	plain := ansi.Strip(model.View().Content)
	for _, expected := range []string{"Replace the first implementation", "Keep the human-facing workflow", "existing navigation remains"} {
		if !strings.Contains(plain, expected) {
			t.Fatalf("Overview prose missing %q:\n%s", expected, plain)
		}
	}
	for _, line := range strings.Split(plain, "\n") {
		if ansi.StringWidth(line) > 80 {
			t.Fatalf("Overview line width = %d: %q", ansi.StringWidth(line), line)
		}
	}
}

func TestOverviewNextActionsDeriveFromDurableFacts(t *testing.T) {
	tests := []struct {
		name  string
		facts overviewFacts
		ids   []string
	}{
		{"definition", overviewFacts{SetupActive: true}, []string{"overview.next.definition"}},
		{"implementation", overviewFacts{BaselineReady: true}, []string{"overview.next.prompt", "overview.next.plan.capture", "overview.next.review", "overview.next.explore"}},
		{"planned", overviewFacts{BaselineReady: true, PlanAvailable: true}, []string{"overview.next.prompt", "overview.next.plan.view", "overview.next.review", "overview.next.explore"}},
		{"review", overviewFacts{BaselineReady: true, ReviewEntered: true}, []string{"overview.next.review", "overview.next.evidence", "overview.next.summary", "overview.next.plan", "overview.next.implement"}},
		{"evidence", overviewFacts{BaselineReady: true, ReviewEntered: true, EvidenceCount: 1}, []string{"overview.next.review", "overview.next.evidence", "overview.next.summary", "overview.next.plan", "overview.next.implement"}},
		{"complete", overviewFacts{Completed: true}, []string{"overview.next.history"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			model := newOverviewModel(overviewData{Facts: test.facts})
			if got := model.screen().selectableItemIDs(); !reflect.DeepEqual(got, test.ids) {
				t.Fatalf("NEXT ids = %v, want %v", got, test.ids)
			}
			if model.next().Message == "" {
				t.Fatal("NEXT message is empty")
			}
		})
	}
}

func TestOverviewNextActivationCopiesOrRoutesExplicitly(t *testing.T) {
	model := newOverviewModel(overviewData{Root: "/workspace", Facts: overviewFacts{BaselineReady: true}})
	copied := ""
	model.copyPrompt = func(root string) error { copied = root; return nil }
	model.Update(key(tea.KeyEnter, ""))
	if copied != "/workspace" || model.nav != actionNone {
		t.Fatalf("prompt action copied=%q nav=%q", copied, model.nav)
	}

	model.status, model.cursor = "", 1
	model.Update(key(tea.KeyEnter, ""))
	if model.nav != actionPlanCapture {
		t.Fatalf("optional plan action = %q", model.nav)
	}

	review := newOverviewModel(overviewData{Facts: overviewFacts{BaselineReady: true}})
	review.cursor = 2
	review.Update(key(tea.KeyEnter, ""))
	if review.nav != actionReview {
		t.Fatalf("review action = %q", review.nav)
	}
}

func TestOverviewLifecycleMarkersAreStateNotSelection(t *testing.T) {
	model := newOverviewModel(overviewData{Baseline: "a1b2c3d4", Facts: overviewFacts{BaselineReady: true}})
	plain := ansi.Strip(model.View().Content)
	for _, expected := range []string{"✓ Define", "● Implement", "CURRENT", "○ Review", "○ Complete"} {
		if !strings.Contains(plain, expected) {
			t.Fatalf("lifecycle missing %q:\n%s", expected, plain)
		}
	}
	for _, line := range strings.Split(plain, "\n") {
		if strings.Contains(line, ">") && strings.Contains(line, "Implementation") && !strings.Contains(line, "prompt") {
			t.Fatalf("lifecycle stage rendered as cursor selection: %q", line)
		}
	}
}

func TestOverviewLargeBaselineDriftBoundaryAndActions(t *testing.T) {
	base := overviewData{Baseline: "c83e894123", StatsRefreshed: true, RefreshedAt: time.Now(), Facts: overviewFacts{BaselineReady: true}}
	base.Stats.Files = overviewLargeDriftFiles
	if plain := ansi.Strip(newOverviewModel(base).View().Content); strings.Contains(plain, "unrelated work") {
		t.Fatalf("boundary unexpectedly warned:\n%s", plain)
	}
	base.Stats.Files++
	model := newOverviewModel(base)
	plain := ansi.Strip(model.View().Content)
	for _, expected := range []string{"changed since starting state c83e894", "may contain unrelated work", "Review anyway", "View starting state details"} {
		if !strings.Contains(plain, expected) {
			t.Fatalf("drift warning missing %q:\n%s", expected, plain)
		}
	}
	if got, want := model.screen().selectableItemIDs(), []string{"overview.next.prompt", "overview.next.plan.capture", "overview.next.review", "overview.next.explore", "overview.drift.review", "overview.drift.details"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("drift actions = %v, want %v", got, want)
	}
	model.cursor = 4
	model.Update(key(tea.KeyEnter, ""))
	if model.nav != actionReview {
		t.Fatalf("Review anyway action = %q", model.nav)
	}
	model.nav = actionNone
	model.cursor = 5
	model.Update(key(tea.KeyEnter, ""))
	if !model.showBaselineDetails || model.nav != actionNone {
		t.Fatalf("starting state detail action = details:%v nav:%q", model.showBaselineDetails, model.nav)
	}
}

func TestOverviewIsHonestBeforeExplicitReviewRefresh(t *testing.T) {
	root, workspace, _ := definitionRepository(t, false)
	setup := *workspace.Setup
	setup.Title, setup.Outcome = "Disable indexing", "Manual indexing remains available"
	if err := workspace.SaveSetup(setup); err != nil {
		t.Fatal(err)
	}
	if _, err := saveDefinitionContract(root, setup, io.Discard); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "unreviewed.go"), []byte("package base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	data, err := loadOverview(root, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if data.StatsRefreshed || data.Stats.Files != 0 {
		t.Fatalf("Overview silently refreshed Git facts: %+v", data)
	}
	plain := ansi.Strip(newOverviewModel(data).View().Content)
	if !strings.Contains(plain, "Not refreshed") || !strings.Contains(plain, "Open Review to refresh") || strings.Contains(plain, filepath.Base("unreviewed.go")) {
		t.Fatalf("unrefreshed provenance is dishonest:\n%s", plain)
	}
}

func TestOverviewStageDerivationHasExactlyOneCurrentStage(t *testing.T) {
	tests := []struct {
		name  string
		facts overviewFacts
		want  string
	}{
		{"setup", overviewFacts{SetupActive: true}, "intent"},
		{"baseline", overviewFacts{BaselineReady: true}, "implementation"},
		{"plan", overviewFacts{BaselineReady: true, PlanAvailable: true}, "implementation"},
		{"workspace", overviewFacts{BaselineReady: true, WorkspaceDirty: true}, "implementation"},
		{"review", overviewFacts{BaselineReady: true, WorkspaceDirty: true, ReviewEntered: true}, "review"},
		{"evidence", overviewFacts{BaselineReady: true, WorkspaceDirty: true, ReviewEntered: true, EvidenceCount: 1}, "review"},
		{"complete", overviewFacts{Completed: true}, ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stages := deriveOverviewStages(test.facts)
			current := ""
			count := 0
			for _, stage := range stages {
				if stage.Status == stageCurrent {
					current, count = stage.ID, count+1
				}
			}
			wantCount := 1
			if test.facts.Completed {
				wantCount = 0
			}
			if count != wantCount || current != test.want {
				t.Fatalf("stages = %+v, want current %q", stages, test.want)
			}
			if !test.facts.PlanAvailable {
				for _, stage := range stages {
					if stage.ID == "plan" && stage.Status != stageOmitted {
						t.Fatalf("absent optional plan = %+v", stage)
					}
				}
			}
		})
	}
}
