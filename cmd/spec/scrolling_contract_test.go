package main

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/TaylorEdgerton/spec-cli/internal/discovery"
	"github.com/TaylorEdgerton/spec-cli/internal/gitutil"
	"github.com/TaylorEdgerton/spec-cli/internal/review"
	"github.com/charmbracelet/x/ansi"
)

func longContextFixture(count int) []discovery.Result {
	results := make([]discovery.Result, count)
	for index := range results {
		results[index] = discovery.Result{
			Path: fmt.Sprintf("internal/context/file-%02d.go", index),
			Symbols: []discovery.Symbol{{
				Name:       fmt.Sprintf("Symbol%02d", index),
				Capability: discovery.CapabilityStructural,
			}},
		}
	}
	return results
}

func longReviewFixture(count int) reviewSnapshot {
	snapshot := reviewSnapshotFixture(nil)
	snapshot.Projection.Files = make([]review.FileReview, count)
	snapshot.Hunks = make(map[string][]review.HunkReview, count)
	for index := range snapshot.Projection.Files {
		path := fmt.Sprintf("internal/review/file-%02d.go", index)
		change := gitutil.FileChange{Path: path, Kind: gitutil.ChangeModified, Additions: index + 1}
		snapshot.Projection.Files[index] = review.FileReview{
			Path: path, ActualAction: gitutil.ChangeModified, Status: review.StatusAdditional, Change: &change,
		}
		snapshot.Hunks[path] = []review.HunkReview{{Hunk: gitutil.DiffHunk{
			Header: fmt.Sprintf("@@ -%d +%d @@", index+1, index+1),
			Lines:  []gitutil.DiffLine{{Kind: gitutil.DiffAddition, Text: "changed", NewLine: index + 1}},
		}}}
	}
	return snapshot
}

func TestImplementationContextLongResultsAreCursorAndPageNavigable(t *testing.T) {
	for _, size := range []tea.WindowSizeMsg{{Width: 120, Height: 34}, {Width: 80, Height: 24}} {
		t.Run(fmt.Sprintf("%dx%d", size.Width, size.Height), func(t *testing.T) {
			results := longContextFixture(40)
			model := newContextReviewModel(results)
			model.Update(size)

			// Continue remains the safe default. Moving up enters the result list at
			// its final item and the result viewport must follow that cursor.
			model.Update(key(tea.KeyUp, ""))
			plain := ansi.Strip(model.View().Content)
			if model.cursor != len(results)-1 || !strings.Contains(plain, "> internal/context/file-39.go") {
				t.Fatalf("last context result is not selected and visible (cursor=%d):\n%s", model.cursor, plain)
			}
			if !strings.Contains(plain, "/ 40 results") {
				t.Fatalf("context result viewport has no range/total label:\n%s", plain)
			}

			before := model.cursor
			model.Update(key(tea.KeyPgUp, ""))
			if model.cursor >= before-1 {
				t.Fatalf("Page Up moved %d -> %d, want a pane of results", before, model.cursor)
			}
			if page := ansi.Strip(model.View().Content); !strings.Contains(page, fmt.Sprintf("> internal/context/file-%02d.go", model.cursor)) {
				t.Fatalf("paged context selection is outside its pane:\n%s", page)
			}
		})
	}
}

func TestChangesAndDiffLongNavigatorsOwnTheirViewport(t *testing.T) {
	for _, size := range []tea.WindowSizeMsg{{Width: 120, Height: 34}, {Width: 80, Height: 24}} {
		for _, tab := range []reviewTab{tabChanges, tabDiff} {
			t.Run(fmt.Sprintf("%s-%dx%d", reviewTabLabels[tab], size.Width, size.Height), func(t *testing.T) {
				const count = 48
				model := newReviewModel(t.TempDir(), longReviewFixture(count))
				model.tab = tab
				model.Update(size)

				// Wrapping to the final file reproduces the bug: its path is also
				// rendered near the top of the detail pane, but only the navigator
				// occurrence carries this selected-row marker.
				model.Update(key(tea.KeyUp, ""))
				plain := ansi.Strip(model.View().Content)
				selected := "> + internal/review/file-47.go"
				if model.cursors[tab] != count-1 || !strings.Contains(plain, selected) {
					t.Fatalf("selected navigator row is not visible; duplicate detail text must not anchor scrolling:\n%s", plain)
				}
				if !strings.Contains(plain, "/ 48 files") {
					t.Fatalf("file navigator has no range/total label:\n%s", plain)
				}

				before := model.cursors[tab]
				model.Update(key(tea.KeyPgUp, ""))
				minimumStep := 2
				if size.Width >= reviewSplitWidth {
					minimumStep = 10
				}
				if before-model.cursors[tab] < minimumStep {
					t.Fatalf("Page Up moved %d -> %d, want a pane of files", before, model.cursors[tab])
				}
				page := ansi.Strip(model.View().Content)
				want := fmt.Sprintf("> + internal/review/file-%02d.go", model.cursors[tab])
				if !strings.Contains(page, want) {
					t.Fatalf("paged file selection is outside its navigator; want %q:\n%s", want, page)
				}
			})
		}
	}
}

func TestDiffFileAndFocusedHunkScrollingRemainIndependent(t *testing.T) {
	model := newReviewModel(t.TempDir(), longReviewFixture(48))
	model.tab = tabDiff
	model.Update(tea.WindowSizeMsg{Width: 120, Height: 34})
	model.Update(key(tea.KeyPgDown, ""))
	selected := model.cursors[tabDiff]
	if selected < 10 {
		t.Fatalf("Page Down did not move through the file pane: %d", selected)
	}
	model.Update(key(tea.KeyEnter, ""))
	if !model.diffFocused {
		t.Fatal("Enter did not focus the selected hunk")
	}
	model.Update(key(tea.KeyDown, ""))
	if model.cursors[tabDiff] != selected {
		t.Fatalf("focused code scrolling changed file %d -> %d", selected, model.cursors[tabDiff])
	}
	model.Update(key(tea.KeyEscape, ""))
	if model.diffFocused || model.cursors[tabDiff] != selected {
		t.Fatalf("Escape did not return to the same file row: focused=%v cursor=%d", model.diffFocused, model.cursors[tabDiff])
	}
}

func TestDiffNavigatorBoundsLongHunkPreviewBeforeFocus(t *testing.T) {
	snapshot := longReviewFixture(1)
	path := snapshot.Projection.Files[0].Path
	lines := make([]gitutil.DiffLine, 80)
	for index := range lines {
		lines[index] = gitutil.DiffLine{Kind: gitutil.DiffAddition, Text: fmt.Sprintf("long preview line %02d", index), NewLine: index + 1}
	}
	snapshot.Hunks[path] = []review.HunkReview{{Hunk: gitutil.DiffHunk{Header: "@@ -0,0 +1,80 @@", Lines: lines}}}
	model := newReviewModel(t.TempDir(), snapshot)
	model.tab = tabDiff
	model.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	view := model.View().Content
	plain := ansi.Strip(view)
	for _, expected := range []string{"long preview line 00", "Enter to focus and scroll", "[ Open VS Code ]", "[ Integration ]"} {
		if !strings.Contains(plain, expected) {
			t.Fatalf("bounded Diff preview missing %q:\n%s", expected, plain)
		}
	}
	if strings.Contains(plain, "long preview line 79") || lipgloss.Height(view) > 24 {
		t.Fatalf("unfocused long hunk was not bounded to the terminal:\n%s", plain)
	}
}
