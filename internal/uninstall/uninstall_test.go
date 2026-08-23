//go:build !windows

package uninstall

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunCancelledMakesNoChanges(t *testing.T) {
	home := t.TempDir()
	executable := filepath.Join(home, "bin", "spec")
	writeTestFile(t, executable, "binary")
	profile := filepath.Join(home, ".profile")
	content := managedProfile(home)
	writeTestFile(t, profile, content)
	stateHome := filepath.Join(home, "state")
	writeTestFile(t, filepath.Join(stateHome, "projects", "history"), "history")
	t.Setenv("SPEC_STATE_HOME", stateHome)

	var output bytes.Buffer
	if err := Run(Options{
		Input:      strings.NewReader("no\n"),
		Output:     &output,
		Executable: executable,
		HomeDir:    home,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(executable); err != nil {
		t.Fatalf("binary changed after cancellation: %v", err)
	}
	assertFileContent(t, profile, content)
	if _, err := os.Stat(stateHome); err != nil {
		t.Fatalf("state changed after cancellation: %v", err)
	}
	if !strings.Contains(output.String(), "Uninstall cancelled.") {
		t.Fatalf("output = %q", output.String())
	}
}

func TestRunRemovesBinaryAndManagedPathButPreservesState(t *testing.T) {
	home := t.TempDir()
	executable := filepath.Join(home, "bin", "spec")
	writeTestFile(t, executable, "binary")
	profile := filepath.Join(home, ".profile")
	writeTestFile(t, profile, managedProfile(home))
	stateHome := filepath.Join(home, "state")
	writeTestFile(t, filepath.Join(stateHome, "projects", "history"), "history")
	t.Setenv("SPEC_STATE_HOME", stateHome)

	if err := Run(Options{
		AssumeYes:  true,
		Input:      strings.NewReader(""),
		Output:     &bytes.Buffer{},
		Executable: executable,
		HomeDir:    home,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(executable); !os.IsNotExist(err) {
		t.Fatalf("binary still exists: %v", err)
	}
	assertFileContent(t, profile, "export EDITOR=vi\nalias ll='ls -l'\n")
	if _, err := os.Stat(stateHome); err != nil {
		t.Fatalf("state was not preserved: %v", err)
	}
}

func TestRunPurgeRemovesExternalState(t *testing.T) {
	home := t.TempDir()
	executable := filepath.Join(home, "bin", "spec")
	writeTestFile(t, executable, "binary")
	stateHome := filepath.Join(home, "state")
	writeTestFile(t, filepath.Join(stateHome, "projects", "history"), "history")
	t.Setenv("SPEC_STATE_HOME", stateHome)

	if err := Run(Options{
		AssumeYes:  true,
		Purge:      true,
		Output:     &bytes.Buffer{},
		Executable: executable,
		HomeDir:    home,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stateHome); !os.IsNotExist(err) {
		t.Fatalf("state still exists: %v", err)
	}
}

func TestRemovePathBlockIgnoresUnmanagedProfile(t *testing.T) {
	profile := filepath.Join(t.TempDir(), ".profile")
	content := "export PATH=\"$HOME/.local/bin:$PATH\"\n"
	writeTestFile(t, profile, content)
	if err := removePathBlock(profile); err != nil {
		t.Fatal(err)
	}
	assertFileContent(t, profile, content)
}

func managedProfile(home string) string {
	return "export EDITOR=vi\n\n" + pathMarkerBegin + "\n" +
		"export PATH=\"" + filepath.Join(home, "bin") + ":$PATH\"\n" +
		pathMarkerEnd + "\n" +
		"alias ll='ls -l'\n"
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func assertFileContent(t *testing.T, path, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != want {
		t.Fatalf("%s = %q, want %q", path, data, want)
	}
}
