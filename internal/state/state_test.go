package state

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/TaylorEdgerton/spec-cli/internal/aiusage"
)

func TestRegisterUsesExternalState(t *testing.T) {
	root := t.TempDir()
	stateHome := filepath.Join(t.TempDir(), "state")
	configHome := filepath.Join(t.TempDir(), "config")
	t.Setenv("SPEC_STATE_HOME", stateHome)
	t.Setenv("SPEC_CONFIG_HOME", configHome)

	workspace, err := Register(root)
	if err != nil {
		t.Fatal(err)
	}
	if workspace.Root != root || workspace.ID == "" {
		t.Fatalf("unexpected workspace: %+v", workspace)
	}
	if filepath.Base(filepath.Dir(workspace.Dir)) != "projects" {
		t.Fatalf("workspace directory = %s", workspace.Dir)
	}
	if _, err := os.Stat(filepath.Join(root, ".spec")); !os.IsNotExist(err) {
		t.Fatalf("repository-local state exists: %v", err)
	}
	if _, err := os.Stat(filepath.Join(workspace.Dir, "metadata.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(workspace.Dir, "current.md")); !os.IsNotExist(err) {
		t.Fatalf("obsolete external current.md exists: %v", err)
	}
	if _, err := os.Stat(filepath.Join(configHome, "templates")); err != nil {
		t.Fatal(err)
	}
}

func TestHistoryRecordsLoadsCompletedSpecs(t *testing.T) {
	root := t.TempDir()
	t.Setenv("SPEC_STATE_HOME", filepath.Join(t.TempDir(), "state"))
	t.Setenv("SPEC_CONFIG_HOME", filepath.Join(t.TempDir(), "config"))
	workspace, err := Register(root)
	if err != nil {
		t.Fatal(err)
	}

	records, err := workspace.HistoryRecords()
	if err != nil || len(records) != 0 {
		t.Fatalf("empty history = %+v, %v", records, err)
	}
	want := History{
		Title:       "Usage tracking",
		FinishedAt:  time.Date(2026, 8, 23, 4, 5, 6, 0, time.UTC),
		SpecArchive: "specs/usage-tracking.md",
		AIUsage: &aiusage.Summary{
			Available: true,
			Usage:     []aiusage.AIUsage{{Provider: "Codex", InputTokens: 42}},
		},
	}
	if err := workspace.appendHistory(want); err != nil {
		t.Fatal(err)
	}
	records, err = workspace.HistoryRecords()
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Title != want.Title || records[0].AIUsage == nil ||
		records[0].AIUsage.Usage[0].InputTokens != 42 {
		t.Fatalf("history = %+v", records)
	}
}

func TestSetupAndWorkspaceVerificationRoundTrip(t *testing.T) {
	root := t.TempDir()
	t.Setenv("SPEC_STATE_HOME", filepath.Join(t.TempDir(), "state"))
	t.Setenv("SPEC_CONFIG_HOME", filepath.Join(t.TempDir(), "config"))
	workspace, err := Register(root)
	if err != nil {
		t.Fatal(err)
	}
	setup := Setup{Stage: "outcome", Title: "Reconnect", Input: "partial answer"}
	if err := workspace.BeginSetup("abc123", time.Now(), setup); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(root)
	if err != nil || loaded.Setup == nil || loaded.Setup.Input != setup.Input {
		t.Fatalf("loaded setup = %+v, %v", loaded.Setup, err)
	}
	loaded.Setup.Stage = "criteria"
	loaded.Setup.Criteria = []SetupCriterion{{Text: "Reconnect automatically", Included: true}}
	if err := loaded.SaveSetup(*loaded.Setup); err != nil {
		t.Fatal(err)
	}
	if err := loaded.SetVerificationCommands([]string{" go test ./... ", ""}); err != nil {
		t.Fatal(err)
	}
	loaded, err = Load(root)
	if err != nil || len(loaded.VerifyCommands) != 1 || loaded.VerifyCommands[0] != "go test ./..." {
		t.Fatalf("verification commands = %v, %v", loaded.VerifyCommands, err)
	}
	if err := loaded.CompleteSetup("Reconnect"); err != nil {
		t.Fatal(err)
	}
	loaded, err = Load(root)
	if err != nil || loaded.Setup != nil || !loaded.Active || loaded.Title != "Reconnect" {
		t.Fatalf("completed setup = %+v, %v", loaded.Metadata, err)
	}
}

func TestPurgeRefusesHomeDirectory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SPEC_STATE_HOME", home)
	if err := Purge(); err == nil {
		t.Fatal("Purge should reject the home directory")
	}
}
