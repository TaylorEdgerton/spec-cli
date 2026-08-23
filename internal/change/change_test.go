package change

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/TaylorEdgerton/spec-cli/internal/aiusage"
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
	finalUsage := &aiusage.Summary{
		Available: true,
		Usage: []aiusage.AIUsage{{
			Provider: "Codex", Model: "gpt-5.6-sol", InputTokens: 120, OutputTokens: 30, Requests: 2,
		}},
		SandboxDurationSeconds: 90,
	}
	record, err := DoneWithUsage(root, "complete", time.Now(), finalUsage)
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
	if workspace.SandboxSession != nil {
		t.Fatalf("sandbox session was not cleared: %+v", workspace.SandboxSession)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("active specification remains: %v", err)
	}
	if record.SpecArchive == "" {
		t.Fatal("archive path is empty")
	}
	if record.AIUsage == nil || !record.AIUsage.Available || record.AIUsage.Usage[0].InputTokens != 120 {
		t.Fatalf("AI usage = %+v", record.AIUsage)
	}
	history, err := os.ReadFile(filepath.Join(workspace.Dir, "history.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(history), `"ai_usage"`) || !strings.Contains(string(history), `"input_tokens":120`) {
		t.Fatalf("history does not contain aggregate AI usage: %s", history)
	}
	if strings.Contains(string(history), "prompt text must remain private") {
		t.Fatalf("history contains raw telemetry content: %s", history)
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

func TestPopulateWritesGuidedSpecWithUncheckedCriteria(t *testing.T) {
	root := committedRepo(t)
	if _, err := state.Register(root); err != nil {
		t.Fatal(err)
	}
	path, err := New(root, "Fix reconnect handling", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	guided := GuidedSpec{
		Change:              "Fix reconnect handling",
		Reason:              "Connections do not recover after an outage.",
		Outcome:             "The application reconnects automatically.",
		MustNotBreak:        "Health checks remain responsive.",
		ImportantConstraint: "Do not change the database library.",
		RelevantFiles:       []string{"src/db/database.go", "tests/test_database.go"},
		AcceptanceCriteria: []string{
			"The application reconnects automatically after a temporary outage",
			"An application restart is not required",
		},
	}
	if err := Populate(root, guided); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	for _, expected := range []string{
		"# Fix reconnect handling",
		"## Intent\n\nConnections do not recover after an outage.",
		"- Must not break: Health checks remain responsive.",
		"- Do not change the database library.",
		"- [ ] The application reconnects automatically after a temporary outage",
		"- [ ] An application restart is not required",
		"- `src/db/database.go`",
		"- `tests/test_database.go`",
	} {
		if !strings.Contains(content, expected) {
			t.Errorf("guided specification does not contain %q:\n%s", expected, content)
		}
	}
	if strings.Contains(content, "- [x]") {
		t.Fatalf("criteria were incorrectly saved as completed:\n%s", content)
	}
	workspace, err := state.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if workspace.Title != guided.Change {
		t.Fatalf("workspace title = %q", workspace.Title)
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
