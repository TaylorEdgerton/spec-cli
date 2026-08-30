package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/TaylorEdgerton/spec-cli/internal/change"
	"github.com/TaylorEdgerton/spec-cli/internal/discovery"
	"github.com/TaylorEdgerton/spec-cli/internal/evidence"
	"github.com/TaylorEdgerton/spec-cli/internal/gitutil"
	"github.com/TaylorEdgerton/spec-cli/internal/review"
	"github.com/TaylorEdgerton/spec-cli/internal/state"
	"github.com/TaylorEdgerton/spec-cli/internal/verify"
	"github.com/charmbracelet/x/ansi"
)

// The supported window floor from the screen
const (
	reviewMinWidth  = 80
	reviewMinHeight = 16
)

type reviewTab int

const (
	tabOverview reviewTab = iota
	tabFiles
	tabIntegration
	tabEvidence
	tabDiff
	tabStats
)

var reviewTabLabels = []string{"Overview", "Files", "Integration", "Evidence", "Diff", "Stats"}

type reviewDecision string

const (
	decisionNone     reviewDecision = ""
	decisionComplete reviewDecision = "complete"
	decisionChanges  reviewDecision = "request_changes"
)

type reviewRow struct{ ID, Label, Detail string }

// reviewSnapshot is the single explicitly refreshed set of facts
type reviewSnapshot struct {
	SpecID, Title, Intent, Scope, Baseline string
	Plan                                   *state.StoredChangePlan
	Criteria                               []change.Criterion
	Projection                             review.Projection
	Evidence                               evidence.Report
	Hunks                                  map[string][]review.HunkReview
	RefreshedAt                            time.Time
}

type reviewModel struct {
	root          string
	snap          reviewSnapshot
	tab           reviewTab
	cursors       map[reviewTab]int
	hunk          int
	filter        review.FileStatus
	status        string
	decision      reviewDecision
	width, height int
	done          bool
	help          bool
	nav           string
	viewport      int

	refresh  func(string) (reviewSnapshot, error)
	record   func(string, state.TimelineEvent) error
	open     func(string, discovery.Result) error
	runTests func(string) error
}

func newReviewModel(root string, snap reviewSnapshot) *reviewModel {
	return &reviewModel{
		root: root, snap: snap, cursors: map[reviewTab]int{},
		refresh: loadReviewSnapshot,
		record:  recordReviewEvent,
		open:    openInVSCode,
		runTests: func(root string) error {
			_, _, err := verify.RunWithEvidence(root, evidence.PhaseImplementation, time.Now())
			return err
		},
	}
}

func (m *reviewModel) screen() canonicalScreen {
	items := make([]screenItem, 0)
	sections := make([]screenSection, 0, 1)
	switch m.tab {
	case tabFiles:
		for _, file := range m.filteredFiles() {
			items = append(items, screenItem{ID: "review.file." + file.Path, Label: file.Path, Detail: file.Reason, Selectable: true, Preview: file})
		}
	case tabIntegration:
		for index, item := range m.snap.Projection.Integrations {
			items = append(items, screenItem{ID: fmt.Sprintf("review.integration.%d", index), Label: item.Symbol, Detail: item.Relationship, Selectable: true, Preview: item})
		}
	case tabEvidence:
		for _, category := range []struct {
			id, title string
			category  evidence.Category
		}{{"existing", "Existing before change", evidence.CategoryExisting}, {"reproduction", "Pre-change reproduction", evidence.CategoryFailThenPass}, {"new", "Added during implementation", evidence.CategoryNewTest}, {"modified", "Modified existing tests", evidence.CategoryModifiedExisting}, {"manual", "Manual claims", evidence.CategoryManual}} {
			categoryItems := make([]screenItem, 0)
			for _, item := range m.snap.Evidence.Items {
				if item.Category == category.category {
					categoryItems = append(categoryItems, screenItem{ID: "review.evidence." + item.ID, Label: item.Name, Detail: string(item.Category), Selectable: true, Preview: item})
				}
			}
			if len(categoryItems) > 0 {
				sections = append(sections, screenSection{ID: "review.evidence." + category.id, Title: category.title, Items: categoryItems})
			}
		}
	case tabDiff:
		for _, file := range m.diffFiles() {
			items = append(items, screenItem{ID: "review.diff." + file.Path, Label: file.Path, Selectable: true, Preview: file})
		}
	case tabStats:
		items = []screenItem{
			{ID: "review.complete", Label: "Complete Spec", Selectable: true, Action: screenAction(actionComplete)},
			{ID: "review.request_changes", Label: "Request Changes", Selectable: true, Action: screenAction(actionChanges)},
		}
	}
	if len(sections) > 0 {
		return canonicalScreen{Sections: sections, Cursor: m.cursors[m.tab]}
	}
	section := screenSection{ID: "review." + strings.ToLower(reviewTabLabels[m.tab]), Title: reviewTabLabels[m.tab], Items: items, EmptyReason: "No items are available in this view."}
	return canonicalScreen{Sections: []screenSection{section}, Cursor: m.cursors[m.tab]}
}

