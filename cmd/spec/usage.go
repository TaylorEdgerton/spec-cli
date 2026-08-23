package main

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/TaylorEdgerton/spec-cli/internal/aiusage"
	"github.com/TaylorEdgerton/spec-cli/internal/sandbox"
	"github.com/TaylorEdgerton/spec-cli/internal/state"
)

func cmdUsage(args []string) error {
	if len(args) > 1 || (len(args) == 1 && args[0] != "history") {
		return fmt.Errorf("usage: spec usage [history]")
	}
	if len(args) == 1 {
		return cmdUsageHistory()
	}
	root, err := currentRoot()
	if err != nil {
		fmt.Fprintln(os.Stdout, "AI usage")
		fmt.Fprintln(os.Stdout)
		fmt.Fprintln(os.Stdout, "usage unavailable: use a spec-managed sandbox environment for usage stats")
		return nil
	}
	aiusage.Format(os.Stdout, sandbox.Usage(root, time.Now()))
	return nil
}

func cmdUsageHistory() error {
	root, err := currentRoot()
	if err != nil {
		return fmt.Errorf("Git workspace is required: %w", err)
	}
	workspace, err := state.Load(root)
	if err != nil {
		return err
	}
	records, err := workspace.HistoryRecords()
	if err != nil {
		return err
	}
	formatUsageHistory(os.Stdout, records)
	return nil
}

func formatUsageHistory(writer io.Writer, records []state.History) {
	fmt.Fprintln(writer, "AI usage history")
	if len(records) == 0 {
		fmt.Fprintln(writer)
		fmt.Fprintln(writer, "No completed Specs in this workspace.")
		return
	}

	for index := len(records) - 1; index >= 0; index-- {
		record := records[index]
		fmt.Fprintln(writer)
		title := strings.Join(strings.Fields(record.Title), " ")
		if title == "" {
			title = "Untitled Spec"
		}
		fmt.Fprintln(writer, title)
		if !record.FinishedAt.IsZero() {
			fmt.Fprintf(writer, "  Finished     %s\n", record.FinishedAt.Local().Format("02 Jan 2006 15:04 MST"))
		}
		if summary := strings.Join(strings.Fields(record.Summary), " "); summary != "" {
			fmt.Fprintf(writer, "  Summary      %s\n", summary)
		}
		fmt.Fprintln(writer)
		if record.AIUsage == nil {
			missing := aiusage.Unavailable("no sandbox usage was recorded", 0)
			aiusage.FormatSummary(writer, missing, "  ")
			continue
		}
		aiusage.FormatSummary(writer, *record.AIUsage, "  ")
	}
}
