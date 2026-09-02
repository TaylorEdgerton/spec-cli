package main

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/TaylorEdgerton/spec-cli/internal/state"
)

const validPlanJSON = `{"summary":"Disable automatic indexing","files":[{"path":"config\\config.go","action":"modify","reason":"add setting"}],"integration_points":[{"existing_symbol":"ensureIndex","planned_change":"read setting","relationship":"controls flow"}],"verification":[{"behaviour":"manual indexing remains available","likely_location":"indexer/indexer_test.go"}],"uncertainties":["CLI location"]}`

func TestExtractPlanBlockRequiresExactlyOneValidFence(t *testing.T) {
	raw, err := extractPlanBlock("Here is the plan.\n```spec-plan\n" + validPlanJSON + "\n```\nReady.")
	if err != nil || string(raw) != validPlanJSON {
		t.Fatalf("block = %q, %v", raw, err)
	}
	for name, input := range map[string]string{
		"missing":   validPlanJSON,
		"multiple":  "```spec-plan\n" + validPlanJSON + "\n```\n```spec-plan\n" + validPlanJSON + "\n```",
		"malformed": "```spec-plan\n{bad}\n```",
	} {
		t.Run(name, func(t *testing.T) {
			raw, err := extractPlanBlock(input)
			if err == nil {
				if _, validateErr := validateChangePlan(raw); validateErr == nil {
					t.Fatalf("input accepted: %s", input)
				}
			}
		})
	}
}

func TestValidatePlanNormalizesAndRejectsInvalidActionsAndPaths(t *testing.T) {
	plan, err := validateChangePlan([]byte(validPlanJSON))
	if err != nil || len(plan.Files) != 1 || plan.Files[0].Path != "config/config.go" {
		t.Fatalf("plan = %+v, %v", plan, err)
	}
	for _, invalid := range []string{
		`{"summary":""}`,
		`{"summary":"x","files":[{"path":"../outside","action":"modify"}]}`,
		`{"summary":"x","files":[{"path":"x.go","action":"move"}]}`,
		`{"summary":"x","files":[{"path":"/tmp/x.go","action":"modify"}]}`,
		`{"summary":"x","files":[{"path":"config\\config.go","action":"modify"},{"path":"config/config.go","action":"modify"}]}`,
	} {
		if _, err := validateChangePlan([]byte(invalid)); err == nil {
			t.Fatalf("invalid plan accepted: %s", invalid)
		}
	}
	preview := planPreview(plan)
	for _, expected := range []string{"Disable automatic indexing", "config/config.go", "modify", "1 file", "1 integration", "1 uncertainty"} {
		if !strings.Contains(preview, expected) {
			t.Fatalf("preview missing %q: %s", expected, preview)
		}
	}
}

func TestCLIWaitReloadsPlanWrittenByPlanSubmitWithoutResavingIt(t *testing.T) {
	root, workspace, _ := definitionRepository(t, false)
	setup := *workspace.Setup
	setup.Title = "CLI wait"
	if err := workspace.SaveSetup(setup); err != nil {
		t.Fatal(err)
	}
	if _, err := saveDefinitionContract(root, setup, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}

	app := newWorkflowApp(root, shellScreen(actionPlanCapture))
	capture := app.active.(*planCaptureModel)
	capture.cursor = 1
	app.Update(key(tea.KeyEnter, ""))
	if capture.mode != planCaptureCLIWait {
		t.Fatalf("mode=%v", capture.mode)
	}
	now := time.Date(2026, 8, 31, 3, 4, 5, 0, time.UTC)
	if err := runPlanCommand(root, []string{"submit", "--stdin"}, strings.NewReader(validPlanJSON), &bytes.Buffer{}, now); err != nil {
		t.Fatal(err)
	}
	app.Update(key('r', "r"))
	capture = app.active.(*planCaptureModel)
	if capture.mode != planCaptureClipboardPreview || !capture.acceptedPersisted || capture.plan.Files[0].Path != "config/config.go" {
		t.Fatalf("refreshed capture=%+v", capture)
	}
	app.Update(key(tea.KeyEnter, ""))
	if app.screen != screenPlan {
		t.Fatalf("accepted CLI plan routed to %q", app.screen)
	}
	events, err := workspace.TimelineEvents()
	if err != nil {
		t.Fatal(err)
	}
	accepted := 0
	for _, event := range events {
		if event.Type == state.TimelinePlanAccepted {
			accepted++
		}
	}
	if accepted != 1 {
		t.Fatalf("CLI plan was accepted %d times; want one durable acceptance", accepted)
	}
}

