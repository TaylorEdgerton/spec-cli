package prompt

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/TaylorEdgerton/spec-cli/internal/change"
	"github.com/TaylorEdgerton/spec-cli/internal/state"
)

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
	result, info, err := Build(root, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"Read and follow `.spec.md`", "Relevant file: main.go", "Current Git diff", "test failed marker"} {
		if !strings.Contains(result, required) {
			t.Errorf("prompt does not contain %q", required)
		}
	}
	if strings.Contains(result, "Add output.") || strings.Contains(result, "## Current specification") {
		t.Fatal("prompt duplicates the active specification")
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
