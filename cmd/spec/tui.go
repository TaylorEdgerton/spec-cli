package main

import (
	"fmt"
	"io"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/TaylorEdgerton/spec-cli/internal/change"
	"github.com/TaylorEdgerton/spec-cli/internal/state"
)

type lineEditor struct {
	value string
	pos   int
}

func newLineEditor(value string) lineEditor {
	return lineEditor{value: value, pos: len([]rune(value))}
}

func (editor *lineEditor) insert(value string) {
	value = strings.NewReplacer("\r\n", " ", "\r", " ", "\n", " ").Replace(value)
	current, inserted := []rune(editor.value), []rune(value)
	editor.pos = clamp(editor.pos, 0, len(current))
	current = append(current[:editor.pos], append(inserted, current[editor.pos:]...)...)
	editor.value = string(current)
	editor.pos += len(inserted)
}

func (editor *lineEditor) key(message tea.KeyPressMsg) bool {
	current := []rune(editor.value)
	editor.pos = clamp(editor.pos, 0, len(current))
	switch message.Keystroke() {
	case "backspace":
		if editor.pos > 0 {
			editor.value = string(append(current[:editor.pos-1], current[editor.pos:]...))
			editor.pos--
		}
	case "delete":
		if editor.pos < len(current) {
			editor.value = string(append(current[:editor.pos], current[editor.pos+1:]...))
		}
	case "left":
		editor.pos = clamp(editor.pos-1, 0, len(current))
	case "right":
		editor.pos = clamp(editor.pos+1, 0, len(current))
	case "home", "ctrl+a":
		editor.pos = 0
	case "end", "ctrl+e":
		editor.pos = len(current)
	default:
		editor.insert(message.Key().Text)
		return message.Key().Text != ""
	}
	return true
}

func (editor lineEditor) view() string {
	current := []rune(editor.value)
	position := clamp(editor.pos, 0, len(current))
	return string(current[:position]) + "█" + string(current[position:])
}

type textPromptModel struct {
	title       string
	question    string
	optional    bool
	canBack     bool
	editor      lineEditor
	done        bool
	back        bool
	interrupted bool
}

func (model *textPromptModel) Init() tea.Cmd { return nil }

func (model *textPromptModel) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch message := message.(type) {
	case tea.PasteMsg:
		model.editor.insert(message.Content)
	case tea.InterruptMsg:
		model.interrupted, model.done = true, true
		return model, tea.Quit
	case tea.KeyPressMsg:
		switch message.Keystroke() {
		case "ctrl+c", "esc":
			model.interrupted, model.done = true, true
			return model, tea.Quit
		case "shift+tab":
			if model.canBack {
				model.back, model.done = true, true
				return model, tea.Quit
			}
		case "enter":
			if model.optional || strings.TrimSpace(model.editor.value) != "" {
				model.done = true
				return model, tea.Quit
			}
		default:
			model.editor.key(message)
		}
	}
	return model, nil
}

func (model *textPromptModel) View() tea.View {
	if model.done {
		return tea.NewView("")
	}
	help := "Enter continue"
	if model.canBack {
		help += "  Shift+Tab back"
	}
	return tea.NewView(fmt.Sprintf("%s\n\n%s\n> %s\n\n%s  Esc save and exit\n", model.title, model.question, model.editor.view(), help))
}

func runTextPrompt(input io.Reader, output io.Writer, title, question, initial string, optional, canBack bool) (string, bool, bool, error) {
	model := &textPromptModel{title: title, question: question, optional: optional, canBack: canBack, editor: newLineEditor(initial)}
	final, err := tea.NewProgram(model, tea.WithInput(input), tea.WithOutput(output)).Run()
	if err != nil {
		return initial, false, false, err
	}
	result := final.(*textPromptModel)
	return strings.TrimSpace(result.editor.value), result.back, result.interrupted, nil
}

type choiceModel struct {
	title       string
	detail      string
	items       []string
	cursor      int
	selected    int
	done        bool
	interrupted bool
}

func (model *choiceModel) Init() tea.Cmd { return nil }

func (model *choiceModel) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch message := message.(type) {
	case tea.InterruptMsg:
		model.interrupted, model.done = true, true
		return model, tea.Quit
	case tea.KeyPressMsg:
		switch message.Keystroke() {
		case "ctrl+c", "esc":
			model.interrupted, model.done = true, true
			return model, tea.Quit
		case "up", "k":
			model.cursor = wrap(model.cursor-1, len(model.items))
		case "down", "j":
			model.cursor = wrap(model.cursor+1, len(model.items))
		case "enter":
			model.selected, model.done = model.cursor, true
			return model, tea.Quit
		}
	}
	return model, nil
}

