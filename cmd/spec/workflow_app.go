package main

import (
	"bytes"
	"fmt"
	"io"
	"reflect"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/TaylorEdgerton/spec-cli/internal/change"
	"github.com/TaylorEdgerton/spec-cli/internal/discovery"
	"github.com/TaylorEdgerton/spec-cli/internal/state"
)

type workflowApp struct {
	root          string
	screen        shellScreen
	width, height int
	done          bool
	active        tea.Model
	stack         []shellScreen
	status        string
	output        io.Writer
	context       []discovery.Result
	promptPending bool
	reviewed      *reviewSnapshot
	navOpen       bool
	navCursor     int
	quitConfirm   bool
	quitCursor    int
}

type workflowNavigateMsg struct{ Action string }

func newWorkflowApp(root string, start shellScreen) *workflowApp {
	stack := []shellScreen{screenHome}
	if start != screenHome {
		stack = append(stack, start)
	}
	app := &workflowApp{root: root, screen: start, stack: stack, output: io.Discard}
	if err := app.load(start, actionNone); err != nil {
		app.status = err.Error()
	}
	return app
}

func (app *workflowApp) Init() tea.Cmd { return nil }
func (app *workflowApp) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch message := message.(type) {
	case tea.WindowSizeMsg:
		app.width, app.height = message.Width, message.Height
		app.resizeActive()
	case workflowNavigateMsg:
		return app, app.navigate(message.Action)
	case tea.InterruptMsg:
		app.saveDefinitionDraft()
		app.done = true
		return app, tea.Quit
	case tea.KeyPressMsg:
		if message.Keystroke() == "ctrl+c" {
			app.saveDefinitionDraft()
			app.done = true
			return app, tea.Quit
		}
		if app.quitConfirm {
			return app, app.updateQuitConfirmation(message.Keystroke())
		}
		if app.navOpen {
			return app, app.updateNavigation(message.Keystroke())
		}
		if app.activeCapturesTextInput() {
			return app, app.updateActive(message)
		}
		if message.Keystroke() == "g" && app.screen != screenHome {
			return app, app.navigate(actionHome)
		}
		if message.Keystroke() == "n" && app.screen != screenHome && !app.activeUsesNavigationKey() {
			app.openNavigation()
			return app, nil
		}
		if message.Keystroke() == "q" && app.screen != screenHome {
			app.quitConfirm, app.quitCursor = true, 0
			return app, nil
		}
		return app, app.updateActive(message)
	default:
		return app, app.updateActive(message)
	}
	return app, nil
}

func (app *workflowApp) updateActive(message tea.Msg) tea.Cmd {
	if app.active == nil {
		return nil
	}
	updated, cmd := app.active.Update(message)
	app.active = updated
	if reviewModel, ok := app.active.(*reviewModel); ok {
		snapshot := reviewModel.snap
		app.reviewed = &snapshot
	}
	if action := modelNavigation(app.active); action != actionNone {
		clearModelNavigation(app.active)
		return app.navigate(action)
	}
	return cmd
}

func (app *workflowApp) View() tea.View {
	if app.done {
		return workflowView("")
	}
	width, height := defaultSize(app.width, app.height)
	if width < homeMinWidth || height < homeMinHeight {
		return workflowView(uiMinimumSize(width, height))
	}
	if app.quitConfirm {
		return workflowView(app.quitConfirmationView(width, height))
	}
	if app.navOpen {
		return workflowView(app.navigationOverlay(width, height))
	}
	if app.active != nil {
		activeView := app.active.View()
		content := activeView.Content
		if app.screen == screenHome || width < explorerWideWidth {
			return workflowView(content, activeView)
		}
		rail := app.navigationRail(24, height)
		return workflowView(lipgloss.JoinHorizontal(lipgloss.Top, rail, content), activeView)
	}
	body := "Workflow · " + string(app.screen)
	if app.status != "" {
		body += "\n\n" + uiMutedStyle.Render(app.status)
	}
	return workflowView(uiAppShell(width, height, "Spec", body, "b back   g home"))
}

