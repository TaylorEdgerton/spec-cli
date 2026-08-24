package verify

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/TaylorEdgerton/spec-cli/internal/state"
)

func TestRunStoresPassFailureAndFreshness(t *testing.T) {
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
	writeConfig(t, configHome, "verify:\n  - printf pass-marker\n")
	result, err := Run(root, time.Now())
	if err != nil || !result.Passed || result.Fingerprint == "" || !strings.Contains(result.Output, "pass-marker") {
		t.Fatalf("pass result: %+v, %v", result, err)
	}
	stored, err := workspace.Verification()
	if err != nil || stored == nil || !stored.Passed {
		t.Fatalf("stored pass: %+v, %v", stored, err)
	}
	current, err := Current(root, stored)
	if err != nil || !current {
		t.Fatalf("current pass = %v, %v", current, err)
	}
	if err := os.WriteFile(filepath.Join(root, "changed.txt"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	current, err = Current(root, stored)
	if err != nil || current {
		t.Fatalf("changed workspace current = %v, %v", current, err)
	}

	writeConfig(t, configHome, "verify:\n  - printf fail-marker; exit 3\n")
	result, err = Run(root, time.Now())
	if err == nil || result.Passed || result.FailedCommand == "" || !strings.Contains(result.Output, "fail-marker") {
		t.Fatalf("failure result: %+v, %v", result, err)
	}
	stored, err = workspace.Verification()
	if err != nil || stored == nil || stored.Passed {
		t.Fatalf("stored failure: %+v, %v", stored, err)
	}
}

func TestCurrentTreatsLegacyResultAsStale(t *testing.T) {
	current, err := Current(t.TempDir(), &state.Verification{Passed: true})
	if err != nil || current {
		t.Fatalf("legacy current = %v, %v", current, err)
	}
}

func TestDetectUsesConservativeProjectMarkers(t *testing.T) {
	root := t.TempDir()
	for name, content := range map[string]string{
		"go.mod":         "module example.test/project\n",
		"package.json":   `{"scripts":{"test":"vitest","lint":"eslint ."}}`,
		"pnpm-lock.yaml": "lockfileVersion: 9\n",
		"Makefile":       "test:\n\ttrue\n",
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	commands := Detect(root)
	got := strings.Join(commands, "|")
	if got != "go test ./...|pnpm test|pnpm lint" {
		t.Fatalf("detected commands = %q", got)
	}
}

func writeConfig(t *testing.T, configHome, content string) {
	t.Helper()
	if err := os.MkdirAll(configHome, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configHome, "config.yml"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func initRepository(t *testing.T, root string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, "base.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	commands := [][]string{
		{"git", "-C", root, "init"},
		{"git", "-C", root, "config", "user.name", "Spec Test"},
		{"git", "-C", root, "config", "user.email", "spec@example.invalid"},
		{"git", "-C", root, "add", "base.txt"},
		{"git", "-C", root, "commit", "-m", "baseline"},
	}
	for _, args := range commands {
		command := exec.Command(args[0], args[1:]...)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, output)
		}
	}
}
