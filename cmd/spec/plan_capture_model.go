package main

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/TaylorEdgerton/spec-cli/internal/state"
)

type planCaptureMode int

const (
	planCaptureChoice planCaptureMode = iota
	planCaptureClipboardPreview
	planCaptureClipboardError
	planCapturePaste
	planCaptureCLIWait
)

type planCaptureModel struct {
	raw               string
	amend             bool
	editor            lineEditor
	plan              state.ChangePlan
	mode              planCaptureMode
	decision          planDecision
	status            string
	cursor            int
	width, height     int
	nav               string
	viewport          int
	acceptedPersisted bool

	readClipboard  func() (string, error)
	reloadPlan     func() (*state.StoredChangePlan, error)
	copyPlanPrompt func() error
}

func newPlanCaptureModel(raw string) *planCaptureModel {
	editor := newLineEditor(raw)
	editor.multiline = true
	return &planCaptureModel{raw: raw, editor: editor, mode: planCaptureChoice, readClipboard: readClipboardText}
}

func (model *planCaptureModel) screen() canonicalScreen {
	var section screenSection
	switch model.mode {
	case planCaptureChoice:
		section = screenSection{ID: "plan.choice", Title: "AI Plan", Items: []screenItem{
			{ID: "plan.choice.prompt", Label: "Copy planning prompt", Selectable: true},
			{ID: "plan.choice.paste", Label: "Paste AI plan", Selectable: true},
			{ID: "plan.choice.skip", Label: "Continue without plan", Selectable: true},
		}}
	case planCaptureClipboardPreview:
		section = screenSection{ID: "plan.preview", Title: "Plan preview", Items: []screenItem{
			{ID: "plan.preview.accept", Label: "Accept Plan", Selectable: true, Action: screenAction(planAccept)},
			{ID: "plan.preview.paste", Label: "Paste different response", Selectable: true},
			{ID: "plan.preview.edit", Label: "Inspect/Edit", Selectable: true, Action: screenAction(planEdit)},
			{ID: "plan.preview.skip", Label: "Skip", Selectable: true, Action: screenAction(planSkip)},
		}}
	case planCaptureClipboardError:
		section = screenSection{ID: "plan.clipboard.error", Title: "Clipboard plan unavailable", Items: []screenItem{
			{ID: "plan.error.manual", Label: "Paste response manually", Selectable: true},
			{ID: "plan.error.retry", Label: "Try clipboard again", Selectable: true},
			{ID: "plan.error.back", Label: "Back to plan choices", Selectable: true},
			{ID: "plan.error.skip", Label: "Skip plan for this change", Selectable: true, Action: screenAction(planSkip)},
		}}
	case planCaptureCLIWait:
		section = screenSection{ID: "plan.cli", Title: "CLI plan submission", Items: []screenItem{
			{ID: "plan.cli.continue", Label: "Continue without a plan", Selectable: true, Action: screenAction(planSkip)},
		}}
	default:
		section = screenSection{ID: "plan.capture", Title: "Paste AI response", EmptyReason: "Paste one response containing a fenced spec-plan block."}
	}
	return canonicalScreen{Sections: []screenSection{section}, Cursor: model.cursor}
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
		} else if model.mode == planCaptureChoice || model.mode == planCaptureClipboardError {
			model.raw = message.Content
			model.previewRaw(message.Content)
		}
	case tea.KeyPressMsg:
		switch model.mode {
		case planCaptureChoice:
			model.updateChoice(message)
		case planCaptureClipboardPreview:
			model.updatePreview(message)
		case planCaptureClipboardError:
			model.updateClipboardError(message)
		case planCapturePaste:
			model.updatePaste(message)
		case planCaptureCLIWait:
			model.updateCLIWait(message)
		}
	}
	return model, nil
}

func (model *planCaptureModel) updateChoice(message tea.KeyPressMsg) {
	switch message.Keystroke() {
	case "up", "k", "shift+tab":
		model.cursor = wrap(model.cursor-1, 3)
	case "down", "j", "tab":
		model.cursor = wrap(model.cursor+1, 3)
	case "p":
		model.copyPlanningPrompt()
	case "r":
		model.refreshCLIPlan()
	case "v":
		model.importClipboard()
	case "enter":
		switch model.cursor {
		case 0:
			model.copyPlanningPrompt()
		case 1:
			model.openManualPaste(model.raw, "Paste the agent response, then Ctrl+Enter to preview.")
		case 2:
			model.decision, model.nav = planSkip, actionContinue
		}
	case "b", "esc":
		model.nav = actionBack
	case "ctrl+c":
		model.nav = actionQuit
	}
}

