package main

import (
	"bytes"
	"encoding/json"
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

func historyFixture() []state.History {
	older := time.Date(2026, 8, 20, 9, 0, 0, 0, time.UTC)
	newer := time.Date(2026, 8, 28, 14, 30, 0, 0, time.UTC)
	return []state.History{
		{
			SpecID: "SPEC-001", Title: "Add config loader", Intent: "Add config loader", Scope: "config package only",
			StartedAt: older, FinishedAt: older.Add(time.Hour), BaseSHA: "abcdef1234567", SpecArchive: "specs/one.md",
			Stats:           state.ChangeStats{Files: 3, Additions: 40, Deletions: 5},
			PlanDrift:       state.PlanDriftSummary{Matched: 2, Additional: 1},
			EvidenceSummary: state.EvidenceSummary{Existing: 2, NewTests: 1},
			DurationSeconds: 3600, CompletionAcknowledged: true,
			Plan: reviewPlanFixture(),
		},
		{
			SpecID: "SPEC-002", Title: "Disable automatic indexing", Intent: "Disable automatic indexing",
			StartedAt: newer, FinishedAt: newer.Add(2 * time.Hour), BaseSHA: "0123456789abc", SpecArchive: "specs/two.md",
			Stats: state.ChangeStats{Files: 4, Additions: 12, Deletions: 30},
			Timeline: []state.TimelineEvent{
				timelineEventFixture("a", state.TimelineSpecCreated, newer, "human", "spec", state.TimelineDetails{SpecID: "SPEC-002"}),
				timelineEventFixture("b", state.TimelinePlanAccepted, newer.Add(time.Hour), "agent", "paste", state.TimelineDetails{Summary: "Three file plan", Count: 3}),
			},
		},
	}
}

func newHistoryFixtureModel(t *testing.T, records []state.History, _ bool) *historyModel {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "specs"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "specs", "one.md"), []byte("# Add config loader\n\n## Intent\nLoad config from disk.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	model := newHistoryModel(t.TempDir(), dir, records, false)
	model.Update(tea.WindowSizeMsg{Width: 120, Height: 34})
	return model
}

func historyPlain(model *historyModel) string { return ansi.Strip(model.View().Content) }

func historyTitles(records []state.History) []string {
	titles := make([]string, len(records))
	for index, record := range records {
		titles[index] = record.Title
	}
	return titles
}

func TestHistoryListsCompletedSpecsNewestFirstWithDateStatusAndTitle(t *testing.T) {
	model := newHistoryFixtureModel(t, historyFixture(), false)
	want := []string{"Disable automatic indexing", "Add config loader"}
	if got := historyTitles(model.visible()); !reflect.DeepEqual(got, want) {
		t.Fatalf("record order = %v, want %v", got, want)
	}
	plain := historyPlain(model)
	for _, fragment := range []string{"2026-08-28", "2026-08-20", "SPEC-002", "SPEC-001", "completed", "archived"} {
		if !strings.Contains(plain, fragment) {
			t.Fatalf("history list missing %q:\n%s", fragment, plain)
		}
	}
	newest := strings.Index(plain, "Disable automatic indexing")
	oldest := strings.Index(plain, "Add config loader")
	if newest < 0 || oldest < 0 || newest > oldest {
		t.Fatalf("newest record is not listed first:\n%s", plain)
	}
}

func TestHistoryExplainsAnEmptyRecordList(t *testing.T) {
	model := newHistoryFixtureModel(t, nil, false)
	plain := historyPlain(model)
	if !strings.Contains(plain, "No completed Specs") {
		t.Fatalf("empty history does not explain itself:\n%s", plain)
	}
	updateModel(model, key(tea.KeyDown, ""))
	updateModel(model, key(tea.KeyEnter, ""))
	if model.done {
		t.Fatal("navigating an empty history ended the screen")
	}
}

func TestHistorySearchFiltersVisibleRecordsWithoutChangingThem(t *testing.T) {
	records := historyFixture()
	model := newHistoryFixtureModel(t, records, false)
	updateModel(model, key('/', "/"))
	for _, letter := range "index" {
		updateModel(model, key(letter, string(letter)))
	}
	if got := historyTitles(model.visible()); !reflect.DeepEqual(got, []string{"Disable automatic indexing"}) {
		t.Fatalf("search results = %v", got)
	}
	if plain := historyPlain(model); !strings.Contains(plain, "index") {
		t.Fatalf("search query is not visible:\n%s", plain)
	}
	if !reflect.DeepEqual(model.all, records) {
		t.Fatalf("search mutated the loaded records: %+v", model.all)
	}
	updateModel(model, key(tea.KeyEsc, ""))
	if got := historyTitles(model.visible()); len(got) != 2 {
		t.Fatalf("esc did not clear the search: %v", got)
	}
	if model.done {
		t.Fatal("esc while searching left the screen")
	}
}

func TestHistorySelectedSummaryAlwaysShowsStoredReviewFacts(t *testing.T) {
	model := newHistoryFixtureModel(t, historyFixture(), false)
	updateModel(model, key(tea.KeyDown, ""))
	for _, size := range [][2]int{{120, 34}, {80, 24}} {
		model.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		plain := historyPlain(model)
		for _, fragment := range []string{
			"Add config loader", "config package only", "abcdef1", "Completed 2026-08-20",
			"Files 3", "+40 -5", "duration 1h0m", "Agent plan yes", "Tests 3",
			"Additional 1", "Evidence", "new 1",
		} {
			if !strings.Contains(plain, fragment) {
				t.Fatalf("selected summary at %dx%d missing %q:\n%s", size[0], size[1], fragment, plain)
			}
		}
	}
	plain := historyPlain(model)
	updateModel(model, key('s', "s"))
	if historyPlain(model) != plain {
		t.Fatal("obsolete stats shortcut changed the canonical History summary")
	}
}

func TestHistoryShowsFollowUpLinksWithoutMutatingSourceRecord(t *testing.T) {
	records := historyFixture()
	records = append(records, state.History{
		SpecID: "SPEC-003", OriginSpecID: "SPEC-001", Title: "Config loader follow-up",
		FinishedAt: time.Date(2026, 8, 29, 10, 0, 0, 0, time.UTC), CompletionAcknowledged: true,
	})
	source := records[0]
	model := newHistoryFixtureModel(t, records, false)
	for selected, _ := model.selected(); selected.SpecID != "SPEC-001"; selected, _ = model.selected() {
		updateModel(model, key(tea.KeyDown, ""))
	}
	plain := historyPlain(model)
	if !strings.Contains(plain, "Follow-ups") || !strings.Contains(plain, "SPEC-003") {
		t.Fatalf("source does not expose its linked follow-up:\n%s", plain)
	}
	if !reflect.DeepEqual(records[0], source) {
		t.Fatal("rendering follow-up links mutated the archived source")
	}
}

func TestLegacyHistoryWithoutIDsDoesNotLinkUnrelatedRecordsAsFollowUps(t *testing.T) {
	records := []state.History{{Title: "Legacy one"}, {Title: "Legacy two"}}
	model := newHistoryFixtureModel(t, records, false)
	if plain := historyPlain(model); strings.Contains(plain, "Follow-ups") {
		t.Fatalf("ID-less legacy records were linked together:\n%s", plain)
	}
}

func TestHistoryWrapsScopeAndCompletionSummaryWithoutChangingSelection(t *testing.T) {
	records := historyFixture()
	records[1].Scope = "Preserve the existing command behaviour while making the selected history details readable across narrow terminals."
	records[1].Summary = "The implementation completed the intended change and retained enough evidence for a future maintainer to understand the outcome."
	model := newHistoryFixtureModel(t, records, false)
	model.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	plain := historyPlain(model)
	for _, expected := range []string{"Preserve the existing command", "Completion summary", "implementation completed"} {
		if !strings.Contains(plain, expected) {
			t.Fatalf("History detail missing %q:\n%s", expected, plain)
		}
	}
	if model.cursor != 0 || len(model.screen().selectableItemIDs()) != len(records) {
		t.Fatalf("prose rendering changed History selection: cursor=%d ids=%v", model.cursor, model.screen().selectableItemIDs())
	}
}

func TestHistoryOpensTheArchivedSpecReadOnly(t *testing.T) {
	model := newHistoryFixtureModel(t, historyFixture(), false)
	archive := filepath.Join(model.dir, "specs", "one.md")
	before, err := os.Stat(archive)
	if err != nil {
		t.Fatal(err)
	}
	updateModel(model, key(tea.KeyDown, ""))
	updateModel(model, key(tea.KeyEnter, ""))
	plain := historyPlain(model)
	if !strings.Contains(plain, "Load config from disk.") {
		t.Fatalf("archived Spec content was not shown:\n%s", plain)
	}
	after, err := os.Stat(archive)
	if err != nil {
		t.Fatal(err)
	}
	if before.ModTime() != after.ModTime() || before.Mode() != after.Mode() || before.Size() != after.Size() {
		t.Fatal("opening a completed Spec modified its archive")
	}

	updateModel(model, key(tea.KeyEsc, ""))
	if model.done {
		t.Fatal("esc closed the screen instead of the archived Spec")
	}
	if strings.Contains(historyPlain(model), "Load config from disk.") {
		t.Fatal("esc did not return to the record list")
	}

	model.cursor = 0
	updateModel(model, key(tea.KeyEnter, ""))
	if !strings.Contains(historyPlain(model), "Could not read") {
		t.Fatalf("a missing archive is not reported:\n%s", historyPlain(model))
	}
}

func TestHistoryShowsThePerSpecTimelineForTheSelectedRecord(t *testing.T) {
	model := newHistoryFixtureModel(t, historyFixture(), false)
	updateModel(model, key('t', "t"))
	plain := historyPlain(model)
	for _, fragment := range []string{"spec_created", "plan_accepted", "agent", "Three file plan"} {
		if !strings.Contains(plain, fragment) {
			t.Fatalf("timeline missing %q:\n%s", fragment, plain)
		}
	}
	updateModel(model, key(tea.KeyDown, ""))
	plain = historyPlain(model)
	if !strings.Contains(plain, "derived") {
		t.Fatalf("legacy record timeline is not labelled derived:\n%s", plain)
	}
	updateModel(model, key('t', "t"))
	if strings.Contains(historyPlain(model), "spec_created") {
		t.Fatal("timeline toggle did not turn off")
	}
}

func TestHistoryRendersInsideSupportedWindowsAndExplainsSmallerOnes(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {120, 34}} {
		model := newHistoryFixtureModel(t, historyFixture(), true)
		model.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		updateModel(model, key('t', "t"))
		lines := strings.Split(historyPlain(model), "\n")
		if len(lines) > size[1] {
			t.Fatalf("%dx%d rendered %d rows", size[0], size[1], len(lines))
		}
		for _, line := range lines {
			if ansi.StringWidth(line) > size[0] {
				t.Fatalf("%dx%d rendered a %d column row: %q", size[0], size[1], ansi.StringWidth(line), line)
			}
		}
	}
	model := newHistoryFixtureModel(t, historyFixture(), false)
	model.Update(tea.WindowSizeMsg{Width: 60, Height: 20})
	if plain := historyPlain(model); !strings.Contains(plain, "too small") {
		t.Fatalf("undersized terminal is not explained:\n%s", plain)
	}
	updateModel(model, key(tea.KeyDown, ""))
	if model.cursor != 1 {
		t.Fatalf("navigation broke in an undersized terminal: cursor = %d", model.cursor)
	}
}

