package main

import (
	"fmt"
	"io"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/TaylorEdgerton/spec-cli/internal/change"
	promptbuilder "github.com/TaylorEdgerton/spec-cli/internal/prompt"
	"github.com/TaylorEdgerton/spec-cli/internal/state"
)

const (
	definitionIntentID     = "definition.intent"
	definitionScopeID      = "definition.scope"
	definitionAcceptanceID = "definition.acceptance"
	definitionCreateID     = "definition.create"
)

type definitionModel struct {
	setup      state.Setup
	gitState   string
	focus      int
	width      int
	height     int
	done       bool
	cancelled  bool
	created    bool
	editing    bool
	editTarget int
	criterion  int
	editor     lineEditor
	help       bool
	nav        string
	viewport   int
}

func newDefinitionModel(setup state.Setup, gitState string) *definitionModel {
	return &definitionModel{setup: setup, gitState: gitState, editTarget: -1}
}

func (model *definitionModel) screen() canonicalScreen {
	items := []screenItem{
		{ID: definitionIntentID, Label: "Intent", Selectable: true},
		{ID: definitionScopeID, Label: "Scope / expected behaviour", Selectable: true},
		{ID: definitionAcceptanceID, Label: "Acceptance", Selectable: true},
		{ID: definitionCreateID, Label: "Create Spec", Selectable: true, Action: screenAction(actionOverview)},
	}
	return canonicalScreen{Sections: []screenSection{{ID: "definition", Title: "Define Change", Items: items}}, Cursor: model.focus}
}

func (model *definitionModel) Init() tea.Cmd { return nil }

func (model *definitionModel) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch message := message.(type) {
	case tea.WindowSizeMsg:
		model.width, model.height = message.Width, message.Height
		return model, nil
	case tea.InterruptMsg:
		model.done, model.cancelled = true, true
		return model, tea.Quit
	case tea.PasteMsg:
		if model.editing {
			model.editor.insert(message.Content)
		}
		return model, nil
	case tea.KeyPressMsg:
		keystroke := message.Keystroke()
		if keystroke == "ctrl+c" {
			model.leave(actionQuit)
			return model, tea.Quit
		}
		if keystroke == "ctrl+enter" {
			if model.editing {
				model.commitEdit()
			}
			if model.createEnabled() {
				model.done, model.created, model.nav = true, true, actionOverview
				return model, tea.Quit
			}
			return model, nil
		}
		if keystroke == "?" && !model.editing {
			model.help = !model.help
			return model, nil
		}
		if model.editing {
			switch keystroke {
			case "esc":
				model.editing, model.editTarget = false, -1
			case "enter":
				model.commitEdit()
			case "tab":
				model.commitEdit()
				model.focus = wrap(model.focus+1, len(model.screen().selectableItems()))
			default:
				model.editor.key(message)
			}
			return model, nil
		}
		switch keystroke {
		case "esc", "b":
			model.leave(actionBack)
			return model, tea.Quit
		case "tab", "down", "j":
			model.focus = wrap(model.focus+1, len(model.screen().selectableItems()))
		case "shift+tab", "up", "k":
			model.focus = wrap(model.focus-1, len(model.screen().selectableItems()))
		case "a":
			if model.focusedID() == definitionAcceptanceID {
				model.startEdit(-1)
			}
		case "delete", "d":
			if model.focusedID() == definitionAcceptanceID && len(model.setup.Criteria) > 0 {
				model.setup.Criteria = append(model.setup.Criteria[:model.criterion], model.setup.Criteria[model.criterion+1:]...)
				model.criterion = clamp(model.criterion, 0, max(0, len(model.setup.Criteria)-1))
			}
		case "enter":
			switch model.focusedID() {
			case definitionIntentID, definitionScopeID:
				model.startEdit(model.focus)
			case definitionAcceptanceID:
				if len(model.setup.Criteria) == 0 {
					model.startEdit(-1)
				} else {
					model.startEdit(model.criterion + 2)
				}
			case definitionCreateID:
				if model.createEnabled() {
					model.done, model.created, model.nav = true, true, actionOverview
					return model, tea.Quit
				}
			}
		}
	}
	return model, nil
}