func (m *reviewModel) Init() tea.Cmd { return nil }

func (m *reviewModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch v := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = v.Width, v.Height
	case tea.InterruptMsg:
		m.leave(actionQuit)
		return m, tea.Quit
	case tea.KeyPressMsg:
		return m, m.key(v.Keystroke())
	}
	return m, nil
}

func (m *reviewModel) key(keystroke string) tea.Cmd {
	switch keystroke {
	case "tab":
		m.selectTab(reviewTab(wrap(int(m.tab)+1, len(reviewTabLabels))))
	case "shift+tab":
		m.selectTab(reviewTab(wrap(int(m.tab)-1, len(reviewTabLabels))))
	case "up", "k":
		m.move(-1)
	case "down", "j":
		m.move(1)
	case "pgup":
		m.viewport = max(0, m.viewport-max(1, m.height/2))
	case "pgdown":
		m.viewport += max(1, m.height/2)
	case "n":
		m.moveHunk(1)
	case "p":
		m.moveHunk(-1)
	case "f":
		m.cycleFilter()
	case "d":
		m.showDiff()
	case "i":
		m.selectTab(tabIntegration)
	case "e":
		m.selectTab(tabEvidence)
	case "o":
		m.openSelected()
	case "t":
		m.runSelectedTests()
	case "r":
		m.refreshSnapshot()
	case "s":
		if m.tab == tabEvidence {
			m.leave(actionSummary)
			return tea.Quit
		}
	case "enter":
		return m.activate()
	case "?":
		m.help = !m.help
	case "b", "esc":
		m.leave(actionBack)
		return tea.Quit
	case "q", "ctrl+c":
		m.leave(actionQuit)
		return tea.Quit
	}
	return nil
}

func (m *reviewModel) leave(action string) { m.done, m.nav = true, action }

func (m *reviewModel) selectTab(tab reviewTab) {
	m.tab, m.hunk, m.status, m.viewport = tab, 0, "", 0
}

func (m *reviewModel) move(delta int) {
	count := len(m.screen().selectableItems())
	if count == 0 {
		return
	}
	m.cursors[m.tab] = wrap(m.cursors[m.tab]+delta, count)
	m.hunk, m.status = 0, ""
}

func (m *reviewModel) moveHunk(delta int) {
	count := len(m.snap.Hunks[m.diffFile()])
	if m.tab != tabDiff || count == 0 {
		return
	}
	m.hunk = wrap(m.hunk+delta, count)
}

func (m *reviewModel) cycleFilter() {
	if m.tab != tabFiles {
		return
	}
	order := []review.FileStatus{"", review.StatusMatched, review.StatusAdditional, review.StatusUntouched}
	for index, status := range order {
		if status == m.filter {
			m.filter = order[(index+1)%len(order)]
			break
		}
	}
	m.cursors[tabFiles], m.status = 0, ""
}

func (m *reviewModel) showDiff() {
	if m.tab == tabFiles {
		if row, ok := m.selectedRow(); ok {
			m.selectDiffFile(strings.TrimPrefix(row.ID, "review.file."))
		}
	}
	m.selectTab(tabDiff)
}

func (m *reviewModel) selectDiffFile(path string) {
	for index, file := range m.diffFiles() {
		if file.Path == path {
			m.cursors[tabDiff] = index
			return
		}
	}
}

func (m *reviewModel) openSelected() {
	path, line := m.selectedLocation()
	if path == "" {
		m.status = "This row has no source location to open."
		return
	}
	if err := m.open(m.root, discovery.Result{Path: path, Line: line}); err != nil {
		m.status = "Could not open in VS Code: " + err.Error()
		return
	}
	m.status = "Opened " + path
}

func (m *reviewModel) runSelectedTests() {
	if err := m.runTests(m.root); err != nil {
		m.status = "Could not run verification: " + err.Error()
		return
	}
	m.status = "Verification finished; press r to refresh the review snapshot."
}

func (m *reviewModel) refreshSnapshot() {
	snapshot, err := m.refresh(m.root)
	if err != nil {
		m.status = "Could not refresh: " + err.Error()
		return
	}
	m.snap, m.hunk = snapshot, 0
	m.status = "Refreshed actual state."
	m.recordEvent(state.TimelineActualRefreshed, "actual state refreshed")
}

