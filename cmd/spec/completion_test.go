package main

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/TaylorEdgerton/spec-cli/internal/change"
	"github.com/charmbracelet/x/ansi"
)

func TestCompletionSignOffShowsDurableFactsAndExplicitChoices(t *testing.T) {
	snapshot := reviewSnapshotFixture(reviewPlanFixture())
	snapshot.Criteria = []change.Criterion{{Text: "Automatic indexing can be disabled", Checked: true}, {Text: "Manual indexing remains available"}}
	model := newCompletionModel(snapshot)
	model.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	plain := ansi.Strip(model.View().Content)
	for _, expected := range []string{
		"Complete · Sign-off", "Original intent", "Final change", "Files", "Lines",
		"Review attention", "Additional scope", "Needs attention", "Acceptance review",
		"1 / 2 reviewed", "Complete and archive Spec", "Return to implementation",
	} {
		if !strings.Contains(plain, expected) {
			t.Fatalf("completion sign-off missing %q:\n%s", expected, plain)
		}
	}
	if model.nav != actionNone {
		t.Fatalf("opening completion mutated navigation: %q", model.nav)
	}
	model.Update(key(tea.KeyDown, ""))
	if model.nav != actionNone {
		t.Fatalf("moving completion cursor mutated navigation: %q", model.nav)
	}
	model.Update(key(tea.KeyEnter, ""))
	if model.nav != actionChanges {
		t.Fatalf("return action = %q", model.nav)
	}
}

func TestCompletionCanExplicitlyArchiveWithoutPlanOrAutomatedEvidence(t *testing.T) {
	snapshot := reviewSnapshotFixture(nil)
	snapshot.Evidence.Items = nil
	snapshot.Criteria = []change.Criterion{{Text: "Reviewed manually"}}
	model := newCompletionModel(snapshot)
	plain := ansi.Strip(model.View().Content)
	for _, expected := range []string{"No implementation plan", "No evidence recorded", "0 / 1 reviewed", "Complete and archive Spec"} {
		if !strings.Contains(plain, expected) {
			t.Fatalf("optional-state sign-off missing %q:\n%s", expected, plain)
		}
	}
	model.Update(key(tea.KeyEnter, ""))
	if model.nav != actionComplete {
		t.Fatalf("explicit completion action = %q", model.nav)
	}
}
