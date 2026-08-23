package main

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/TaylorEdgerton/spec-cli/internal/change"
	"github.com/TaylorEdgerton/spec-cli/internal/state"
)

type newSpecStage int

const (
	newSpecChoice newSpecStage = iota
	newSpecQuestion
	newSpecCriteria
	newSpecCriterionInput
)

type newSpecAction int

const (
	newSpecSkipped newSpecAction = iota
	newSpecGuided
	newSpecAskAI
)

type criterion struct {
	text     string
	included bool
}

type newSpecModel struct {
	title    string
	askTitle bool
	stage    newSpecStage
	action   newSpecAction
	cursor   int
	question int
	input    string
	inputPos int
	answers  []string
	criteria []criterion
	editing  int
	done     bool
}

var guidedQuestions = []string{
	"Why does this change need to be made?",
	"What should work when you're finished?",
	"What must not break?",
	"Any important constraints?",
	"Relevant files? Separate multiple paths with commas.",
}

func cmdNew(args []string) error {
	interactive := false
	if info, err := os.Stdin.Stat(); err == nil {
		interactive = info.Mode()&os.ModeCharDevice != 0
	}
	return runNew(args, os.Stdin, os.Stdout, interactive)
}

func runNew(args []string, input io.Reader, output io.Writer, interactive bool) error {
	root, err := currentRoot()
	if err != nil {
		return fmt.Errorf("Git repository is required; run `spec init`")
	}
	title := strings.TrimSpace(strings.Join(args, " "))
	path, err := change.New(root, title, time.Now())
	if err != nil {
		return err
	}
	if !interactive {
		printNewCreated(output, path)
		return nil
	}

	program := tea.NewProgram(newNewSpecModel(title), tea.WithInput(input), tea.WithOutput(output))
	final, err := program.Run()
	if err != nil {
		return err
	}
	model, ok := final.(*newSpecModel)
	if !ok {
		return fmt.Errorf("interactive Spec returned an unexpected result")
	}
	switch model.action {
	case newSpecGuided:
		if err := change.Populate(root, model.guidedSpec()); err != nil {
			return err
		}
	case newSpecAskAI:
		prompt := change.AcceptanceCriteriaPrompt()
		workspace, err := state.Load(root)
		if err != nil {
			return err
		}
		if err := workspace.SavePrompt(prompt); err != nil {
			return err
		}
		printNewCreated(output, path)
		fmt.Fprintln(output, "\nAI prompt:")
		fmt.Fprintln(output)
		fmt.Fprint(output, prompt)
		return nil
	}
	printNewCreated(output, path)
	return nil
}

func newNewSpecModel(title string) *newSpecModel {
	title = strings.TrimSpace(title)
	return &newSpecModel{title: title, askTitle: title == "", stage: newSpecChoice, editing: -1}
}

func (model *newSpecModel) Init() tea.Cmd { return nil }

func (model *newSpecModel) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	if _, ok := message.(tea.InterruptMsg); ok {
		model.done, model.action = true, newSpecSkipped
		return model, tea.Quit
	}
	switch message := message.(type) {
	case tea.PasteMsg:
		if model.stage == newSpecQuestion || model.stage == newSpecCriterionInput {
			model.insertInput(message.Content)
		}
		return model, nil
	case tea.KeyPressMsg:
		if message.Keystroke() == "ctrl+c" {
			model.done, model.action = true, newSpecSkipped
			return model, tea.Quit
		}
		switch model.stage {
		case newSpecChoice:
			return model.updateChoice(message)
		case newSpecQuestion:
			return model.updateQuestion(message)
		case newSpecCriteria:
			return model.updateCriteria(message)
		case newSpecCriterionInput:
			return model.updateCriterionInput(message)
		}
	}
	return model, nil
}

func (model *newSpecModel) updateChoice(message tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch message.Keystroke() {
	case "up", "k":
		model.cursor = wrap(model.cursor-1, 3)
	case "down", "j":
		model.cursor = wrap(model.cursor+1, 3)
	case "esc":
		model.action, model.done = newSpecSkipped, true
		return model, tea.Quit
	case "enter":
		switch model.cursor {
		case 0:
			model.stage, model.cursor = newSpecQuestion, 0
		case 1:
			model.action, model.done = newSpecAskAI, true
			return model, tea.Quit
		case 2:
			model.action, model.done = newSpecSkipped, true
			return model, tea.Quit
		}
	}
	return model, nil
}

