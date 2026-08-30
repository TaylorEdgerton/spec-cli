package verify

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/TaylorEdgerton/spec-cli/internal/gitutil"
	"github.com/TaylorEdgerton/spec-cli/internal/state"
)

func TestCollectParsesGoJSONTestResults(t *testing.T) {
	output := []byte(
		"{\"Action\":\"run\",\"Package\":\"example.test/feature\",\"Test\":\"TestPass\"}\n" +
			"{\"Action\":\"pass\",\"Package\":\"example.test/feature\",\"Test\":\"TestPass\"}\n" +
			"{\"Action\":\"run\",\"Package\":\"example.test/feature\",\"Test\":\"TestFail\"}\n" +
			"{\"Action\":\"fail\",\"Package\":\"example.test/feature\",\"Test\":\"TestFail\"}\n" +
			"{\"Action\":\"skip\",\"Package\":\"example.test/feature\",\"Test\":\"TestSkip\"}\n",
	)
	collected := Collect("go test -json ./...", output, false)
	if collected.ParserError != "" || len(collected.Tests) != 3 {
		t.Fatalf("collected = %+v", collected)
	}
	assertCollectedTest(t, collected.Tests, "example.test/feature:TestPass", "passed")
	assertCollectedTest(t, collected.Tests, "example.test/feature:TestFail", "failed")
	assertCollectedTest(t, collected.Tests, "example.test/feature:TestSkip", "skipped")
}

func TestCollectFallsBackForArbitraryCommandsAndDoesNotPromoteParserFailures(t *testing.T) {
	fallback := Collect("make integration", []byte("all good\n"), true)
	if len(fallback.Tests) != 1 || fallback.Tests[0].Name != "make integration" || fallback.Tests[0].Status != "passed" {
		t.Fatalf("command fallback = %+v", fallback)
	}
	broken := Collect("go test -json ./...", []byte("not-json\n"), true)
	if broken.ParserError == "" {
		t.Fatalf("parser failure = %+v", broken)
	}
	for _, test := range broken.Tests {
		if test.Status == "passed" {
			t.Fatalf("parser failure promoted to pass: %+v", broken)
		}
	}
}

func TestRunWithEvidencePersistsRawRunProvenance(t *testing.T) {
	root := t.TempDir()
	stateHome := filepath.Join(t.TempDir(), "state")
	configHome := filepath.Join(t.TempDir(), "config")
	t.Setenv("SPEC_STATE_HOME", stateHome)
	t.Setenv("SPEC_CONFIG_HOME", configHome)
	workspace, err := state.Register(root)
	if err != nil {
		t.Fatal(err)
	}
	initRepository(t, root)
	baseSHA, err := gitutil.Head(root)
	if err != nil {
		t.Fatal(err)
	}
	started := time.Date(2026, 8, 30, 2, 0, 0, 0, time.UTC)
	if err := workspace.Start("Evidence test", baseSHA, started); err != nil {
		t.Fatal(err)
	}
	writeConfig(t, configHome, "verify:\n  - printf pass-marker\n")
	verification, runs, err := RunWithEvidence(root, "implementation", started)
	if err != nil || !verification.Passed || len(runs) != 1 {
		t.Fatalf("run result = %+v, runs=%+v, err=%v", verification, runs, err)
	}
	run := runs[0]
	if run.Phase != "implementation" || run.Command != "printf pass-marker" || run.BaselineSHA != baseSHA || run.WorktreeFingerprint == "" || !run.StartedAt.Equal(started) || run.FinishedAt.Before(run.StartedAt) {
		t.Fatalf("run provenance = %+v", run)
	}
	stored, err := workspace.EvidenceRuns()
	if err != nil || len(stored) != 1 || stored[0].ID != run.ID {
		t.Fatalf("stored evidence = %+v, %v", stored, err)
	}
}

func TestRunWithEvidenceRecordsPreChangeFailureAndLaterFingerprint(t *testing.T) {
	root := t.TempDir()
	stateHome := filepath.Join(t.TempDir(), "state")
	configHome := filepath.Join(t.TempDir(), "config")
	t.Setenv("SPEC_STATE_HOME", stateHome)
	t.Setenv("SPEC_CONFIG_HOME", configHome)
	workspace, err := state.Register(root)
	if err != nil {
		t.Fatal(err)
	}
	initRepository(t, root)
	baseSHA, err := gitutil.Head(root)
	if err != nil {
		t.Fatal(err)
	}
	redAt := time.Date(2026, 8, 30, 3, 0, 0, 0, time.UTC)
	if err := workspace.Start("Red then green", baseSHA, redAt); err != nil {
		t.Fatal(err)
	}
	writeConfig(t, configHome, "verify:\n  - test -f ready.flag\n")
	_, redRuns, redErr := RunWithEvidence(root, "pre_change", redAt)
	if redErr == nil || len(redRuns) != 1 || redRuns[0].Passed || redRuns[0].BaselineSHA != baseSHA || redRuns[0].WorktreeFingerprint == "" {
		t.Fatalf("red provenance = %+v, err=%v", redRuns, redErr)
	}
	if err := os.WriteFile(filepath.Join(root, "ready.flag"), []byte("ready\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	greenAt := redAt.Add(time.Minute)
	_, greenRuns, greenErr := RunWithEvidence(root, "implementation", greenAt)
	if greenErr != nil || len(greenRuns) != 1 || !greenRuns[0].Passed || !greenRuns[0].StartedAt.Equal(greenAt) {
		t.Fatalf("green provenance = %+v, err=%v", greenRuns, greenErr)
	}
	if greenRuns[0].WorktreeFingerprint == redRuns[0].WorktreeFingerprint {
		t.Fatalf("run fingerprints did not retain their own worktree state: red=%s green=%s", redRuns[0].WorktreeFingerprint, greenRuns[0].WorktreeFingerprint)
	}
	stored, err := workspace.EvidenceRuns()
	if err != nil || len(stored) != 2 {
		t.Fatalf("stored runs = %+v, err=%v", stored, err)
	}
}

func assertCollectedTest(t *testing.T, tests []state.EvidenceTest, id, status string) {
	t.Helper()
	for _, test := range tests {
		if test.ID == id {
			if test.Status != status {
				t.Fatalf("test %q status = %q, want %q", id, test.Status, status)
			}
			return
		}
	}
	t.Fatalf("test %q not found in %+v", id, tests)
}