func workflowView(content string, inherited ...tea.View) tea.View {
	view := tea.NewView(content)
	view.AltScreen = true
	if len(inherited) > 0 {
		view.MouseMode = inherited[0].MouseMode
		view.OnMouse = inherited[0].OnMouse
	}
	return view
}

func (app *workflowApp) activeUsesNavigationKey() bool {
	reviewModel, ok := app.active.(*reviewModel)
	return ok && reviewModel.tab == tabDiff
}

func (app *workflowApp) activeCapturesTextInput() bool {
	switch model := app.active.(type) {
	case *definitionModel:
		return model.editing
	case *documentModel:
		return model.editing
	case *historyModel:
		return model.searching
	case *planCaptureModel:
		return model.mode == planCapturePaste
	case *contextExplorerModel:
		return model.querying
	}
	return false
}

func runWorkflowApp(root string, start shellScreen, input io.Reader, output io.Writer) (bool, error) {
	app := newWorkflowApp(root, start)
	app.output = output
	final, err := tea.NewProgram(app, tea.WithInput(input), tea.WithOutput(output)).Run()
	if err != nil {
		return false, err
	}
	return final.(*workflowApp).done, nil
}

func (app *workflowApp) navigate(action string) tea.Cmd {
	if action == actionQuit {
		if app.screen != screenHome {
			app.quitConfirm, app.quitCursor = true, 0
			return nil
		}
		app.saveDefinitionDraft()
		app.done = true
		return tea.Quit
	}
	if action == actionHome {
		app.saveDefinitionDraft()
		app.stack = []shellScreen{screenHome}
		app.reviewed = nil
		if err := app.load(screenHome, actionHome); err != nil {
			app.status = err.Error()
		}
		return nil
	}
	if action == actionInitialize {
		var result bytes.Buffer
		if err := runInit(&result); err != nil {
			app.setActiveStatus(err.Error())
			return nil
		}
		root, err := currentRoot()
		if err != nil {
			app.setActiveStatus(err.Error())
			return nil
		}
		app.root = root
		command := app.navigate(actionHome)
		app.setActiveStatus(firstOutputLine(result.String(), "Workspace initialized."))
		return command
	}
	if action == actionNew {
		if _, err := change.BeginSetup(app.root, "", time.Now()); err != nil {
			app.setActiveStatus(err.Error())
			return nil
		}
		action = actionDefinition
	}
	if action == actionResume {
		workspace, err := state.Load(app.root)
		if err != nil {
			app.setActiveStatus(err.Error())
			return nil
		}
		if !workspace.Active {
			return app.navigate(actionNew)
		}
		if workspace.Setup != nil {
			action = actionDefinition
		} else {
			action = actionOverview
		}
	}
	if action == actionRecent {
		action = actionHistory
	}
	if action == actionREADME {
		var result bytes.Buffer
		if err := runREADME(nil, &result); err != nil {
			app.setActiveStatus(err.Error())
		} else {
			app.setActiveStatus(firstOutputLine(result.String(), "README action completed."))
		}
		return nil
	}
	if action == actionRunbook {
		documents, ok := app.active.(*documentModel)
		if !ok || documents.runbookTitle() == "" {
			return nil
		}
		var result bytes.Buffer
		if err := runRunbook(app.root, []string{documents.runbookTitle()}, &result); err != nil {
			documents.status = err.Error()
		} else {
			documents.status = firstOutputLine(result.String(), "Runbook action completed.")
			documents.editing, documents.editor = false, lineEditor{}
		}
		return nil
	}
	if action == actionChanges {
		app.stack = []shellScreen{screenOverview}
		if err := app.load(screenOverview, actionChanges); err != nil {
			app.status = err.Error()
		}
		return nil
	}
	if action == actionBack {
		if _, ok := app.active.(*definitionModel); ok {
			app.saveDefinitionDraft()
		}
		if len(app.stack) > 1 {
			app.stack = app.stack[:len(app.stack)-1]
			_ = app.load(app.stack[len(app.stack)-1], actionBack)
		} else {
			app.stack = []shellScreen{screenHome}
			_ = app.load(screenHome, actionBack)
		}
		return nil
	}
	replaceWithOverview := false
	if action == actionContinue {
		app.reviewed = nil
		if _, ok := app.active.(*contextReviewModel); ok && app.promptPending {
			if err := deliverDefinitionPrompt(app.root, io.Discard, definitionServices{}); err != nil {
				app.status = err.Error()
				return nil
			}
			app.promptPending = false
		}
		action = actionOverview
		replaceWithOverview = true
	}
	if definition, ok := app.active.(*definitionModel); ok && action == actionOverview && definition.created {
		if _, err := saveDefinitionContract(app.root, definition.result(), io.Discard); err != nil {
			app.status = err.Error()
			return nil
		}
		app.context = discoverImplementationContext(app.root, definition.result())
		recordDiscoveredContext(app.root, len(app.context))
		app.promptPending = true
		action = actionContextReview
	}
	if capture, ok := app.active.(*planCaptureModel); ok && action == actionPlan {
		if capture.decision != planAccept {
			return nil
		}
		if !capture.acceptedPersisted {
			if _, err := saveAcceptedPlan(app.root, capture.plan, state.PlanSourcePaste, "human paste", time.Now()); err != nil {
				capture.status = err.Error()
				capture.nav = actionNone
				return nil
			}
		}
		if app.reviewed != nil {
			app.reviewed.PlanStale = true
		}
	}
	if action == actionComplete {
		var completion bytes.Buffer
		if err := completeReviewedSpec(app.root, &completion); err != nil {
			app.setActiveStatus(err.Error())
			return nil
		}
		app.stack = []shellScreen{screenHome}
		action = actionHistory
	}
	target := screenForAction(action)
	if action == actionPlanCapture {
		target = shellScreen(actionPlanCapture)
	}
	if action == actionContextReview {
		target = shellScreen(actionContextReview)
	}
	if target == screenStay {
		return nil
	}
	if replaceWithOverview {
		app.stack = []shellScreen{screenOverview}
	} else {
		app.stack = append(app.stack, target)
	}
	if err := app.load(target, action); err != nil {
		app.status = err.Error()
		app.stack = app.stack[:len(app.stack)-1]
	}
	return nil
}