func (model *newSpecModel) updateQuestion(message tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch message.Keystroke() {
	case "esc":
		model.action, model.done = newSpecSkipped, true
		return model, tea.Quit
	case "enter":
		model.answers = append(model.answers, strings.TrimSpace(model.input))
		if model.askTitle && model.question == 0 {
			model.title = strings.TrimSpace(model.input)
		}
		model.input, model.inputPos = "", 0
		model.question++
		if model.question == len(model.questionPrompts()) {
			model.prepareCriteria()
		}
	case "backspace":
		model.deleteBeforeCursor()
	case "delete":
		model.deleteAtCursor()
	case "left":
		model.moveInputCursor(-1)
	case "right":
		model.moveInputCursor(1)
	case "home", "ctrl+a":
		model.inputPos = 0
	case "end", "ctrl+e":
		model.inputPos = len([]rune(model.input))
	default:
		model.insertInput(message.Key().Text)
	}
	return model, nil
}

func (model *newSpecModel) updateCriteria(message tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	itemCount := len(model.criteria) + 2
	switch message.Keystroke() {
	case "up", "k":
		model.cursor = wrap(model.cursor-1, itemCount)
	case "down", "j":
		model.cursor = wrap(model.cursor+1, itemCount)
	case "a":
		model.startCriterionInput(-1)
	case "e":
		if model.cursor < len(model.criteria) {
			model.startCriterionInput(model.cursor)
		}
	case "d", "delete":
		if model.cursor < len(model.criteria) {
			model.criteria = append(model.criteria[:model.cursor], model.criteria[model.cursor+1:]...)
			if model.cursor >= len(model.criteria)+2 {
				model.cursor = len(model.criteria) + 1
			}
		}
	case "esc":
		model.action, model.done = newSpecSkipped, true
		return model, tea.Quit
	case "space", "enter":
		switch {
		case model.cursor < len(model.criteria):
			model.criteria[model.cursor].included = !model.criteria[model.cursor].included
		case model.cursor == len(model.criteria):
			model.startCriterionInput(-1)
		default:
			model.action, model.done = newSpecGuided, true
			return model, tea.Quit
		}
	}
	return model, nil
}

func (model *newSpecModel) updateCriterionInput(message tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch message.Keystroke() {
	case "esc":
		model.stage, model.input, model.inputPos, model.editing = newSpecCriteria, "", 0, -1
	case "enter":
		value := strings.TrimSpace(model.input)
		if value != "" {
			if model.editing >= 0 {
				model.criteria[model.editing].text = value
				model.cursor = model.editing
			} else {
				model.criteria = append(model.criteria, criterion{text: value, included: true})
				model.cursor = len(model.criteria) - 1
			}
		}
		model.stage, model.input, model.inputPos, model.editing = newSpecCriteria, "", 0, -1
	case "backspace":
		model.deleteBeforeCursor()
	case "delete":
		model.deleteAtCursor()
	case "left":
		model.moveInputCursor(-1)
	case "right":
		model.moveInputCursor(1)
	case "home", "ctrl+a":
		model.inputPos = 0
	case "end", "ctrl+e":
		model.inputPos = len([]rune(model.input))
	default:
		model.insertInput(message.Key().Text)
	}
	return model, nil
}

func (model *newSpecModel) prepareCriteria() {
	model.stage, model.cursor = newSpecCriteria, 0
	for _, value := range []string{model.answer(1), model.answer(2)} {
		if value = strings.TrimSpace(value); value != "" {
			model.criteria = append(model.criteria, criterion{text: value, included: true})
		}
	}
}

func (model *newSpecModel) startCriterionInput(index int) {
	model.stage, model.editing, model.input, model.inputPos = newSpecCriterionInput, index, "", 0
	if index >= 0 {
		model.input = model.criteria[index].text
		model.inputPos = len([]rune(model.input))
	}
}

func (model *newSpecModel) guidedSpec() change.GuidedSpec {
	criteria := make([]string, 0, len(model.criteria))
	for _, item := range model.criteria {
		if item.included {
			criteria = append(criteria, item.text)
		}
	}
	return change.GuidedSpec{
		Change: model.title, Reason: model.answer(0), Outcome: model.answer(1),
		MustNotBreak: model.answer(2), ImportantConstraint: model.answer(3),
		RelevantFiles: splitPaths(model.answer(4)), AcceptanceCriteria: criteria,
	}
}

func (model *newSpecModel) answer(index int) string {
	if model.askTitle {
		index++
	}
	if index < 0 || index >= len(model.answers) {
		return ""
	}
	return model.answers[index]
}

