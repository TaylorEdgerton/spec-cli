package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/TaylorEdgerton/spec-cli/internal/change"
	"github.com/TaylorEdgerton/spec-cli/internal/state"
)

func TestPlanSubmitStdinValidatesActiveSpecAndPersistsVersionedEnvelope(t *testing.T) {
	root, workspace, _ := definitionRepository(t, false)
	if err := workspace.Abandon(); err != nil {
		t.Fatal(err)
	}
	if err := runPlanCommand(root, []string{"submit", "--stdin"}, strings.NewReader(validPlanJSON), &bytes.Buffer{}, time.Now()); err == nil || !strings.Contains(err.Error(), "active") {
		t.Fatalf("inactive contract accepted: %v", err)
	}
	if _, err := change.BeginSetup(root, "", time.Now()); err != nil {
		t.Fatal(err)
	}
	workspace, _ = state.Load(root)
	setup := *workspace.Setup
	setup.Title = "CLI plan"
	if err := workspace.SaveSetup(setup); err != nil {
		t.Fatal(err)
	}
	if _, err := completeDefinition(root, setup, &bytes.Buffer{}, definitionServices{BuildPrompt: func(string) (string, error) { return "prompt", nil }, CopyPrompt: func(string) error { return nil }}); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	now := time.Date(2026, 8, 30, 11, 0, 0, 0, time.UTC)
	if err := runPlanCommand(root, []string{"submit", "--stdin"}, strings.NewReader(validPlanJSON), &output, now); err != nil {
		t.Fatal(err)
	}
	stored, err := workspace.Plan()
	if err != nil || stored == nil || stored.SchemaVersion != state.ArtifactSchemaVersion || stored.Source != state.PlanSourceCLI || stored.Plan.Files[0].Path != "config/config.go" {
		t.Fatalf("stored=%+v err=%v", stored, err)
	}
	if !strings.Contains(output.String(), "AI Plan received") {
		t.Fatalf("output=%q", output.String())
	}
}
func TestPlanSubmitStdinRejectsUsageAndInvalidPayload(t *testing.T) {
	if err := runPlanCommand("", []string{"submit"}, strings.NewReader(""), &bytes.Buffer{}, time.Now()); err == nil || !strings.Contains(err.Error(), "usage") {
		t.Fatalf("usage error=%v", err)
	}
	if _, err := validateChangePlan([]byte(`{"summary":"x","files":[{"path":"x","action":"bad"}]}`)); err == nil {
		t.Fatal("invalid action accepted")
	}
}