func TestHistoryFollowUpConfirmationIsSafeAndBoundedAtSupportedWidths(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {120, 34}} {
		model := newHistoryFixtureModel(t, historyFixture(), false)
		model.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		updateModel(model, key('f', "f"))
		content := model.View().Content
		plain := ansi.Strip(content)
		words := strings.Join(strings.Fields(plain), " ")
		for _, expected := range []string{"Reopen as follow-up?", "new Spec ID", "current Git baseline", "archived", "plan and evidence", "Cancel", "Create linked Spec"} {
			if !strings.Contains(words, expected) {
				t.Fatalf("follow-up confirmation at %dx%d missing %q:\n%s", size[0], size[1], expected, plain)
			}
		}
		if lipgloss.Width(content) > size[0] || lipgloss.Height(content) > size[1] {
			t.Fatalf("follow-up confirmation bounds = %dx%d at %dx%d", lipgloss.Width(content), lipgloss.Height(content), size[0], size[1])
		}
		updateModel(model, key(tea.KeyEnter, ""))
		if model.followupConfirm || model.nav != actionNone {
			t.Fatalf("default confirmation did not safely cancel: %+v", model)
		}
	}
}

func TestLoadHistoryReadsCompletedRecordsFromTheWorkspace(t *testing.T) {
	root := t.TempDir()
	t.Setenv("SPEC_STATE_HOME", filepath.Join(t.TempDir(), "state"))
	t.Setenv("SPEC_CONFIG_HOME", filepath.Join(t.TempDir(), "config"))
	workspace, err := state.Register(root)
	if err != nil {
		t.Fatal(err)
	}
	var stored bytes.Buffer
	for _, record := range historyFixture() {
		encoded, err := json.Marshal(record)
		if err != nil {
			t.Fatal(err)
		}
		stored.Write(append(encoded, '\n'))
	}
	if err := os.WriteFile(filepath.Join(workspace.Dir, "history.jsonl"), stored.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}

	dir, records, active, err := loadHistory(root)
	if err != nil {
		t.Fatal(err)
	}
	if dir != workspace.Dir {
		t.Fatalf("archive directory = %q, want %q", dir, workspace.Dir)
	}
	if active {
		t.Fatal("history load reported an inactive fixture as active")
	}
	if got := historyTitles(records); !reflect.DeepEqual(got, []string{"Add config loader", "Disable automatic indexing"}) {
		t.Fatalf("loaded records = %v", got)
	}
	model := newHistoryModel(root, dir, records, active)
	model.Update(tea.WindowSizeMsg{Width: 120, Height: 34})
	plain := historyPlain(model)
	for _, fragment := range []string{"Disable automatic indexing", "+12 -30"} {
		if !strings.Contains(plain, fragment) {
			t.Fatalf("history screen missing %q:\n%s", fragment, plain)
		}
	}
}
