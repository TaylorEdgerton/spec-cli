package main

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/TaylorEdgerton/spec-cli/internal/discovery"
)

const (
	actionContextReview = "context-review"
	actionPlanCapture   = "plan-capture"
	actionContinue      = "continue"
)

type contextReviewModel struct {
	results       []discovery.Result
	cursor        int
	width, height int
	nav           string
	viewport      int
}

func newContextReviewModel(results []discovery.Result) *contextReviewModel {
	return &contextReviewModel{results: results}
}

func (model *contextReviewModel) screen() canonicalScreen {
	context := make([]screenItem, 0, len(model.results))
	for index, result := range model.results {
		context = append(context, screenItem{ID: fmt.Sprintf("context.result.%d", index), Label: result.Path, Preview: result})
	}
	actions := []screenItem{
		{ID: "context.continue", Label: "Continue", Selectable: true, Action: screenAction(actionContinue)},
		{ID: "context.skip", Label: "Skip", Selectable: true, Action: screenAction(actionContinue)},
	}
	return canonicalScreen{Sections: []screenSection{
		{ID: "context.results", Title: "Likely implementation context", Items: context, EmptyReason: "No likely context was discovered; implementation can continue."},
		{ID: "context.actions", Title: "Actions", Items: actions},
	}, Cursor: model.cursor}
}

func (model *contextReviewModel) Init() tea.Cmd { return nil }
func (model *contextReviewModel) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch message := message.(type) {
	case tea.WindowSizeMsg:
		model.width, model.height = message.Width, message.Height
	case tea.KeyPressMsg:
		switch message.Keystroke() {
		case "up", "k", "shift+tab":
			model.cursor = wrap(model.cursor-1, 2)
		case "down", "j", "tab":
			model.cursor = wrap(model.cursor+1, 2)
		case "pgup":
			model.viewport = max(0, model.viewport-max(1, model.height/2))
		case "pgdown":
			model.viewport += max(1, model.height/2)
		case "enter":
			model.nav = string(model.screen().activate())
		case "b", "esc":
			model.nav = actionBack
		case "ctrl+c":
			model.nav = actionQuit
		}
	}
	return model, nil
}

func (model *contextReviewModel) View() tea.View {
	width, height := defaultSize(model.width, model.height)
	lines := []string{uiTitleStyle.Render("Likely implementation context")}
	if len(model.results) == 0 {
		lines = append(lines, "", uiEmptyState("", "No likely context was discovered; implementation can continue."))
	}
	for _, result := range model.results {
		lines = append(lines, "", "  "+result.Path)
		for _, symbol := range result.Symbols {
			lines = append(lines, fmt.Sprintf("    %s  %s", symbol.Name, uiBadge(string(symbol.Capability), uiTeal)))
			for _, related := range symbol.Related {
				lines = append(lines, fmt.Sprintf("      ↳ %s · %s", related.Name, related.Relation))
			}
		}
	}
	lines = append(lines, "")
	for index, label := range []string{"Continue", "Skip"} {
		row := "  [ " + label + " ]"
		if index == model.cursor {
			row = uiSelectedRow("> [ "+label+" ]", 0)
		}
		lines = append(lines, row)
	}
	footer := uiKeyHints([][2]string{{"↑/↓", "select"}, {"enter", "action"}, {"b", "back"}, {"g", "home"}}, "  ")
	body, next := uiViewportBody(strings.Join(lines, "\n"), uiWorkflowBodyHeight(height, "Implementation context"), model.viewport, model.screen().selectedItemLabel())
	model.viewport = next
	return tea.NewView(uiAppShell(width, height, "Implementation context", body, footer))
}
