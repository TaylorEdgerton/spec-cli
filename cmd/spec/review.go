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
	"charm.land/lipgloss/v2"
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
	reviewMinWidth   = 80
	reviewMinHeight  = 16
	reviewSplitWidth = 92 // 120-column shell minus the persistent 24-column rail.
)

type reviewTab int

const (
	tabSummary reviewTab = iota
	tabChanges
	tabIntegration
	tabEvidence
	tabDiff
)

var reviewTabLabels = []string{"Summary", "Changes", "Integration", "Evidence", "Diff"}

type reviewDecision string

const (
	decisionNone    reviewDecision = ""
	decisionChanges reviewDecision = "request_changes"
)

type reviewRow struct{ ID, Label, Detail string }

type attentionKind string

const (
	attentionNeutral attentionKind = "neutral"
	attentionReview  attentionKind = "review"
)

type reviewAttentionItem struct {
	ID, Label string
	Kind      attentionKind
	Target    reviewTab
}

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
	diffFocused   bool
	diffScroll    int
	filter        review.FileStatus
	status        string
	decision      reviewDecision
	width, height int
	done          bool
	help          bool
	nav           string
	viewport      int
	listViewports map[reviewTab]int

	refresh  func(string) (reviewSnapshot, error)
	record   func(string, state.TimelineEvent) error
	open     func(string, discovery.Result) error
	runTests func(string) error
}

func newReviewModel(root string, snap reviewSnapshot) *reviewModel {
	return &reviewModel{
		root: root, snap: snap, cursors: map[reviewTab]int{}, listViewports: map[reviewTab]int{},
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
	case tabChanges:
		files := m.filteredFiles()
		for _, group := range []struct {
			id, title string
			status    review.FileStatus
		}{
			{"matched", "Matched", review.StatusMatched},
			{"additional", "Additional", review.StatusAdditional},
			{"untouched", "Planned but untouched", review.StatusUntouched},
		} {
			groupItems := make([]screenItem, 0)
			for _, file := range files {
				if file.Status == group.status {
					groupItems = append(groupItems, screenItem{ID: "review.file." + file.Path, Label: file.Path, Detail: file.Reason, Selectable: true, Preview: file})
				}
			}
			if len(groupItems) > 0 {
				sections = append(sections, screenSection{ID: "review.changes." + group.id, Title: fmt.Sprintf("%s (%d)", group.title, len(groupItems)), Items: groupItems})
			}
		}
	case tabIntegration:
		for index, item := range m.snap.Projection.Integrations {
			items = append(items, screenItem{ID: fmt.Sprintf("review.integration.%d", index), Label: item.Symbol, Detail: item.Relationship, Selectable: true, Preview: item})
		}
	case tabEvidence:
		for _, category := range []struct{ id, title string }{
			{"before", "Before implementation"},
			{"reproduced", "Behaviour reproduced before change"},
			{"added", "Added during implementation"},
			{"attention", "Needs attention"},
			{"manual", "Manual"},
		} {
			categoryItems := make([]screenItem, 0)
			for _, item := range m.snap.Evidence.Items {
				if evidenceSection(item) == category.id {
					categoryItems = append(categoryItems, screenItem{ID: "review.evidence." + item.ID, Label: item.Name, Detail: string(item.Category), Selectable: true, Preview: item})
				}
			}
			sections = append(sections, screenSection{ID: "review.evidence." + category.id, Title: category.title, Items: categoryItems})
		}
	case tabDiff:
		for _, file := range m.diffFiles() {
			items = append(items, screenItem{ID: "review.diff." + file.Path, Label: file.Path, Selectable: true, Preview: file})
		}
	case tabSummary:
		for _, attention := range projectReviewAttention(m.snap) {
			items = append(items, screenItem{ID: "review.attention." + attention.ID, Label: attention.Label, Selectable: true, Preview: attention})
		}
		items = append(items,
			screenItem{ID: "review.complete", Label: "Complete Spec", Selectable: true, Action: screenAction(actionCompletion)},
			screenItem{ID: "review.request_changes", Label: "Request Changes", Selectable: true, Action: screenAction(actionChanges)},
		)
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
		if m.diffFocused {
			m.scrollDiff(-1)
		} else {
			m.move(-1)
		}
	case "down", "j":
		if m.diffFocused {
			m.scrollDiff(1)
		} else {
			m.move(1)
		}
	case "pgup":
		if m.diffFocused {
			m.scrollDiff(-max(1, m.focusedCodeCapacity()-1))
		} else if m.tab == tabChanges || m.tab == tabDiff {
			m.movePage(-1)
		} else {
			m.viewport = max(0, m.viewport-max(1, m.height/2))
		}
	case "pgdown":
		if m.diffFocused {
			m.scrollDiff(max(1, m.focusedCodeCapacity()-1))
		} else if m.tab == tabChanges || m.tab == tabDiff {
			m.movePage(1)
		} else {
			m.viewport += max(1, m.height/2)
		}
	case "n":
		m.moveHunk(1)
	case "p":
		m.moveHunk(-1)
	case "f":
		m.cycleFilter()
	case "d":
		m.showDiff()
	case "i":
		m.showIntegration()
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
		if m.diffFocused {
			m.diffFocused, m.diffScroll, m.status = false, 0, ""
			return nil
		}
		m.leave(actionBack)
		return tea.Quit
	case "ctrl+c":
		m.leave(actionQuit)
		return tea.Quit
	}
	return nil
}