func (m *reviewModel) activate() tea.Cmd {
	row, ok := m.selectedRow()
	if !ok {
		return nil
	}
	switch {
	case row.ID == "review.complete":
		m.decision = decisionComplete
		m.status = "Completion acknowledged; the Spec is being archived."
		m.recordEvent(state.TimelineReviewDecision, "human acknowledged completion")
		m.leave(actionComplete)
		return tea.Quit
	case row.ID == "review.request_changes":
		m.decision = decisionChanges
		m.status = "Changes requested; the plan, evidence, and review facts are kept."
		m.recordEvent(state.TimelineChangesRequested, "human requested more changes")
		m.leave(actionChanges)
		return tea.Quit
	case m.tab == tabFiles:
		m.showDiff()
	case m.tab == tabIntegration, m.tab == tabDiff:
		m.openSelected()
	}
	return nil
}

func (m *reviewModel) recordEvent(eventType state.TimelineEventType, summary string) {
	event := state.TimelineEvent{
		SchemaVersion: state.ArtifactSchemaVersion,
		ID:            fmt.Sprintf("%s:%s:%d", m.snap.SpecID, eventType, time.Now().UnixNano()),
		Type:          eventType,
		Actor:         "human",
		Source:        "review",
		OccurredAt:    time.Now().UTC(),
		Details:       state.TimelineDetails{SpecID: m.snap.SpecID, Summary: summary, Count: m.snap.Projection.Stats.Files},
	}
	if err := m.record(m.root, event); err != nil {
		m.status = "Could not record the review event: " + err.Error()
	}
}

func (m *reviewModel) filteredFiles() []review.FileReview {
	files := make([]review.FileReview, 0, len(m.snap.Projection.Files))
	for _, file := range m.snap.Projection.Files {
		if m.filter == "" || file.Status == m.filter {
			files = append(files, file)
		}
	}
	return files
}

func (m *reviewModel) diffFiles() []review.FileReview {
	files := make([]review.FileReview, 0, len(m.snap.Projection.Files))
	for _, file := range m.snap.Projection.Files {
		if file.Change != nil {
			files = append(files, file)
		}
	}
	sort.SliceStable(files, func(left, right int) bool { return files[left].Path < files[right].Path })
	return files
}

func (m *reviewModel) selectedRow() (reviewRow, bool) {
	item, ok := m.screen().selectedItem()
	if !ok {
		return reviewRow{}, false
	}
	return reviewRow{ID: item.ID, Label: item.Label, Detail: item.Detail}, true
}

func (m *reviewModel) diffFile() string {
	if m.tab != tabDiff {
		return ""
	}
	item, ok := m.screen().selectedItem()
	if !ok {
		return ""
	}
	file, ok := item.Preview.(review.FileReview)
	if !ok {
		return ""
	}
	return file.Path
}

func (m *reviewModel) selectedIntegration() (review.Integration, bool) {
	if m.tab != tabIntegration {
		return review.Integration{}, false
	}
	item, ok := m.screen().selectedItem()
	if !ok {
		return review.Integration{}, false
	}
	integration, ok := item.Preview.(review.Integration)
	return integration, ok
}

func (m *reviewModel) selectedLocation() (string, int) {
	switch m.tab {
	case tabIntegration:
		if item, ok := m.selectedIntegration(); ok {
			return item.Path, item.Line
		}
	case tabFiles:
		if row, ok := m.selectedRow(); ok {
			return strings.TrimPrefix(row.ID, "review.file."), 0
		}
	case tabDiff:
		return m.diffFile(), m.currentHunkLine()
	}
	return "", 0
}

func (m *reviewModel) currentHunkLine() int {
	hunks := m.snap.Hunks[m.diffFile()]
	if len(hunks) == 0 {
		return 0
	}
	return hunks[clamp(m.hunk, 0, len(hunks)-1)].Hunk.NewStart
}

