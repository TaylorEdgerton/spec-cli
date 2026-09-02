package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/TaylorEdgerton/spec-cli/internal/change"
	"github.com/TaylorEdgerton/spec-cli/internal/gitutil"
	"github.com/TaylorEdgerton/spec-cli/internal/state"
	"github.com/charmbracelet/x/ansi"
)

func TestDefinitionUsesStableFieldOrderAndIntentValidation(t *testing.T) {
	model := newDefinitionModel(state.Setup{}, "clean")
	if got, want := model.screen().selectableItemIDs(), []string{definitionIntentID, definitionScopeID, definitionAcceptanceID, definitionCreateID}; !reflect.DeepEqual(got, want) {
		t.Fatalf("field IDs = %v, want %v", got, want)
	}
	if model.createEnabled() {
		t.Fatal("create enabled without intent")
	}
	for _, want := range []string{definitionScopeID, definitionAcceptanceID, definitionCreateID, definitionIntentID} {
		updateModel(model, key(tea.KeyTab, ""))
		if model.focusedID() != want {
			t.Fatalf("focused ID = %q, want %q", model.focusedID(), want)
		}
	}
	updateModel(model, key(tea.KeyEnter, ""))
	updateModel(model, tea.PasteMsg{Content: "Disable automatic indexing"})
	updateModel(model, key(tea.KeyEnter, ""))
	if !model.createEnabled() || model.result().Title != "Disable automatic indexing" {
		t.Fatalf("intent state = %+v enabled=%v", model.result(), model.createEnabled())
	}
}

func TestDefinitionAllowsEmptyScopeAndAcceptanceAndRequiresExplicitCreate(t *testing.T) {
	model := newDefinitionModel(state.Setup{Title: "Keep manual indexing"}, "dirty")
	updateModel(model, tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter, Mod: tea.ModCtrl}))
	if !model.done || !model.created || model.cancelled {
		t.Fatalf("ctrl+enter result = %+v", model)
	}
	result := model.result()
	if result.Outcome != "" || len(result.Criteria) != 0 {
		t.Fatalf("optional fields changed = %+v", result)
	}

	cancelled := newDefinitionModel(state.Setup{Title: "Do not create"}, "clean")
	updateModel(cancelled, key('q', "q"))
	if cancelled.done || cancelled.cancelled || cancelled.created || cancelled.nav != actionNone {
		t.Fatalf("q directly left a non-Home screen: %+v", cancelled)
	}
}

func TestDefinitionEnterEditsSelectedFieldsAndAcceptance(t *testing.T) {
	model := newDefinitionModel(state.Setup{Title: "Intent"}, "clean")
	model.focus = 1
	updateModel(model, key(tea.KeyEnter, ""))
	updateModel(model, tea.PasteMsg{Content: "Automatic indexing can be disabled.\nManual indexing remains available."})
	updateModel(model, key(tea.KeyEnter, ""))
	if model.result().Outcome != "Automatic indexing can be disabled.\nManual indexing remains available." {
		t.Fatalf("scope = %q", model.result().Outcome)
	}
	model.focus = 2
	updateModel(model, key(tea.KeyEnter, ""))
	updateModel(model, tea.PasteMsg{Content: "Automatic indexing remains enabled by default"})
	updateModel(model, key(tea.KeyEnter, ""))
	if len(model.result().Criteria) != 1 || model.result().Criteria[0].Text != "Automatic indexing remains enabled by default" {
		t.Fatalf("acceptance = %+v", model.result().Criteria)
	}
}

func TestDefinitionResumeRetainsDraftAndLegacyConstraints(t *testing.T) {
	draft := state.Setup{
		Stage: "definition", Title: "Draft intent", Outcome: "Draft scope", Limits: "Keep compatibility",
		Criteria: []state.SetupCriterion{{Text: "Draft acceptance", Included: true}},
	}
	model := newDefinitionModel(draft, "dirty")
	if !reflect.DeepEqual(model.result(), draft) || model.gitState != "dirty" {
		t.Fatalf("resumed model = %+v", model)
	}
}

