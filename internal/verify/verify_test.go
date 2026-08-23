package verify

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/TaylorEdgerton/spec-cli/internal/state"
)

func TestRunStoresPassAndFailure(t *testing.T) {
	root := t.TempDir()
	stateHome := filepath.Join(t.TempDir(), "state")
	configHome := filepath.Join(t.TempDir(), "config")
	t.Setenv("SPEC_STATE_HOME", stateHome)
	t.Setenv("SPEC_CONFIG_HOME", configHome)
	workspace, err := state.Register(root)
	if err != nil {
		t.Fatal(err)
	}
	writeConfig(t, configHome, "verify:\n  - printf pass-marker\n")
	var stdout, stderr bytes.Buffer
	result, err := Run(root, &stdout, &stderr, time.Now())
	if err != nil || !result.Passed || !strings.Contains(result.Output, "pass-marker") {
		t.Fatalf("pass result: %+v, %v", result, err)
	}
	stored, err := workspace.Verification()
	if err != nil || stored == nil || !stored.Passed {
		t.Fatalf("stored pass: %+v, %v", stored, err)
	}

	writeConfig(t, configHome, "verify:\n  - printf fail-marker; exit 3\n")
	stdout.Reset()
	result, err = Run(root, &stdout, &stderr, time.Now())
	if err == nil || result.Passed || !strings.Contains(result.Output, "fail-marker") {
		t.Fatalf("failure result: %+v, %v", result, err)
	}
	stored, err = workspace.Verification()
	if err != nil || stored == nil || stored.Passed {
		t.Fatalf("stored failure: %+v, %v", stored, err)
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
