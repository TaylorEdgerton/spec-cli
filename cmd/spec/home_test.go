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

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/TaylorEdgerton/spec-cli/internal/state"
	"github.com/charmbracelet/x/ansi"
)

func TestFramedHomeUsesOneCanonicalOrderForRenderingAndNavigation(t *testing.T) {
	started := time.Date(2026, 8, 31, 8, 0, 0, 0, time.UTC)
	model := newHomeModel(homeData{
		Registered: true, GitWorkspace: true, Active: true,
		Title: "Disable automatic indexing", Stage: "Implementation",
		Branch: "main", GitState: "clean", StartedAt: started, Now: started.Add(2*time.Hour + 14*time.Minute),
	})
	want := []string{"home.resume", "home.explore", "home.recent", "home.documents", "home.exit"}
	if got := model.screen().selectableItemIDs(); !reflect.DeepEqual(got, want) {
		t.Fatalf("home canonical order = %v, want %v", got, want)
	}
	model.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	plain := ansi.Strip(model.View().Content)
	assertTextOrder(t, plain, "Active change", "Disable automatic indexing", "Status", "Implementation", "Started", "2h 14m ago", "Resume change", "Explore codebase", "Recent changes", "Create a doc", "Exit")
	if !strings.Contains(plain, "esc stays") || lipgloss.Width(model.View().Content) > 80 || lipgloss.Height(model.View().Content) > 24 {
		t.Fatalf("home frame contract failed:\n%s", plain)
	}

	before := model.data
	model.Update(key(tea.KeyDown, ""))
	if model.data != before {
		t.Fatalf("cursor movement mutated home facts: before=%+v after=%+v", before, model.data)
	}
}

func TestHomeCoversIdleAndUnregisteredWorkspaceActions(t *testing.T) {
	idle := newHomeModel(homeData{Registered: true, GitWorkspace: true, Branch: "main", GitState: "clean"})
	if got, want := idle.screen().selectableItemIDs(), []string{"home.new", "home.explore", "home.recent", "home.documents", "home.exit"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("idle actions = %v, want %v", got, want)
	}
	if plain := ansi.Strip(idle.View().Content); !strings.Contains(plain, "No active change") || !strings.Contains(plain, "Create a change") {
		t.Fatalf("idle Home summary missing:\n%s", plain)
	}

	for name, data := range map[string]homeData{
		"unregistered": {GitWorkspace: true},
		"no-git":       {},
	} {
		model := newHomeModel(data)
		if got, want := model.screen().selectableItemIDs(), []string{"home.initialize", "home.exit"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("%s actions = %v, want %v", name, got, want)
		}
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

	results := discoverImplementationContext(root, state.Setup{Title: "how are groups mapped to permissions"})
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
	if err := workspace.BeginSetup("base", time.Now(), state.Setup{Stage: "criteria", Title: "Change"}); err != nil {
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