func TestDefinitionRendersThroughSharedFullScreenShell(t *testing.T) {
	model := newDefinitionModel(state.Setup{Title: "Disable automatic indexing"}, "dirty")
	updateModel(model, tea.WindowSizeMsg{Width: 100, Height: 32})
	view := model.View().Content
	plain := ansi.Strip(view)
	for _, expected := range []string{"Spec · New Change", "Git: dirty", "Define Change", "Intent", "Scope / expected behaviour", "Acceptance", "Create Spec", "Ctrl+Enter"} {
		if !strings.Contains(plain, expected) {
			t.Fatalf("definition view missing %q:\n%s", expected, plain)
		}
	}
	if width := lipgloss.Width(view); width > 100 {
		t.Fatalf("definition width = %d", width)
	}
}

func TestCancelledDefinitionPersistsDraftWithoutCreatingContract(t *testing.T) {
	root, workspace, _ := definitionRepository(t, false)
	draft := *workspace.Setup
	draft.Stage = "definition"
	draft.Title = "Paused intent"
	draft.Outcome = "Paused scope"
	var output bytes.Buffer
	if err := saveAndExit(root, draft, &output); err != nil {
		t.Fatal(err)
	}
	loaded, err := state.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Setup == nil || loaded.Setup.Title != "Paused intent" || loaded.Setup.Outcome != "Paused scope" {
		t.Fatalf("saved draft = %+v", loaded.Setup)
	}
	if _, err := os.Stat(change.ActivePath(root)); !os.IsNotExist(err) {
		t.Fatalf("cancel created contract: %v", err)
	}
}

