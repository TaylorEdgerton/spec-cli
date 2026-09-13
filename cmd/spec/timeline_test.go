package main

import (
	"strings"
	"testing"
	"time"

	"github.com/TaylorEdgerton/spec-cli/internal/state"
)

func timelineEventFixture(id string, kind state.TimelineEventType, at time.Time, actor, source string, details state.TimelineDetails) state.TimelineEvent {
	return state.TimelineEvent{
		SchemaVersion: state.ArtifactSchemaVersion, ID: id, Type: kind,
		Actor: actor, Source: source, OccurredAt: at, Details: details,
	}
}

func entryTypes(entries []timelineEntry) []string {
	types := make([]string, len(entries))
	for index, entry := range entries {
		types[index] = entry.Type
	}
	return types
}

func TestTimelineOrdersRecordedEventsChronologicallyWithActorsSourcesAndDetails(t *testing.T) {
	start := time.Date(2026, 8, 28, 9, 0, 0, 0, time.UTC)
	record := state.History{
		SpecID: "SPEC-002", Title: "Disable automatic indexing", StartedAt: start, FinishedAt: start.Add(2 * time.Hour),
		Timeline: []state.TimelineEvent{
			timelineEventFixture("c", state.TimelineSpecCompleted, start.Add(2*time.Hour), "human", "spec", state.TimelineDetails{Title: "Disable automatic indexing"}),
			timelineEventFixture("a", state.TimelineSpecCreated, start, "human", "spec", state.TimelineDetails{SpecID: "SPEC-002", Title: "Disable automatic indexing"}),
			timelineEventFixture("b1", state.TimelinePlanAccepted, start.Add(time.Hour), "agent", "paste", state.TimelineDetails{Summary: "Three file plan", Count: 3}),
			timelineEventFixture("b2", state.TimelineActualRefreshed, start.Add(time.Hour), "human", "review", state.TimelineDetails{Count: 4}),
		},
	}

	entries := timelineEntries(record)
	want := []string{
		string(state.TimelineSpecCreated), string(state.TimelinePlanAccepted),
		string(state.TimelineActualRefreshed), string(state.TimelineSpecCompleted),
	}
	if got := entryTypes(entries); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("entry order = %v, want %v", got, want)
	}
	if entries[1].Actor != "agent" || entries[1].Source != "paste" || !strings.Contains(entries[1].Detail, "Three file plan") {
		t.Fatalf("plan entry = %+v", entries[1])
	}
	if !strings.Contains(entries[2].Detail, "4") {
		t.Fatalf("refresh entry detail = %q", entries[2].Detail)
	}
	for _, entry := range entries {
		if entry.Derived {
			t.Fatalf("recorded event was marked derived: %+v", entry)
		}
	}

	lines := strings.Join(timelineLines(entries), "\n")
	for _, want := range []string{"09:00", "spec_created", "human", "plan_accepted", "agent", "spec_completed"} {
		if !strings.Contains(lines, want) {
			t.Fatalf("timeline lines missing %q:\n%s", want, lines)
		}
	}
}

func TestTimelineDerivesOnlyLegacyFactsThatArePresent(t *testing.T) {
	start := time.Date(2026, 8, 20, 9, 0, 0, 0, time.UTC)
	legacy := state.History{
		Title: "Add config loader", StartedAt: start, FinishedAt: start.Add(time.Hour), BaseSHA: "abcdef1234567",
		Verification: &state.Verification{Commands: []string{"go test ./..."}, Passed: true, FinishedAt: start.Add(30 * time.Minute)},
	}
	entries := timelineEntries(legacy)
	want := []string{
		string(state.TimelineSpecCreated), string(state.TimelineBaselineCaptured),
		string(state.TimelineEvidenceRecorded), string(state.TimelineSpecCompleted),
	}
	if got := entryTypes(entries); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("derived order = %v, want %v", got, want)
	}
	for _, entry := range entries {
		if !entry.Derived {
			t.Fatalf("legacy entry was not marked derived: %+v", entry)
		}
	}
	if !strings.Contains(entries[1].Detail, "abcdef1") {
		t.Fatalf("starting state detail = %q", entries[1].Detail)
	}
	if !strings.Contains(entries[2].Detail, "go test ./...") {
		t.Fatalf("verification detail = %q", entries[2].Detail)
	}

	bare := state.History{Title: "Old change", FinishedAt: start.Add(time.Hour)}
	if got := entryTypes(timelineEntries(bare)); strings.Join(got, ",") != string(state.TimelineSpecCompleted) {
		t.Fatalf("bare legacy record invented events: %v", got)
	}
	lines := strings.Join(timelineLines(timelineEntries(bare)), "\n")
	if !strings.Contains(lines, "derived") {
		t.Fatalf("derived facts are not labelled as such:\n%s", lines)
	}
}

func TestTimelineTolerablyRendersIncompleteEventData(t *testing.T) {
	record := state.History{
		Title: "Partial", FinishedAt: time.Date(2026, 8, 28, 10, 0, 0, 0, time.UTC),
		Timeline: []state.TimelineEvent{
			{SchemaVersion: 1, ID: "no-time", Type: state.TimelinePromptCopied},
			{SchemaVersion: 1, ID: "unknown", Type: "invented_by_a_future_version", OccurredAt: time.Date(2026, 8, 28, 10, 0, 0, 0, time.UTC)},
		},
	}
	entries := timelineEntries(record)
	if len(entries) != 2 {
		t.Fatalf("entries = %+v", entries)
	}
	if entries[0].Type != string(state.TimelinePromptCopied) || entries[1].Type != "invented_by_a_future_version" {
		t.Fatalf("entry types = %+v", entries)
	}
	lines := strings.Join(timelineLines(entries), "\n")
	if !strings.Contains(lines, "time not recorded") {
		t.Fatalf("missing timestamp is not explained:\n%s", lines)
	}
	if !strings.Contains(lines, "unknown") {
		t.Fatalf("missing actor is not explained:\n%s", lines)
	}
	if !strings.Contains(lines, "invented_by_a_future_version") {
		t.Fatalf("unrecognised event type was dropped:\n%s", lines)
	}
}
