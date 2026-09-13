package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TaylorEdgerton/spec-cli/internal/config"
)

func TestDocumentMenuActionsUseProvidedOutput(t *testing.T) {
	root := t.TempDir()
	t.Setenv("SPEC_STATE_HOME", filepath.Join(t.TempDir(), "state"))
	t.Setenv("SPEC_CONFIG_HOME", filepath.Join(t.TempDir(), "config"))
	if _, err := config.InstallDefaults(); err != nil {
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
	if err := runREADME(nil, &output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "Created ") {
		t.Fatalf("README output = %q", output.String())
	}
	if _, err := os.Stat(filepath.Join(root, "README.md")); err != nil {
		t.Fatalf("README was not created: %v", err)
	}

	output.Reset()
	if err := runRunbook(root, []string{"Database recovery"}, &output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "Created ") {
		t.Fatalf("runbook output = %q", output.String())
	}
	if _, err := os.Stat(filepath.Join(root, "docs", "runbooks", "database-recovery.md")); err != nil {
		t.Fatalf("runbook was not created: %v", err)
	}
}
