package main

import (
	"reflect"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/TaylorEdgerton/spec-cli/internal/state"
	"github.com/charmbracelet/x/ansi"
)

func TestPlanScreenExplainsAbsence(t *testing.T) {
	model := newPlanModel("", nil)
	if plain := ansi.Strip(model.View().Content); !strings.Contains(plain, "No implementation plan") || !strings.Contains(plain, "optional") {
		t.Fatalf("absent plan = %q", plain)
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
	model := newPlanModel(t.TempDir(), stored)
	model.Update(tea.WindowSizeMsg{Width: 120, Height: 34})
	plain := ansi.Strip(model.View().Content)
	for _, expected := range []string{"Implementation Plan", "AGENT PLAN", "Codex", "Disable automatic indexing", "config/config.go", "add setting", "ensureIndex", "controls flow", "manual indexing remains available", "CLI flag location", "paste"} {
		if !strings.Contains(plain, expected) {
			t.Fatalf("plan missing %q:\n%s", expected, plain)
		}
	}
	model.Update(key(tea.KeyDown, ""))
	model.Update(key(tea.KeyDown, ""))
	if !reflect.DeepEqual(*stored, original) {
		t.Fatalf("selection mutated plan: %+v", stored)
	}
	if len(model.selectableIDs()) < 3 || !strings.HasPrefix(model.selectedID(), "plan.") {
		t.Fatalf("selection IDs = %v selected=%q", model.selectableIDs(), model.selectedID())
	}
}
