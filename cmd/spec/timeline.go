package main

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/TaylorEdgerton/spec-cli/internal/state"
)

// timelineEntry is one displayable moment in a Spec's life. Derived marks a fact
// reconstructed from a legacy record that predates recorded timeline events.
type timelineEntry struct {
	At                          time.Time
	Type, Actor, Source, Detail string
	Derived                     bool
}

// timelineEntries returns the recorded events of a completed Spec in stable
// chronological order, or the facts a legacy record actually contains. It never
// invents an event a record does not evidence.
func timelineEntries(record state.History) []timelineEntry {
	entries := make([]timelineEntry, 0, len(record.Timeline))
	if len(record.Timeline) > 0 {
		for _, event := range record.Timeline {
			entries = append(entries, timelineEntry{
				At: event.OccurredAt, Type: string(event.Type),
				Actor: strings.TrimSpace(event.Actor), Source: strings.TrimSpace(event.Source),
				Detail: timelineDetail(event.Details),
			})
		}
	} else {
		entries = derivedTimelineEntries(record)
	}
	sort.SliceStable(entries, func(left, right int) bool { return entries[left].At.Before(entries[right].At) })
	return entries
}

func derivedTimelineEntries(record state.History) []timelineEntry {
	var entries []timelineEntry
	add := func(kind state.TimelineEventType, at time.Time, detail string) {
		entries = append(entries, timelineEntry{At: at, Type: string(kind), Source: "history", Detail: detail, Derived: true})
	}
	if !record.StartedAt.IsZero() {
		add(state.TimelineSpecCreated, record.StartedAt, record.Title)
	}
	if record.BaseSHA != "" {
		add(state.TimelineBaselineCaptured, record.StartedAt, "starting state "+shortSHA(record.BaseSHA))
	}
	if record.Verification != nil {
		outcome := "did not pass"
		if record.Verification.Passed {
			outcome = "passed"
		}
		add(state.TimelineEvidenceRecorded, record.Verification.FinishedAt,
			strings.Join(record.Verification.Commands, ", ")+" "+outcome)
	}
	if !record.FinishedAt.IsZero() {
		add(state.TimelineSpecCompleted, record.FinishedAt, "")
	}
	return entries
}

func timelineDetail(details state.TimelineDetails) string {
	parts := make([]string, 0, 4)
	for _, value := range []string{details.Summary, details.Title} {
		if strings.TrimSpace(value) != "" && !contains(parts, value) {
			parts = append(parts, value)
		}
	}
	if details.BaselineSHA != "" {
		parts = append(parts, "starting state "+shortSHA(details.BaselineSHA))
	}
	if details.Count > 0 {
		parts = append(parts, fmt.Sprintf("%d items", details.Count))
	}
	if details.PromptKind != "" {
		parts = append(parts, details.PromptKind+" prompt")
	}
	return strings.Join(parts, " · ")
}

func timelineLines(entries []timelineEntry) []string {
	if len(entries) == 0 {
		return []string{uiMutedStyle.Render("  No timeline events were recorded for this Spec.")}
	}
	lines := make([]string, 0, len(entries))
	for _, entry := range entries {
		stamp := entry.At.UTC().Format("2006-01-02 15:04")
		if entry.At.IsZero() {
			stamp = "time not recorded"
		}
		detail := entry.Detail
		if entry.Derived {
			detail = strings.TrimSpace(detail + " " + uiMutedStyle.Render("(derived from the stored record)"))
		}
		lines = append(lines, fmt.Sprintf("  %-17s %-34s %-14s %s",
			stamp, timelineLabel(entry.Type), actorSource(entry), detail))
	}
	return lines
}

func timelineLabel(kind string) string {
	labels := map[string]string{
		string(state.TimelineSpecCreated):        "Spec created",
		string(state.TimelineBaselineCaptured):   "Starting state captured",
		string(state.TimelineDiscoveryRefreshed): "Discovery refreshed",
		string(state.TimelinePromptCopied):       "Prompt copied",
		string(state.TimelinePromptPrinted):      "Prompt printed",
		string(state.TimelinePlanAccepted):       "Plan accepted",
		string(state.TimelinePlanAmended):        "Plan amended",
		string(state.TimelineActualRefreshed):    "Actual state refreshed",
		string(state.TimelineEvidenceRecorded):   "Evidence recorded",
		string(state.TimelineReviewDecision):     "Review decision",
		string(state.TimelineChangesRequested):   "Changes requested",
		string(state.TimelineSpecCompleted):      "Spec completed",
		string(state.TimelineFollowUpStarted):    "Follow-up started",
	}
	if label, ok := labels[kind]; ok {
		return label + " (" + kind + ")"
	}
	return kind
}

func actorSource(entry timelineEntry) string {
	actor, source := entry.Actor, entry.Source
	if actor == "" {
		actor = "unknown"
	}
	if source == "" {
		source = "unknown"
	}
	return actor + "/" + source
}

func contains(values []string, value string) bool {
	for _, existing := range values {
		if existing == value {
			return true
		}
	}
	return false
}