func (model *definitionModel) leave(action string) {
	model.done, model.cancelled, model.nav = true, true, action
}

func (model *definitionModel) hints() [][2]string {
	return [][2]string{{"Tab", "field"}, {"Enter", "edit/select"}, {"Ctrl+Enter", "create"},
		{"?", "help"}, {"b", "back"}, {"g", "home"}}
}

func (model *definitionModel) View() tea.View {
	if model.done {
		return tea.NewView("")
	}
	width, height := model.width, model.height
	if width <= 0 {
		width = 100
	}
	if height <= 0 {
		height = 32
	}
	if model.help {
		return tea.NewView(uiAppShell(width, height, uiSplit("Spec · New Change", "Git: "+model.gitState, max(1, width-8)),
			uiHelpOverlay(model.hints()), uiKeyHints(model.hints(), "    ")))
	}
	screen := model.screen()
	items := screen.Sections[0].Items
	inner := max(20, width-8)
	field := func(id, label, value string, target int) string {
		if model.editing && model.editTarget == target {
			value = model.editor.view()
		}
		border := uiBorder
		if model.focusedID() == id {
			border = uiPurple
		}
		return uiTitleStyle.Render(label) + "\n" + uiPanel(inner, 3, border, "", "", value)
	}
	criteria := make([]string, 0, len(model.setup.Criteria)+1)
	for index, criterion := range model.setup.Criteria {
		line := "• " + criterion.Text
		if model.focusedID() == definitionAcceptanceID && index == model.criterion {
			line = uiSelectedRow("> "+criterion.Text, 0)
		}
		criteria = append(criteria, line)
	}
	if model.editing && model.editTarget >= 2 {
		criteria = append(criteria, "> "+model.editor.view())
	} else if len(criteria) == 0 {
		criteria = append(criteria, uiMutedStyle.Render("No acceptance criteria yet; this is optional."))
	}
	createLabel := items[3].Label
	create := "[ " + createLabel + " ]"
	if !model.createEnabled() {
		create = uiMutedStyle.Render("[ " + createLabel + " · intent required ]")
	} else if model.focusedID() == definitionCreateID {
		create = uiSelectedRow(create, 0)
	} else {
		create = uiEvidenceStyle.Render(create)
	}
	body := strings.Join([]string{
		uiTitleStyle.Render(screen.Sections[0].Title),
		field(items[0].ID, items[0].Label, model.setup.Title, 0),
		field(items[1].ID, items[1].Label, model.setup.Outcome, 1),
		uiTitleStyle.Render(items[2].Label) + "\n" + strings.Join(criteria, "\n"),
		strings.Repeat(" ", max(0, inner-len("[ "+createLabel+" ]"))) + create,
	}, "\n\n")
	header := uiSplit("Spec · New Change", "Git: "+model.gitState, max(1, width-8))
	anchor := model.focusedID()
	anchor = map[string]string{definitionIntentID: "Intent", definitionScopeID: "Scope / expected behaviour", definitionAcceptanceID: "Acceptance", definitionCreateID: "Create Spec"}[anchor]
	body, model.viewport = uiViewportBody(body, uiWorkflowBodyHeight(height, header), model.viewport, anchor)
	return tea.NewView(uiAppShell(width, height, header, body, uiKeyHints(model.hints(), "    ")))
}

func (model *definitionModel) focusedID() string {
	items := model.screen().selectableItems()
	return items[clamp(model.focus, 0, len(items)-1)].ID
}

func (model *definitionModel) createEnabled() bool {
	value := model.setup.Title
	if model.editing && model.editTarget == 0 {
		value = model.editor.value
	}
	return strings.TrimSpace(value) != ""
}

