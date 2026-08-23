package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallDefaultsSeedsMissingFilesWithoutOverwritingCustomTemplates(t *testing.T) {
	configHome := filepath.Join(t.TempDir(), "config")
	t.Setenv("SPEC_CONFIG_HOME", configHome)
	t.Setenv("SPEC_STATE_HOME", filepath.Join(t.TempDir(), "state"))

	created, err := InstallDefaults()
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Fatal("defaults were not installed")
	}
	loaded, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Verify) != 0 || loaded.Workspaces == nil {
		t.Fatalf("default configuration: %+v", loaded)
	}
	adrTemplate, err := Template(ADR)
	if err != nil || !strings.Contains(string(adrTemplate), ".Number") || !strings.Contains(string(adrTemplate), ".Title") {
		t.Fatalf("documented ADR template: %q, %v", adrTemplate, err)
	}

	templatePath := filepath.Join(configHome, "templates", "readme.md")
	if err := os.WriteFile(templatePath, []byte("# Custom README\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(configHome, "config.yml")
	if err := os.WriteFile(configPath, []byte("verify:\n  - custom-check\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	created, err = InstallDefaults()
	if err != nil {
		t.Fatal(err)
	}
	if created {
		t.Fatal("existing defaults were reported as installed")
	}
	content, err := Template(README)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "# Custom README\n" {
		t.Fatalf("custom template was replaced: %q", content)
	}
	configContent, err := os.ReadFile(configPath)
	if err != nil || string(configContent) != "verify:\n  - custom-check\n" {
		t.Fatalf("custom config was replaced: %q, %v", configContent, err)
	}
}

func TestReadersDoNotCreateMissingConfiguration(t *testing.T) {
	configHome := filepath.Join(t.TempDir(), "config")
	t.Setenv("SPEC_CONFIG_HOME", configHome)
	t.Setenv("SPEC_STATE_HOME", filepath.Join(t.TempDir(), "state"))

	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "run `spec init`") {
		t.Fatalf("missing config error: %v", err)
	}
	if _, err := Template(README); err == nil || !strings.Contains(err.Error(), "run `spec init`") {
		t.Fatalf("missing template error: %v", err)
	}
	if _, err := os.Stat(configHome); !os.IsNotExist(err) {
		t.Fatalf("reader created configuration directory: %v", err)
	}
}

func TestVerificationCommandsDoNotDetectProjectTools(t *testing.T) {
	root := t.TempDir()
	t.Setenv("SPEC_CONFIG_HOME", filepath.Join(t.TempDir(), "config"))
	t.Setenv("SPEC_STATE_HOME", filepath.Join(t.TempDir(), "state"))
	if _, err := InstallDefaults(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "Makefile"), []byte("test:\n\ttrue\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	commands, err := VerificationCommands(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(commands) != 0 {
		t.Fatalf("detected unconfigured commands: %v", commands)
	}

	configPath := filepath.Join(os.Getenv("SPEC_CONFIG_HOME"), "config.yml")
	configured := strings.Replace("verify: []\nworkspaces: {}\n", "verify: []", "verify:\n  - custom-check", 1)
	if err := os.WriteFile(configPath, []byte(configured), 0o600); err != nil {
		t.Fatal(err)
	}
	commands, err = VerificationCommands(root)
	if err != nil || len(commands) != 1 || commands[0] != "custom-check" {
		t.Fatalf("configured commands: %v, %v", commands, err)
	}
}
