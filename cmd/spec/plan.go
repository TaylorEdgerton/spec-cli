package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/TaylorEdgerton/spec-cli/internal/discovery"
	"github.com/TaylorEdgerton/spec-cli/internal/state"
	"github.com/charmbracelet/x/ansi"
)

type planModel struct {
	root                  string
	stored                *state.StoredChangePlan
	open                  func(string, discovery.Result) error
	status                string
	cursor, width, height int
	done                  bool
	help                  bool
	raw                   bool
	rawColumn             int
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
		{ID: "files", Title: "Planned changes", Items: files},
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
			if m.raw {
				m.viewport = max(0, m.viewport-1)
				break
			}
			m.cursor, m.status = wrap(m.cursor-1, len(m.screen().selectableItems())), ""
		case "down", "j":
			if m.raw {
				m.viewport++
				break
			}
			m.cursor, m.status = wrap(m.cursor+1, len(m.screen().selectableItems())), ""
		case "left":
			if m.raw {
				m.rawColumn = max(0, m.rawColumn-m.rawColumnStep())
			}
		case "right":
			if m.raw {
				m.rawColumn += m.rawColumnStep()
			}
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
		case "x":
			m.raw, m.viewport, m.rawColumn = !m.raw, 0, 0
		case "r":
			m.leave(actionReview)
			return m, tea.Quit
		case "b", "esc":
			if m.raw {
				m.raw, m.viewport, m.rawColumn = false, 0, 0
				return m, nil
			}
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
	if m.raw {
		return [][2]string{{"↑/↓", "rows"}, {"←/→", "columns"}, {"x", "readable plan"}, {"b", "back"}, {"g", "home"}, {"?", "help"}}
	}
	return [][2]string{{"↑/↓", "select"}, {"enter", "inspect"}, {"x", "inspect raw"}, {"e", "edit plan"}, {"c", "continue"}, {"b", "back"}, {"g", "home"}, {"?", "help"}}
}

func (m *planModel) rawColumnStep() int { return max(8, max(20, m.width-8)/3) }
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
		return tea.NewView(uiAppShell(w, h, "Implementation Plan", uiHelpOverlayWidth(m.hints(), max(20, w-8)), uiKeyHints(m.hints(), "  ")))
	}
	if m.stored == nil {
		body := uiEmptyState("No implementation plan", "No implementation plan was submitted. The plan is optional; actual-change review remains available.")
		return tea.NewView(uiAppShell(w, h, "Implementation Plan", body, uiKeyHints(m.hints(), "  ")))
	}
	p := m.stored.Plan
	if m.raw {
		canonical, err := json.MarshalIndent(p, "", "  ")
		body := ""
		if err != nil {
			body = uiEmptyState("Raw ChangePlan unavailable", err.Error())
		} else {
			width := max(20, w-8)
			jsonLines := strings.Split(string(canonical), "\n")
			maxWidth := 0
			for _, line := range jsonLines {
				maxWidth = max(maxWidth, ansi.StringWidth(line))
			}
			m.rawColumn = clamp(m.rawColumn, 0, max(0, maxWidth-width))
			for index, line := range jsonLines {
				jsonLines[index] = ansi.Cut(line, m.rawColumn, m.rawColumn+width)
			}
			columns := fmt.Sprintf("columns %d–%d of %d", m.rawColumn+1, min(maxWidth, m.rawColumn+width), maxWidth)
			body = uiSplit("Raw ChangePlan", columns, width) + "\n\n" + strings.Join(jsonLines, "\n")
		}
		body, m.viewport = uiViewportBody(body, uiWorkflowBodyHeight(h, "Implementation Plan · Raw"), m.viewport, "")
		return tea.NewView(uiAppShell(w, h, "Implementation Plan · Raw", body, uiKeyHints(m.hints(), "  ")))
	}
	screen := m.screen()
	proseWidth := max(20, w-8)
	lines := []string{uiTitleStyle.Render("Summary"), uiProse(p.Summary, proseWidth), "", uiTitleStyle.Render("Planned changes")}
	selected := m.selectedID()
	for _, item := range screen.Sections[0].Items {
		f := item.Preview.(state.PlannedFile)
		row := fmt.Sprintf("  %s %s", strings.ToUpper(string(f.Action[:1])), f.Path)
		if selected == item.ID {
			row = uiSelectedRow("> "+string(f.Action)+" "+f.Path, 0)
		}
		lines = append(lines, row)
		if strings.TrimSpace(f.Reason) != "" {
			lines = append(lines, uiIndentedProse(f.Reason, proseWidth, 4))
		}
	}
	lines = append(lines, "", uiTitleStyle.Render("Existing integration points"))
	for _, screenItem := range screen.Sections[1].Items {
		item := screenItem.Preview.(state.PlannedIntegration)
		row := "  " + item.ExistingSymbol
		if selected == screenItem.ID {
			row = uiSelectedRow("> "+item.ExistingSymbol, 0)
		}
		lines = append(lines, row)
		lines = append(lines, uiIndentedProse("↳ "+item.PlannedChange+" · "+item.Relationship, proseWidth, 4))
	}
	lines = append(lines, "", uiTitleStyle.Render("Verification"))
	if len(p.Verification) == 0 {
		lines = append(lines, uiMutedStyle.Render("No verification behaviours declared."))
	} else {
		for _, v := range p.Verification {
			lines = append(lines, uiIndentedProse("- "+v.Behaviour+emptySuffix(v.LikelyLocation, " · "), proseWidth, 2))
		}
	}
	lines = append(lines, "", uiTitleStyle.Render("Uncertainties"))
	if len(p.Uncertainties) == 0 {
		lines = append(lines, uiMutedStyle.Render("No uncertainties declared."))
	} else {
		for _, u := range p.Uncertainties {
			lines = append(lines, uiIndentedProse("- "+u, proseWidth, 2))
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