func (model *definitionModel) result() state.Setup { return model.setup }

func (model *definitionModel) startEdit(target int) {
	model.editing, model.editTarget = true, target
	value := ""
	switch {
	case target == 0:
		value = model.setup.Title
	case target == 1:
		value = model.setup.Outcome
	case target >= 2 && target-2 < len(model.setup.Criteria):
		value = model.setup.Criteria[target-2].Text
	}
	model.editor = newLineEditor(value)
	model.editor.multiline = target == 1
}

func (model *definitionModel) commitEdit() {
	value := strings.TrimSpace(model.editor.value)
	switch {
	case model.editTarget == 0:
		model.setup.Title = value
	case model.editTarget == 1:
		model.setup.Outcome = value
	case model.editTarget >= 2 && model.editTarget-2 < len(model.setup.Criteria):
		if value != "" {
			model.setup.Criteria[model.editTarget-2].Text = value
		}
	case model.editTarget == -1 && value != "":
		model.setup.Criteria = append(model.setup.Criteria, state.SetupCriterion{Text: value, Included: true})
		model.criterion = len(model.setup.Criteria) - 1
	}
	model.editing, model.editTarget = false, -1
	model.setup.Stage = "definition"
}

type definitionServices struct {
	BuildPrompt func(string) (string, error)
	CopyPrompt  func(string) error
}

func completeDefinition(root string, setup state.Setup, output io.Writer, services definitionServices) (string, error) {
	path, err := saveDefinitionContract(root, setup, output)
	if err != nil {
		return "", err
	}
	if err := deliverDefinitionPrompt(root, output, services); err != nil {
		return path, err
	}
	return path, nil
}

func saveDefinitionContract(root string, setup state.Setup, output io.Writer) (string, error) {
	setup.Stage = "definition"
	path, err := change.SaveSetup(root, setup)
	if err != nil {
		return "", err
	}
	if setup.Editing {
		fmt.Fprintf(output, "Updated active specification: %s\n", path)
	} else {
		printNewCreated(output, path)
	}
	return path, nil
}

func deliverDefinitionPrompt(root string, output io.Writer, services definitionServices) error {
	if services.BuildPrompt == nil {
		services.BuildPrompt = func(root string) (string, error) {
			content, _, err := promptbuilder.Build(root, false)
			return content, err
		}
	}
	if services.CopyPrompt == nil {
		services.CopyPrompt = copyText
	}
	prompt, err := services.BuildPrompt(root)
	if err != nil {
		return err
	}
	if err := services.CopyPrompt(prompt); err == nil {
		fmt.Fprintln(output, "Implementation prompt copied to the clipboard.")
		recordPromptDelivery(root, state.TimelinePromptCopied, promptbuilder.Implementation, "clipboard")
		return nil
	} else {
		fmt.Fprintf(output, "Clipboard unavailable: %v\n", err)
		fmt.Fprintln(output, "Print/copy fallback (retry with `spec prompt --copy`):")
		fmt.Fprintln(output, prompt)
		recordPromptDelivery(root, state.TimelinePromptPrinted, promptbuilder.Implementation, "stdout")
		return nil
	}
}

func recordPromptDelivery(root string, eventType state.TimelineEventType, kind promptbuilder.Kind, source string) {
	workspace, err := state.Load(root)
	if err != nil {
		return
	}
	now := time.Now().UTC()
	_ = workspace.AppendTimeline(state.TimelineEvent{
		SchemaVersion: state.ArtifactSchemaVersion,
		ID:            fmt.Sprintf("%s:prompt:%d", workspace.SpecID, now.UnixNano()),
		Type:          eventType,
		Actor:         "spec",
		Source:        source,
		OccurredAt:    now,
		Details:       state.TimelineDetails{SpecID: workspace.SpecID, PromptKind: string(kind)},
	})
}
