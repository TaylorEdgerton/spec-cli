package main

import (
	"bytes"
	"fmt"
	"io"
	"time"

	tea "charm.land/bubbletea/v2"
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
}

type workflowNavigateMsg struct{ Action string }

func newWorkflowApp(root string, start shellScreen) *workflowApp {
	app := &workflowApp{root: root, screen: start, stack: []shellScreen{start}, output: io.Discard}
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
		if app.active != nil {
			updated, _ := app.active.Update(message)
			app.active = updated
		}
	case workflowNavigateMsg:
		return app, app.navigate(message.Action)
	default:
		if app.active == nil {
			return app, nil
		}
		updated, cmd := app.active.Update(message)
		app.active = updated
		if action := modelNavigation(app.active); action != actionNone {
			clearModelNavigation(app.active)
			return app, app.navigate(action)
		}
		return app, cmd
	}
	return app, nil
}

func (app *workflowApp) View() tea.View {
	if app.done {
		return tea.NewView("")
	}
	if app.active != nil {
		return app.active.View()
	}
	width, height := defaultSize(app.width, app.height)
	body := "Workflow · " + string(app.screen)
	if app.status != "" {
		body += "\n\n" + uiMutedStyle.Render(app.status)
	}
	return tea.NewView(uiAppShell(width, height, "Spec", body, "q exit"))
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
		app.saveDefinitionDraft()
		app.done = true
		return tea.Quit
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
			app.done = true
			return tea.Quit
		}
		return nil
	}
	replaceWithOverview := false
	if action == actionContinue {
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
		if _, err := saveAcceptedPlan(app.root, capture.plan, state.PlanSourcePaste, "human paste", time.Now()); err != nil {
			capture.status = err.Error()
			capture.nav = actionNone
			return nil
		}
	}
	if action == actionComplete {
		var completion bytes.Buffer
		if err := completeReviewedSpec(app.root, &completion); err != nil {
			app.status = err.Error()
			return nil
		}
		app.stack = nil
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
	case screenOverview:
		data, err := loadOverview(app.root, time.Now())
		if err != nil {
			app.active = nil
			return err
		}
		if app.reviewed != nil {
			data.Stats = app.reviewed.Projection.Stats
			data.Facts.WorkspaceDirty = data.Stats.Files > 0
			data.Facts.ReviewEntered = true
		}
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
		model = newPlanModel(app.root, stored)
	case screenReview:
		snapshot, err := loadReviewSnapshot(app.root)
		if err != nil {
			return err
		}
		reviewModel := newReviewModel(app.root, snapshot)
		app.reviewed = &snapshot
		if via == actionEvidence {
			reviewModel.tab = tabEvidence
		}
		reviewModel.recordEvent(state.TimelineActualRefreshed, "actual state refreshed for review")
		model = reviewModel
	case screenSummary:
		if app.reviewed == nil {
			snapshot, err := loadReviewSnapshot(app.root)
			if err != nil {
				return err
			}
			app.reviewed = &snapshot
		}
		reviewModel := newReviewModel(app.root, *app.reviewed)
		reviewModel.tab = tabStats
		model = reviewModel
	case screenHistory:
		dir, records, err := loadHistory(app.root)
		if err != nil {
			return err
		}
		model = newHistoryModel(dir, records, false)
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
		model = newPlanCaptureModel(raw)
	case shellScreen(actionContextReview):
		model = newContextReviewModel(app.context)
	default:
		app.active = nil
		return fmt.Errorf("workflow screen %q is unavailable", target)
	}
	app.active = model
	if app.width > 0 || app.height > 0 {
		updated, _ := app.active.Update(tea.WindowSizeMsg{Width: app.width, Height: app.height})
		app.active = updated
	}
	return nil
}

func (app *workflowApp) saveDefinitionDraft() {
	definition, ok := app.active.(*definitionModel)
	if !ok || definition.created {
		return
	}
	_ = saveAndExit(app.root, definition.result(), io.Discard)
}

func discoverImplementationContext(root string, setup state.Setup) []discovery.Result {
	results, err := discovery.Find(root, discovery.Query{Intent: setup.Title, Outcome: setup.Outcome})
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
	case *definitionModel:
		return model.nav
	case *overviewModel:
		return model.nav
	case *planModel:
		return model.nav
	case *reviewModel:
		return model.nav
	case *historyModel:
		return model.nav
	case *contextReviewModel:
		return model.nav
	case *planCaptureModel:
		return model.nav
	}
	return actionNone
}

func clearModelNavigation(model tea.Model) {
	switch model := model.(type) {
	case *definitionModel:
		model.nav = actionNone
	case *overviewModel:
		model.nav = actionNone
	case *planModel:
		model.nav = actionNone
	case *reviewModel:
		model.nav = actionNone
	case *historyModel:
		model.nav = actionNone
	case *contextReviewModel:
		model.nav = actionNone
	case *planCaptureModel:
		model.nav = actionNone
	}
}