func (m *reviewModel) View() tea.View {
	width, height := m.width, m.height
	if width <= 0 {
		width = 100
	}
	if height <= 0 {
		height = 32
	}
	if width < reviewMinWidth || height < reviewMinHeight {
		return tea.NewView(uiEmptyState("Terminal is too small",
			fmt.Sprintf("Review needs at least %dx%d; this terminal is %dx%d.", reviewMinWidth, reviewMinHeight, width, height)))
	}
	body := []string{}
	if m.tab != tabStats {
		body = append(body, uiTabs(reviewTabLabels, int(m.tab)), "")
	}
	if m.help {
		body = append(body, uiHelpOverlay(m.hints()))
	} else {
		body = append(body, m.tabBody(width)...)
	}
	if m.status != "" {
		body = append(body, "", uiMutedStyle.Render(m.status))
	}
	header := uiSplit(
		fmt.Sprintf("Review · %s", reviewTabLabels[m.tab]),
		fmt.Sprintf("%s · %s", m.snap.SpecID, shortSHA(m.snap.Baseline)),
		max(1, width-4),
	)
	if m.tab == tabStats {
		header = uiSplit("Change Summary", fmt.Sprintf("%s · %s", m.snap.SpecID, shortSHA(m.snap.Baseline)), max(1, width-4))
	}
	rendered := strings.Join(body, "\n")
	anchor := ""
	if item, ok := m.screen().selectedItem(); ok {
		anchor = item.Label
	}
	rendered, m.viewport = uiViewportBody(rendered, uiWorkflowBodyHeight(height, header), m.viewport, anchor)
	return tea.NewView(uiAppShell(width, height, header, rendered, m.footer()))
}

func (m *reviewModel) footer() string { return uiKeyHints(m.hints(), "  ") }

func (m *reviewModel) hints() [][2]string {
	hints := [][2]string{{"tab", "view"}, {"↑/↓", "select"}, {"enter", "action"}}
	switch m.tab {
	case tabFiles:
		hints = append(hints, [2]string{"f", "filter"}, [2]string{"d", "diff"})
	case tabIntegration:
		hints = append(hints, [2]string{"o", "VS Code"}, [2]string{"d", "diff"})
	case tabEvidence:
		hints = append(hints, [2]string{"t", "run tests"}, [2]string{"d", "test diff"}, [2]string{"s", "summary"})
	case tabDiff:
		hints = append(hints, [2]string{"n/p", "hunk"}, [2]string{"o", "VS Code"}, [2]string{"i", "integration"})
	case tabStats:
		hints = append(hints, [2]string{"d", "diff"}, [2]string{"i", "integration"}, [2]string{"e", "evidence"})
	}
	return append(hints, [2]string{"r", "refresh"}, [2]string{"?", "help"}, [2]string{"b", "back"}, [2]string{"q", "exit"})
}

func (m *reviewModel) tabBody(width int) []string {
	switch m.tab {
	case tabFiles:
		return m.filesBody()
	case tabIntegration:
		return m.integrationBody(width)
	case tabEvidence:
		return m.evidenceBody()
	case tabDiff:
		return m.diffBody(width)
	case tabStats:
		return m.statsBody()
	}
	return m.overviewBody()
}

func (m *reviewModel) overviewBody() []string {
	stats := m.snap.Projection.Stats
	lines := []string{uiTitleStyle.Render("Original intent"), "  " + emptyAs(m.snap.Intent, "No intent was recorded.")}
	if m.snap.Plan != nil {
		lines = append(lines,
			uiTitleStyle.Render("Implementation plan"),
			fmt.Sprintf("  %s (%d planned files · %s)", m.snap.Plan.Plan.Summary, len(m.snap.Plan.Plan.Files), m.snap.Plan.Source))
	}
	lines = append(lines,
		uiTitleStyle.Render("Actual change"),
		fmt.Sprintf("  Changed files %d · +%d -%d · reviewability %s",
			stats.Files, stats.Additions, stats.Deletions, strings.ToUpper(string(m.snap.Projection.Reviewability))))
	if m.snap.Plan != nil {
		drift := m.snap.Projection.Drift
		lines = append(lines, uiTitleStyle.Render("Plan vs actual"),
			fmt.Sprintf("  Matched %d   Additional %d   Planned but untouched %d", drift.Matched, drift.Additional, drift.Untouched))
	}
	lines = append(lines, uiTitleStyle.Render("Existing code integrations"))
	lines = append(lines, previewRows(m.integrationSummaries(), 2, "No existing-code relationships are available.")...)
	lines = append(lines, uiTitleStyle.Render("Implementation"))
	lines = append(lines, previewRows(m.changedSummaries(), 2, "No file changes have been observed since the baseline.")...)
	lines = append(lines, uiTitleStyle.Render("Evidence"), "  "+m.evidenceCounts())
	return append(lines, uiTitleStyle.Render("Diff"), uiMutedStyle.Render("  The raw diff stays on the Diff tab."))
}

func (m *reviewModel) integrationSummaries() []string {
	rows := make([]string, 0, len(m.snap.Projection.Integrations))
	for _, item := range m.snap.Projection.Integrations {
		rows = append(rows, "  "+integrationLabel(item)+"  "+uiMutedStyle.Render(string(item.Precision)))
	}
	return rows
}