func (m *reviewModel) leave(action string) { m.done, m.nav = true, action }

func (m *reviewModel) selectTab(tab reviewTab) {
	m.tab, m.hunk, m.status, m.viewport = tab, 0, "", 0
	m.diffFocused, m.diffScroll = false, 0
}

func (m *reviewModel) move(delta int) {
	count := len(m.screen().selectableItems())
	if count == 0 {
		return
	}
	m.cursors[m.tab] = wrap(m.cursors[m.tab]+delta, count)
	m.hunk, m.diffScroll, m.status = 0, 0, ""
}

func (m *reviewModel) movePage(direction int) {
	count := len(m.screen().selectableItems())
	if count == 0 {
		return
	}
	step := max(2, m.fileListCapacity()-1)
	m.cursors[m.tab] = clamp(m.cursors[m.tab]+direction*step, 0, count-1)
	m.listViewports[m.tab] = max(0, m.listViewports[m.tab]+direction*step)
	m.hunk, m.diffScroll, m.status = 0, 0, ""
}

func (m *reviewModel) moveHunk(delta int) {
	count := len(m.snap.Hunks[m.diffFile()])
	if m.tab != tabDiff || count == 0 {
		return
	}
	m.hunk = wrap(m.hunk+delta, count)
	m.diffScroll = 0
}

func (m *reviewModel) scrollDiff(delta int) {
	file, ok := m.selectedDiffFile()
	if !ok {
		return
	}
	capacity := m.focusedCodeCapacity()
	maximum := max(0, len(m.hunkCodeLines(file, max(20, m.width-8)))-capacity)
	m.diffScroll = clamp(m.diffScroll+delta, 0, maximum)
}

func (m *reviewModel) cycleFilter() {
	if m.tab != tabChanges {
		return
	}
	order := []review.FileStatus{"", review.StatusMatched, review.StatusAdditional, review.StatusUntouched}
	for index, status := range order {
		if status == m.filter {
			m.filter = order[(index+1)%len(order)]
			break
		}
	}
	m.cursors[tabChanges], m.listViewports[tabChanges], m.status = 0, 0, ""
}

func (m *reviewModel) showDiff() {
	path, contextual := "", false
	if m.tab == tabChanges {
		contextual = true
		if row, ok := m.selectedRow(); ok {
			path = strings.TrimPrefix(row.ID, "review.file.")
		}
	} else if m.tab == tabIntegration {
		contextual = true
		if item, ok := m.selectedIntegration(); ok && item.Path != "" {
			path = item.Path
		}
	}
	if contextual && path == "" {
		m.status = "This relationship has no changed file with an actual diff."
		return
	}
	if contextual && !m.selectDiffFile(path) {
		m.status = "No actual diff is available for " + path + "."
		return
	}
	m.selectTab(tabDiff)
}

func (m *reviewModel) showIntegration() {
	if m.tab != tabDiff {
		m.selectTab(tabIntegration)
		return
	}
	path := m.diffFile()
	if path != "" {
		for index, item := range m.snap.Projection.Integrations {
			if item.Path == path {
				m.cursors[tabIntegration] = index
				m.selectTab(tabIntegration)
				return
			}
		}
	}
	m.status = "No integration relationship is attributed to " + emptyAs(path, "this file") + "."
}

func (m *reviewModel) selectDiffFile(path string) bool {
	for index, file := range m.diffFiles() {
		if file.Path == path {
			m.cursors[tabDiff] = index
			return true
		}
	}
	return false
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
	m.snap, m.hunk, m.diffScroll, m.diffFocused = snapshot, 0, 0, false
	m.status = "Refreshed actual state."
	m.recordEvent(state.TimelineActualRefreshed, "actual state refreshed")
}

func (m *reviewModel) activate() tea.Cmd {
	selected, selectedOK := m.screen().selectedItem()
	if selectedOK {
		if attention, ok := selected.Preview.(reviewAttentionItem); ok {
			m.openAttention(attention)
			return nil
		}
	}
	row, ok := m.selectedRow()
	if !ok {
		return nil
	}
	switch {
	case row.ID == "review.complete":
		m.status = ""
		m.leave(actionCompletion)
		return tea.Quit
	case row.ID == "review.request_changes":
		m.decision = decisionChanges
		m.status = "Changes requested; the plan, evidence, and review facts are kept."
		m.recordEvent(state.TimelineChangesRequested, "human requested more changes")
		m.leave(actionChanges)
		return tea.Quit
	case m.tab == tabChanges:
		m.showDiff()
	case m.tab == tabIntegration:
		m.openSelected()
	case m.tab == tabDiff:
		m.diffFocused, m.diffScroll, m.status = true, 0, ""
	}
	return nil
}

