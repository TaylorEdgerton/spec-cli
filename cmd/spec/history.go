package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/TaylorEdgerton/spec-cli/internal/change"
	"github.com/TaylorEdgerton/spec-cli/internal/state"
)

type historyModel struct {
	root            string
	dir             string
	all             []state.History
	active          bool
	cursor          int
	search          lineEditor
	searching       bool
	timeline        bool
	spec            string
	status          string
	width, height   int
	done            bool
	stopped         bool
	help            bool
	nav             string
	viewport        int
	followupConfirm bool
	followupCursor  int

	readArchive func(string) (string, error)
	reopen      func(string, state.History, time.Time) (state.Setup, error)
}

func newHistoryModel(root, dir string, records []state.History, active bool) *historyModel {
	return &historyModel{root: root, dir: dir, all: records, active: active, readArchive: readSpecArchive, reopen: change.BeginFollowUp}
}

func (m *historyModel) screen() canonicalScreen {
	items := make([]screenItem, 0, len(m.visible()))
	for index, record := range m.visible() {
		items = append(items, screenItem{ID: fmt.Sprintf("history.%d", index), Label: record.Title, Selectable: true, Preview: record})
	}
	section := screenSection{ID: "history", Title: "Spec History", Items: items, EmptyReason: "No completed Specs are recorded for this workspace yet."}
	return canonicalScreen{Sections: []screenSection{section}, Cursor: m.cursor}
}

// visible applies the search filter and presents completed Specs newest first.
// The loaded records themselves are never reordered or rewritten.
func (m *historyModel) visible() []state.History {
	query := strings.ToLower(strings.TrimSpace(m.search.value))
	matched := make([]state.History, 0, len(m.all))
	for _, record := range m.all {
		haystack := strings.ToLower(strings.Join([]string{record.SpecID, record.Title, record.Intent, record.Scope, record.Summary}, " "))
		if query == "" || strings.Contains(haystack, query) {
			matched = append(matched, record)
		}
	}
	sort.SliceStable(matched, func(left, right int) bool { return matched[left].FinishedAt.After(matched[right].FinishedAt) })
	return matched
}

func (m *historyModel) selected() (state.History, bool) {
	screen := m.screen()
	items := screen.selectableItems()
	if len(items) == 0 {
		return state.History{}, false
	}
	record, ok := items[clamp(m.cursor, 0, len(items)-1)].Preview.(state.History)
	return record, ok
}

func (m *historyModel) Init() tea.Cmd { return nil }

func (m *historyModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch v := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = v.Width, v.Height
	case tea.InterruptMsg:
		m.leave(actionQuit)
		return m, tea.Quit
	case tea.KeyPressMsg:
		return m, m.key(v)
	}
	return m, nil
}

