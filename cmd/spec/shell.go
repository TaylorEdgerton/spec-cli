package main

import (
	"io"
)

type shellScreen string

const (
	screenDefinition shellScreen = "definition"
	screenOverview   shellScreen = "overview"
	screenPlan       shellScreen = "plan"
	screenReview     shellScreen = "review"
	screenSummary    shellScreen = "summary"
	screenHistory    shellScreen = "history"
	screenBack       shellScreen = "back"
	screenExit       shellScreen = "exit"
	screenStay       shellScreen = ""
)

const (
	actionNone       = ""
	actionBack       = "back"
	actionQuit       = "quit"
	actionDefinition = "definition"
	actionOverview   = "overview"
	actionPlan       = "plan"
	actionReview     = "review"
	actionSummary    = "summary"
	actionEvidence   = "evidence"
	actionHistory    = "history"
	actionComplete   = "complete"
	actionChanges    = "request-changes"
)

func screenForAction(action string) shellScreen {
	switch action {
	case actionQuit:
		return screenExit
	case actionBack, actionChanges:
		return screenBack
	case actionComplete:
		return screenHistory
	case actionDefinition:
		return screenDefinition
	case actionOverview:
		return screenOverview
	case actionPlan:
		return screenPlan
	case actionReview, actionEvidence:
		return screenReview
	case actionSummary:
		return screenSummary
	case actionHistory:
		return screenHistory
	}
	return screenStay
}

func runShell(root string, start shellScreen, input io.Reader, output io.Writer) (bool, error) {
	return runWorkflowApp(root, start, input, output)
}
