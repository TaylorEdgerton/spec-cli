package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TaylorEdgerton/spec-cli/internal/discovery"
)

func TestVSCodeCommandOpensExactRepositoryLocation(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "src", "health file.go")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("package health\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	command, err := vsCodeCommand(root, discovery.Result{Path: "src/health file.go", Line: 12, Column: 5}, func(name string) (string, error) {
		if name == "code" {
			return "/tools/code", nil
		}
		return "", errors.New("missing")
	})
	if err != nil {
		t.Fatal(err)
	}
	wantLocation := path + ":12:5"
	if command.Path != "/tools/code" || len(command.Args) != 4 || command.Args[1] != "--reuse-window" || command.Args[2] != "--goto" || command.Args[3] != wantLocation {
		t.Fatalf("command = path %q args %q", command.Path, command.Args)
	}
}

func TestVSCodeCommandFallsBackToInsiders(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "main.go")
	if err := os.WriteFile(path, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	command, err := vsCodeCommand(root, discovery.Result{Path: "main.go"}, func(name string) (string, error) {
		if name == "code-insiders" {
			return "/tools/code-insiders", nil
		}
		return "", errors.New("missing")
	})
	if err != nil {
		t.Fatal(err)
	}
	if command.Path != "/tools/code-insiders" || !strings.HasSuffix(command.Args[3], "main.go:1:1") {
		t.Fatalf("command = path %q args %q", command.Path, command.Args)
	}
}

func TestStyledDiscoveryLocationIncludesExactLineAndHighlight(t *testing.T) {
	location := styledDiscoveryLocation(discovery.Result{Path: "src/health file.go", Line: 12, Column: 5})
	for _, expected := range []string{"src/health file.go:12:5", "\x1b[92;4m"} {
		if !strings.Contains(location, expected) {
			t.Fatalf("location missing %q: %q", expected, location)
		}
	}
	if strings.Contains(location, "\x1b]8;") {
		t.Fatalf("location still delegates clicks to the terminal: %q", location)
	}
}

func TestVSCodeCommandRejectsOutsidePathAndMissingCLI(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.go")
	if err := os.WriteFile(outside, []byte("package outside\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := vsCodeCommand(root, discovery.Result{Path: outside}, func(string) (string, error) { return "", errors.New("missing") }); err == nil || !strings.Contains(err.Error(), "relative") {
		t.Fatalf("outside path error = %v", err)
	}
	inside := filepath.Join(root, "main.go")
	if err := os.WriteFile(inside, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := vsCodeCommand(root, discovery.Result{Path: "main.go"}, func(string) (string, error) { return "", errors.New("missing") }); err == nil || !strings.Contains(err.Error(), "unavailable") {
		t.Fatalf("missing CLI error = %v", err)
	}
}