func (model *choiceModel) View() tea.View {
	if model.done {
		return tea.NewView("")
	}
	var builder strings.Builder
	builder.WriteString(model.title)
	builder.WriteString("\n")
	if model.detail != "" {
		builder.WriteString("\n")
		builder.WriteString(model.detail)
		builder.WriteString("\n")
	}
	builder.WriteString("\n")
	for index, item := range model.items {
		fmt.Fprintf(&builder, "%s %s\n", cursor(index == model.cursor), item)
	}
	builder.WriteString("\n↑/↓ move  Enter select  Esc exit\n")
	return tea.NewView(builder.String())
}

func runChoice(input io.Reader, output io.Writer, title, detail string, items []string) (int, bool, error) {
	if len(items) == 0 {
		return -1, false, fmt.Errorf("no choices are available")
	}
	model := &choiceModel{title: title, detail: detail, items: items, selected: -1}
	final, err := tea.NewProgram(model, tea.WithInput(input), tea.WithOutput(output)).Run()
	if err != nil {
		return -1, false, err
	}
	result := final.(*choiceModel)
	return result.selected, result.interrupted, nil
}

type setupCriteriaModel struct {
	criteria    []state.SetupCriterion
	cursor      int
	editing     int
	editor      lineEditor
	entering    bool
	done        bool
	back        bool
	interrupted bool
}

func (model *setupCriteriaModel) Init() tea.Cmd { return nil }

func (model *setupCriteriaModel) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	if _, ok := message.(tea.InterruptMsg); ok {
		model.interrupted, model.done = true, true
		return model, tea.Quit
	}
	if paste, ok := message.(tea.PasteMsg); ok && model.entering {
		model.editor.insert(paste.Content)
		return model, nil
	}
	key, ok := message.(tea.KeyPressMsg)
	if !ok {
		return model, nil
	}
	if key.Keystroke() == "ctrl+c" {
		model.interrupted, model.done = true, true
		return model, tea.Quit
	}
	if model.entering {
		switch key.Keystroke() {
		case "esc":
			model.entering, model.editing = false, -1
		case "enter":
			value := strings.TrimSpace(model.editor.value)
			if value != "" {
				if model.editing >= 0 {
					model.criteria[model.editing].Text = value
				} else {
					model.criteria = append(model.criteria, state.SetupCriterion{Text: value, Included: true})
					model.cursor = len(model.criteria) - 1
				}
			}
			model.entering, model.editing = false, -1
		default:
			model.editor.key(key)
		}
		return model, nil
	}
	count := len(model.criteria) + 3
	switch key.Keystroke() {
	case "esc":
		model.interrupted, model.done = true, true
		return model, tea.Quit
	case "up", "k":
		model.cursor = wrap(model.cursor-1, count)
	case "down", "j":
		model.cursor = wrap(model.cursor+1, count)
	case "b":
		model.back, model.done = true, true
		return model, tea.Quit
	case "a":
		model.startEdit(-1)
	case "e":
		if model.cursor < len(model.criteria) {
			model.startEdit(model.cursor)
		}
	case "d", "delete":
		if model.cursor < len(model.criteria) {
			model.criteria = append(model.criteria[:model.cursor], model.criteria[model.cursor+1:]...)
			model.cursor = clamp(model.cursor, 0, len(model.criteria)+1)
		}
	case "space", "enter":
		switch {
		case model.cursor < len(model.criteria):
			model.criteria[model.cursor].Included = !model.criteria[model.cursor].Included
		case model.cursor == len(model.criteria):
			model.startEdit(-1)
		case model.cursor == len(model.criteria)+1:
			model.back, model.done = true, true
			return model, tea.Quit
		default:
			model.done = true
			return model, tea.Quit
		}
	}
	return model, nil
}

func (model *setupCriteriaModel) startEdit(index int) {
	model.entering, model.editing = true, index
	value := ""
	if index >= 0 {
		value = model.criteria[index].Text
	}
	model.editor = newLineEditor(value)
}