func TestPlanIsNotPersistedUntilExplicitAcceptance(t *testing.T) {
	root, workspace, _ := definitionRepository(t, false)
	setup := *workspace.Setup
	setup.Title = "Plan capture"
	if err := workspace.SaveSetup(setup); err != nil {
		t.Fatal(err)
	}
	if _, err := completeDefinition(root, setup, &bytes.Buffer{}, definitionServices{BuildPrompt: func(string) (string, error) { return "prompt", nil }, CopyPrompt: func(string) error { return nil }}); err != nil {
		t.Fatal(err)
	}
	raw, _ := extractPlanBlock("```spec-plan\n" + validPlanJSON + "\n```")
	plan, err := validateChangePlan(raw)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := workspace.Plan()
	if err != nil || loaded != nil {
		t.Fatalf("plan persisted before acceptance: %+v, %v", loaded, err)
	}
	accepted, err := saveAcceptedPlan(root, plan, state.PlanSourcePaste, "Codex", time.Now())
	if err != nil || accepted == nil || accepted.Source != state.PlanSourcePaste {
		t.Fatalf("accepted = %+v, %v", accepted, err)
	}
}

func TestPlanCapturePersistsOnlyAfterAcceptDecision(t *testing.T) {
	root, workspace, _ := definitionRepository(t, false)
	setup := *workspace.Setup
	setup.Title = "Capture decisions"
	if err := workspace.SaveSetup(setup); err != nil {
		t.Fatal(err)
	}
	if _, err := completeDefinition(root, setup, &bytes.Buffer{}, definitionServices{BuildPrompt: func(string) (string, error) { return "prompt", nil }, CopyPrompt: func(string) error { return nil }}); err != nil {
		t.Fatal(err)
	}
	raw := "Here is the plan.\n```spec-plan\n" + validPlanJSON + "\n```\nReady."
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)

	for _, decision := range []planDecision{planSkip, planEdit} {
		plan, stored, err := capturePlanDecision(root, raw, decision, "Codex", now)
		if err != nil || stored != nil {
			t.Fatalf("%s stored=%+v err=%v", decision, stored, err)
		}
		if decision == planEdit && (len(plan.Files) != 1 || plan.Files[0].Path != "config/config.go") {
			t.Fatalf("edit did not return the validated plan: %+v", plan)
		}
		if loaded, err := workspace.Plan(); err != nil || loaded != nil {
			t.Fatalf("%s persisted a plan: %+v, %v", decision, loaded, err)
		}
	}

	if _, _, err := capturePlanDecision(root, "no fenced block here", planAccept, "Codex", now); err == nil {
		t.Fatal("accept of an unparseable paste succeeded")
	}
	if loaded, err := workspace.Plan(); err != nil || loaded != nil {
		t.Fatalf("rejected accept persisted a plan: %+v, %v", loaded, err)
	}

	_, stored, err := capturePlanDecision(root, raw, planAccept, "Codex", now)
	if err != nil || stored == nil || stored.Source != state.PlanSourcePaste || stored.Submitter != "Codex" {
		t.Fatalf("accept stored=%+v err=%v", stored, err)
	}
	loaded, err := workspace.Plan()
	if err != nil || loaded == nil || len(loaded.Plan.Files) != 1 || loaded.Plan.Files[0].Path != "config/config.go" {
		t.Fatalf("accepted plan not persisted: %+v, %v", loaded, err)
	}
}

func TestCLIAndPastedPlansUseIdenticalCanonicalState(t *testing.T) {
	pasted, err := validateChangePlan([]byte(validPlanJSON))
	if err != nil {
		t.Fatal(err)
	}
	cli, err := validateChangePlan([]byte(validPlanJSON))
	if err != nil {
		t.Fatal(err)
	}
	pastedBytes, _ := canonicalPlanBytes(pasted)
	cliBytes, _ := canonicalPlanBytes(cli)
	if !bytes.Equal(pastedBytes, cliBytes) || !reflect.DeepEqual(pasted, cli) || !json.Valid(cliBytes) {
		t.Fatalf("canonical mismatch:\n%s\n%s", pastedBytes, cliBytes)
	}
}
