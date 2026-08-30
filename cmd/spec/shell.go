package main

import (
	"io"
)

type shellScreen string

const (
	screenHome       shellScreen = "home"
	screenDefinition shellScreen = "definition"
	screenOverview   shellScreen = "overview"
	screenPlan       shellScreen = "plan"
	screenReview     shellScreen = "review"
	screenSummary    shellScreen = "summary"
	screenHistory    shellScreen = "history"
	screenExplore    shellScreen = "explore"
	screenDocuments  shellScreen = "documents"
	screenBack       shellScreen = "back"
	screenExit       shellScreen = "exit"
	screenStay       shellScreen = ""
)

const (
	actionNone       = ""
	actionBack       = "back"
	actionHome       = "home"
	actionQuit       = "quit"
	actionInitialize = "initialize"
	actionNew        = "new"
	actionResume     = "resume"
	actionExplore    = "explore"
	actionRecent     = "recent"
	actionDocuments  = "documents"
	actionREADME     = "create-readme"
	actionRunbook    = "create-runbook"
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
	case actionHome:
		return screenHome
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
	case actionExplore:
		return screenExplore
	case actionDocuments:
		return screenDocuments
	}
	return screenStay
}

func runShell(root string, start shellScreen, input io.Reader, output io.Writer) (bool, error) {
	return runWorkflowApp(root, start, input, output)
}
