package main

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/TaylorEdgerton/spec-cli/internal/state"
	"github.com/charmbracelet/x/ansi"
)

func TestOverviewRendersIdentityBaselineElapsedContractAndLiveCounts(t *testing.T) {
	started := time.Date(2026, 8, 30, 1, 0, 0, 0, time.UTC)
	model := newOverviewModel(overviewData{
		Title: "Disable automatic indexing", SpecID: "SPEC-014", Branch: "feature/indexing", Baseline: "a1b2c3d4",
		Intent: "Add an option to disable automatic indexing", Scope: "Preserve manual indexing", StartedAt: started, Now: started.Add(12 * time.Minute),
		Stats: state.ChangeStats{Files: 6, Additions: 184, Deletions: 31, TestsAdded: 4}, Facts: overviewFacts{BaselineReady: true, WorkspaceDirty: true},
	})
	model.Update(tea.WindowSizeMsg{Width: 120, Height: 34})
	plain := ansi.Strip(model.View().Content)
	for _, expected := range []string{"SPEC-014", "Disable automatic indexing", "OPEN", "feature/indexing", "a1b2c3d", "12 min", "Add an option", "Preserve manual", "Changed files", "6", "+ lines", "184", "Tests", "4"} {
		if !strings.Contains(plain, expected) {
			t.Fatalf("overview missing %q:\n%s", expected, plain)
		}
	}
	if current, ok := model.currentStage(); !ok || current.ID != "implementation" {
		t.Fatalf("current stage = %+v, %v", current, ok)
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
		{"evidence", overviewFacts{BaselineReady: true, WorkspaceDirty: true, ReviewEntered: true, EvidenceCount: 1}, "evidence"},
		{"complete", overviewFacts{Completed: true}, "complete"},
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
			if count != 1 || current != test.want {
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
