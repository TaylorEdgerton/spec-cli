package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/TaylorEdgerton/spec-cli/internal/gitutil"
	"github.com/TaylorEdgerton/spec-cli/internal/state"
)

func TestInitUsesLocalGitExcludeAndExternalState(t *testing.T) {
	root := t.TempDir()
	t.Setenv("SPEC_STATE_HOME", filepath.Join(t.TempDir(), "state"))
	t.Setenv("SPEC_CONFIG_HOME", filepath.Join(t.TempDir(), "config"))
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(previous) })

	if err := cmdInit(nil); err != nil {
		t.Fatal(err)
	}
	excluded, err := gitutil.ActiveSpecExcluded(root)
	if err != nil || !excluded {
		t.Fatalf("excluded=%v err=%v", excluded, err)
	}
	if _, err := os.Stat(filepath.Join(root, ".gitignore")); !os.IsNotExist(err) {
		t.Fatalf(".gitignore was created: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, ".spec.md")); !os.IsNotExist(err) {
		t.Fatalf("active specification was created by init: %v", err)
	}
	if _, err := state.Load(root); err != nil {
		t.Fatal(err)
	}
}
