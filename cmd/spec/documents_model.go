package main

import (
	"strings"

	tea "charm.land/bubbletea/v2"
)

type documentModel struct {
	cursor        int
	width, height int
	nav           string
	status        string
	editing       bool
	editor        lineEditor
}

func newDocumentModel() *documentModel { return &documentModel{} }

func (model *documentModel) screen() canonicalScreen {
	return canonicalScreen{Sections: []screenSection{{ID: "documents", Title: "Create a Doc", Items: []screenItem{
		{ID: "documents.readme", Label: "README", Detail: "Create or prepare README.md", Selectable: true, Action: screenAction(actionREADME)},
		{ID: "documents.runbook", Label: "Runbook", Detail: "Create a scenario runbook", Selectable: true, Action: screenAction(actionRunbook)},
	}}}, Cursor: model.cursor}
}

func (model *documentModel) Init() tea.Cmd { return nil }

func (model *documentModel) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch message := message.(type) {
	case tea.WindowSizeMsg:
		model.width, model.height = message.Width, message.Height
	case tea.InterruptMsg:
		model.nav = actionQuit
		return model, tea.Quit
	case tea.PasteMsg:
		if model.editing {
			model.editor.insert(message.Content)
		}
	case tea.KeyPressMsg:
		if model.editing {
			switch message.Keystroke() {
			case "esc":
				model.editing, model.editor = false, lineEditor{}
			case "enter":
				if strings.TrimSpace(model.editor.value) != "" {
					model.nav = actionRunbook
				}
			default:
				model.editor.key(message)
			}
			return model, nil
		}
		switch message.Keystroke() {
		case "up", "k":
			model.move(-1)
		case "down", "j":
			model.move(1)
		case "enter":
			if model.cursor == 1 {
				model.editing = true
				model.editor = newLineEditor("")
			} else {
				model.nav = string(model.screen().activate())
			}
		case "b", "esc":
			model.nav = actionBack
		case "ctrl+c":
			model.nav = actionQuit
			return model, tea.Quit
		}
	}
	return model, nil
}

func (model *documentModel) move(delta int) {
	screen := model.screen()
	screen.move(delta)
	model.cursor = screen.Cursor
}

func (model *documentModel) View() tea.View {
	width, height := defaultSize(model.width, model.height)
	if width < homeMinWidth || height < homeMinHeight {
		return tea.NewView(uiMinimumSize(width, height))
	}
	lines := []string{uiTitleStyle.Render("Create a Doc"), ""}
	selected, _ := model.screen().selectedItem()
	for _, item := range model.screen().Sections[0].Items {
		label := "  " + item.Label + "  " + uiMutedStyle.Render(item.Detail)
		if item.ID == selected.ID {
			label = uiSelectedRow("> "+item.Label, 0) + "  " + uiMutedStyle.Render(item.Detail)
		}
		lines = append(lines, label)
	}
	if model.editing {
		lines = append(lines, "", uiTitleStyle.Render("Scenario title"), uiPanel(max(20, width-8), 3, uiPurple, "", "", model.editor.view()))
	}
	if model.status != "" {
		lines = append(lines, "", uiMutedStyle.Render(model.status))
	}
	hints := [][2]string{{"↑/↓", "navigate"}, {"enter", "select"}, {"b/esc", "back"}, {"g", "home"}}
	if model.editing {
		hints = [][2]string{{"enter", "create"}, {"esc", "cancel"}}
	}
	return tea.NewView(uiAppShell(width, height, "Spec · Create a Doc", strings.Join(lines, "\n"), uiKeyHints(hints, "    ")))
}

func (model *documentModel) runbookTitle() string { return strings.TrimSpace(model.editor.value) }
