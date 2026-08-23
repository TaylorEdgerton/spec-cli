package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigureDoesNotCreateMissingConfigurationFolder(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "missing-config")
	t.Setenv("SPEC_CONFIG_HOME", directory)

	err := cmdConfigure(nil)
	if err == nil || !strings.Contains(err.Error(), "run `spec init` or reinstall Spec") {
		t.Fatalf("configure error: %v", err)
	}
	if _, err := os.Stat(directory); !os.IsNotExist(err) {
		t.Fatalf("configure created the directory: %v", err)
	}
}