func (app *workflowApp) load(target shellScreen, via string) error {
	app.screen, app.status = target, ""
	var model tea.Model
	switch target {
	case screenHome:
		model = newHomeModel(loadHomeData(app.root, time.Now()))
	case screenOverview:
		data, err := loadOverview(app.root, time.Now())
		if err != nil {
			app.active = nil
			return err
		}
		if app.reviewed != nil {
			data.Stats = app.reviewed.Projection.Stats
			data.StatsRefreshed = true
			data.RefreshedAt = app.reviewed.RefreshedAt
			data.Facts.WorkspaceDirty = data.Stats.Files > 0
			data.Facts.ReviewEntered = false
		}
		data.Facts.ReviewEntered = false
		model = newOverviewModel(data)
	case screenPlan:
		workspace, err := state.Load(app.root)
		if err != nil {
			return err
		}
		stored, err := workspace.Plan()
		if err != nil {
			return err
		}
		if stored == nil {
			return app.load(shellScreen(actionPlanCapture), actionPlanCapture)
		}
		amend, err := workspace.PlanNeedsAmendment()
		if err != nil {
			return err
		}
		plan := newPlanModel(app.root, stored)
		plan.received, plan.amend = true, amend
		model = plan
	case screenReview:
		refreshed := true
		var snapshot reviewSnapshot
		if via != actionReview && app.reviewed != nil {
			snapshot = *app.reviewed
			workspace, err := state.Load(app.root)
			if err != nil {
				return err
			}
			current, err := workspace.Plan()
			if err != nil {
				return err
			}
			snapshot.PlanStale = !reflect.DeepEqual(current, snapshot.Plan)
			app.reviewed = &snapshot
			refreshed = false
		} else {
			loaded, err := loadReviewSnapshot(app.root)
			if err != nil {
				return err
			}
			snapshot = loaded
			app.reviewed = &snapshot
		}
		reviewModel := newReviewModel(app.root, snapshot)
		switch via {
		case actionReviewChanges:
			reviewModel.tab = tabChanges
		case actionIntegration:
			reviewModel.tab = tabIntegration
		case actionEvidence:
			reviewModel.tab = tabEvidence
		case actionDiff:
			reviewModel.tab = tabDiff
		}
		if refreshed {
			reviewModel.recordEvent(state.TimelineActualRefreshed, "actual state refreshed for review")
		}
		model = reviewModel
	case screenComplete:
		if app.reviewed == nil {
			snapshot, err := loadReviewSnapshot(app.root)
			if err != nil {
				return err
			}
			app.reviewed = &snapshot
		}
		model = newCompletionModel(*app.reviewed)
	case screenHistory:
		dir, records, active, err := loadHistory(app.root)
		if err != nil {
			return err
		}
		model = newHistoryModel(app.root, dir, records, active)
	case screenExplore:
		model = newContextExplorer(app.root, "", nil)
	case screenDocuments:
		model = newDocumentModel()
	case screenDefinition:
		workspace, err := state.Load(app.root)
		if err != nil {
			return err
		}
		if workspace.Setup == nil {
			if _, err := change.BeginEdit(app.root); err != nil {
				return err
			}
			workspace, err = state.Load(app.root)
			if err != nil {
				return err
			}
		}
		model = newDefinitionModel(*workspace.Setup, workspace.GitState)
	case shellScreen(actionPlanCapture):
		raw := ""
		if workspace, err := state.Load(app.root); err == nil {
			if stored, _ := workspace.Plan(); stored != nil {
				if canonical, canonicalErr := canonicalPlanBytes(stored.Plan); canonicalErr == nil {
					raw = "```spec-plan\n" + string(canonical) + "\n```"
				}
			}
		}
		capture := newPlanCaptureModel(raw)
		workspace, err := state.Load(app.root)
		if err != nil {
			return err
		}
		capture.amend, err = workspace.PlanNeedsAmendment()
		if err != nil {
			return err
		}
		if raw != "" {
			capture.openManualPaste(raw, planEditLabel(capture.amend)+": Ctrl+Enter to preview; Escape for planning prompt options.")
		}
		capture.copyPlanPrompt = func() error { return copyPlanningPrompt(app.root) }
		capture.reloadPlan = func() (*state.StoredChangePlan, error) {
			workspace, err := state.Load(app.root)
			if err != nil {
				return nil, err
			}
			return workspace.Plan()
		}
		model = capture
	case shellScreen(actionContextReview):
		model = newContextReviewModel(app.context)
	default:
		app.active = nil
		return fmt.Errorf("workflow screen %q is unavailable", target)
	}
	app.active = model
	app.resizeActive()
	return nil
}

