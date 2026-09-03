package main

import (
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
	"github.com/TaylorEdgerton/spec-cli/internal/state"
	"github.com/charmbracelet/x/ansi"
)

func TestPlanScreenExplainsAbsence(t *testing.T) {
	model := newPlanModel("", nil)
	if plain := ansi.Strip(model.View().Content); !strings.Contains(plain, "No implementation plan") || !strings.Contains(plain, "optional") {
		t.Fatalf("absent plan = %q", plain)
	}
}

func TestPlanDefaultsToReadableDocumentAndRawJSONRequiresExplicitInspection(t *testing.T) {
	stored := &state.StoredChangePlan{SchemaVersion: 1, Source: state.PlanSourceCLI, Plan: state.ChangePlan{
		Summary:           "Replace the hard-coded ignored word collection with a reusable text filtering library while preserving discovery behaviour.",
		Files:             []state.PlannedFile{{Path: "internal/presentation/readable_plan_renderer_with_a_deliberately_long_filename_for_horizontal_inspection.go", Action: state.PlanFileModify, Reason: "Route significant word checks through the shared filter without changing ranking."}},
		IntegrationPoints: []state.PlannedIntegration{{ExistingSymbol: "significantWords", PlannedChange: "call textfilter.IsCommonEnglish", Relationship: "delegates ignored-word classification"}},
		Verification:      []state.PlannedVerification{{Behaviour: "Existing discovery results remain stable and common English words are ignored", LikelyLocation: "internal/discovery/discovery_test.go"}},
		Uncertainties:     []string{"Whether project-specific terms should remain configurable after the shared filter is introduced."},
	}}
	model := newPlanModel(t.TempDir(), stored)
	model.Update(tea.WindowSizeMsg{Width: 80, Height: 34})
	plain := ansi.Strip(model.View().Content)
	for _, heading := range []string{"Summary", "Planned changes", "Existing integration points", "Verification", "Uncertainties"} {
		if !strings.Contains(plain, heading) {
			t.Fatalf("readable plan missing %q:\n%s", heading, plain)
		}
	}
	if strings.Contains(plain, `"summary"`) || lipgloss.Width(model.View().Content) > 80 {
		t.Fatalf("default plan exposed raw JSON or overflowed:\n%s", plain)
	}
	if len(model.screen().selectableItemIDs()) != 2 {
		t.Fatalf("readable rendering replaced canonical navigation: %v", model.screen().selectableItemIDs())
	}
	model.Update(key('x', "x"))
	raw := ansi.Strip(model.View().Content)
	if !strings.Contains(raw, "Raw ChangePlan") || !strings.Contains(raw, `"summary"`) || !strings.Contains(raw, `"integration_points"`) {
		t.Fatalf("explicit raw inspection missing JSON:\n%s", raw)
	}
	if strings.Contains(raw, "horizontal_inspection.go") {
		t.Fatalf("raw JSON should not wrap a long string into the initial column viewport:\n%s", raw)
	}
	for range 3 {
		model.Update(key(tea.KeyRight, ""))
	}
	if shifted := ansi.Strip(model.View().Content); !strings.Contains(shifted, "horizontal_inspection.go") {
		t.Fatalf("raw JSON inspector did not expose later columns after horizontal navigation:\n%s", shifted)
	}
	model.Update(key('x', "x"))
	if strings.Contains(ansi.Strip(model.View().Content), `"summary"`) {
		t.Fatal("raw inspection did not return to the readable plan")
	}
}

func TestPlanScreenOpensSelectedPlannedFileAndReportsFailureVisibly(t *testing.T) {
	stored := &state.StoredChangePlan{SchemaVersion: 1, Source: state.PlanSourcePaste, Plan: state.ChangePlan{
		Summary:           "Disable automatic indexing",
		Files:             []state.PlannedFile{{Path: "config/config.go", Action: state.PlanFileModify, Reason: "add setting"}},
		IntegrationPoints: []state.PlannedIntegration{{ExistingSymbol: "ensureIndex", PlannedChange: "read setting", Relationship: "controls flow"}},
	}}
	model := newPlanModel(t.TempDir(), stored)
	requested := make([]discovery.Result, 0, 2)
	model.open = func(string, discovery.Result) error {
		requested = append(requested, discovery.Result{})
		return errors.New("code is unavailable")
	}
	model.Update(tea.WindowSizeMsg{Width: 120, Height: 34})
	model.Update(key('o', "o"))
	if len(requested) != 1 {
		t.Fatalf("planned file did not reach the opener: %+v", requested)
	}
	if plain := ansi.Strip(model.View().Content); !strings.Contains(plain, "code is unavailable") {
		t.Fatalf("open failure is not visible:\n%s", plain)
	}

	model.Update(key(tea.KeyDown, ""))
	model.Update(key('o', "o"))
	if len(requested) != 1 {
		t.Fatalf("integration row must not open a file: %+v", requested)
	}
	if plain := ansi.Strip(model.View().Content); !strings.Contains(plain, "planned file") {
		t.Fatalf("non-file open is not explained:\n%s", plain)
	}
}

func TestPlanScreenRendersAllSectionsAndSelectionIsReadOnly(t *testing.T) {
	now := time.Date(2026, 8, 30, 10, 28, 0, 0, time.UTC)
	stored := &state.StoredChangePlan{SchemaVersion: 1, Source: state.PlanSourcePaste, Submitter: "Codex", SubmittedAt: now, AcceptedAt: now, Plan: state.ChangePlan{
		Summary:           "Disable automatic indexing",
		Files:             []state.PlannedFile{{Path: "config/config.go", Action: state.PlanFileModify, Reason: "add setting"}, {Path: "indexer/indexer.go", Action: state.PlanFileModify, Reason: "respect setting"}},
		IntegrationPoints: []state.PlannedIntegration{{ExistingSymbol: "ensureIndex", PlannedChange: "read setting", Relationship: "controls flow"}},
		Verification:      []state.PlannedVerification{{Behaviour: "manual indexing remains available", LikelyLocation: "indexer/indexer_test.go"}},
		Uncertainties:     []string{"CLI flag location"},
	}}
	original := *stored
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "config", "config.go"), []byte("package config\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	model := newPlanModel(root, stored)
	model.Update(tea.WindowSizeMsg{Width: 120, Height: 34})
	plain := ansi.Strip(model.View().Content)
	for _, expected := range []string{"Implementation Plan", "AGENT PLAN", "Codex", "Disable automatic indexing", "config/config.go", "add setting", "ensureIndex", "controls flow", "manual indexing remains available", "CLI flag location", "paste", "Preview", "package config"} {
		if !strings.Contains(plain, expected) {
			t.Fatalf("plan missing %q:\n%s", expected, plain)
		}
	}
	model.Update(key(tea.KeyDown, ""))
	model.Update(key(tea.KeyDown, ""))
	if !reflect.DeepEqual(*stored, original) {
		t.Fatalf("selection mutated plan: %+v", stored)
	}
	if ids := model.screen().selectableItemIDs(); len(ids) < 3 || !strings.HasPrefix(model.selectedID(), "plan.") {
		t.Fatalf("selection IDs = %v selected=%q", ids, model.selectedID())
	}
}