func (model *newSpecModel) View() tea.View {
	if model.done {
		return tea.NewView("")
	}
	switch model.stage {
	case newSpecChoice:
		return tea.NewView(model.choiceView())
	case newSpecQuestion:
		return tea.NewView(model.questionView())
	case newSpecCriteria:
		return tea.NewView(model.criteriaView())
	case newSpecCriterionInput:
		return tea.NewView(model.criterionInputView())
	default:
		return tea.NewView("")
	}
}

func (model *newSpecModel) choiceView() string {
	items := []string{"Add them myself", "Ask AI to suggest them", "Skip for now"}
	var builder strings.Builder
	builder.WriteString("Acceptance criteria are not defined.\n\n")
	for index, item := range items {
		fmt.Fprintf(&builder, "%s %s\n", cursor(index == model.cursor), item)
	}
	builder.WriteString("\n↑/↓ move  Enter select  Esc skip\n")
	return builder.String()
}

func (model *newSpecModel) questionView() string {
	var builder strings.Builder
	builder.WriteString("New Spec\n\n")
	if !model.askTitle || model.question > 0 {
		builder.WriteString("What are you changing?\n> ")
		if model.title == "" {
			builder.WriteString("Change")
		} else {
			builder.WriteString(model.title)
		}
		builder.WriteString("\n\n")
	}
	fmt.Fprintf(&builder, "%s\n> %s\n\nEnter continue  Esc skip\n", model.questionPrompts()[model.question], model.inputView())
	return builder.String()
}

func (model *newSpecModel) criteriaView() string {
	var builder strings.Builder
	builder.WriteString("Acceptance criteria\n\nBased on your answers:\n\n")
	for index, item := range model.criteria {
		checked := " "
		if item.included {
			checked = "x"
		}
		fmt.Fprintf(&builder, "%s [%s] %s\n", cursor(index == model.cursor), checked, item.text)
	}
	addIndex := len(model.criteria)
	fmt.Fprintf(&builder, "%s + Add criterion\n", cursor(model.cursor == addIndex))
	fmt.Fprintf(&builder, "%s ✓ Accept and save\n", cursor(model.cursor == addIndex+1))
	builder.WriteString("\n↑/↓ move  Space/Enter toggle  a add  e edit  d delete  Esc skip\n")
	return builder.String()
}

func (model *newSpecModel) criterionInputView() string {
	action := "Add criterion"
	if model.editing >= 0 {
		action = "Edit criterion"
	}
	return fmt.Sprintf("%s\n\n> %s\n\nEnter save  Esc cancel\n", action, model.inputView())
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

func (model *newSpecModel) questionPrompts() []string {
	if !model.askTitle {
		return guidedQuestions
	}
	return append([]string{"What are you changing?"}, guidedQuestions...)
}

func (model *newSpecModel) insertInput(value string) {
	value = strings.NewReplacer("\r\n", " ", "\r", " ", "\n", " ").Replace(value)
	if value == "" {
		return
	}
	current, inserted := []rune(model.input), []rune(value)
	model.inputPos = clamp(model.inputPos, 0, len(current))
	current = append(current[:model.inputPos], append(inserted, current[model.inputPos:]...)...)
	model.input = string(current)
	model.inputPos += len(inserted)
}

func (model *newSpecModel) moveInputCursor(offset int) {
	model.inputPos = clamp(model.inputPos+offset, 0, len([]rune(model.input)))
}

func (model *newSpecModel) deleteBeforeCursor() {
	current := []rune(model.input)
	model.inputPos = clamp(model.inputPos, 0, len(current))
	if model.inputPos == 0 {
		return
	}
	model.input = string(append(current[:model.inputPos-1], current[model.inputPos:]...))
	model.inputPos--
}

func (model *newSpecModel) deleteAtCursor() {
	current := []rune(model.input)
	model.inputPos = clamp(model.inputPos, 0, len(current))
	if model.inputPos == len(current) {
		return
	}
	model.input = string(append(current[:model.inputPos], current[model.inputPos+1:]...))
}

func (model *newSpecModel) inputView() string {
	current := []rune(model.input)
	position := clamp(model.inputPos, 0, len(current))
	return string(current[:position]) + "█" + string(current[position:])
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

func splitPaths(value string) []string {
	parts := strings.FieldsFunc(value, func(r rune) bool { return r == ',' || r == ';' })
	paths := make([]string, 0, len(parts))
	for _, path := range parts {
		if path = strings.TrimSpace(path); path != "" {
			paths = append(paths, path)
		}
	}
	return paths
}

func printNewCreated(output io.Writer, path string) {
	fmt.Fprintf(output, "Created active specification: %s\n", path)
	fmt.Fprintln(output, "Edit the specification, then run `spec prompt`.")
}