func (m *reviewModel) changedSummaries() []string {
	rows := make([]string, 0)
	for _, file := range m.diffFiles() {
		rows = append(rows, fmt.Sprintf("  %s  %s", file.Path, uiMutedStyle.Render(string(file.Status))))
	}
	return rows
}

func (m *reviewModel) filesBody() []string {
	drift := m.snap.Projection.Drift
	lines := []string{
		fmt.Sprintf("  ✓ Matched %d       + Additional %d       ! Planned but untouched %d", drift.Matched, drift.Additional, drift.Untouched),
		"",
		fmt.Sprintf("  %-30s %-8s %-8s %s", "File", "Plan", "Actual", "Status"),
		"  " + strings.Repeat("─", 64),
	}
	if m.filter != "" {
		lines = append(lines, uiMutedStyle.Render("  Filter: "+string(m.filter)+" (f cycles)"), "")
	}
	items := m.screen().selectableItems()
	if len(items) == 0 {
		return append(lines, uiEmptyState("", "No file changes match this view. Press r to refresh the actual state."))
	}
	selected, _ := m.selectedRow()
	for _, item := range items {
		file := item.Preview.(review.FileReview)
		row := fmt.Sprintf("%-30s %-8s %-8s %s %s", file.Path,
			emptyDash(string(file.PlannedAction)), emptyDash(string(file.ActualAction)),
			fileStatusMark(file.Status), file.Status)
		if selected.ID == "review.file."+file.Path {
			row = uiSelectedRow("> "+row, 0)
		} else {
			row = "  " + row
		}
		lines = append(lines, row)
	}
	if row, ok := m.selectedRow(); ok {
		lines = append(lines, "", uiTitleStyle.Render("Selected"), "  "+row.Label,
			uiMutedStyle.Render("  "+fileStatusLabel(m.selectedFileStatus())))
		if row.Detail != "" {
			lines = append(lines, "  "+row.Detail)
		}
	}
	return lines
}

func (m *reviewModel) selectedFileStatus() review.FileStatus {
	item, ok := m.screen().selectedItem()
	if !ok {
		return ""
	}
	file, ok := item.Preview.(review.FileReview)
	if !ok {
		return ""
	}
	return file.Status
}

func (m *reviewModel) integrationBody(width int) []string {
	lines := []string{uiTitleStyle.Render("Existing code interaction"), ""}
	items := m.screen().selectableItems()
	if len(items) == 0 {
		return append(lines, uiEmptyState("", "No declared or discovered relationships are available for this change."))
	}
	selected, _ := m.selectedRow()
	for _, screenItem := range items {
		item := screenItem.Preview.(review.Integration)
		label := integrationLabel(item) + "  " + uiMutedStyle.Render(string(item.Precision))
		if item.Path != "" {
			label = uiSplit(label, uiMutedStyle.Render(fmt.Sprintf("%s:%d", item.Path, item.Line)), max(20, width-8))
		}
		if selected.ID == screenItem.ID {
			lines = append(lines, uiSelectedRow("> "+ansi.Strip(label), 0))
		} else {
			lines = append(lines, "  "+label)
		}
		if item.Change != "" {
			lines = append(lines, uiMutedStyle.Render("    └─ "+item.Change))
		}
	}
	return append(append(lines, "", uiTitleStyle.Render("Preview")), m.integrationPreview(width)...)
}

func (m *reviewModel) integrationPreview(width int) []string {
	item, ok := m.selectedIntegration()
	if !ok || item.Path == "" {
		return []string{uiMutedStyle.Render("  A declared relationship has no source location to preview.")}
	}
	data, err := os.ReadFile(filepath.Join(m.root, filepath.FromSlash(item.Path)))
	if err != nil {
		return []string{uiMutedStyle.Render("  Source is unavailable: " + item.Path)}
	}
	source := strings.Split(string(data), "\n")
	start := max(0, item.Line-3)
	end := min(len(source), start+5)
	rows := make([]string, 0, end-start)
	for index := start; index < end; index++ {
		rows = append(rows, uiCodeLine(index+1, index+1 == item.Line, source[index], max(20, width-8)))
	}
	return rows
}

func (m *reviewModel) evidenceBody() []string {
	if len(m.snap.Evidence.Items) == 0 {
		return []string{uiEmptyState("", "No evidence has been recorded. Press t to run the configured verification.")}
	}
	selected, _ := m.selectedRow()
	var lines []string
	for _, section := range m.screen().Sections {
		lines = append(lines, uiTitleStyle.Render(section.Title))
		for _, screenItem := range section.Items {
			item := screenItem.Preview.(evidence.Item)
			row := fmt.Sprintf("%s %s", evidenceMark(item), item.Name)
			if selected.ID == screenItem.ID {
				lines = append(lines, uiSelectedRow("> "+row, 0))
			} else {
				lines = append(lines, "  "+row)
			}
			lines = append(lines, evidenceDetails(item)...)
		}
	}
	return append(lines, "", uiTitleStyle.Render("Evidence"), "  "+m.evidenceCounts())
}

