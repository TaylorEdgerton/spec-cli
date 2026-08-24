package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/TaylorEdgerton/spec-cli/internal/state"
)

func TestBareSpecReportsResumableSetupWithoutTTY(t *testing.T) {
	root := t.TempDir()
	t.Setenv("SPEC_STATE_HOME", filepath.Join(t.TempDir(), "state"))
	t.Setenv("SPEC_CONFIG_HOME", filepath.Join(t.TempDir(), "config"))
	runHomeGit(t, root, "init")
	workspace, err := state.Register(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := workspace.BeginSetup("base", time.Now(), state.Setup{Stage: setupCriteria, Title: "Change"}); err != nil {
		t.Fatal(err)
	}
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(previous) })
	var output bytes.Buffer
	if err := runHome(strings.NewReader(""), &output, false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "paused at criteria") || !strings.Contains(output.String(), "interactively to resume") {
		t.Fatalf("status = %q", output.String())
	}
}

func runHomeGit(t *testing.T, root string, args ...string) {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", root}, args...)...)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
}