func (m *historyModel) key(msg tea.KeyPressMsg) tea.Cmd {
	if m.followupConfirm {
		return m.updateFollowUpConfirmation(msg.Keystroke())
	}
	if m.searching {
		switch msg.Keystroke() {
		case "enter":
			m.searching = false
		case "esc":
			m.searching, m.search = false, lineEditor{}
		default:
			m.search.key(msg)
		}
		m.cursor = 0
		return nil
	}
	switch msg.Keystroke() {
	case "up", "k":
		if m.spec != "" {
			m.viewport = max(0, m.viewport-1)
			return nil
		}
		m.move(-1)
	case "down", "j":
		if m.spec != "" {
			m.viewport++
			return nil
		}
		m.move(1)
	case "pgup":
		m.viewport = max(0, m.viewport-max(1, m.height/2))
	case "pgdown":
		m.viewport += max(1, m.height/2)
	case "/":
		m.searching, m.status = true, ""
	case "t":
		m.timeline = !m.timeline
	case "f":
		m.startFollowUp()
	case "?":
		m.help = !m.help
	case "enter":
		m.openSelected()
	case "esc", "b":
		if m.spec != "" {
			m.spec, m.status, m.viewport = "", "", 0
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

func (m *historyModel) leave(action string) {
	m.done, m.nav = true, action
	m.stopped = action == actionQuit
}

func (m *historyModel) hints() [][2]string {
	if m.followupConfirm {
		return [][2]string{{"←/→", "choose"}, {"enter", "confirm"}, {"esc", "cancel"}}
	}
	if m.spec != "" {
		return [][2]string{{"esc", "close Spec"}, {"?", "help"}, {"g", "home"}}
	}
	if m.searching {
		return [][2]string{{"type", "search"}, {"enter", "keep"}, {"esc", "clear"}}
	}
	if m.width <= reviewMinWidth {
		return [][2]string{
			{"↑/↓", "select"}, {"enter", "open"}, {"/", "search"}, {"t", "timeline"},
			{"f", "follow-up"}, {"b", "back"}, {"g", "home"},
		}
	}
	return [][2]string{
		{"↑/↓", "select"}, {"enter", "open Spec"}, {"/", "search"},
		{"t", "timeline"}, {"f", "follow-up"}, {"?", "help"}, {"b", "back"}, {"g", "home"},
	}
}

func (m *historyModel) startFollowUp() {
	record, ok := m.selected()
	if !ok {
		return
	}
	if reason := m.followUpUnavailable(record); reason != "" {
		m.status = reason
		return
	}
	m.followupConfirm, m.followupCursor, m.status = true, 0, ""
}

func (m *historyModel) followUpUnavailable(record state.History) string {
	if m.active {
		return "A follow-up is unavailable while another active Spec exists."
	}
	if strings.TrimSpace(record.SpecID) == "" {
		return "A follow-up is unavailable because this legacy record has no Spec ID."
	}
	if strings.TrimSpace(record.SpecArchive) == "" {
		return "A follow-up is unavailable because this record has no archived Spec."
	}
	return ""
}

func (m *historyModel) updateFollowUpConfirmation(keystroke string) tea.Cmd {
	switch keystroke {
	case "left", "right", "up", "down", "j", "k", "tab", "shift+tab":
		m.followupCursor = wrap(m.followupCursor+1, 2)
	case "esc", "b":
		m.followupConfirm, m.followupCursor = false, 0
	case "enter":
		if m.followupCursor == 0 {
			m.followupConfirm = false
			return nil
		}
		record, ok := m.selected()
		if !ok {
			m.followupConfirm = false
			return nil
		}
		if _, err := m.reopen(m.root, record, time.Now()); err != nil {
			m.followupConfirm = false
			m.status = "Could not start follow-up: " + err.Error()
			return nil
		}
		m.active, m.done, m.nav = true, true, actionDefinition
		return tea.Quit
	}
	return nil
}

func (m *historyModel) move(delta int) {
	count := len(m.screen().selectableItems())
	if count == 0 {
		return
	}
	m.cursor = wrap(m.cursor+delta, count)
	m.spec, m.status = "", ""
}

// openSelected shows the archived Spec. History is a read-only view: nothing
// here writes workspace state or touches the archived file.
func (m *historyModel) openSelected() {
	record, ok := m.selected()
	if !ok {
		return
	}
	if strings.TrimSpace(record.SpecArchive) == "" {
		m.status = "Could not read the archived Spec: this record stored no archive path."
		return
	}
	content, err := m.readArchive(filepath.Join(m.dir, filepath.FromSlash(record.SpecArchive)))
	if err != nil {
		m.status = "Could not read the archived Spec: " + err.Error()
		return
	}
	m.spec, m.status = content, ""
}

func (m *historyModel) View() tea.View {
	width, height := m.width, m.height
	if width <= 0 {
		width = 100
	}
	if height <= 0 {
		height = 32
	}
	if width < reviewMinWidth || height < reviewMinHeight {
		return tea.NewView(uiEmptyState("Terminal is too small",
			fmt.Sprintf("Spec history needs at least %dx%d; this terminal is %dx%d.", reviewMinWidth, reviewMinHeight, width, height)))
	}
	body := m.body(max(20, width-8))
	if m.status != "" {
		body = append(body, "", uiMutedStyle.Render(m.status))
	}
	header := uiSplit("Spec history", fmt.Sprintf("%d completed", len(m.all)), max(1, width-4))
	if m.timeline {
		if record, ok := m.selected(); ok {
			header = fmt.Sprintf("%s · Timeline", emptyDash(record.SpecID))
		}
	}
	rendered := strings.Join(body, "\n")
	anchor := ""
	if m.spec == "" {
		if item, ok := m.screen().selectedItem(); ok {
			anchor = item.Label
		}
	}
	rendered, m.viewport = uiViewportBody(rendered, uiWorkflowBodyHeight(height, header), m.viewport, anchor)
	return tea.NewView(uiAppShell(width, height, header, rendered, m.footer()))
}

func (m *historyModel) footer() string { return uiKeyHints(m.hints(), "  ") }

func (m *historyModel) body(width int) []string {
	if m.help {
		return []string{uiHelpOverlayWidth(m.hints(), width)}
	}
	if m.followupConfirm {
		return m.followUpConfirmationBody(width)
	}
	if m.spec != "" {
		return []string{uiTitleStyle.Render("Archived Spec (read-only)"), "", uiProse(m.spec, width)}
	}
	if m.timeline {
		record, ok := m.selected()
		if !ok {
			return []string{uiEmptyState("Timeline", "No completed Spec is selected.")}
		}
		return append([]string{uiTitleStyle.Render("Timeline"), ""}, timelineLines(m.timelineWithFollowUps(record))...)
	}
	var lines []string
	if m.searching || m.search.value != "" {
		lines = append(lines, "  "+uiMutedStyle.Render("Search: ")+m.search.view(), "")
	}
	items := m.screen().selectableItems()
	if len(items) == 0 {
		reason := "No completed Specs are recorded for this workspace yet."
		if strings.TrimSpace(m.search.value) != "" {
			reason = "No completed Specs match this search. Press esc to clear it."
		}
		return append(lines, uiEmptyState("", reason))
	}
	cursor := clamp(m.cursor, 0, len(items)-1)
	lines = append(lines,
		fmt.Sprintf("  %-10s %-45s %s", "Date", "Spec", "Status"),
		"  "+strings.Repeat("─", 68),
	)
	for index, item := range items {
		record := item.Preview.(state.History)
		identity := fmt.Sprintf("%s · %s", emptyDash(record.SpecID), record.Title)
		row := fmt.Sprintf("%-10s %-45s %s", record.FinishedAt.UTC().Format("2006-01-02"), identity, historyStatus(record))
		if index == cursor {
			lines = append(lines, uiSelectedRow("> "+row, 0))
		} else {
			lines = append(lines, "  "+row)
		}
	}
	record := items[cursor].Preview.(state.History)
	lines = append(lines, "", uiTitleStyle.Render("Selected"), uiIndentedProse(record.Title, width, 2))
	if strings.TrimSpace(record.Scope) != "" {
		lines = append(lines, uiMutedStyle.Render("  Scope"), uiIndentedProse(record.Scope, width, 2))
	}
	if strings.TrimSpace(record.Summary) != "" {
		lines = append(lines, uiTitleStyle.Render("Completion summary"), uiIndentedProse(record.Summary, width, 2))
	}
	lines = append(lines, fmt.Sprintf("  Baseline %s  ·  archive %s",
		emptyDash(shortSHA(record.BaseSHA)), emptyDash(record.SpecArchive)))
	if reason := m.followUpUnavailable(record); reason != "" {
		lines = append(lines, uiMutedStyle.Render("  "+reason))
	} else {
		lines = append(lines, "  [ Reopen as follow-up ]  press f")
	}
	lines = append(lines, historyStatsLines(record)...)
	var followups []string
	if record.SpecID != "" {
		for _, candidate := range m.all {
			if candidate.OriginSpecID != record.SpecID {
				continue
			}
			followups = append(followups, emptyDash(candidate.SpecID)+" · "+candidate.Title)
		}
	}
	if len(followups) > 0 {
		lines = append(lines, uiTitleStyle.Render("Follow-ups"))
		for _, followup := range followups {
			lines = append(lines, "  "+followup)
		}
	}
	return lines
}

func (m *historyModel) followUpConfirmationBody(width int) []string {
	record, _ := m.selected()
	cancel, create := "  [ Cancel ]", "  [ Create linked Spec ]"
	if m.followupCursor == 0 {
		cancel = uiSelectedRow("> [ Cancel ]", 0)
	} else {
		create = uiSelectedRow("> [ Create linked Spec ]", 0)
	}
	copyWidth := max(20, width-4)
	body := strings.Join([]string{
		uiTitleStyle.Render("Reopen as follow-up?"), "",
		uiIndentedProse(record.Title, copyWidth, 2), "",
		uiProse("A new Spec ID and current Git baseline will be used.", copyWidth),
		uiProse("Intent, scope, and acceptance criteria will be copied; the archived plan and evidence remain historical.", copyWidth),
		"", cancel + "    " + create,
	}, "\n")
	return strings.Split(uiPanel(max(20, width), lipgloss.Height(body)+2, uiPurple, "Follow-up", "", body), "\n")
}

func (m *historyModel) timelineWithFollowUps(record state.History) []timelineEntry {
	entries := timelineEntries(record)
	if record.SpecID == "" {
		return entries
	}
	for _, candidate := range m.all {
		if candidate.OriginSpecID != record.SpecID {
			continue
		}
		entries = append(entries, timelineEntry{
			At: candidate.StartedAt, Type: string(state.TimelineFollowUpStarted), Actor: "human", Source: "history",
			Detail: "continued as " + emptyDash(candidate.SpecID), Derived: true,
		})
	}
	sort.SliceStable(entries, func(left, right int) bool { return entries[left].At.Before(entries[right].At) })
	return entries
}

func historyStatsLines(record state.History) []string {
	evidence := record.EvidenceSummary
	plan := "no"
	if record.Plan != nil {
		plan = "yes"
	}
	completed := "not recorded"
	if !record.FinishedAt.IsZero() {
		completed = record.FinishedAt.UTC().Format("2006-01-02 15:04 UTC")
	}
	tests := evidence.Existing + evidence.FailThenPass + evidence.NewTests + evidence.ModifiedExisting
	evidenceTotal := tests + evidence.Manual
	return []string{
		fmt.Sprintf("  Files %d  ·  +%d -%d  ·  duration %s", record.Stats.Files,
			record.Stats.Additions, record.Stats.Deletions, historyDuration(record)),
		fmt.Sprintf("  Completed %s  ·  Agent plan %s", completed, plan),
		fmt.Sprintf("  Tests %d  ·  Evidence %d item(s)  ·  Additional %d", tests, evidenceTotal, record.PlanDrift.Additional),
		fmt.Sprintf("  Evidence: existing %d · reproduced %d · new %d · modified %d · manual %d",
			evidence.Existing, evidence.FailThenPass, evidence.NewTests, evidence.ModifiedExisting, evidence.Manual),
	}
}

func historyDuration(record state.History) string {
	if record.DurationSeconds > 0 {
		return (time.Duration(record.DurationSeconds) * time.Second).String()
	}
	if !record.StartedAt.IsZero() && record.FinishedAt.After(record.StartedAt) {
		return record.FinishedAt.Sub(record.StartedAt).Round(time.Second).String()
	}
	return "not recorded"
}

// historyStatus stays neutral: a record either carries a human completion
// acknowledgement or it does not.
func historyStatus(record state.History) string {
	if record.CompletionAcknowledged {
		return "completed"
	}
	return "archived"
}

func readSpecArchive(path string) (string, error) {
	data, err := os.ReadFile(path)
	return string(data), err
}

func loadHistory(root string) (string, []state.History, bool, error) {
	workspace, err := state.Load(root)
	if err != nil {
		return "", nil, false, err
	}
	records, err := workspace.HistoryRecords()
	return workspace.Dir, records, workspace.Active, err
}
