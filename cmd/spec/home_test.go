package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/TaylorEdgerton/spec-cli/internal/state"
)

func TestHomeMenusReflectWorkspaceState(t *testing.T) {
	if got, want := homeMenuItems(false), []string{"Create a spec", "Explore codebase", "Recent changes", "Create a doc", "Exit"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("idle menu = %v, want %v", got, want)
	}
	if got, want := homeMenuItems(true), []string{"Resume Spec", "Explore codebase", "Recent changes", "Create a doc", "Exit"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("active menu = %v, want %v", got, want)
	}
	if got, want := recentChangesMenuItems(), []string{"Spec history", "Code changes", "Back"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("recent changes menu = %v, want %v", got, want)
	}
	if got, want := createDocumentMenuItems(), []string{"README", "Runbook", "Back"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("document menu = %v, want %v", got, want)
	}
}

func TestExploreCodebaseDoesNotCreateOrActivateSpec(t *testing.T) {
	root := t.TempDir()
	t.Setenv("SPEC_STATE_HOME", filepath.Join(t.TempDir(), "state"))
	t.Setenv("SPEC_CONFIG_HOME", filepath.Join(t.TempDir(), "config"))
	runHomeGit(t, root, "init")
	workspace, err := state.Register(root)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "internal", "permissions", "groups.go")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("package permissions\n\nfunc mapGroupsToPermissions() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	results, err := findCodebaseContext(root, "how are groups mapped to permissions")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 || results[0].Path != "internal/permissions/groups.go" {
		t.Fatalf("results = %+v", results)
	}
	if _, err := os.Stat(filepath.Join(root, ".spec.md")); !os.IsNotExist(err) {
		t.Fatalf("standalone exploration created .spec.md: %v", err)
	}
	workspace, err = state.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if workspace.Active || workspace.Setup != nil {
		t.Fatalf("standalone exploration changed workspace state: %+v", workspace.Metadata)
	}
}

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
