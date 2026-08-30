package main

import (
	"reflect"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestCanonicalScreenUsesSectionOrderForRenderingAndNavigation(t *testing.T) {
	screen := canonicalScreen{Sections: []screenSection{
		{
			ID: "intent", Title: "Intent",
			Items: []screenItem{
				{ID: "intent.summary", Label: "Disable automatic indexing"},
				{ID: "intent.open", Label: "Open intent", Selectable: true, Action: "open-intent"},
			},
		},
		{
			ID: "review", Title: "Review",
			Items: []screenItem{
				{ID: "review.files", Label: "Files", Selectable: true, Action: "open-files"},
				{ID: "review.note", Label: "Plan data is advisory"},
				{ID: "review.diff", Label: "Diff", Selectable: true, Action: "open-diff"},
			},
		},
	}}

	if got, want := screen.visibleItemIDs(), []string{"intent.summary", "intent.open", "review.files", "review.note", "review.diff"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("visible order = %v, want %v", got, want)
	}
	if got, want := screen.selectableItemIDs(), []string{"intent.open", "review.files", "review.diff"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("navigation order = %v, want %v", got, want)
	}
	plain := screen.body()
	assertTextOrder(t, plain, "Intent", "Disable automatic indexing", "Open intent", "Review", "Files", "Plan data is advisory", "Diff")
}

func TestCanonicalScreenMovementIsReadOnlyAndActivationIsExplicit(t *testing.T) {
	screen := canonicalScreen{Sections: []screenSection{{ID: "actions", Title: "Actions", Items: []screenItem{
		{ID: "first", Label: "First", Selectable: true, Action: "first-action"},
		{ID: "information", Label: "Information only"},
		{ID: "second", Label: "Second", Selectable: true, Action: "second-action"},
	}}}}
	original := append([]screenSection(nil), screen.Sections...)

	if got := screen.activate(); got != "first-action" {
		t.Fatalf("initial action = %q", got)
	}
	if action := screen.key("down"); action != noScreenAction || screen.Cursor != 1 {
		t.Fatalf("down = cursor:%d action:%q", screen.Cursor, action)
	}
	if action := screen.key("k"); action != noScreenAction || screen.Cursor != 0 {
		t.Fatalf("k = cursor:%d action:%q", screen.Cursor, action)
	}
	if action := screen.key("j"); action != noScreenAction || screen.Cursor != 1 {
		t.Fatalf("j = cursor:%d action:%q", screen.Cursor, action)
	}
	if action := screen.key("enter"); action != "second-action" {
		t.Fatalf("enter action = %q", action)
	}
	screen.move(1)
	if screen.Cursor != 0 || screen.activate() != "first-action" {
		t.Fatalf("wrapped selection = cursor:%d action:%q", screen.Cursor, screen.activate())
	}
	screen.move(-1)
	if screen.Cursor != 1 || screen.activate() != "second-action" {
		t.Fatalf("reverse selection = cursor:%d action:%q", screen.Cursor, screen.activate())
	}
	if !reflect.DeepEqual(screen.Sections, original) {
		t.Fatalf("movement mutated screen content: %+v", screen.Sections)
	}
}

func TestSharedUIComponentsExposeTheEstablishedVisualLanguage(t *testing.T) {
	columns := uiColumns("one\ntwo", 5, "right")
	if !strings.Contains(columns, "one") || !strings.Contains(columns, "right") || lipgloss.Width(strings.Split(columns, "\n")[0]) < 11 {
		t.Fatalf("columns = %q", columns)
	}
	line := uiSplit("left", "right", 24)
	if lipgloss.Width(line) != 24 || !strings.HasPrefix(line, "left") || !strings.HasSuffix(line, "right") {
		t.Fatalf("split = %q width=%d", line, lipgloss.Width(line))
	}
	for name, value := range map[string]string{
		"empty":    uiEmptyState("Evidence", "No evidence recorded"),
		"hints":    uiKeyHints([][2]string{{"enter", "open"}, {"q", "exit"}}, "   "),
		"tabs":     uiTabs([]string{"Overview", "Files", "Evidence"}, 1),
		"badge":    uiBadge("precise", uiGreen),
		"selected": uiSelectedRow("config.go", 20),
		"code":     uiCodeLine(84, true, "return nil", 30),
	} {
		if strings.TrimSpace(value) == "" {
			t.Fatalf("%s component is empty", name)
		}
	}
	if plain := ansi.Strip(uiTabs([]string{"Overview", "Files", "Evidence"}, 1)); !strings.Contains(plain, "[Files]") {
		t.Fatalf("active tab is not identifiable: %q", plain)
	}
	if plain := ansi.Strip(uiCodeLine(84, true, "return nil", 30)); !strings.Contains(plain, "›") || !strings.Contains(plain, "84") {
		t.Fatalf("current code line is not identifiable: %q", plain)
	}
}

func TestCanonicalScreenExplainsEmptySections(t *testing.T) {
	screen := canonicalScreen{Sections: []screenSection{{
		ID: "evidence", Title: "Evidence", EmptyReason: "No evidence has been recorded for this Spec.",
	}}}
	body := screen.body()
	for _, expected := range []string{"Evidence", "No evidence has been recorded for this Spec."} {
		if !strings.Contains(body, expected) {
			t.Fatalf("empty section missing %q: %q", expected, body)
		}
	}
	if _, ok := screen.selectedItem(); ok || screen.activate() != noScreenAction {
		t.Fatal("empty screen exposed an action")
	}
}

func TestSharedAppShellIsPersistentAndBounded(t *testing.T) {
	for _, size := range []struct{ width, height int }{{80, 24}, {120, 34}} {
		content := uiAppShell(size.width, size.height, "SPEC-014 · OPEN", "Review body", "enter open   q exit")
		for _, expected := range []string{"SPEC-014 · OPEN", "Review body", "enter open", "q exit"} {
			if !strings.Contains(content, expected) {
				t.Fatalf("%dx%d shell missing %q:\n%s", size.width, size.height, expected, content)
			}
		}
		if got := lipgloss.Width(content); got > size.width {
			t.Fatalf("%dx%d shell width = %d", size.width, size.height, got)
		}
		if got := lipgloss.Height(content); got > size.height {
			t.Fatalf("%dx%d shell height = %d", size.width, size.height, got)
		}
	}
}
