package documents

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TaylorEdgerton/spec-cli/internal/config"
)

func TestDocumentHelpersPreserveExistingFiles(t *testing.T) {
	configureTest(t)
	root := t.TempDir()
	created, prompt, err := README(root, "README.md")
	if err != nil || !created || prompt != "" {
		t.Fatalf("README create: %v %q %v", created, prompt, err)
	}
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("# Existing\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	created, prompt, err = README(root, "README.md")
	if err != nil || created || !strings.Contains(prompt, "# Existing") {
		t.Fatalf("README update: %v %q %v", created, prompt, err)
	}
	data, err := os.ReadFile(filepath.Join(root, "README.md"))
	if err != nil || string(data) != "# Existing\n" {
		t.Fatalf("README was replaced: %q %v", data, err)
	}

	first, err := ADR(root, "Choose Queue")
	if err != nil {
		t.Fatal(err)
	}
	second, err := ADR(root, "Use Cache")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(first) != "0001-choose-queue.md" || filepath.Base(second) != "0002-use-cache.md" {
		t.Fatalf("ADR names: %s, %s", first, second)
	}
}

func TestREADMECanCreateIndependentDirectoryDocumentation(t *testing.T) {
	configureTest(t)
	root := t.TempDir()
	scripts := filepath.Join(root, "scripts")
	if err := os.Mkdir(scripts, 0o755); err != nil {
		t.Fatal(err)
	}

	created, prompt, err := README(root, "README.md")
	if err != nil || !created || prompt != "" {
		t.Fatalf("root README create: %v %q %v", created, prompt, err)
	}
	created, prompt, err = README(scripts, "scripts/README.md")
	if err != nil || !created || prompt != "" {
		t.Fatalf("scripts README create: %v %q %v", created, prompt, err)
	}

	if err := os.WriteFile(filepath.Join(scripts, "README.md"), []byte("# Scripts\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	created, prompt, err = README(scripts, "scripts/README.md")
	if err != nil || created {
		t.Fatalf("scripts README update: %v %q %v", created, prompt, err)
	}
	if !strings.Contains(prompt, "Update scripts/README.md.") || !strings.Contains(prompt, "# Scripts") {
		t.Fatalf("scripts README prompt: %q", prompt)
	}
}

func TestRunbooksUseScenarioFilesAndPrepareExistingContent(t *testing.T) {
	configureTest(t)
	root := t.TempDir()

	path, created, prompt, err := Runbook(root, "Database Recovery")
	if err != nil || !created || prompt != "" {
		t.Fatalf("runbook create: %s %v %q %v", path, created, prompt, err)
	}
	want := filepath.Join(root, "docs", "runbooks", "database-recovery.md")
	if path != want {
		t.Fatalf("runbook path = %s, want %s", path, want)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "# Database Recovery") {
		t.Fatalf("runbook content: %q", content)
	}
	if strings.Contains(string(content), "Available fields") {
		t.Fatalf("template documentation was rendered: %q", content)
	}

	path, created, prompt, err = Runbook(root, "Database Recovery")
	if err != nil || created || !strings.Contains(prompt, "Update docs/runbooks/database-recovery.md.") {
		t.Fatalf("runbook update: %s %v %q %v", path, created, prompt, err)
	}
	paths, err := Runbooks(root)
	if err != nil || len(paths) != 1 || paths[0] != "docs/runbooks/database-recovery.md" {
		t.Fatalf("runbooks: %v, %v", paths, err)
	}
}

func TestDocumentHelpersUseCustomizedGlobalTemplates(t *testing.T) {
	configureTest(t)
	templateDirectory := filepath.Join(os.Getenv("SPEC_CONFIG_HOME"), "templates")
	if err := os.MkdirAll(templateDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(templateDirectory, "readme.md"), []byte("# Custom Project\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(templateDirectory, "adr.md"), []byte("# {{.Number}} - {{.Title}}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(templateDirectory, "runbook.md"), []byte("# Run: {{.Title}}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	root := t.TempDir()
	if _, _, err := README(root, "README.md"); err != nil {
		t.Fatal(err)
	}
	readme, _ := os.ReadFile(filepath.Join(root, "README.md"))
	if string(readme) != "# Custom Project\n" {
		t.Fatalf("custom README: %q", readme)
	}
	adr, err := ADR(root, "Choose Queue")
	if err != nil {
		t.Fatal(err)
	}
	adrContent, _ := os.ReadFile(adr)
	if string(adrContent) != "# 0001 - Choose Queue\n" {
		t.Fatalf("custom ADR: %q", adrContent)
	}
	runbook, _, _, err := Runbook(root, "Restore Queue")
	if err != nil {
		t.Fatal(err)
	}
	runbookContent, _ := os.ReadFile(runbook)
	if string(runbookContent) != "# Run: Restore Queue\n" {
		t.Fatalf("custom runbook: %q", runbookContent)
	}
}

func configureTest(t *testing.T) {
	t.Helper()
	t.Setenv("SPEC_CONFIG_HOME", filepath.Join(t.TempDir(), "config"))
	t.Setenv("SPEC_STATE_HOME", filepath.Join(t.TempDir(), "state"))
	if _, err := config.InstallDefaults(); err != nil {
		t.Fatal(err)
	}
}
