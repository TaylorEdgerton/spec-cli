package main

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/TaylorEdgerton/spec-cli/internal/gitutil"
	"github.com/TaylorEdgerton/spec-cli/internal/state"
)

const (
	homeMinWidth  = 64
	homeMinHeight = 18
)

type homeData struct {
	GitWorkspace bool
	Registered   bool
	Active       bool
	Title        string
	Stage        string
	Branch       string
	GitState     string
	StartedAt    time.Time
	Now          time.Time
}

type homeModel struct {
	data          homeData
	cursor        int
	width, height int
	help          bool
	nav           string
	status        string
}

func newHomeModel(data homeData) *homeModel {
	if data.Now.IsZero() {
		data.Now = time.Now()
	}
	return &homeModel{data: data}
}

func loadHomeData(root string, now time.Time) homeData {
	data := homeData{GitWorkspace: root != "", Now: now, GitState: "unknown"}
	if root == "" {
		return data
	}
	data.Branch, _ = gitutil.Branch(root)
	if status, err := gitutil.Status(root); err == nil {
		data.GitState = "clean"
		lines := strings.Split(strings.TrimSpace(status), "\n")
		if len(lines) > 1 || (len(lines) == 1 && !strings.HasPrefix(lines[0], "##")) {
			data.GitState = "dirty"
		}
	}
	workspace, err := state.Load(root)
	if err != nil {
		return data
	}
	data.Registered = true
	data.Active = workspace.Active
	data.Title = workspace.Title
	data.StartedAt = workspace.StartedAt
	data.Stage = homeStage(&workspace)
	if data.GitState == "unknown" && workspace.GitState != "" {
		data.GitState = workspace.GitState
	}
	return data
}

func homeStage(workspace *state.Workspace) string {
	if !workspace.Active {
		return "No active change"
	}
	if workspace.Setup != nil {
		return "Intent & Scope"
	}
	events, _ := workspace.TimelineEvents()
	for index := len(events) - 1; index >= 0; index-- {
		switch events[index].Type {
		case state.TimelineReviewDecision, state.TimelineActualRefreshed:
			return "Review"
		}
	}
	return "Implementation"
}

func (model *homeModel) screen() canonicalScreen {
	var items []screenItem
	if !model.data.GitWorkspace || !model.data.Registered {
		items = []screenItem{
			{ID: "home.initialize", Label: "Initialize workspace", Selectable: true, Action: screenAction(actionInitialize)},
			{ID: "home.exit", Label: "Exit", Selectable: true, Action: screenAction(actionQuit)},
		}
	} else {
		primary := screenItem{ID: "home.new", Label: "Create a change", Selectable: true, Action: screenAction(actionNew)}
		if model.data.Active {
			primary = screenItem{ID: "home.resume", Label: "Resume change", Selectable: true, Action: screenAction(actionResume)}
		}
		items = []screenItem{
			primary,
			{ID: "home.explore", Label: "Explore codebase", Selectable: true, Action: screenAction(actionExplore)},
			{ID: "home.recent", Label: "Recent changes", Selectable: true, Action: screenAction(actionRecent)},
			{ID: "home.documents", Label: "Create a doc", Selectable: true, Action: screenAction(actionDocuments)},
			{ID: "home.exit", Label: "Exit", Selectable: true, Action: screenAction(actionQuit)},
		}
	}
	return canonicalScreen{Sections: []screenSection{{ID: "home.actions", Title: "Actions", Items: items}}, Cursor: model.cursor}
}

func (model *homeModel) Init() tea.Cmd { return nil }

func (model *homeModel) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch message := message.(type) {
	case tea.WindowSizeMsg:
		model.width, model.height = message.Width, message.Height
	case tea.InterruptMsg:
		model.nav = actionQuit
		return model, tea.Quit
	case tea.KeyPressMsg:
		switch message.Keystroke() {
		case "up", "k":
			model.screenMove(-1)
		case "down", "j":
			model.screenMove(1)
		case "enter":
			model.nav = string(model.screen().activate())
		case "?":
			model.help = !model.help
		case "q", "ctrl+c":
			model.nav = actionQuit
		case "esc", "b", "g":
			// Home is the root. These keys deliberately do not navigate or mutate.
		}
	}
	return model, nil
}

func (model *homeModel) screenMove(delta int) {
	screen := model.screen()
	screen.move(delta)
	model.cursor = screen.Cursor
}

func (model *homeModel) View() tea.View {
	width, height := defaultSize(model.width, model.height)
	if width < homeMinWidth || height < homeMinHeight {
		return tea.NewView(uiMinimumSize(width, height))
	}
	header := uiSplit("Spec", strings.Trim(strings.Join([]string{model.data.Branch, model.data.GitState}, " · "), " ·"), max(1, width-4))
	body := model.body()
	if model.help {
		body = uiHelpOverlay(model.hints())
	}
	return tea.NewView(uiAppShell(width, height, header, body, uiKeyHints(model.hints(), "     ")))
}

func (model *homeModel) body() string {
	var lines []string
	switch {
	case !model.data.GitWorkspace:
		lines = []string{uiTitleStyle.Render("Workspace"), uiMutedStyle.Render("This directory is not a Git workspace.")}
	case !model.data.Registered:
		lines = []string{uiTitleStyle.Render("Workspace"), uiMutedStyle.Render("This Git workspace is not registered with Spec.")}
	case model.data.Active:
		lines = []string{uiTitleStyle.Render("Active change"), "", emptyAs(model.data.Title, "Untitled change"),
			fmt.Sprintf("Status   %s", emptyDash(model.data.Stage)),
			fmt.Sprintf("Started  %s", homeElapsed(model.data.StartedAt, model.data.Now))}
	default:
		lines = []string{uiTitleStyle.Render("No active change"), "", uiMutedStyle.Render("Create a change or explore the workspace without changing workflow state.")}
	}
	lines = append(lines, "")
	screen := model.screen()
	selected, _ := screen.selectedItem()
	for _, item := range screen.Sections[0].Items {
		line := "  " + item.Label
		if item.ID == selected.ID {
			line = uiSelectedRow("> "+item.Label, 0)
		}
		lines = append(lines, line)
	}
	if model.status != "" {
		lines = append(lines, "", uiMutedStyle.Render(model.status))
	}
	return strings.Join(lines, "\n")
}

func (model *homeModel) hints() [][2]string {
	return [][2]string{{"↑/↓", "navigate"}, {"enter", "select"}, {"esc", "stays"}, {"?", "help"}, {"q", "exit"}}
}

func homeElapsed(start, now time.Time) string {
	if start.IsZero() || now.Before(start) {
		return "—"
	}
	duration := now.Sub(start).Round(time.Minute)
	if duration < time.Minute {
		return "just now"
	}
	hours, minutes := int(duration/time.Hour), int(duration%time.Hour/time.Minute)
	if hours > 0 && minutes > 0 {
		return fmt.Sprintf("%dh %dm ago", hours, minutes)
	}
	if hours > 0 {
		return fmt.Sprintf("%dh ago", hours)
	}
	return fmt.Sprintf("%dm ago", minutes)
}
