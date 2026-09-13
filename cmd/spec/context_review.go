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
	// Results precede the two actions in canonical navigation order. Continue is
	// deliberately the initial selection so Enter keeps the safe fast path.
	return &contextReviewModel{results: results, cursor: len(results)}
}

func (model *contextReviewModel) screen() canonicalScreen {
	context := make([]screenItem, 0, len(model.results))
	for index, result := range model.results {
		context = append(context, screenItem{ID: fmt.Sprintf("context.result.%d", index), Label: result.Path, Selectable: true, Preview: result})
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
			model.cursor = wrap(model.cursor-1, len(model.results)+2)
		case "down", "j", "tab":
			model.cursor = wrap(model.cursor+1, len(model.results)+2)
		case "pgup":
			model.movePage(-1)
		case "pgdown":
			model.movePage(1)
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
	capacity := model.resultCapacity()
	resultLines, itemLines := model.resultLines()
	selectedResult := -1
	if model.cursor < len(model.results) {
		selectedResult = model.cursor
	}
	selectedLine := -1
	for index, itemIndex := range itemLines {
		if itemIndex == selectedResult {
			selectedLine = index
			break
		}
	}
	visible, next := (screenViewport{Height: capacity, Offset: model.viewport}).visible(resultLines, selectedLine)
	model.viewport = next
	first, last := visibleItemRange(itemLines, next, len(visible))
	rangeText := uiRangeLabel(first, last, len(model.results), "results")
	lines := []string{uiSplit(uiTitleStyle.Render("Likely implementation context"), uiMutedStyle.Render(rangeText), max(20, width-8))}
	if len(model.results) == 0 {
		lines = append(lines, "", uiEmptyState("", "No likely context was discovered; implementation can continue."))
	} else {
		lines = append(lines, visible...)
	}
	lines = append(lines, "")
	for index, label := range []string{"Continue", "Skip"} {
		row := "  [ " + label + " ]"
		if len(model.results)+index == model.cursor {
			row = uiSelectedRow("> [ "+label+" ]", 0)
		}
		lines = append(lines, row)
	}
	footer := uiKeyHints([][2]string{{"↑/↓", "select"}, {"PgUp/PgDn", "page"}, {"enter", "action"}, {"b", "back"}, {"g", "home"}}, "  ")
	body := strings.Join(lines, "\n")
	return tea.NewView(uiAppShell(width, height, "Implementation context", body, footer))
}

func (model *contextReviewModel) resultCapacity() int {
	_, height := defaultSize(model.width, model.height)
	return max(3, uiWorkflowBodyHeight(height, "Implementation context")-5)
}

func (model *contextReviewModel) movePage(direction int) {
	if len(model.results) == 0 {
		return
	}
	step := max(2, model.resultCapacity()/2)
	if model.cursor >= len(model.results) {
		model.cursor = len(model.results) - 1
	}
	model.cursor = clamp(model.cursor+direction*step, 0, len(model.results)-1)
}

func (model *contextReviewModel) resultLines() ([]string, []int) {
	var lines []string
	var itemLines []int
	for index, result := range model.results {
		row := "  " + result.Path
		if model.cursor == index {
			row = uiSelectedRow("> "+result.Path, 0)
		}
		lines = append(lines, row)
		itemLines = append(itemLines, index)
		for _, symbol := range result.Symbols {
			lines = append(lines, fmt.Sprintf("    %s  %s", symbol.Name, uiBadge(string(symbol.Capability), uiTeal)))
			itemLines = append(itemLines, index)
			for _, related := range symbol.Related {
				lines = append(lines, fmt.Sprintf("      ↳ %s · %s", related.Name, related.Relation))
				itemLines = append(itemLines, index)
			}
		}
	}
	return lines, itemLines
}
