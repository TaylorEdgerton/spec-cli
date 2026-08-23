package gitutil

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnsureActiveSpecExcluded(t *testing.T) {
	root := t.TempDir()
	runGit(t, root, "init")
	gitignore := filepath.Join(root, ".gitignore")
	if err := os.WriteFile(gitignore, []byte("dist/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	added, err := EnsureActiveSpecExcluded(root)
	if err != nil || !added {
		t.Fatalf("first ensure: added=%v err=%v", added, err)
	}
	added, err = EnsureActiveSpecExcluded(root)
	if err != nil || added {
		t.Fatalf("second ensure: added=%v err=%v", added, err)
	}
	excluded, err := ActiveSpecExcluded(root)
	if err != nil || !excluded {
		t.Fatalf("excluded=%v err=%v", excluded, err)
	}
	path, err := LocalExcludePath(root)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(data), ActiveSpecPattern) != 1 || !strings.Contains(string(data), "# spec-cli\n.spec.md") {
		t.Fatalf("exclude content:\n%s", data)
	}
	gitignoreData, err := os.ReadFile(gitignore)
	if err != nil || string(gitignoreData) != "dist/\n" {
		t.Fatalf(".gitignore changed: %q %v", gitignoreData, err)
	}
}

func runGit(t *testing.T, root string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
}
