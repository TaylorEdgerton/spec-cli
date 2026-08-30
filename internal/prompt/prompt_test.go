package prompt

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/TaylorEdgerton/spec-cli/internal/change"
	"github.com/TaylorEdgerton/spec-cli/internal/discovery"
	"github.com/TaylorEdgerton/spec-cli/internal/state"
)

func TestBuildIncludesChangeContractAndOptionalPlanInstructions(t *testing.T) {
	root := promptRepository(t, state.Setup{
		Title: "Disable automatic indexing", Outcome: "Manual indexing remains available",
		Criteria: []state.SetupCriterion{{Text: "Automatic indexing remains enabled by default", Included: true}},
	})
	content, _, err := Build(root, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		"Disable automatic indexing", "Manual indexing remains available", "Automatic indexing remains enabled by default",
		"```spec-plan", `"summary"`, `"files"`, `"integration_points"`, `"verification"`, `"uncertainties"`,
		"optional", "advisory",
	} {
		if !strings.Contains(content, expected) {
			t.Fatalf("prompt missing %q:\n%s", expected, content)
		}
	}
}

func TestBuildContinuesWhenDiscoveryReturnsNoResultsOrFails(t *testing.T) {
	for _, test := range []struct {
		name string
		find func(string, discovery.Query) ([]discovery.Result, error)
	}{
		{name: "empty", find: func(string, discovery.Query) ([]discovery.Result, error) { return nil, nil }},
		{name: "failure", find: func(string, discovery.Query) ([]discovery.Result, error) { return nil, errors.New("index unavailable") }},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := promptRepository(t, state.Setup{Title: "Small change"})
			original := findContext
			called := false
			findContext = func(root string, query discovery.Query) ([]discovery.Result, error) {
				called = true
				return test.find(root, query)
			}
			t.Cleanup(func() { findContext = original })
			content, _, err := Build(root, false)
			if err != nil || !called || !strings.Contains(content, "Small change") {
				t.Fatalf("prompt=%q called=%v err=%v", content, called, err)
			}
		})
	}
}