func (m *reviewModel) openAttention(item reviewAttentionItem) {
	switch item.ID {
	case "additional":
		m.filter = review.StatusAdditional
	case "untouched":
		m.filter = review.StatusUntouched
	}
	m.selectTab(item.Target)
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

func (m *reviewModel) selectedDiffFile() (review.FileReview, bool) {
	if m.tab != tabDiff {
		return review.FileReview{}, false
	}
	item, ok := m.screen().selectedItem()
	if !ok {
		return review.FileReview{}, false
	}
	file, ok := item.Preview.(review.FileReview)
	return file, ok
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
	case tabChanges:
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
	body = append(body, uiTabs(reviewTabLabels, int(m.tab)))
	if height > 24 {
		body = append(body, "")
	}
	if m.help {
		body = append(body, uiHelpOverlayWidth(m.hints(), max(20, width-8)))
	} else {
		body = append(body, m.tabBody(width)...)
	}
	if m.status != "" {
		body = append(body, uiMutedStyle.Render(m.status))
	}
	header := uiSplit(
		fmt.Sprintf("Review · %s", reviewTabLabels[m.tab]),
		fmt.Sprintf("%s · %s", m.snap.SpecID, shortSHA(m.snap.Baseline)),
		max(1, width-4),
	)
	rendered := strings.Join(body, "\n")
	anchor := ""
	if item, ok := m.screen().selectedItem(); ok && m.tab != tabChanges && m.tab != tabDiff {
		anchor = item.Label
	}
	rendered, m.viewport = uiViewportBody(rendered, uiWorkflowBodyHeight(height, header), m.viewport, anchor)
	return tea.NewView(uiAppShell(width, height, header, rendered, m.footer()))
}

func (m *reviewModel) footer() string { return uiKeyHints(m.hints(), "  ") }

func (m *reviewModel) hints() [][2]string {
	hints := [][2]string{{"tab", "view"}, {"↑/↓", "select"}}
	if (m.tab == tabChanges || m.tab == tabDiff) && !m.diffFocused {
		hints = append(hints, [2]string{"PgUp/PgDn", "page"})
	}
	if m.tab != tabDiff {
		hints = append(hints, [2]string{"enter", "action"})
	}
	switch m.tab {
	case tabChanges:
		hints = append(hints, [2]string{"f", "filter"}, [2]string{"d", "diff"})
	case tabIntegration:
		hints = append(hints, [2]string{"o", "VS Code"}, [2]string{"d", "diff"})
	case tabEvidence:
		hints = append(hints, [2]string{"t", "run tests"}, [2]string{"d", "test diff"}, [2]string{"s", "summary"})
	case tabDiff:
		if m.diffFocused {
			hints = [][2]string{{"↑/↓", "scroll code"}, {"n/p", "hunk"}, {"esc", "file list"}, {"o", "VS Code"}, {"i", "integration"}}
			if m.width < reviewSplitWidth {
				hints = [][2]string{{"↑/↓", "scroll code"}, {"n/p", "hunk"}, {"esc", "file list"}, {"o", "VS Code"}}
			}
		} else {
			hints = append(hints, [2]string{"enter", "focus hunk"}, [2]string{"n/p", "hunk"}, [2]string{"o", "VS Code"}, [2]string{"i", "integration"})
		}
	case tabSummary:
		hints = append(hints, [2]string{"d", "diff"}, [2]string{"i", "integration"}, [2]string{"e", "evidence"})
	}
	if !m.diffFocused || m.width >= reviewSplitWidth {
		hints = append(hints, [2]string{"r", "refresh"})
	}
	hints = append(hints, [2]string{"?", "help"})
	if !m.diffFocused {
		hints = append(hints, [2]string{"b", "back"}, [2]string{"g", "home"})
	}
	return hints
}

func (m *reviewModel) tabBody(width int) []string {
	switch m.tab {
	case tabChanges:
		return m.filesBody(width)
	case tabIntegration:
		return m.integrationBody(width)
	case tabEvidence:
		return m.evidenceBody()
	case tabDiff:
		if m.diffFocused {
			return m.focusedDiffBody(width)
		}
		return m.diffBody(width)
	case tabSummary:
		return m.summaryBody(width)
	}
	return nil
}

func (m *reviewModel) filesBody(width int) []string {
	items := m.screen().selectableItems()
	if len(items) == 0 {
		return []string{uiEmptyState("", "No file changes match this view. Press r to refresh the actual state.")}
	}
	contentWidth := max(20, width-4)
	selected := items[clamp(m.cursors[tabChanges], 0, len(items)-1)].Preview.(review.FileReview)
	filter := ""
	if m.filter != "" {
		filter = uiMutedStyle.Render("Filter: "+string(m.filter)+" (f cycles)") + "\n"
	}
	if width >= reviewSplitWidth {
		leftWidth := max(36, contentWidth*2/5)
		rightWidth := max(24, contentWidth-leftWidth)
		leftBody, listRange := m.changeList(leftWidth-4, false)
		leftBody = filter + leftBody
		rightBody := m.changeDetail(selected, rightWidth-4, false)
		height := max(lipgloss.Height(leftBody), lipgloss.Height(rightBody)) + 2
		body := lipgloss.JoinHorizontal(lipgloss.Top,
			uiPanel(leftWidth, height, uiBorder, "Files", listRange, leftBody),
			uiPanel(rightWidth, height, uiYellow, "Change detail", "", rightBody),
		)
		return strings.Split(body, "\n")
	}
	listBody, listRange := m.changeList(contentWidth-4, true)
	listBody = filter + listBody
	detailBody := m.changeDetail(selected, contentWidth-4, true)
	body := lipgloss.JoinVertical(lipgloss.Left,
		uiPanel(contentWidth, lipgloss.Height(listBody)+2, uiBorder, "Files", listRange, listBody),
		uiPanel(contentWidth, lipgloss.Height(detailBody)+2, uiYellow, "Selected change", "", detailBody),
	)
	return strings.Split(body, "\n")
}

func (m *reviewModel) changeList(width int, compact bool) (string, string) {
	selectedID := ""
	if item, ok := m.screen().selectedItem(); ok {
		selectedID = item.ID
	}
	items := m.screen().selectableItems()
	selectedIndex := clamp(m.cursors[tabChanges], 0, len(items)-1)
	if compact {
		matched, additional, untouched := 0, 0, 0
		for _, file := range m.snap.Projection.Files {
			switch file.Status {
			case review.StatusMatched:
				matched++
			case review.StatusAdditional:
				additional++
			case review.StatusUntouched:
				untouched++
			}
		}
		file := items[selectedIndex].Preview.(review.FileReview)
		row := uiSelectedRow("> "+fileStatusMark(file.Status)+" "+ansi.Truncate(file.Path, max(8, width-4), "…"), 0)
		body := fmt.Sprintf("Matched %d · Additional %d · Planned but untouched %d\n%s", matched, additional, untouched, row)
		m.listViewports[tabChanges] = selectedIndex
		return body, uiRangeLabel(selectedIndex, selectedIndex, len(items), "files")
	}
	var lines []string
	var itemLines []int
	itemIndex := 0
	for _, section := range m.screen().Sections {
		lines = append(lines, uiTitleStyle.Render(section.Title))
		itemLines = append(itemLines, -1)
		for _, item := range section.Items {
			file := item.Preview.(review.FileReview)
			row := fileStatusMark(file.Status) + " " + ansi.Truncate(file.Path, max(8, width-4), "…")
			if item.ID == selectedID {
				row = uiSelectedRow("> "+row, 0)
			} else {
				row = "  " + row
			}
			lines = append(lines, row)
			itemLines = append(itemLines, itemIndex)
			itemIndex++
		}
	}
	selectedLine := selectedItemLine(itemLines, selectedIndex)
	visible, offset := (screenViewport{Height: m.fileListCapacity(), Offset: m.listViewports[tabChanges]}).visible(lines, selectedLine)
	m.listViewports[tabChanges] = offset
	first, last := visibleItemRange(itemLines, offset, len(visible))
	return strings.Join(visible, "\n"), uiRangeLabel(first, last, len(items), "files")
}

func (m *reviewModel) changeDetail(file review.FileReview, width int, compact bool) string {
	lines := strings.Split(uiProse(file.Path, width), "\n")
	lineStats := "—"
	if file.Change != nil {
		lineStats = fmt.Sprintf("+%d -%d", file.Change.Additions, file.Change.Deletions)
	}
	if compact {
		lines = append(lines, fmt.Sprintf("Plan %s · Actual %s · Lines %s · %s",
			emptyDash(string(file.PlannedAction)), emptyDash(string(file.ActualAction)), lineStats, file.Status))
		if file.Reason != "" {
			lines = append(lines, uiLabelledProse("Reason", file.Reason, width))
		}
		symbols := m.changedSymbols(file.Path)
		symbolText := "not attributed"
		if len(symbols) > 0 {
			symbolText = strings.Join(symbols, ", ")
		}
		lines = append(lines, uiLabelledProse("Changed symbols", symbolText, width), "[ View diff ]   [ Open VS Code ]")
		return strings.Join(lines, "\n")
	}
	lines = append(lines,
		fmt.Sprintf("Planned  %s", emptyDash(string(file.PlannedAction))),
		fmt.Sprintf("Actual   %s", emptyDash(string(file.ActualAction))),
	)
	lines = append(lines, "Lines    "+lineStats)
	lines = append(lines, uiMutedStyle.Render(fileStatusLabel(file.Status)))
	if file.Reason != "" {
		lines = append(lines, uiTitleStyle.Render("Reason"), uiProse(file.Reason, width))
	}
	symbols := m.changedSymbols(file.Path)
	lines = append(lines, uiTitleStyle.Render("Changed symbols"))
	if len(symbols) == 0 {
		lines = append(lines, uiMutedStyle.Render("No symbol attribution is available."))
	} else {
		for _, symbol := range symbols {
			lines = append(lines, "  "+symbol)
		}
	}
	lines = append(lines, "")
	lines = append(lines, "[ View diff ]   [ Open VS Code ]")
	return strings.Join(lines, "\n")
}

func (m *reviewModel) changedSymbols(path string) []string {
	seen := map[string]bool{}
	var symbols []string
	for _, hunk := range m.snap.Hunks[path] {
		if symbol := strings.TrimSpace(hunk.Symbol); symbol != "" && !seen[symbol] {
			seen[symbol] = true
			symbols = append(symbols, symbol)
		}
	}
	return symbols
}

func (m *reviewModel) integrationBody(width int) []string {
	items := m.screen().selectableItems()
	if len(items) == 0 {
		return []string{uiEmptyState("", "No declared or discovered relationships are available for this change.")}
	}
	contentWidth := max(20, width-4)
	selected, _ := m.selectedIntegration()
	if width >= reviewSplitWidth {
		leftWidth := max(38, contentWidth*2/5)
		rightWidth := max(24, contentWidth-leftWidth)
		leftBody := m.integrationList(leftWidth-4, false)
		rightBody := m.integrationDetail(selected, rightWidth-4, false)
		height := max(lipgloss.Height(leftBody), lipgloss.Height(rightBody)) + 2
		body := lipgloss.JoinHorizontal(lipgloss.Top,
			uiPanel(leftWidth, height, uiBorder, "Existing-code boundaries", "", leftBody),
			uiPanel(rightWidth, height, uiYellow, "Code / relationship detail", integrationProvenance(selected), rightBody),
		)
		return strings.Split(body, "\n")
	}
	listBody := m.integrationList(contentWidth-4, true)
	detailBody := m.integrationDetail(selected, contentWidth-4, true)
	body := lipgloss.JoinVertical(lipgloss.Left,
		uiPanel(contentWidth, lipgloss.Height(listBody)+2, uiBorder, "Existing-code boundaries", "", listBody),
		uiPanel(contentWidth, lipgloss.Height(detailBody)+2, uiYellow, "Code / relationship detail", integrationProvenance(selected), detailBody),
	)
	return strings.Split(body, "\n")
}

func (m *reviewModel) integrationList(width int, compact bool) string {
	selectedID := ""
	if item, ok := m.screen().selectedItem(); ok {
		selectedID = item.ID
	}
	var lines []string
	for _, screenItem := range m.screen().selectableItems() {
		if compact && screenItem.ID != selectedID {
			continue
		}
		item := screenItem.Preview.(review.Integration)
		row := ansi.Truncate(integrationLabel(item), max(8, width-4), "…")
		if screenItem.ID == selectedID {
			row = uiSelectedRow("> "+row, 0)
		} else {
			row = "  " + row
		}
		lines = append(lines, row, uiMutedStyle.Render("    "+integrationProvenance(item)))
		if item.Path != "" && !compact {
			lines = append(lines, uiMutedStyle.Render(fmt.Sprintf("    %s:%d", item.Path, item.Line)))
		}
	}
	return strings.Join(lines, "\n")
}

func (m *reviewModel) integrationDetail(item review.Integration, width int, compact bool) string {
	lines := strings.Split(uiProse(integrationLabel(item), width), "\n")
	lines = append(lines, "Provenance  "+integrationProvenance(item))
	if item.Path == "" {
		lines = append(lines, uiMutedStyle.Render("Location    AI-declared; no source location was supplied."))
	} else {
		lines = append(lines, fmt.Sprintf("Location    %s:%d", item.Path, item.Line))
		if item.Change != "" {
			lines = append(lines, "Actual      "+item.Change)
		}
	}
	if !compact {
		lines = append(lines, "")
	}
	lines = append(lines, uiTitleStyle.Render("Code Preview"))
	lines = append(lines, m.integrationPreview(width)...)
	lines = append(lines, "[ View diff ]   [ Open VS Code ]")
	return strings.Join(lines, "\n")
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
		if len(section.Items) == 0 {
			lines = append(lines, uiMutedStyle.Render("  No evidence in this category."))
		}
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
	if item.Status == "parser_error" {
		lines = append(lines, uiMutedStyle.Render("    command-level fallback · structured test provenance unavailable"))
	}
	if item.Category == evidence.CategoryNewTest {
		lines = append(lines, uiMutedStyle.Render("    new test · supporting coverage; not independent proof"))
	}
	if item.Category == evidence.CategoryModifiedExisting {
		lines = append(lines, uiMutedStyle.Render("    modified during implementation; not independent proof"))
	}
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
	if item.Category == evidence.CategoryExisting && item.Status != "parser_error" {
		baseline := "unknown"
		if len(item.Observations) > 0 && item.Observations[0].BaselineSHA != "" {
			baseline = shortSHA(item.Observations[0].BaselineSHA)
		}
		lines = append(lines, uiMutedStyle.Render("    baseline "+baseline+" · "+strings.ToUpper(emptyAs(item.Status, "unknown"))))
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

func evidenceSection(item evidence.Item) string {
	if item.Category == evidence.CategoryManual {
		return "manual"
	}
	if !item.Fresh || item.Status == "parser_error" || item.Category == evidence.CategoryModifiedExisting {
		return "attention"
	}
	switch item.Category {
	case evidence.CategoryFailThenPass:
		return "reproduced"
	case evidence.CategoryNewTest:
		return "added"
	default:
		return "before"
	}
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
	contentWidth := max(20, width-4)
	if width >= reviewSplitWidth {
		leftWidth := max(34, contentWidth*2/5)
		rightWidth := max(24, contentWidth-leftWidth)
		leftBody, listRange := m.diffFileList(items, current, leftWidth-4, false)
		rightBody := m.diffDetail(file, rightWidth-4, false)
		height := max(lipgloss.Height(leftBody), lipgloss.Height(rightBody)) + 2
		body := lipgloss.JoinHorizontal(lipgloss.Top,
			uiPanel(leftWidth, height, uiBorder, "Files", listRange, leftBody),
			uiPanel(rightWidth, height, uiYellow, "Focused hunk", "", rightBody),
		)
		return strings.Split(body, "\n")
	}
	leftBody, listRange := m.diffFileList(items, current, contentWidth-4, true)
	rightBody := m.diffDetail(file, contentWidth-4, true)
	body := lipgloss.JoinVertical(lipgloss.Left,
		uiPanel(contentWidth, lipgloss.Height(leftBody)+2, uiBorder, "Files", listRange, leftBody),
		uiPanel(contentWidth, lipgloss.Height(rightBody)+2, uiYellow, "Focused hunk", "", rightBody),
	)
	return strings.Split(body, "\n")
}

func (m *reviewModel) focusedDiffBody(width int) []string {
	file, ok := m.selectedDiffFile()
	if !ok {
		return []string{uiEmptyState("", "No diff is available. Press Escape to return to files.")}
	}
	contentWidth := max(20, width-4)
	panelHeight := max(8, uiWorkflowBodyHeight(m.height, "Review · Diff")-1)
	innerWidth := max(10, contentWidth-4)
	code := m.hunkCodeLines(file, innerWidth)
	capacity := m.focusedCodeCapacity()
	maximum := max(0, len(code)-capacity)
	m.diffScroll = clamp(m.diffScroll, 0, maximum)
	end := min(len(code), m.diffScroll+capacity)
	visible := code[m.diffScroll:end]
	lines := []string{ansi.Truncate(file.Path, innerWidth, "…")}
	lines = append(lines, m.hunkHeading(file))
	lines = append(lines, visible...)
	lines = append(lines, uiMutedStyle.Render(fmt.Sprintf("Lines %d–%d of %d", min(len(code), m.diffScroll+1), end, len(code))))
	lines = append(lines, strings.Join(m.diffAnnotations(file), "   "))
	return strings.Split(uiPanel(contentWidth, panelHeight, uiPurple, "Focused hunk", "FOCUSED", strings.Join(lines, "\n")), "\n")
}

func (m *reviewModel) focusedCodeCapacity() int {
	panelHeight := max(8, uiWorkflowBodyHeight(m.height, "Review · Diff")-1)
	return max(1, panelHeight-6)
}

func (m *reviewModel) diffFileList(items []screenItem, current, width int, compact bool) (string, string) {
	if compact {
		entry := items[current].Preview.(review.FileReview)
		row := uiSelectedRow("> "+fileStatusMark(entry.Status)+" "+ansi.Truncate(entry.Path, max(8, width-4), "…"), 0)
		m.listViewports[tabDiff] = current
		return row, uiRangeLabel(current, current, len(items), "files")
	}
	var lines []string
	var itemLines []int
	for index, item := range items {
		entry := item.Preview.(review.FileReview)
		row := fileStatusMark(entry.Status) + " " + ansi.Truncate(entry.Path, max(8, width-4), "…")
		if index == current {
			row = uiSelectedRow("> "+row, 0)
		} else {
			row = "  " + row
		}
		lines = append(lines, row)
		itemLines = append(itemLines, index)
	}
	selectedLine := selectedItemLine(itemLines, current)
	visible, offset := (screenViewport{Height: m.fileListCapacity(), Offset: m.listViewports[tabDiff]}).visible(lines, selectedLine)
	m.listViewports[tabDiff] = offset
	first, last := visibleItemRange(itemLines, offset, len(visible))
	rangeLabel := uiRangeLabel(first, last, len(items), "files")
	if len(items) <= m.fileListCapacity() {
		rangeLabel = fmt.Sprintf("%d / %d files", current+1, len(items))
	}
	return strings.Join(visible, "\n"), rangeLabel
}

func (m *reviewModel) fileListCapacity() int {
	_, height := defaultSize(m.width, m.height)
	if m.width < reviewSplitWidth {
		return 1
	}
	return max(5, uiWorkflowBodyHeight(height, "Review")-6)
}

func selectedItemLine(itemLines []int, selected int) int {
	for line, item := range itemLines {
		if item == selected {
			return line
		}
	}
	return -1
}

func (m *reviewModel) diffDetail(file review.FileReview, width int, compact bool) string {
	lines := []string{ansi.Truncate(file.Path, width, "…")}
	preview := m.hunkLines(file, width)
	limit := max(4, m.fileListCapacity()-4)
	if compact {
		limit = 3
	}
	if len(preview) > limit {
		hidden := len(preview) - limit
		preview = append(preview[:limit], uiMutedStyle.Render(fmt.Sprintf("… %d more lines · Enter to focus and scroll", hidden)))
	}
	lines = append(lines, preview...)
	if !compact {
		lines = append(lines, "")
	}
	lines = append(lines, m.diffAnnotations(file)...)
	lines = append(lines, "[ Open VS Code ]   [ Integration ]")
	return strings.Join(lines, "\n")
}

func (m *reviewModel) hunkLines(file review.FileReview, width int) []string {
	if file.Change != nil && file.Change.Binary {
		return []string{uiMutedStyle.Render("Binary file — no textual diff is shown.")}
	}
	hunks := m.snap.Hunks[file.Path]
	if len(hunks) == 0 {
		return []string{uiMutedStyle.Render("No hunks were recorded for this file.")}
	}
	lines := []string{m.hunkHeading(file)}
	return append(lines, m.hunkCodeLines(file, width)...)
}

func (m *reviewModel) hunkHeading(file review.FileReview) string {
	hunks := m.snap.Hunks[file.Path]
	if len(hunks) == 0 {
		return ""
	}
	hunk := hunks[clamp(m.hunk, 0, len(hunks)-1)]
	return uiMutedStyle.Render(fmt.Sprintf("%s  hunk %d/%d", hunk.Hunk.Header, clamp(m.hunk, 0, len(hunks)-1)+1, len(hunks)))
}

func (m *reviewModel) hunkCodeLines(file review.FileReview, width int) []string {
	hunks := m.snap.Hunks[file.Path]
	if len(hunks) == 0 || (file.Change != nil && file.Change.Binary) {
		return nil
	}
	hunk := hunks[clamp(m.hunk, 0, len(hunks)-1)]
	lines := make([]string, 0, len(hunk.Hunk.Lines))
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
		"Symbol               " + emptyAs(symbol, "not attributed"),
		"Planned              " + yesMark(planned),
		"Integration changed  " + yesMark(integrated),
	}
}

func yesMark(value string) string {
	if value == "yes" {
		return "✓"
	}
	return "—"
}

func (m *reviewModel) integrationTouches(path, symbol string) bool {
	for _, item := range m.snap.Projection.Integrations {
		if item.Path == path || (symbol != "" && (item.Symbol == symbol || item.Parent == symbol)) {
			return true
		}
	}
	return false
}

func (m *reviewModel) summaryBody(width int) []string {
	stats := m.snap.Projection.Stats
	compact := m.height > 0 && m.height <= 24
	proseWidth := max(20, width-8)
	tests := 0
	for _, item := range m.snap.Evidence.Items {
		if item.Automated {
			tests++
		}
	}
	lines := []string{uiTitleStyle.Render("Original intent"), uiIndentedProse(emptyAs(m.snap.Intent, "No intent was recorded."), proseWidth, 2)}
	if m.snap.Plan == nil {
		lines = append(lines, uiTitleStyle.Render("Implementation plan"), uiIndentedProse(uiMutedStyle.Render("No implementation plan was submitted; planning is optional."), proseWidth, 2))
	} else {
		plan := fmt.Sprintf("%s (%d planned files · %s)", m.snap.Plan.Plan.Summary, len(m.snap.Plan.Plan.Files), m.snap.Plan.Source)
		lines = append(lines, uiTitleStyle.Render("Implementation plan"), uiIndentedProse(plan, proseWidth, 2))
	}
	if compact {
		lines = append(lines, uiTitleStyle.Render("Actual change"),
			fmt.Sprintf("  Files %d · Lines +%d -%d · Tests %d · Reviewability %s", stats.Files, stats.Additions, stats.Deletions, tests, strings.ToUpper(string(m.snap.Projection.Reviewability))))
	} else {
		lines = append(lines,
			uiTitleStyle.Render("Actual change"),
			fmt.Sprintf("  %-14s %-14s %-14s %s", "Files", "Lines", "Tests", "Reviewability"),
			fmt.Sprintf("  %-14d %-14s %-14d %s", stats.Files,
				fmt.Sprintf("+%d -%d", stats.Additions, stats.Deletions), tests,
				strings.ToUpper(string(m.snap.Projection.Reviewability))))
	}
	if m.snap.Plan != nil {
		drift := m.snap.Projection.Drift
		lines = append(lines, uiTitleStyle.Render("Plan vs actual"),
			fmt.Sprintf("  Matched %d   Additional %d   Planned but untouched %d", drift.Matched, drift.Additional, drift.Untouched))
	}
	lines = append(lines, uiTitleStyle.Render("Review attention"))
	attention := projectReviewAttention(m.snap)
	if len(attention) == 0 {
		lines = append(lines, uiMutedStyle.Render("  Nothing stands out from the recorded facts."))
	} else {
		selected, _ := m.screen().selectedItem()
		visible, hidden := visibleAttention(attention, selected.ID, compact)
		for _, item := range visible {
			mark := "!"
			if item.Kind == attentionNeutral {
				mark = "+"
			}
			row := fmt.Sprintf("%s %s", mark, item.Label)
			if selected.ID == "review.attention."+item.ID {
				lines = append(lines, uiSelectedRow("> "+row, 0))
			} else {
				lines = append(lines, "  "+row)
			}
		}
		if hidden > 0 {
			lines = append(lines, uiMutedStyle.Render(fmt.Sprintf("  … %d more attention items; use ↑/↓ to inspect", hidden)))
		}
	}
	if len(m.snap.Evidence.Items) == 0 {
		lines = append(lines, uiTitleStyle.Render("Evidence")+"  "+uiMutedStyle.Render("No evidence has been recorded. Open Evidence to run verification."))
	} else {
		if compact {
			lines = append(lines, uiTitleStyle.Render("Evidence")+"  "+m.evidenceCounts())
		} else {
			lines = append(lines, uiTitleStyle.Render("Evidence"), "  "+m.evidenceCounts())
		}
	}

	selected, _ := m.screen().selectedItem()
	actions := make([]string, 0, 2)
	for _, item := range m.screen().selectableItems() {
		if item.ID != "review.complete" && item.ID != "review.request_changes" {
			continue
		}
		action := "[ " + item.Label + " ]"
		if selected.ID == item.ID {
			action = uiSelectedRow("> "+action, 0)
		} else {
			action = "  " + action
		}
		actions = append(actions, action)
	}
	if compact {
		lines = append(lines, "  "+strings.Join(actions, "   "))
	} else {
		lines = append(lines, "", "               "+strings.Join(actions, "   "))
	}
	if m.decision != decisionNone {
		lines = append(lines, uiMutedStyle.Render("  Recorded decision: "+string(m.decision)))
	}
	return lines
}

func visibleAttention(items []reviewAttentionItem, selectedID string, compact bool) ([]reviewAttentionItem, int) {
	if !compact || len(items) <= 2 {
		return items, 0
	}
	selected := 0
	for index, item := range items {
		if selectedID == "review.attention."+item.ID {
			selected = index
			break
		}
	}
	start := clamp(selected, 0, len(items)-2)
	return items[start : start+2], len(items) - 2
}

func projectReviewAttention(snapshot reviewSnapshot) []reviewAttentionItem {
	items := make([]reviewAttentionItem, 0)
	add := func(id, label string, kind attentionKind, target reviewTab) {
		items = append(items, reviewAttentionItem{ID: id, Label: label, Kind: kind, Target: target})
	}
	drift := snapshot.Projection.Drift
	if snapshot.Plan == nil {
		add("no-plan", "No implementation plan was submitted; planning is optional.", attentionNeutral, tabChanges)
	} else {
		if drift.Additional > 0 {
			add("additional", fmt.Sprintf("%d additional files changed outside the accepted plan", drift.Additional), attentionNeutral, tabChanges)
		}
		if drift.Untouched > 0 {
			add("untouched", fmt.Sprintf("%d planned files remain unchanged", drift.Untouched), attentionReview, tabChanges)
		}
	}
	changedIntegrations := 0
	for _, item := range snapshot.Projection.Integrations {
		if item.Precision != review.PrecisionPlanned && strings.TrimSpace(item.Change) != "" {
			changedIntegrations++
		}
	}
	if changedIntegrations > 0 {
		add("integration", fmt.Sprintf("%d existing-code integrations intersect changed files", changedIntegrations), attentionReview, tabIntegration)
	}
	modified, failing, stale := 0, 0, 0
	for _, item := range snapshot.Evidence.Items {
		if item.Category == evidence.CategoryModifiedExisting {
			modified++
		}
		if item.Automated && !item.Passing {
			failing++
		}
		if !item.Fresh {
			stale++
		}
	}
	if modified > 0 {
		add("modified-tests", fmt.Sprintf("%d existing tests were modified during implementation", modified), attentionReview, tabEvidence)
	}
	if failing > 0 {
		add("failing-evidence", fmt.Sprintf("%d automated evidence items are not passing", failing), attentionReview, tabEvidence)
	}
	if stale > 0 {
		add("stale-evidence", fmt.Sprintf("%d evidence items were recorded against a different worktree", stale), attentionReview, tabEvidence)
	}
	if len(snapshot.Evidence.Items) == 0 {
		add("no-evidence", "No evidence has been recorded for this change.", attentionReview, tabEvidence)
	}
	unreviewed := 0
	for _, criterion := range snapshot.Criteria {
		if !criterion.Checked {
			unreviewed++
		}
	}
	if unreviewed > 0 {
		noun := "criterion has"
		if unreviewed > 1 {
			noun = "criteria have"
		}
		add("acceptance", fmt.Sprintf("%d acceptance %s not been reviewed", unreviewed, noun), attentionReview, tabEvidence)
	}
	if snapshot.Projection.Stats.Files > overviewLargeDriftFiles {
		add("large-baseline-drift", fmt.Sprintf("%d files changed since baseline; the review may include unrelated work", snapshot.Projection.Stats.Files), attentionReview, tabChanges)
	}
	return items
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
	source, target := item.Parent, item.Symbol
	if source == "" {
		source, target = item.Symbol, item.Change
	}
	parts := make([]string, 0, 3)
	for _, part := range []string{source, item.Relationship, target} {
		if strings.TrimSpace(part) != "" {
			parts = append(parts, strings.TrimSpace(part))
		}
	}
	return strings.Join(parts, " → ")
}

func integrationProvenance(item review.Integration) string {
	switch item.Precision {
	case review.PrecisionPrecise:
		return "precise · compiler-backed"
	case review.PrecisionStructural:
		return "structural · parser-derived"
	case review.PrecisionPlanned:
		return "planned · AI-declared"
	}
	return string(item.Precision)
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
	case review.StatusUntouched:
		return "–"
	}
	return "•"
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
