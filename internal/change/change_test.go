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
	verifyrun "github.com/TaylorEdgerton/spec-cli/internal/verify"
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
	if err := os.WriteFile(filepath.Join(os.Getenv("SPEC_CONFIG_HOME"), "config.yml"), []byte("verify:\n  - 'true'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := verifyrun.Run(root, time.Now()); err != nil {
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

func TestSetupCreatesSpecWithUncheckedCriteria(t *testing.T) {
	root := committedRepo(t)
	if _, err := state.Register(root); err != nil {
		t.Fatal(err)
	}
	setup, err := BeginSetup(root, "Fix reconnect handling", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	setup.Outcome = "The application reconnects automatically."
	setup.Limits = "Do not change the database library."
	setup.Criteria = []state.SetupCriterion{
		{Text: "The application reconnects automatically after a temporary outage", Included: true},
		{Text: "An application restart is not required", Included: true},
		{Text: "Excluded suggestion", Included: false},
	}
	workspace, err := state.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := workspace.SaveSetup(setup); err != nil {
		t.Fatal(err)
	}
	path, err := CreateSetup(root, setup)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	for _, expected := range []string{
		"# Fix reconnect handling",
		"## Intent\n\nFix reconnect handling",
		"## Scope\n\nThe application reconnects automatically.",
		"- Do not change the database library.",
		"- [ ] The application reconnects automatically after a temporary outage",
		"- [ ] An application restart is not required",
	} {
		if !strings.Contains(content, expected) {
			t.Errorf("created specification does not contain %q:\n%s", expected, content)
		}
	}
	if strings.Contains(content, "Excluded suggestion") || strings.Contains(content, "- [x]") {
		t.Fatalf("created criteria are incorrect:\n%s", content)
	}
	workspace, err = state.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if workspace.Title != setup.Title || workspace.Setup != nil {
		t.Fatalf("workspace after setup = %+v", workspace.Metadata)
	}
}

func TestEditSetupPreservesOtherSectionsAndInvalidatesVerification(t *testing.T) {
	root := committedRepo(t)
	workspace, err := state.Register(root)
	if err != nil {
		t.Fatal(err)
	}
	setup, err := BeginSetup(root, "Original change", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	setup.Outcome = "Original outcome"
	setup.Criteria = []state.SetupCriterion{{Text: "Original criterion", Included: true}}
	workspace, err = state.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := workspace.SaveSetup(setup); err != nil {
		t.Fatal(err)
	}
	path, err := CreateSetup(root, setup)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	data = []byte(strings.Replace(string(data), "## Notes\n", "## Notes\n\nKeep this note.\n", 1))
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	workspace, err = state.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := workspace.SetVerificationCommands([]string{"true"}); err != nil {
		t.Fatal(err)
	}
	if err := workspace.SaveVerification(state.Verification{Passed: true}); err != nil {
		t.Fatal(err)
	}
	edit, err := BeginEdit(root)
	if err != nil {
		t.Fatal(err)
	}
	edit.Title = "Updated change"
	edit.Outcome = "Updated outcome"
	edit.Limits = "Keep compatibility"
	edit.Criteria = []state.SetupCriterion{{Text: "Updated criterion", Included: true}}
	workspace, err = state.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := workspace.SaveSetup(edit); err != nil {
		t.Fatal(err)
	}
	if _, err := SaveSetup(root, edit); err != nil {
		t.Fatal(err)
	}
	updated, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"# Updated change", "Updated outcome", "- Keep compatibility", "- [ ] Updated criterion", "Keep this note."} {
		if !strings.Contains(string(updated), expected) {
			t.Fatalf("updated specification missing %q:\n%s", expected, updated)
		}
	}
	workspace, err = state.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	verification, err := workspace.Verification()
	if err != nil || verification != nil {
		t.Fatalf("verification was not invalidated: %+v, %v", verification, err)
	}
	if workspace.Setup != nil || workspace.Title != "Updated change" || len(workspace.VerifyCommands) != 1 {
		t.Fatalf("workspace after edit = %+v", workspace.Metadata)
	}
}

func TestDeletedSpecCanBeRecreatedFromPausedEdit(t *testing.T) {
	root := committedRepo(t)
	if _, err := state.Register(root); err != nil {
		t.Fatal(err)
	}
	setup, err := BeginSetup(root, "Recover deleted Spec", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	setup.Outcome = "The guided content is restored"
	setup.Criteria = []state.SetupCriterion{{Text: "The Spec exists again", Included: true}}
	workspace, err := state.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := workspace.SaveSetup(setup); err != nil {
		t.Fatal(err)
	}
	path, err := CreateSetup(root, setup)
	if err != nil {
		t.Fatal(err)
	}
	edit, err := BeginEdit(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, err := SaveSetup(root, edit); err == nil || !strings.Contains(err.Error(), "run `spec` to recover") {
		t.Fatalf("missing edit error = %v", err)
	}
	if _, err := CreateSetup(root, edit); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"# Recover deleted Spec", "The guided content is restored", "- [ ] The Spec exists again"} {
		if !strings.Contains(string(data), expected) {
			t.Fatalf("recreated specification missing %q:\n%s", expected, data)
		}
	}
	workspace, err = state.Load(root)
	if err != nil || workspace.Setup != nil {
		t.Fatalf("workspace after recreation = %+v, %v", workspace.Metadata, err)
	}
}

func TestAcceptanceCriteriaParsesAndUpdatesLegacyLists(t *testing.T) {
	markdown := "# Change\n\n## Acceptance Criteria\n\n- [x] Already reviewed\n- [ ] Still open\n- Legacy item\n\n## Notes\n"
	criteria := AcceptanceCriteria(markdown)
	if len(criteria) != 3 || !criteria[0].Checked || criteria[1].Checked || criteria[2].Text != "Legacy item" {
		t.Fatalf("criteria = %+v", criteria)
	}
	for index := range criteria {
		criteria[index].Checked = true
	}
	updated, err := UpdateAcceptanceCriteria(markdown, criteria)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		"- [x] Already reviewed", "- [x] Still open", "- [x] Legacy item", "## Notes",
	} {
		if !strings.Contains(updated, expected) {
			t.Fatalf("updated criteria missing %q:\n%s", expected, updated)
		}
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