func evidenceDetails(item evidence.Item) []string {
	var lines []string
	if item.Category == evidence.CategoryFailThenPass {
		baseline, modified := "", "NO"
		digests := map[string]bool{}
		for _, observation := range item.Observations {
			if observation.Status == "failed" && observation.BaselineSHA != "" {
				baseline = shortSHA(observation.BaselineSHA)
			}
			if observation.SourceDigest != "" {
				digests[observation.SourceDigest] = true
			}
		}
		if len(digests) > 1 {
			modified = "YES"
		}
		lines = append(lines,
			uiMutedStyle.Render("    failed against baseline "+emptyAs(baseline, "unknown")),
			uiEvidenceStyle.Render("    ✓ now passes"),
			uiMutedStyle.Render("    test modified after baseline: "+modified))
	}
	if item.Category == evidence.CategoryManual {
		lines = append(lines, uiMutedStyle.Render("    manual claim · "+emptyAs(item.Command, "no command recorded")))
	}
	if !item.Fresh {
		lines = append(lines, uiMutedStyle.Render("    stale: recorded against a different worktree"))
	}
	if item.Reason != "" {
		lines = append(lines, uiMutedStyle.Render("    "+item.Reason))
	}
	return lines
}

func (m *reviewModel) evidenceCounts() string {
	summary := m.snap.Evidence.Summary
	return fmt.Sprintf("Existing tests %d · Fail → pass %d · New tests %d · Modified existing %d · Manual %d",
		summary.Existing, summary.FailThenPass, summary.NewTests, summary.ModifiedExisting, summary.Manual)
}

func (m *reviewModel) diffBody(width int) []string {
	items := m.screen().selectableItems()
	if len(items) == 0 {
		return []string{uiEmptyState("", "No diff is available. Press r to refresh the actual state.")}
	}
	current := clamp(m.cursors[tabDiff], 0, len(items)-1)
	file := items[current].Preview.(review.FileReview)
	lines := []string{uiSplit(
		uiTitleStyle.Render("Diff · "+file.Path),
		uiMutedStyle.Render(fmt.Sprintf("%d / %d files", current+1, len(items))),
		max(20, width-8)), ""}

	names := make([]string, 0, len(items))
	for index, item := range items {
		entry := item.Preview.(review.FileReview)
		row := fmt.Sprintf("%s %s", fileStatusMark(entry.Status), entry.Path)
		if index == current {
			row = uiSelectedRow("> "+row, 0)
		} else {
			row = "  " + row
		}
		names = append(names, row)
	}

	paneWidth := max(24, width/2-6)
	lines = append(lines, uiColumns(strings.Join(names, "\n"), max(20, width/3), strings.Join(m.hunkLines(file, paneWidth), "\n")))
	return append(append(lines, ""), m.diffAnnotations(file)...)
}

func (m *reviewModel) hunkLines(file review.FileReview, width int) []string {
	if file.Change != nil && file.Change.Binary {
		return []string{uiMutedStyle.Render("Binary file — no textual diff is shown.")}
	}
	hunks := m.snap.Hunks[file.Path]
	if len(hunks) == 0 {
		return []string{uiMutedStyle.Render("No hunks were recorded for this file.")}
	}
	hunk := hunks[clamp(m.hunk, 0, len(hunks)-1)]
	lines := []string{uiMutedStyle.Render(fmt.Sprintf("%s  hunk %d/%d", hunk.Hunk.Header, clamp(m.hunk, 0, len(hunks)-1)+1, len(hunks)))}
	for _, line := range hunk.Hunk.Lines {
		marker, style := " ", uiMutedStyle
		number := line.NewLine
		switch line.Kind {
		case gitutil.DiffAddition:
			marker, style = "+", uiEvidenceStyle
		case gitutil.DiffDeletion:
			marker, style, number = "-", uiLineStyle, line.OldLine
		}
		text := fmt.Sprintf("%4d %s %s", number, marker, line.Text)
		lines = append(lines, style.Render(ansi.Truncate(text, max(10, width), "…")))
	}
	return lines
}

