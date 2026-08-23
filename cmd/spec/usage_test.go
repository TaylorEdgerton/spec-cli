package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/TaylorEdgerton/spec-cli/internal/aiusage"
	"github.com/TaylorEdgerton/spec-cli/internal/state"
)

func TestFormatUsageHistoryShowsCompletedSpecsNewestFirst(t *testing.T) {
	records := []state.History{
		{
			Title: "Older Spec", FinishedAt: time.Date(2026, 8, 20, 1, 2, 3, 0, time.UTC),
		},
		{
			Title: "Newer Spec", Summary: "completed successfully",
			FinishedAt: time.Date(2026, 8, 23, 4, 5, 6, 0, time.UTC),
			AIUsage: &aiusage.Summary{
				Available: true, SandboxDurationSeconds: 90,
				Usage: []aiusage.AIUsage{{
					Provider: "Codex", Model: "gpt-5.6-sol", InputTokens: 1234,
					CachedInputTokens: 200, OutputTokens: 56, Requests: 3,
				}},
			},
		},
	}
	var output bytes.Buffer
	formatUsageHistory(&output, records)
	text := output.String()

	for _, expected := range []string{
		"AI usage history", "Newer Spec", "Summary      completed successfully",
		"Codex (gpt-5.6-sol)", "1,234", "Cached      200", "Output      56",
		"Requests    3", "Sandbox       1m", "Older Spec",
		"usage unavailable: no sandbox usage was recorded",
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("history output missing %q:\n%s", expected, text)
		}
	}
	if strings.Index(text, "Newer Spec") > strings.Index(text, "Older Spec") {
		t.Fatalf("history is not newest-first:\n%s", text)
	}
}

func TestFormatUsageHistoryHandlesEmptyWorkspace(t *testing.T) {
	var output bytes.Buffer
	formatUsageHistory(&output, nil)
	want := "AI usage history\n\nNo completed Specs in this workspace.\n"
	if output.String() != want {
		t.Fatalf("output = %q, want %q", output.String(), want)
	}
}
