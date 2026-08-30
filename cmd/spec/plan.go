package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/TaylorEdgerton/spec-cli/internal/discovery"
	"github.com/TaylorEdgerton/spec-cli/internal/state"
)

type planModel struct {
	root                  string
	stored                *state.StoredChangePlan
	open                  func(string, discovery.Result) error
	status                string
	cursor, width, height int
	done                  bool
}

func newPlanModel(root string, stored *state.StoredChangePlan) *planModel {
	return &planModel{root: root, stored: stored, open: openInVSCode}
}
func (m *planModel) Init() tea.Cmd { return nil }
func (m *planModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch v := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = v.Width, v.Height
	case tea.InterruptMsg:
		m.done = true
		return m, tea.Quit
	case tea.KeyPressMsg:
		switch v.Keystroke() {
		case "up", "k":
			m.cursor, m.status = wrap(m.cursor-1, len(m.selectableIDs())), ""
		case "down", "j":
			m.cursor, m.status = wrap(m.cursor+1, len(m.selectableIDs())), ""
		case "o":
			m.openSelected()
		case "q", "b", "esc", "ctrl+c":
			m.done = true
			return m, tea.Quit
		}
	}
	return m, nil
}
func (m *planModel) selectableIDs() []string {
	if m.stored == nil {
		return nil
	}
	ids := make([]string, 0, len(m.stored.Plan.Files)+len(m.stored.Plan.IntegrationPoints))
	for i := range m.stored.Plan.Files {
		ids = append(ids, fmt.Sprintf("plan.file.%d", i))
	}
	for i := range m.stored.Plan.IntegrationPoints {
		ids = append(ids, fmt.Sprintf("plan.integration.%d", i))
	}
	return ids
}
func (m *planModel) selectedID() string {
	ids := m.selectableIDs()
	if len(ids) == 0 {
		return ""
	}
	return ids[clamp(m.cursor, 0, len(ids)-1)]
}

// selectedFile reports the planned file path when the cursor is on a file row.
func (m *planModel) selectedFile() (string, bool) {
	if m.stored == nil {
		return "", false
	}
	var index int
	if _, err := fmt.Sscanf(m.selectedID(), "plan.file.%d", &index); err != nil || index < 0 || index >= len(m.stored.Plan.Files) {
		return "", false
	}
	return m.stored.Plan.Files[index].Path, true
}

func (m *planModel) openSelected() {
	path, ok := m.selectedFile()
	if !ok {
		m.status = "Select a planned file to open it in VS Code."
		return
	}
	if err := m.open(m.root, discovery.Result{Path: path}); err != nil {
		m.status = "Could not open in VS Code: " + err.Error()
		return
	}
	m.status = "Opened " + path
}

func (m *planModel) View() tea.View {
	w, h := m.width, m.height
	if w <= 0 {
		w = 100
	}
	if h <= 0 {
		h = 32
	}
	if m.stored == nil {
		body := uiEmptyState("No implementation plan", "No implementation plan was submitted. The plan is optional; actual-change review remains available.")
		return tea.NewView(uiAppShell(w, h, "Implementation Plan", body, uiKeyHints([][2]string{{"b", "back"}}, "  ")))
	}
	p := m.stored.Plan
	lines := []string{uiTitleStyle.Render("Summary"), p.Summary, "", uiTitleStyle.Render("Planned files")}
	selected := m.selectedID()
	for i, f := range p.Files {
		row := fmt.Sprintf("  %s %s\n    %s", strings.ToUpper(string(f.Action[:1])), f.Path, f.Reason)
		if selected == fmt.Sprintf("plan.file.%d", i) {
			row = uiSelectedRow("> "+string(f.Action)+" "+f.Path, 0) + "\n    " + f.Reason
		}
		lines = append(lines, row)
	}
	lines = append(lines, "", uiTitleStyle.Render("Existing integration points"))
	for i, item := range p.IntegrationPoints {
		row := fmt.Sprintf("  %s\n    ↳ %s · %s", item.ExistingSymbol, item.PlannedChange, item.Relationship)
		if selected == fmt.Sprintf("plan.integration.%d", i) {
			row = uiSelectedRow("> "+item.ExistingSymbol, 0) + "\n    ↳ " + item.PlannedChange + " · " + item.Relationship
		}
		lines = append(lines, row)
	}
	lines = append(lines, "", uiTitleStyle.Render("Planned verification"))
	if len(p.Verification) == 0 {
		lines = append(lines, uiMutedStyle.Render("No verification behaviours declared."))
	} else {
		for _, v := range p.Verification {
			lines = append(lines, "  • "+v.Behaviour+emptySuffix(v.LikelyLocation, " · "))
		}
	}
	lines = append(lines, "", uiTitleStyle.Render("Uncertain"))
	if len(p.Uncertainties) == 0 {
		lines = append(lines, uiMutedStyle.Render("No uncertainties declared."))
	} else {
		for _, u := range p.Uncertainties {
			lines = append(lines, "  • "+u)
		}
	}
	if preview := m.preview(); preview != "" {
		lines = append(lines, "", uiTitleStyle.Render("Preview"), preview)
	}
	if m.status != "" {
		lines = append(lines, "", uiMutedStyle.Render(m.status))
	}
	header := fmt.Sprintf("Implementation Plan  AGENT PLAN\nSubmitted %s · %s · %s", m.stored.SubmittedAt.Format("15:04"), emptyAs(m.stored.Submitter, "unknown submitter"), m.stored.Source)
	footer := uiKeyHints([][2]string{{"↑/↓", "select"}, {"o", "VS Code"}, {"b", "back"}}, "  ")
	return tea.NewView(uiAppShell(w, h, header, strings.Join(lines, "\n"), footer))
}
func (m *planModel) preview() string {
	path, ok := m.selectedFile()
	if !ok {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(m.root, filepath.FromSlash(path)))
	if err != nil {
		return uiMutedStyle.Render("File is not present yet: " + path)
	}
	rows := strings.Split(string(data), "\n")
	if len(rows) > 5 {
		rows = rows[:5]
	}
	for i := range rows {
		rows[i] = uiCodeLine(i+1, i == 0, rows[i], 60)
	}
	return strings.Join(rows, "\n")
}
func emptySuffix(value, prefix string) string {
	if strings.TrimSpace(value) == "" {
		return ""
	}
	return prefix + value
}