func (app *workflowApp) resizeActive() {
	if app.active == nil || (app.width <= 0 && app.height <= 0) {
		return
	}
	width := app.width
	if app.screen != screenHome && width >= explorerWideWidth {
		width -= 24
	}
	updated, _ := app.active.Update(tea.WindowSizeMsg{Width: width, Height: app.height})
	app.active = updated
}

func (app *workflowApp) saveDefinitionDraft() {
	definition, ok := app.active.(*definitionModel)
	if !ok || definition.created {
		return
	}
	_ = saveAndExit(app.root, definition.result(), io.Discard)
}

func discoverImplementationContext(root string, setup state.Setup) []discovery.Result {
	results, err := discovery.Find(root, discoveryQuery(setup))
	if err != nil {
		return nil
	}
	return results
}

func recordDiscoveredContext(root string, count int) {
	workspace, err := state.Load(root)
	if err != nil {
		return
	}
	now := time.Now().UTC()
	_ = workspace.AppendTimeline(state.TimelineEvent{
		SchemaVersion: state.ArtifactSchemaVersion,
		ID:            fmt.Sprintf("%s:discovery:%d", workspace.SpecID, now.UnixNano()),
		Type:          state.TimelineDiscoveryRefreshed,
		Actor:         "spec",
		Source:        "built-in discovery",
		OccurredAt:    now,
		Details:       state.TimelineDetails{SpecID: workspace.SpecID, Count: count},
	})
}

