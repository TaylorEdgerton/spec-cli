package change

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/TaylorEdgerton/spec-cli/internal/gitutil"
	"github.com/TaylorEdgerton/spec-cli/internal/state"
)

func TestLifecycleUsesWorkspaceSpecAndArchivesIt(t *testing.T) {
	root := t.TempDir()
	t.Setenv("SPEC_STATE_HOME", filepath.Join(t.TempDir(), "state"))
	t.Setenv("SPEC_CONFIG_HOME", filepath.Join(t.TempDir(), "config"))
	runGit(t, root, "init")
	if _, err := gitutil.EnsureActiveSpecExcluded(root); err != nil {
		t.Fatal(err)
	}
	if _, err := state.Register(root); err != nil {
		t.Fatal(err)
	}
	if _, err := New(root, "change one", time.Now()); err == nil || !strings.Contains(err.Error(), "baseline") {
		t.Fatalf("expected baseline error, got %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "script.py"), []byte("print('one')\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, root, "add", "script.py")
	runGit(t, root, "-c", "user.name=Spec Test", "-c", "user.email=spec@example.invalid", "commit", "-m", "baseline")

	path, err := New(root, "change one", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if path != filepath.Join(root, ActiveFilename) {
		t.Fatalf("active path = %s", path)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(content), "# change one\n\n## Intent\n\nchange one\n") {
		t.Fatalf("active specification:\n%s", content)
	}
	if status := gitOutput(t, root, "status", "--porcelain"); status != "" {
		t.Fatalf(".spec.md appears in Git status: %s", status)
	}
	if _, err := New(root, "change two", time.Now()); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("expected active change error, got %v", err)
	}

	if err := os.WriteFile(filepath.Join(root, "script.py"), []byte("print('two')\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	record, err := Done(root, "complete", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(record.ChangedFiles) != 1 || record.ChangedFiles[0] != "script.py" {
		t.Fatalf("changed files: %v", record.ChangedFiles)
	}
	workspace, err := state.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if workspace.Active {
		t.Fatal("workspace stayed active")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("active specification remains: %v", err)
	}
	if record.SpecArchive == "" {
		t.Fatal("archive path is empty")
	}
	archived, err := os.ReadFile(filepath.Join(workspace.Dir, filepath.FromSlash(record.SpecArchive)))
	if err != nil {
		t.Fatal(err)
	}
	if string(archived) != string(content) {
		t.Fatalf("archive differs:\n%s", archived)
	}
	if _, err := os.Stat(filepath.Join(workspace.Dir, "current.md")); !os.IsNotExist(err) {
		t.Fatalf("obsolete current.md exists: %v", err)
	}
}

func TestNewRefusesExistingSpecFile(t *testing.T) {
	root := committedRepo(t)
	if _, err := state.Register(root); err != nil {
		t.Fatal(err)
	}
	path := ActivePath(root)
	if err := os.WriteFile(path, []byte("existing"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := New(root, "replacement", time.Now()); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("expected existing file error, got %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "existing" {
		t.Fatalf("existing file changed: %q %v", data, err)
	}
}

func TestNewRecoversAfterManualRemoval(t *testing.T) {
	root := committedRepo(t)
	if _, err := state.Register(root); err != nil {
		t.Fatal(err)
	}
	path, err := New(root, "first", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, err := New(root, "second", time.Now()); err != nil {
		t.Fatal(err)
	}
	workspace, err := state.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if !workspace.Active || workspace.Title != "second" {
		t.Fatalf("workspace after recovery: %+v", workspace.Metadata)
	}
}

func TestSectionSummaryUsesIntent(t *testing.T) {
	content := "# Change\n\n## Intent\n\nFix reconnect handling.\n\n## Scope\n\nDatabase only.\n"
	if got := sectionSummary(content, "Intent"); got != "Fix reconnect handling." {
		t.Fatalf("summary = %q", got)
	}
}

func committedRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	t.Setenv("SPEC_STATE_HOME", filepath.Join(t.TempDir(), "state"))
	t.Setenv("SPEC_CONFIG_HOME", filepath.Join(t.TempDir(), "config"))
	runGit(t, root, "init")
	if err := os.WriteFile(filepath.Join(root, "file.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, root, "add", "file.txt")
	runGit(t, root, "-c", "user.name=Spec Test", "-c", "user.email=spec@example.invalid", "commit", "-m", "baseline")
	return root
}

func runGit(t *testing.T, root string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
}

func gitOutput(t *testing.T, root string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
	return strings.TrimSpace(string(output))
}