func TestCompleteDefinitionCapturesBaselineWritesContractAndAutoCopiesPrompt(t *testing.T) {
	root, workspace, started := definitionRepository(t, true)
	setup := *workspace.Setup
	setup.Title = "Disable automatic indexing"
	setup.Outcome = "Manual indexing remains available"
	setup.Criteria = nil
	if err := workspace.SaveSetup(setup); err != nil {
		t.Fatal(err)
	}
	var copied string
	var output bytes.Buffer
	path, err := completeDefinition(root, setup, &output, definitionServices{
		BuildPrompt: func(string) (string, error) { return "implementation prompt", nil },
		CopyPrompt:  func(content string) error { copied = content; return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"## Intent\n\nDisable automatic indexing", "## Scope\n\nManual indexing remains available", "## Acceptance Criteria\n", "## Notes\n"} {
		if !strings.Contains(string(data), expected) {
			t.Fatalf("contract missing %q:\n%s", expected, data)
		}
	}
	if strings.Contains(string(data), "Relevant Files") || copied != "implementation prompt" || !strings.Contains(output.String(), "copied") {
		t.Fatalf("completion output=%q copied=%q contract=%s", output.String(), copied, data)
	}
	loaded, err := state.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.BaseSHA == "" || !loaded.StartedAt.Equal(started) || loaded.GitState != "dirty" || loaded.Setup != nil {
		t.Fatalf("creation metadata = %+v", loaded.Metadata)
	}
	events, err := loaded.TimelineEvents()
	if err != nil {
		t.Fatal(err)
	}
	last := events[len(events)-1]
	if last.Type != state.TimelinePromptCopied || last.Source != "clipboard" || last.Details.PromptKind != "implementation" {
		t.Fatalf("prompt delivery event = %+v", last)
	}
}

func TestCompleteDefinitionPrintsUsableFallbackWhenClipboardFails(t *testing.T) {
	root, workspace, _ := definitionRepository(t, false)
	if workspace.GitState != "clean" {
		t.Fatalf("clean repository label = %q", workspace.GitState)
	}
	setup := *workspace.Setup
	setup.Title = "Fallback prompt"
	if err := workspace.SaveSetup(setup); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	_, err := completeDefinition(root, setup, &output, definitionServices{
		BuildPrompt: func(string) (string, error) { return "PROMPT BODY", nil },
		CopyPrompt:  func(string) error { return errors.New("clipboard unavailable") },
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"clipboard unavailable", "PROMPT BODY", "spec prompt --copy"} {
		if !strings.Contains(output.String(), expected) {
			t.Fatalf("fallback missing %q: %s", expected, output.String())
		}
	}
	loaded, err := state.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	events, err := loaded.TimelineEvents()
	if err != nil {
		t.Fatal(err)
	}
	last := events[len(events)-1]
	if last.Type != state.TimelinePromptPrinted || last.Source != "stdout" || last.Details.PromptKind != "implementation" {
		t.Fatalf("fallback delivery event = %+v", last)
	}
}

func TestCompleteDefinitionEditPreservesCustomNotes(t *testing.T) {
	root, workspace, _ := definitionRepository(t, false)
	setup := *workspace.Setup
	setup.Title = "Original"
	if err := workspace.SaveSetup(setup); err != nil {
		t.Fatal(err)
	}
	path, err := completeDefinition(root, setup, &bytes.Buffer{}, definitionServices{
		BuildPrompt: func(string) (string, error) { return "prompt", nil }, CopyPrompt: func(string) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	data = []byte(strings.Replace(string(data), "## Notes\n", "## Notes\n\nKeep this custom note.\n", 1))
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	edit, err := change.BeginEdit(root)
	if err != nil {
		t.Fatal(err)
	}
	edit.Title = "Updated"
	workspace, err = state.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := workspace.SaveSetup(edit); err != nil {
		t.Fatal(err)
	}
	if _, err := completeDefinition(root, edit, &bytes.Buffer{}, definitionServices{
		BuildPrompt: func(string) (string, error) { return "prompt", nil }, CopyPrompt: func(string) error { return nil },
	}); err != nil {
		t.Fatal(err)
	}
	updated, _ := os.ReadFile(path)
	if !strings.Contains(string(updated), "Keep this custom note.") || !strings.Contains(string(updated), "# Updated") {
		t.Fatalf("edited contract:\n%s", updated)
	}
}

func TestRunNewNonInteractiveKeepsTitleOnlyContract(t *testing.T) {
	root := t.TempDir()
	t.Setenv("SPEC_STATE_HOME", filepath.Join(t.TempDir(), "state"))
	t.Setenv("SPEC_CONFIG_HOME", filepath.Join(t.TempDir(), "config"))
	runDefinitionGit(t, root, "init")
	if err := os.WriteFile(filepath.Join(root, "base.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runDefinitionGit(t, root, "add", "base.txt")
	runDefinitionGit(t, root, "-c", "user.name=Spec Test", "-c", "user.email=spec@example.invalid", "commit", "-m", "baseline")
	if _, err := state.Register(root); err != nil {
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
	if err := runNew([]string{"CLI", "change"}, strings.NewReader(""), &output, false); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(change.ActivePath(root))
	if err != nil || !strings.Contains(string(data), "# CLI change\n\n## Intent\n\nCLI change") {
		t.Fatalf("non-interactive contract=%q err=%v", data, err)
	}
}

func definitionRepository(t *testing.T, dirty bool) (string, state.Workspace, time.Time) {
	t.Helper()
	root := t.TempDir()
	t.Setenv("SPEC_STATE_HOME", filepath.Join(t.TempDir(), "state"))
	t.Setenv("SPEC_CONFIG_HOME", filepath.Join(t.TempDir(), "config"))
	runDefinitionGit(t, root, "init")
	if err := os.WriteFile(filepath.Join(root, "base.go"), []byte("package base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runDefinitionGit(t, root, "add", "base.go")
	runDefinitionGit(t, root, "-c", "user.name=Spec Test", "-c", "user.email=spec@example.invalid", "commit", "-m", "baseline")
	if dirty {
		if err := os.WriteFile(filepath.Join(root, "dirty.txt"), []byte("dirty\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	workspace, err := state.Register(root)
	if err != nil {
		t.Fatal(err)
	}
	started := time.Date(2026, 8, 30, 5, 0, 0, 0, time.UTC)
	if _, err := change.BeginSetup(root, "", started); err != nil {
		t.Fatal(err)
	}
	workspace, err = state.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	base, err := gitutil.Head(root)
	if err != nil || workspace.BaseSHA != base {
		t.Fatalf("baseline = %q, want %q, err=%v", workspace.BaseSHA, base, err)
	}
	return root, workspace, started
}

func runDefinitionGit(t *testing.T, root string, args ...string) {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", root}, args...)...)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
}
