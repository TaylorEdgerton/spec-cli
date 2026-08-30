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
	help                  bool
	nav                   string
	viewport              int
}

func newPlanModel(root string, stored *state.StoredChangePlan) *planModel {
	return &planModel{root: root, stored: stored, open: openInVSCode}
}

func (m *planModel) screen() canonicalScreen {
	if m.stored == nil {
		return canonicalScreen{Sections: []screenSection{{ID: "plan", Title: "Implementation Plan", EmptyReason: "No implementation plan was submitted."}}}
	}
	files := make([]screenItem, 0, len(m.stored.Plan.Files))
	for index, file := range m.stored.Plan.Files {
		files = append(files, screenItem{ID: fmt.Sprintf("plan.file.%d", index), Label: file.Path, Selectable: true, Preview: file})
	}
	integrations := make([]screenItem, 0, len(m.stored.Plan.IntegrationPoints))
	for index, item := range m.stored.Plan.IntegrationPoints {
		integrations = append(integrations, screenItem{ID: fmt.Sprintf("plan.integration.%d", index), Label: item.ExistingSymbol, Selectable: true, Preview: item})
	}
	return canonicalScreen{Sections: []screenSection{
		{ID: "files", Title: "Planned files", Items: files},
		{ID: "integrations", Title: "Existing integration points", Items: integrations},
	}, Cursor: m.cursor}
}
func (m *planModel) Init() tea.Cmd { return nil }
func (m *planModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch v := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = v.Width, v.Height
	case tea.InterruptMsg:
		m.leave(actionQuit)
		return m, tea.Quit
	case tea.KeyPressMsg:
		switch v.Keystroke() {
		case "up", "k":
			m.cursor, m.status = wrap(m.cursor-1, len(m.screen().selectableItems())), ""
		case "down", "j":
			m.cursor, m.status = wrap(m.cursor+1, len(m.screen().selectableItems())), ""
		case "pgup":
			m.viewport = max(0, m.viewport-max(1, m.height/2))
		case "pgdown":
			m.viewport += max(1, m.height/2)
		case "o":
			m.openSelected()
		case "enter":
			if path, ok := m.selectedFile(); ok {
				m.status = "Inspecting " + path + " in the preview."
			} else {
				m.status = "Inspecting the selected integration point."
			}
		case "e":
			m.leave(actionPlanCapture)
			return m, tea.Quit
		case "c":
			m.leave(actionContinue)
			return m, tea.Quit
		case "?":
			m.help = !m.help
		case "r":
			m.leave(actionReview)
			return m, tea.Quit
		case "b", "esc":
			m.leave(actionBack)
			return m, tea.Quit
		case "ctrl+c":
			m.leave(actionQuit)
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m *planModel) leave(action string) { m.done, m.nav = true, action }

func (m *planModel) hints() [][2]string {
	return [][2]string{{"↑/↓", "select"}, {"enter", "inspect"}, {"e", "edit plan"}, {"c", "continue"}, {"b", "back"}, {"g", "home"}, {"?", "help"}}
}
func (m *planModel) selectedID() string {
	item, ok := m.screen().selectedItem()
	if !ok {
		return ""
	}
	return item.ID
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
	if m.help {
		return tea.NewView(uiAppShell(w, h, "Implementation Plan", uiHelpOverlay(m.hints()), uiKeyHints(m.hints(), "  ")))
	}
	if m.stored == nil {
		body := uiEmptyState("No implementation plan", "No implementation plan was submitted. The plan is optional; actual-change review remains available.")
		return tea.NewView(uiAppShell(w, h, "Implementation Plan", body, uiKeyHints(m.hints(), "  ")))
	}
	p := m.stored.Plan
	screen := m.screen()
	lines := []string{uiTitleStyle.Render("Summary"), p.Summary, "", uiTitleStyle.Render("Planned files")}
	selected := m.selectedID()
	for _, item := range screen.Sections[0].Items {
		f := item.Preview.(state.PlannedFile)
		row := fmt.Sprintf("  %s %s\n    %s", strings.ToUpper(string(f.Action[:1])), f.Path, f.Reason)
		if selected == item.ID {
			row = uiSelectedRow("> "+string(f.Action)+" "+f.Path, 0) + "\n    " + f.Reason
		}
		lines = append(lines, row)
	}
	lines = append(lines, "", uiTitleStyle.Render("Existing integration points"))
	for _, screenItem := range screen.Sections[1].Items {
		item := screenItem.Preview.(state.PlannedIntegration)
		row := fmt.Sprintf("  %s\n    ↳ %s · %s", item.ExistingSymbol, item.PlannedChange, item.Relationship)
		if selected == screenItem.ID {
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
	body := strings.Join(lines, "\n")
	anchor := ""
	if item, ok := m.screen().selectedItem(); ok {
		anchor = item.Label
	}
	body, m.viewport = uiViewportBody(body, uiWorkflowBodyHeight(h, header), m.viewport, anchor)
	return tea.NewView(uiAppShell(w, h, header, body, uiKeyHints(m.hints(), "  ")))
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