func (m *reviewModel) diffAnnotations(file review.FileReview) []string {
	symbol := ""
	if hunks := m.snap.Hunks[file.Path]; len(hunks) > 0 {
		symbol = hunks[clamp(m.hunk, 0, len(hunks)-1)].Symbol
	}
	planned := "no"
	if file.PlannedAction != "" {
		planned = "yes"
	}
	integrated := "no"
	if m.integrationTouches(file.Path, symbol) {
		integrated = "yes"
	}
	return []string{
		"  Existing symbol: " + emptyAs(symbol, "not attributed"),
		"  Planned: " + planned,
		"  Integration changed: " + integrated,
	}
}

func (m *reviewModel) integrationTouches(path, symbol string) bool {
	for _, item := range m.snap.Projection.Integrations {
		if item.Path == path || (symbol != "" && (item.Symbol == symbol || item.Parent == symbol)) {
			return true
		}
	}
	return false
}

func (m *reviewModel) statsBody() []string {
	stats := m.snap.Projection.Stats
	tests := 0
	for _, item := range m.snap.Evidence.Items {
		if item.Automated {
			tests++
		}
	}
	lines := []string{
		fmt.Sprintf("  %-14s %-14s %-14s %s", "Files", "Lines", "Tests", "Reviewability"),
		fmt.Sprintf("  %-14d %-14s %-14d %s", stats.Files,
			fmt.Sprintf("+%d -%d", stats.Additions, stats.Deletions), tests,
			strings.ToUpper(string(m.snap.Projection.Reviewability))),
		uiMutedStyle.Render("  Thresholds: GOOD ≤ 6 files / 300 changed lines · MODERATE ≤ 12 / 800 · otherwise LOW"),
		uiTitleStyle.Render("What changed"),
	}
	changed := m.changedSummaries()
	if len(changed) == 0 {
		lines = append(lines, uiMutedStyle.Render("  No file changes have been observed since the baseline."))
	} else {
		for _, summary := range changed {
			lines = append(lines, "  • "+strings.TrimSpace(ansi.Strip(summary)))
		}
	}
	lines = append(lines, uiTitleStyle.Render("Review attention"))
	attention := m.reviewAttention()
	if len(attention) == 0 {
		lines = append(lines, uiMutedStyle.Render("  Nothing stands out from the recorded facts."))
	}
	lines = append(lines, attention...)
	lines = append(lines, uiTitleStyle.Render("Evidence"), "  "+m.evidenceCounts())

	selected, _ := m.selectedRow()
	actions := make([]string, 0, len(m.screen().selectableItems()))
	for _, item := range m.screen().selectableItems() {
		action := "[ " + item.Label + " ]"
		if selected.ID == item.ID {
			action = uiSelectedRow("> "+action, 0)
		} else {
			action = "  " + action
		}
		actions = append(actions, action)
	}
	lines = append(lines, "", "               "+strings.Join(actions, "   "))
	if m.decision != decisionNone {
		lines = append(lines, uiMutedStyle.Render("  Recorded decision: "+string(m.decision)))
	}
	return lines
}

func (m *reviewModel) reviewAttention() []string {
	drift := m.snap.Projection.Drift
	var lines []string
	if m.snap.Plan == nil {
		lines = append(lines, "  ! No implementation plan was submitted; the plan is optional.")
	}
	if unreviewed := m.unreviewedCriteria(); unreviewed > 0 {
		noun := "criterion has"
		if unreviewed > 1 {
			noun = "criteria have"
		}
		lines = append(lines, fmt.Sprintf("  ! %d acceptance %s not been reviewed", unreviewed, noun))
	}
	if len(m.snap.Evidence.Items) == 0 {
		lines = append(lines, "  ! No evidence has been recorded for this change.")
	}
	for _, item := range m.snap.Evidence.Items {
		if item.Automated && !item.Passing {
			lines = append(lines, "  ! "+item.Name+" is not passing")
		}
	}
	if drift.Additional > 0 {
		lines = append(lines, fmt.Sprintf("  ! %d files changed outside the original plan", drift.Additional))
	}
	if drift.Untouched > 0 {
		lines = append(lines, fmt.Sprintf("  ! %d planned files remain unchanged", drift.Untouched))
	}
	for _, item := range m.snap.Evidence.Items {
		if !item.Fresh {
			lines = append(lines, "  ! "+item.Name+" was recorded against a different worktree")
		}
	}
	if m.snap.Projection.Reviewability != review.ReviewabilityGood {
		lines = append(lines, "  ! review size is "+string(m.snap.Projection.Reviewability))
	}
	return lines
}

func (m *reviewModel) unreviewedCriteria() int {
	unreviewed := 0
	for _, criterion := range m.snap.Criteria {
		if !criterion.Checked {
			unreviewed++
		}
	}
	return unreviewed
}

