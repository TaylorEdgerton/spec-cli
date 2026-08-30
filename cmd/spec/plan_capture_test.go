package main

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

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
