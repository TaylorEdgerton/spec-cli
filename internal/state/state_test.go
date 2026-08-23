package state

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRegisterUsesExternalState(t *testing.T) {
	root := t.TempDir()
	stateHome := filepath.Join(t.TempDir(), "state")
	configHome := filepath.Join(t.TempDir(), "config")
	t.Setenv("SPEC_STATE_HOME", stateHome)
	t.Setenv("SPEC_CONFIG_HOME", configHome)

	workspace, err := Register(root)
	if err != nil {
		t.Fatal(err)
	}
	if workspace.Root != root || workspace.ID == "" {
		t.Fatalf("unexpected workspace: %+v", workspace)
	}
	if filepath.Base(filepath.Dir(workspace.Dir)) != "projects" {
		t.Fatalf("workspace directory = %s", workspace.Dir)
	}
	if _, err := os.Stat(filepath.Join(root, ".spec")); !os.IsNotExist(err) {
		t.Fatalf("repository-local state exists: %v", err)
	}
	if _, err := os.Stat(filepath.Join(workspace.Dir, "metadata.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(workspace.Dir, "current.md")); !os.IsNotExist(err) {
		t.Fatalf("obsolete external current.md exists: %v", err)
	}
	if _, err := os.Stat(filepath.Join(configHome, "templates")); err != nil {
		t.Fatal(err)
	}
}

func TestPurgeRefusesHomeDirectory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SPEC_STATE_HOME", home)
	if err := Purge(); err == nil {
		t.Fatal("Purge should reject the home directory")
	}
}