func (model *setupCriteriaModel) View() tea.View {
	if model.done {
		return tea.NewView("")
	}
	if model.entering {
		action := "Add criterion"
		if model.editing >= 0 {
			action = "Edit criterion"
		}
		return tea.NewView(fmt.Sprintf("%s\n\n> %s\n\nEnter save  Esc cancel\n", action, model.editor.view()))
	}
	var builder strings.Builder
	builder.WriteString("Success criteria\n\n")
	for index, item := range model.criteria {
		mark := " "
		if item.Included {
			mark = "x"
		}
		fmt.Fprintf(&builder, "%s [%s] %s\n", cursor(index == model.cursor), mark, item.Text)
	}
	add := len(model.criteria)
	fmt.Fprintf(&builder, "%s + Add criterion\n", cursor(model.cursor == add))
	fmt.Fprintf(&builder, "%s ← Back\n", cursor(model.cursor == add+1))
	fmt.Fprintf(&builder, "%s ✓ Accept criteria\n", cursor(model.cursor == add+2))
	builder.WriteString("\n↑/↓ move  Space/Enter select  a add  e edit  d delete  b back  Esc save and exit\n")
	return tea.NewView(builder.String())
}

func runSetupCriteria(input io.Reader, output io.Writer, criteria []state.SetupCriterion) ([]state.SetupCriterion, bool, bool, error) {
	copy := append([]state.SetupCriterion(nil), criteria...)
	model := &setupCriteriaModel{criteria: copy, editing: -1}
	final, err := tea.NewProgram(model, tea.WithInput(input), tea.WithOutput(output)).Run()
	if err != nil {
		return criteria, false, false, err
	}
	result := final.(*setupCriteriaModel)
	return result.criteria, result.back, result.interrupted, nil
}

type reviewCriteriaModel struct {
	criteria    []change.Criterion
	cursor      int
	done        bool
	interrupted bool
}

func (model *reviewCriteriaModel) Init() tea.Cmd { return nil }

func (model *reviewCriteriaModel) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	if _, ok := message.(tea.InterruptMsg); ok {
		model.interrupted, model.done = true, true
		return model, tea.Quit
	}
	key, ok := message.(tea.KeyPressMsg)
	if !ok {
		return model, nil
	}
	switch key.Keystroke() {
	case "ctrl+c", "esc":
		model.interrupted, model.done = true, true
		return model, tea.Quit
	case "up", "k":
		model.cursor = wrap(model.cursor-1, len(model.criteria)+1)
	case "down", "j":
		model.cursor = wrap(model.cursor+1, len(model.criteria)+1)
	case "space", "enter":
		if model.cursor < len(model.criteria) {
			model.criteria[model.cursor].Checked = !model.criteria[model.cursor].Checked
		} else if allCriteriaChecked(model.criteria) {
			model.done = true
			return model, tea.Quit
		}
	}
	return model, nil
}

func (model *reviewCriteriaModel) View() tea.View {
	if model.done {
		return tea.NewView("")
	}
	var builder strings.Builder
	builder.WriteString("Review success criteria\n\n")
	for index, item := range model.criteria {
		mark := " "
		if item.Checked {
			mark = "x"
		}
		fmt.Fprintf(&builder, "%s [%s] %s\n", cursor(index == model.cursor), mark, item.Text)
	}
	continueLabel := "Review all criteria to continue"
	if allCriteriaChecked(model.criteria) {
		continueLabel = "✓ Continue"
	}
	fmt.Fprintf(&builder, "%s %s\n", cursor(model.cursor == len(model.criteria)), continueLabel)
	builder.WriteString("\n↑/↓ move  Space/Enter review  Esc save and exit\n")
	return tea.NewView(builder.String())
}

func runReviewCriteria(input io.Reader, output io.Writer, criteria []change.Criterion) ([]change.Criterion, bool, error) {
	copy := append([]change.Criterion(nil), criteria...)
	model := &reviewCriteriaModel{criteria: copy}
	final, err := tea.NewProgram(model, tea.WithInput(input), tea.WithOutput(output)).Run()
	if err != nil {
		return criteria, false, err
	}
	result := final.(*reviewCriteriaModel)
	return result.criteria, result.interrupted, nil
}

func allCriteriaChecked(criteria []change.Criterion) bool {
	for _, criterion := range criteria {
		if !criterion.Checked {
			return false
		}
	}
	return true
}

func cursor(selected bool) string {
	if selected {
		return ">"
	}
	return " "
}

func wrap(value, size int) int {
	if size <= 0 {
		return 0
	}
	if value < 0 {
		return size - 1
	}
	return value % size
}

func clamp(value, minimum, maximum int) int {
	if value < minimum {
		return minimum
	}
	if value > maximum {
		return maximum
	}
	return value
}
