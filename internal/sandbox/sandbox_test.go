package sandbox

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestLaunchWarnsAndCancelsBeforeCommand(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("SECRET=value\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	err := Launch(root, "shell", false, strings.NewReader("n\n"), &output)
	if err == nil || !strings.Contains(err.Error(), "cancelled") {
		t.Fatalf("error = %v", err)
	}
	text := output.String()
	if !strings.Contains(text, ".env") || !strings.Contains(text, "Gitignored files remain readable") {
		t.Fatalf("warning = %q", text)
	}
	if strings.Contains(text, "SECRET=value") {
		t.Fatal("warning printed a secret value")
	}
}

func TestLaunchSharesWorkspaceSpec(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test uses a POSIX command stub")
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".spec.md"), []byte("# Change\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tools := t.TempDir()
	logPath := filepath.Join(t.TempDir(), "sbx.log")
	stub := "#!/usr/bin/env sh\nset -e\ntest \"$1\" = run\ntest -f \"$3/.spec.md\"\nprintf '%s\\n' \"$*\" > \"$SPEC_SBX_LOG\"\n"
	if err := os.WriteFile(filepath.Join(tools, "sbx"), []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", tools+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("SPEC_SBX_LOG", logPath)
	if err := Launch(root, "shell", true, strings.NewReader(""), &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "run shell "+root) {
		t.Fatalf("sandbox arguments: %s", data)
	}
}