func (model *planCaptureModel) copyPlanningPrompt() {
	if model.copyPlanPrompt == nil {
		model.status = "Plan prompt is unavailable. Run `spec prompt --plan`."
	} else if err := model.copyPlanPrompt(); err != nil {
		model.status = "Clipboard unavailable: " + err.Error() + " (run `spec prompt --plan`)"
	} else {
		model.status = "Planning prompt copied. Paste into your agent chat; return here with its response."
	}
}

func (model *planCaptureModel) importClipboard() {
	if model.readClipboard == nil {
		model.readClipboard = readClipboardText
	}
	raw, err := model.readClipboard()
	if err != nil {
		model.mode, model.cursor, model.status = planCaptureClipboardError, 0, err.Error()
		return
	}
	model.raw = raw
	model.previewRaw(raw)
}

func (model *planCaptureModel) previewRaw(raw string) {
	model.raw = raw
	block, err := extractPlanBlock(raw)
	if err == nil {
		model.plan, err = validateChangePlan(block)
	}
	if err != nil {
		model.mode, model.cursor, model.status = planCaptureClipboardError, 0, err.Error()
		return
	}
	model.mode, model.cursor, model.status = planCaptureClipboardPreview, 0, ""
	model.decision, model.acceptedPersisted = "", false
}

func (model *planCaptureModel) openManualPaste(raw, status string) {
	model.mode, model.cursor, model.status = planCapturePaste, 0, status
	model.raw = raw
	model.editor = newLineEditor(raw)
	model.editor.multiline = true
}

func (model *planCaptureModel) updateClipboardError(message tea.KeyPressMsg) {
	switch message.Keystroke() {
	case "up", "k", "shift+tab":
		model.cursor = wrap(model.cursor-1, 4)
	case "down", "j", "tab":
		model.cursor = wrap(model.cursor+1, 4)
	case "enter":
		switch model.cursor {
		case 0:
			model.openManualPaste(model.raw, "Correct the response, then press Ctrl+Enter to preview.")
		case 1:
			model.importClipboard()
		case 2:
			model.mode, model.cursor, model.status = planCaptureChoice, 0, ""
		case 3:
			model.decision, model.nav = planSkip, actionContinue
		}
	case "b", "esc":
		model.mode, model.cursor, model.status = planCaptureChoice, 0, ""
	case "ctrl+c":
		model.nav = actionQuit
	}
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
		model.mode, model.cursor, model.status = planCaptureClipboardPreview, 0, ""
		model.decision, model.acceptedPersisted = "", false
	case "esc":
		model.mode, model.cursor, model.status = planCaptureChoice, 0, ""
	case "ctrl+c":
		model.nav = actionQuit
	default:
		model.editor.key(message)
		model.raw = model.editor.value
	}
}

func (model *planCaptureModel) updatePreview(message tea.KeyPressMsg) {
	switch message.Keystroke() {
	case "up", "k", "shift+tab":
		model.cursor = wrap(model.cursor-1, 4)
	case "down", "j", "tab":
		model.cursor = wrap(model.cursor+1, 4)
	case "pgup":
		model.viewport = max(0, model.viewport-max(1, model.height/2))
	case "pgdown":
		model.viewport += max(1, model.height/2)
	case "e":
		model.decision = planEdit
		model.openManualPaste(model.raw, "Edit the fenced plan, then press Ctrl+Enter to preview again.")
	case "enter":
		switch model.cursor {
		case 0:
			model.decision, model.nav = planAccept, actionPlan
		case 1:
			model.decision = planEdit
			model.openManualPaste("", "Paste a different response, then press Ctrl+Enter to preview.")
		case 2:
			model.decision = planEdit
			model.openManualPaste(model.raw, "Edit the fenced plan, then press Ctrl+Enter to preview again.")
		case 3:
			model.decision, model.nav = planSkip, actionContinue
		}
	case "b", "esc":
		model.mode, model.cursor, model.status = planCaptureChoice, 0, ""
	case "ctrl+c":
		model.nav = actionQuit
	}
}

func (model *planCaptureModel) updateCLIWait(message tea.KeyPressMsg) {
	switch message.Keystroke() {
	case "r":
		model.refreshCLIPlan()
	case "enter":
		model.decision, model.nav = planSkip, actionContinue
	case "b", "esc":
		model.mode, model.cursor, model.status = planCaptureChoice, 0, ""
	case "ctrl+c":
		model.nav = actionQuit
	}
}

