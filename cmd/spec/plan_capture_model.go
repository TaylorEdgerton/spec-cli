package main

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/TaylorEdgerton/spec-cli/internal/state"
)

type planCaptureMode int

const (
	planCapturePaste planCaptureMode = iota
	planCapturePreview
)

type planCaptureModel struct {
	raw           string
	editor        lineEditor
	plan          state.ChangePlan
	mode          planCaptureMode
	decision      planDecision
	status        string
	cursor        int
	width, height int
	nav           string
	viewport      int
}

func newPlanCaptureModel(raw string) *planCaptureModel {
	editor := newLineEditor(raw)
	editor.multiline = true
	return &planCaptureModel{raw: raw, editor: editor}
}

func (model *planCaptureModel) screen() canonicalScreen {
	if model.mode == planCapturePaste {
		return canonicalScreen{Sections: []screenSection{{ID: "plan.capture", Title: "Paste AI response", EmptyReason: "Paste one response containing a fenced spec-plan block."}}}
	}
	items := []screenItem{
		{ID: "plan.capture.accept", Label: "Accept", Selectable: true, Action: screenAction(planAccept)},
		{ID: "plan.capture.edit", Label: "Edit", Selectable: true, Action: screenAction(planEdit)},
		{ID: "plan.capture.skip", Label: "Skip", Selectable: true, Action: screenAction(planSkip)},
	}
	return canonicalScreen{Sections: []screenSection{{ID: "plan.preview", Title: "Plan preview", Items: items}}, Cursor: model.cursor}
}

func (model *planCaptureModel) Init() tea.Cmd { return nil }
func (model *planCaptureModel) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch message := message.(type) {
	case tea.WindowSizeMsg:
		model.width, model.height = message.Width, message.Height
	case tea.PasteMsg:
		if model.mode == planCapturePaste {
			model.editor.insert(message.Content)
			model.raw = model.editor.value
		}
	case tea.KeyPressMsg:
		if model.mode == planCapturePaste {
			model.updatePaste(message)
			return model, nil
		}
		model.updatePreview(message)
	}
	return model, nil
}

func (model *planCaptureModel) updatePaste(message tea.KeyPressMsg) {
	switch message.Keystroke() {
	case "ctrl+enter":
		model.raw = model.editor.value
		block, err := extractPlanBlock(model.raw)
		if err == nil {
			model.plan, err = validateChangePlan(block)
		}
		if err != nil {
			model.status = err.Error()
			return
		}
		model.mode, model.cursor, model.status = planCapturePreview, 0, ""
	case "esc", "b":
		model.decision, model.nav = planSkip, actionBack
	case "q", "ctrl+c":
		model.nav = actionQuit
	default:
		model.editor.key(message)
		model.raw = model.editor.value
	}
}

func (model *planCaptureModel) updatePreview(message tea.KeyPressMsg) {
	switch message.Keystroke() {
	case "up", "k", "shift+tab":
		model.cursor = wrap(model.cursor-1, 3)
	case "down", "j", "tab":
		model.cursor = wrap(model.cursor+1, 3)
	case "pgup":
		model.viewport = max(0, model.viewport-max(1, model.height/2))
	case "pgdown":
		model.viewport += max(1, model.height/2)
	case "enter":
		model.decision = planDecision(model.screen().activate())
		switch model.decision {
		case planEdit:
			model.mode, model.status = planCapturePaste, "Edit the fenced plan, then press Ctrl+Enter to preview again."
			model.editor = newLineEditor(model.raw)
			model.editor.multiline = true
		case planAccept:
			model.nav = actionPlan
		case planSkip:
			model.nav = actionContinue
		}
	}
}

func (model *planCaptureModel) View() tea.View {
	width, height := defaultSize(model.width, model.height)
	if model.mode == planCapturePaste {
		body := strings.Join([]string{uiTitleStyle.Render("Paste AI response"), "", uiMutedStyle.Render("Paste one response containing exactly one fenced spec-plan block."), "", model.editor.view(), "", uiMutedStyle.Render(model.status)}, "\n")
		footer := uiKeyHints([][2]string{{"Ctrl+Enter", "preview"}, {"b", "skip"}, {"q", "exit"}}, "  ")
		body, model.viewport = uiViewportBody(body, uiWorkflowBodyHeight(height, "Implementation Plan · Capture"), model.viewport, "")
		return tea.NewView(uiAppShell(width, height, "Implementation Plan · Capture", body, footer))
	}
	lines := []string{uiTitleStyle.Render("Plan preview"), "", planPreview(model.plan), ""}
	for index, label := range []string{"Accept", "Edit", "Skip"} {
		row := "  [ " + label + " ]"
		if index == model.cursor {
			row = uiSelectedRow("> [ "+label+" ]", 0)
		}
		lines = append(lines, row)
	}
	footer := uiKeyHints([][2]string{{"↑/↓", "select"}, {"enter", "action"}, {"q", "exit"}}, "  ")
	body, next := uiViewportBody(strings.Join(lines, "\n"), uiWorkflowBodyHeight(height, "Implementation Plan · Preview"), model.viewport, model.screen().selectedItemLabel())
	model.viewport = next
	return tea.NewView(uiAppShell(width, height, "Implementation Plan · Preview", body, footer))
}
