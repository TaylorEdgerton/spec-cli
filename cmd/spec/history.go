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
	"github.com/TaylorEdgerton/spec-cli/internal/state"
)

type historyModel struct {
	dir           string
	all           []state.History
	cursor        int
	search        lineEditor
	searching     bool
	stats         bool
	timeline      bool
	spec          string
	status        string
	width, height int
	done          bool
	stopped       bool
	help          bool
	nav           string
	viewport      int

	readArchive func(string) (string, error)
}

func newHistoryModel(dir string, records []state.History, stats bool) *historyModel {
	return &historyModel{dir: dir, all: records, stats: stats, readArchive: readSpecArchive}
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
	case "s":
		m.stats = !m.stats
	case "t":
		m.timeline = !m.timeline
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
	case "q", "ctrl+c":
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
	if m.spec != "" {
		return [][2]string{{"esc", "close Spec"}, {"?", "help"}, {"q", "exit"}}
	}
	if m.searching {
		return [][2]string{{"type", "search"}, {"enter", "keep"}, {"esc", "clear"}}
	}
	return [][2]string{
		{"↑/↓", "select"}, {"enter", "open Spec"}, {"/", "search"},
		{"s", "stats"}, {"t", "timeline"}, {"?", "help"}, {"b", "back"}, {"q", "exit"},
	}
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
	body := m.body()
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

func (m *historyModel) body() []string {
	if m.help {
		return []string{uiHelpOverlay(m.hints())}
	}
	if m.spec != "" {
		return append([]string{uiTitleStyle.Render("Archived Spec (read-only)"), ""}, strings.Split(m.spec, "\n")...)
	}
	if m.timeline {
		record, ok := m.selected()
		if !ok {
			return []string{uiEmptyState("Timeline", "No completed Spec is selected.")}
		}
		return append([]string{uiTitleStyle.Render("Timeline"), ""}, timelineLines(timelineEntries(record))...)
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
	lines = append(lines, "", uiTitleStyle.Render("Selected"), "  "+record.Title)
	if strings.TrimSpace(record.Scope) != "" {
		lines = append(lines, "  Scope: "+record.Scope)
	}
	lines = append(lines, fmt.Sprintf("  Baseline %s  ·  archive %s",
		emptyDash(shortSHA(record.BaseSHA)), emptyDash(record.SpecArchive)))
	if m.stats {
		lines = append(lines, historyStatsLines(record)...)
	}
	return lines
}

func historyStatsLines(record state.History) []string {
	evidence := record.EvidenceSummary
	return []string{
		fmt.Sprintf("  Files %d  ·  +%d -%d  ·  duration %s", record.Stats.Files,
			record.Stats.Additions, record.Stats.Deletions, historyDuration(record)),
		fmt.Sprintf("  Plan drift: matched %d  additional %d  untouched %d",
			record.PlanDrift.Matched, record.PlanDrift.Additional, record.PlanDrift.Untouched),
		fmt.Sprintf("  Evidence: existing %d  fail_then_pass %d  new_tests %d  modified_existing %d  manual %d",
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

func loadHistory(root string) (string, []state.History, error) {
	workspace, err := state.Load(root)
	if err != nil {
		return "", nil, err
	}
	records, err := workspace.HistoryRecords()
	return workspace.Dir, records, err
}

func runHistory(root string, stats bool, input io.Reader, output io.Writer) (string, error) {
	dir, records, err := loadHistory(root)
	if err != nil {
		return actionQuit, err
	}
	final, err := tea.NewProgram(newHistoryModel(dir, records, stats), tea.WithInput(input), tea.WithOutput(output)).Run()
	if err != nil {
		return actionQuit, err
	}
	return final.(*historyModel).nav, nil
}