func modelNavigation(model tea.Model) string {
	switch model := model.(type) {
	case *homeModel:
		return model.nav
	case *definitionModel:
		return model.nav
	case *overviewModel:
		return model.nav
	case *planModel:
		return model.nav
	case *reviewModel:
		return model.nav
	case *completionModel:
		return model.nav
	case *historyModel:
		return model.nav
	case *contextReviewModel:
		return model.nav
	case *planCaptureModel:
		return model.nav
	case *contextExplorerModel:
		return model.nav
	case *documentModel:
		return model.nav
	}
	return actionNone
}

func clearModelNavigation(model tea.Model) {
	switch model := model.(type) {
	case *homeModel:
		model.nav = actionNone
	case *definitionModel:
		model.nav = actionNone
	case *overviewModel:
		model.nav = actionNone
	case *planModel:
		model.nav = actionNone
	case *reviewModel:
		model.nav = actionNone
	case *completionModel:
		model.nav = actionNone
	case *historyModel:
		model.nav = actionNone
	case *contextReviewModel:
		model.nav = actionNone
	case *planCaptureModel:
		model.nav = actionNone
	case *contextExplorerModel:
		model.nav = actionNone
	case *documentModel:
		model.nav = actionNone
	}
}

func (app *workflowApp) setActiveStatus(status string) {
	app.status = status
	switch model := app.active.(type) {
	case *homeModel:
		model.status = status
	case *documentModel:
		model.status = status
	case *completionModel:
		model.status = status
	}
}

func firstOutputLine(output, fallback string) string {
	line := strings.TrimSpace(output)
	if index := strings.IndexByte(line, '\n'); index >= 0 {
		line = line[:index]
	}
	if line == "" {
		return fallback
	}
	return line
}

func (app *workflowApp) navigationScreen() canonicalScreen {
	var changeItems []screenItem
	if app.root != "" {
		if overview, err := loadOverview(app.root, time.Now()); err == nil {
			facts := overview.Facts
			if app.reviewed != nil {
				facts.ReviewEntered = app.screen == screenReview || app.screen == screenComplete
			}
			for _, stage := range deriveOverviewStages(facts) {
				marker := map[stageStatus]string{stageComplete: "✓", stageCurrent: "●", stagePending: "○", stageOmitted: "–"}[stage.Status]
				action := actionOverview
				switch stage.ID {
				case "intent":
					action = actionDefinition

				case "review":
					action = actionReview
				case "evidence":
					action = actionEvidence
				case "complete":
					action = actionCompletion
				}
				changeItems = append(changeItems, screenItem{ID: "navigation." + stage.ID, Label: marker + " " + stage.Label, Selectable: true, Action: screenAction(action)})
			}
		}
	}
	return canonicalScreen{Sections: []screenSection{
		{ID: "navigation.change", Title: "CHANGE", Items: changeItems, EmptyReason: "No active change"},
		{ID: "navigation.implement", Title: "IMPLEMENT", Items: []screenItem{
			{ID: "navigation.plan", Label: "AI Plan", Selectable: true, Action: screenAction(actionPlan)},
			{ID: "navigation.explore", Label: "Explore", Selectable: true, Action: screenAction(actionExplore)},
		}},
		{ID: "navigation.review", Title: "REVIEW", Items: []screenItem{
			{ID: "navigation.summary", Label: "Summary", Selectable: true, Action: screenAction(actionSummary)},
			{ID: "navigation.changes", Label: "Changes", Selectable: true, Action: screenAction(actionReviewChanges)},
			{ID: "navigation.integration", Label: "Integration", Selectable: true, Action: screenAction(actionIntegration)},
			{ID: "navigation.evidence", Label: "Evidence", Selectable: true, Action: screenAction(actionEvidence)},
			{ID: "navigation.diff", Label: "Diff", Selectable: true, Action: screenAction(actionDiff)},
		}},
		{ID: "navigation.global", Items: []screenItem{
			{ID: "navigation.history", Label: "History", Selectable: true, Action: screenAction(actionHistory)},
			{ID: "navigation.home", Label: "Home", Selectable: true, Action: screenAction(actionHome)},
		}},
	}, Cursor: app.navCursor}
}