func (model *planCaptureModel) refreshCLIPlan() {
	if model.reloadPlan == nil {
		model.status = "Plan state cannot be refreshed."
		return
	}
	stored, err := model.reloadPlan()
	if err != nil {
		model.status = err.Error()
		return
	}
	if stored == nil || stored.Source != state.PlanSourceCLI {
		model.status = "No submitted plan detected yet."
		return
	}
	canonical, err := canonicalPlanBytes(stored.Plan)
	if err != nil {
		model.status = err.Error()
		return
	}
	validated, err := validateChangePlan(canonical)
	if err != nil {
		model.status = err.Error()
		return
	}
	model.plan = validated
	model.raw = "```spec-plan\n" + string(canonical) + "\n```"
	model.mode, model.cursor, model.status = planCaptureClipboardPreview, 0, "CLI-submitted plan detected and validated."
	model.decision, model.acceptedPersisted = "", true
}

func (model *planCaptureModel) View() tea.View {
	width, height := defaultSize(model.width, model.height)
	var title, body, footer string
	switch model.mode {
	case planCaptureChoice:
		title = "AI Plan · Optional"
		lines := []string{uiTitleStyle.Render("AI Plan"), "", "No plan captured.", ""}
		if model.raw != "" {
			lines[2] = "Update the current plan by paste or agent submission."
		}
		lines = append(lines, selectablePlanRows([]string{"Copy planning prompt", "Paste AI plan", "Continue without plan"}, model.cursor)...)
		lines = append(lines, "", uiMutedStyle.Render("Paste the prompt into your agent chat; copy its response back here."), uiMutedStyle.Render("Agent submitted via spec plan submit --stdin? Press r to refresh."), "", uiMutedStyle.Render(model.status))
		body = strings.Join(lines, "\n")
		footer = uiKeyHints([][2]string{{"↑/↓", "select"}, {"enter", "continue"}, {"r", "refresh"}, {"v", "clipboard"}, {"b", "back"}, {"g", "home"}}, "  ")
	case planCaptureClipboardPreview:
		title = "AI Plan · Clipboard"
		detected := "✓ spec-plan detected on clipboard"
		if model.acceptedPersisted {
			detected = "✓ CLI-submitted plan detected"
		}
		lines := []string{uiTitleStyle.Render(detected), "", planPreview(model.plan), ""}
		labels := []string{"Accept Plan", "Paste different response", "Inspect/Edit", "Skip"}
		if model.acceptedPersisted {
			labels[0] = "Continue to Plan"
		}
		lines = append(lines, selectablePlanRows(labels, model.cursor)...)
		lines = append(lines, "", uiMutedStyle.Render(model.status))
		body = strings.Join(lines, "\n")
		footer = uiKeyHints([][2]string{{"↑/↓", "select"}, {"enter", "action"}, {"e", "inspect/edit"}, {"b", "back"}, {"g", "home"}}, "  ")
	case planCaptureClipboardError:
		title = "AI Plan · Clipboard"
		lines := []string{uiTitleStyle.Render("No valid spec-plan was detected"), "", uiMutedStyle.Render(model.status), ""}
		lines = append(lines, selectablePlanRows([]string{"Paste response manually", "Try clipboard again", "Back to plan choices", "Skip plan for this change"}, model.cursor)...)
		body = strings.Join(lines, "\n")
		footer = uiKeyHints([][2]string{{"↑/↓", "select"}, {"enter", "continue"}, {"b", "back"}, {"g", "home"}}, "  ")
	case planCapturePaste:
		title = "AI Plan · Manual Paste"
		body = strings.Join([]string{uiTitleStyle.Render("Paste AI response"), "", uiMutedStyle.Render("Paste one response containing exactly one fenced spec-plan block."), "", model.editor.view(), "", uiMutedStyle.Render(model.status)}, "\n")
		footer = uiKeyHints([][2]string{{"Ctrl+Enter", "preview"}, {"esc", "back"}}, "  ")
	case planCaptureCLIWait:
		title = "AI Plan · CLI"
		body = strings.Join([]string{uiTitleStyle.Render("Waiting for an implementation plan..."), "", "Ask your AI tool to run:", "", "  spec plan submit --stdin", "", "Spec can remain open. Press r to reload durable workspace state.", "", uiMutedStyle.Render(model.status), "", uiSelectedRow("> Continue without a plan", 0)}, "\n")
		footer = uiKeyHints([][2]string{{"r", "refresh"}, {"enter", "continue"}, {"b", "back"}, {"g", "home"}}, "  ")
	default:
		panic(fmt.Sprintf("unknown plan capture mode %d", model.mode))
	}
	body, model.viewport = uiViewportBody(body, uiWorkflowBodyHeight(height, title), model.viewport, model.screen().selectedItemLabel())
	return tea.NewView(uiAppShell(width, height, title, body, footer))
}

func selectablePlanRows(labels []string, cursor int) []string {
	rows := make([]string, 0, len(labels))
	for index, label := range labels {
		row := "  " + label
		if index == cursor {
			row = uiSelectedRow("> "+label, 0)
		}
		rows = append(rows, row)
	}
	return rows
}