func promptRepository(t *testing.T, setup state.Setup) string {
	t.Helper()
	root := t.TempDir()
	t.Setenv("SPEC_STATE_HOME", filepath.Join(t.TempDir(), "state"))
	t.Setenv("SPEC_CONFIG_HOME", filepath.Join(t.TempDir(), "config"))
	runGit(t, root, "init")
	write(t, root, "main.go", "package main\n")
	runGit(t, root, "add", ".")
	runGit(t, root, "-c", "user.name=Spec Test", "-c", "user.email=spec@example.invalid", "commit", "-m", "baseline")
	if _, err := state.Register(root); err != nil {
		t.Fatal(err)
	}
	draft, err := change.BeginSetup(root, setup.Title, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	draft.Outcome, draft.Limits, draft.Criteria = setup.Outcome, setup.Limits, setup.Criteria
	workspace, err := state.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := workspace.SaveSetup(draft); err != nil {
		t.Fatal(err)
	}
	if _, err := change.CreateSetup(root, draft); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestBuildUsesSelectedContextAndFailedVerification(t *testing.T) {
	root := t.TempDir()
	t.Setenv("SPEC_STATE_HOME", filepath.Join(t.TempDir(), "state"))
	t.Setenv("SPEC_CONFIG_HOME", filepath.Join(t.TempDir(), "config"))
	runGit(t, root, "init")
	write(t, root, "main.go", "package main\n")
	write(t, root, "unrelated.txt", "do not include this marker\n")
	runGit(t, root, "add", ".")
	runGit(t, root, "-c", "user.name=Spec Test", "-c", "user.email=spec@example.invalid", "commit", "-m", "baseline")
	workspace, err := state.Register(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := change.New(root, "add output", time.Now()); err != nil {
		t.Fatal(err)
	}
	current := "# Change\n\n## Intent\n\nAdd output.\n\n## Relevant Files\n\n- `.spec.md`\n- `main.go`\n- ../outside\n\n## Notes\n"
	if err := os.WriteFile(change.ActivePath(root), []byte(current), 0o644); err != nil {
		t.Fatal(err)
	}
	write(t, root, "main.go", "package main\n\nfunc main() {}\n")
	if err := workspace.SaveVerification(state.Verification{Passed: false, Output: "test failed marker"}); err != nil {
		t.Fatal(err)
	}
	activeWorkspace, err := state.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	compact, compactInfo, err := Build(root, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		"Relevant files\n\n- `main.go`",
		"git diff " + activeWorkspace.BaseSHA + " --",
		"The latest verification failed. Run `spec verify`",
	} {
		if !strings.Contains(compact, required) {
			t.Errorf("compact prompt does not contain %q", required)
		}
	}
	for _, excluded := range []string{"func main()", "## Current Git diff", "test failed marker"} {
		if strings.Contains(compact, excluded) {
			t.Errorf("compact prompt unexpectedly contains %q", excluded)
		}
	}
	if compactInfo.IncludedDiff || compactInfo.IncludedError {
		t.Fatalf("compact prompt reports expanded context: %+v", compactInfo)
	}

	result, info, err := Build(root, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"Read and follow `.spec.md`", "Relevant file: main.go", "Current Git diff", "test failed marker"} {
		if !strings.Contains(result, required) {
			t.Errorf("prompt does not contain %q", required)
		}
	}
	if strings.Contains(result, "## Current specification") || strings.Count(result, "Intent: Add output.") != 1 {
		t.Fatal("prompt does not contain exactly one concise change contract")
	}
	if strings.Contains(result, "Relevant file: .spec.md") {
		t.Fatal("prompt includes .spec.md as file content")
	}
	if strings.Contains(result, "do not include this marker") {
		t.Fatal("prompt includes an unrelated file")
	}
	if len(info.MissingFiles) != 1 || info.MissingFiles[0] != "../outside" {
		t.Fatalf("missing files: %v", info.MissingFiles)
	}
}

func TestBuildIncludesRelevantFileContentsOnlyWhenRequested(t *testing.T) {
	root := t.TempDir()
	t.Setenv("SPEC_STATE_HOME", filepath.Join(t.TempDir(), "state"))
	t.Setenv("SPEC_CONFIG_HOME", filepath.Join(t.TempDir(), "config"))
	runGit(t, root, "init")
	write(t, root, "README.md", "# Project\n\nrelevant file content marker\n")
	runGit(t, root, "add", ".")
	runGit(t, root, "-c", "user.name=Spec Test", "-c", "user.email=spec@example.invalid", "commit", "-m", "baseline")
	if _, err := state.Register(root); err != nil {
		t.Fatal(err)
	}
	if _, err := change.New(root, "simplify README", time.Now()); err != nil {
		t.Fatal(err)
	}
	current := "# Simplify README\n\n## Relevant Files\n\n- `README.md`\n"
	if err := os.WriteFile(change.ActivePath(root), []byte(current), 0o644); err != nil {
		t.Fatal(err)
	}

	result, info, err := Build(root, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "## Relevant files\n\n- `README.md`") {
		t.Fatalf("prompt does not list the relevant path:\n%s", result)
	}
	if strings.Contains(result, "relevant file content marker") {
		t.Fatal("default prompt embeds relevant file content")
	}
	if len(info.Files) != 1 || info.Files[0] != "README.md" {
		t.Fatalf("relevant files: %v", info.Files)
	}

	result, _, err = Build(root, true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "## Relevant file: README.md") || !strings.Contains(result, "relevant file content marker") {
		t.Fatalf("prompt does not embed requested file content:\n%s", result)
	}
}

func TestBuildDiscoversCompactContextWithoutRelevantFilesSection(t *testing.T) {
	root := t.TempDir()
	t.Setenv("SPEC_STATE_HOME", filepath.Join(t.TempDir(), "state"))
	t.Setenv("SPEC_CONFIG_HOME", filepath.Join(t.TempDir(), "config"))
	runGit(t, root, "init")
	write(t, root, "health.go", "package health\n\nfunc checkDatabaseHealth() bool { return true }\n")
	write(t, root, "unrelated.go", "package unrelated\n")
	write(t, root, "notes.txt", "report\n")
	runGit(t, root, "add", ".")
	runGit(t, root, "-c", "user.name=Spec Test", "-c", "user.email=spec@example.invalid", "commit", "-m", "baseline")
	workspace, err := state.Register(root)
	if err != nil {
		t.Fatal(err)
	}
	setup := state.Setup{
		Title:    "Report database health",
		Outcome:  "Database health is available",
		Criteria: []state.SetupCriterion{{Text: "checkDatabaseHealth reports status", Included: true}},
	}
	if err := workspace.BeginSetup("baseline", time.Now(), setup); err != nil {
		t.Fatal(err)
	}
	if _, err := change.CreateSetup(root, setup); err != nil {
		t.Fatal(err)
	}
	current, err := os.ReadFile(change.ActivePath(root))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(current), "Relevant Files") {
		t.Fatalf("guided Spec contains Relevant Files:\n%s", current)
	}

	result, info, err := Build(root, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"## Likely change area", "`health.go`", "path matches"} {
		if !strings.Contains(result, expected) {
			t.Fatalf("prompt missing %q:\n%s", expected, result)
		}
	}
	if strings.Contains(result, "`unrelated.go`") || strings.Contains(result, "`notes.txt`") || len(info.Files) != 1 || info.Files[0] != "health.go" {
		t.Fatalf("prompt context = %+v\n%s", info, result)
	}
}

func write(t *testing.T, root, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func runGit(t *testing.T, root string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
}