func (app *workflowApp) openNavigation() {
	app.navOpen = true
	app.navCursor = 0
	items := app.navigationScreen().selectableItems()
	wanted := map[shellScreen]string{screenHome: "navigation.home", screenExplore: "navigation.explore", screenHistory: "navigation.history"}[app.screen]
	for index, item := range items {
		if item.ID == wanted {
			app.navCursor = index
			break
		}
	}
}

func (app *workflowApp) updateNavigation(keystroke string) tea.Cmd {
	screen := app.navigationScreen()
	switch keystroke {
	case "up", "k":
		screen.move(-1)
		app.navCursor = screen.Cursor
	case "down", "j":
		screen.move(1)
		app.navCursor = screen.Cursor
	case "enter":
		app.navOpen = false
		return app.navigate(string(screen.activate()))
	case "esc", "b", "n":
		app.navOpen = false
	case "g":
		app.navOpen = false
		return app.navigate(actionHome)
	}
	return nil
}

func (app *workflowApp) navigationOverlay(width, height int) string {
	screen := app.navigationScreen()
	screen.Cursor = app.navCursor
	return uiAppShell(width, height, "Navigate", screen.body(), uiKeyHints([][2]string{{"↑/↓", "navigate"}, {"enter", "open"}, {"esc", "close"}, {"g", "home"}}, "    "))
}

func (app *workflowApp) navigationRail(width, height int) string {
	screen := app.navigationScreen()
	lines := []string{uiTitleStyle.Render("SPEC"), ""}
	for _, section := range screen.Sections {
		if section.Title != "" {
			lines = append(lines, uiMutedStyle.Render(section.Title))
		}
		if len(section.Items) == 0 {
			lines = append(lines, "  "+uiMutedStyle.Render(section.EmptyReason))
		}
		for _, item := range section.Items {
			label := item.Label
			lines = append(lines, "  "+label)
		}
		lines = append(lines, "")
	}
	return uiPanel(width, height, uiBorder, "", "", strings.Join(lines, "\n"))
}

func (app *workflowApp) updateQuitConfirmation(keystroke string) tea.Cmd {
	switch keystroke {
	case "left", "right", "up", "down", "j", "k", "tab", "shift+tab":
		app.quitCursor = wrap(app.quitCursor+1, 2)
	case "esc", "b", "q":
		app.quitConfirm, app.quitCursor = false, 0
	case "enter":
		if app.quitCursor == 1 {
			app.saveDefinitionDraft()
			app.done = true
			return tea.Quit
		}
		app.quitConfirm = false
	}
	return nil
}

func (app *workflowApp) quitConfirmationView(width, height int) string {
	stay, exitLabel := "[ Stay ]", "  Exit  "
	if app.quitCursor == 0 {
		stay = uiSelectedRow("> Stay", 0)
	} else {
		exitLabel = uiSelectedRow("> Exit", 0)
	}
	body := strings.Join([]string{uiTitleStyle.Render("Exit Spec?"), "", "Your workflow state is preserved, but q only exits directly from Home.", "", stay + "    " + exitLabel}, "\n")
	return uiAppShell(width, height, "Confirm exit", body, uiKeyHints([][2]string{{"←/→", "choose"}, {"enter", "confirm"}, {"esc", "stay"}}, "    "))
}