var findReviewContext = discovery.Find

func loadReviewSnapshot(root string) (reviewSnapshot, error) {
	workspace, err := state.Load(root)
	if err != nil {
		return reviewSnapshot{}, err
	}
	if !workspace.Active {
		return reviewSnapshot{}, fmt.Errorf("no active Spec")
	}
	snapshot := reviewSnapshot{
		SpecID: workspace.SpecID, Title: workspace.Title, Baseline: workspace.BaseSHA,
		Hunks: map[string][]review.HunkReview{}, RefreshedAt: time.Now().UTC(),
	}
	if markdown, err := os.ReadFile(change.ActivePath(root)); err == nil {
		if setup, err := change.SetupFromMarkdown(string(markdown)); err == nil {
			snapshot.Intent, snapshot.Scope = setup.Title, setup.Outcome
		}
		snapshot.Criteria = change.AcceptanceCriteria(string(markdown))
	}
	if snapshot.Plan, err = workspace.Plan(); err != nil {
		return snapshot, err
	}
	changes, err := gitutil.Changes(root, workspace.BaseSHA)
	if err != nil {
		return snapshot, err
	}
	// Discovery is advisory: a failure leaves the actual-change review usable.
	discovered, _ := findReviewContext(root, discovery.Query{Intent: snapshot.Intent, Outcome: snapshot.Scope})
	snapshot.Projection = review.Project(snapshot.Plan, changes, discovered)
	symbols := symbolsByPath(discovered)
	for _, changed := range changes {
		hunks, err := gitutil.Hunks(root, workspace.BaseSHA, changed)
		if err != nil {
			return snapshot, err
		}
		snapshot.Hunks[changed.Path] = review.AssociateHunks(hunks, symbols[changed.Path])
	}
	runs, err := workspace.EvidenceRuns()
	if err != nil {
		return snapshot, err
	}
	snapshot.Evidence = evidence.Classify(runs, "")
	return snapshot, nil
}

func symbolsByPath(results []discovery.Result) map[string][]discovery.Symbol {
	symbols := make(map[string][]discovery.Symbol, len(results))
	for _, result := range results {
		symbols[result.Path] = append(symbols[result.Path], result.Symbols...)
	}
	return symbols
}

func completeReviewedSpec(root string, output io.Writer) error {
	record, err := change.Done(root, "", time.Now())
	if err != nil {
		return err
	}
	fmt.Fprintf(output, "Completed %s with %d changed file(s). Archived: %s\n",
		emptyDash(record.SpecID), record.Stats.Files, record.SpecArchive)
	return nil
}

func recordReviewEvent(root string, event state.TimelineEvent) error {
	workspace, err := state.Load(root)
	if err != nil {
		return err
	}
	return workspace.AppendTimeline(event)
}

func evidenceItemsOf(items []evidence.Item, category evidence.Category) []evidence.Item {
	matched := make([]evidence.Item, 0, len(items))
	for _, item := range items {
		if item.Category == category {
			matched = append(matched, item)
		}
	}
	return matched
}

func evidenceMark(item evidence.Item) string {
	if item.Passing {
		return uiEvidenceStyle.Render("✓")
	}
	if item.Status == "failed" {
		return uiLineStyle.Render("✗")
	}
	return uiMutedStyle.Render("•")
}

func integrationLabel(item review.Integration) string {
	label := item.Symbol
	if item.Parent != "" {
		label = item.Parent + " → " + item.Relationship + " " + item.Symbol
	} else if item.Relationship != "" {
		label += " · " + item.Relationship
	}
	return label
}

func fileStatusLabel(status review.FileStatus) string {
	switch status {
	case review.StatusMatched:
		return "✓ matched · planned and changed"
	case review.StatusAdditional:
		return "+ additional · changed but not planned"
	case review.StatusUntouched:
		return "! untouched · planned but unchanged"
	}
	return string(status)
}

func fileStatusMark(status review.FileStatus) string {
	switch status {
	case review.StatusMatched:
		return "✓"
	case review.StatusAdditional:
		return "+"
	}
	return "!"
}

func previewRows(rows []string, limit int, empty string) []string {
	if len(rows) == 0 {
		return []string{uiMutedStyle.Render("  " + empty)}
	}
	if len(rows) > limit {
		remaining := len(rows) - limit
		return append(rows[:limit], uiMutedStyle.Render(fmt.Sprintf("  … %d more", remaining)))
	}
	return rows
}

func emptyDash(value string) string {
	if strings.TrimSpace(value) == "" {
		return "—"
	}
	return value
}

func shortSHA(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}
