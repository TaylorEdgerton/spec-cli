package gitutil

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestStructuredChangesCoverGitEdgeCases(t *testing.T) {
	root := t.TempDir()
	runGit(t, root, "init")
	writeGitFixture(t, root, "modified.txt", []byte("one\ntwo\n"))
	writeGitFixture(t, root, "deleted.txt", []byte("delete one\ndelete two\n"))
	writeGitFixture(t, root, "old name.txt", []byte("renamed\n"))
	writeGitFixture(t, root, "binary.bin", []byte{0, 1, 2, 3})
	runGit(t, root, "add", ".")
	runGit(t, root, "-c", "user.name=Spec Test", "-c", "user.email=spec@example.invalid", "commit", "-m", "base")
	base, err := Head(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := EnsureActiveSpecExcluded(root); err != nil {
		t.Fatal(err)
	}
	writeGitFixture(t, root, "modified.txt", []byte("one changed\ntwo\nthree\n"))
	if err := os.Remove(filepath.Join(root, "deleted.txt")); err != nil {
		t.Fatal(err)
	}
	runGit(t, root, "mv", "old name.txt", "new name.txt")
	writeGitFixture(t, root, "tracked added.txt", []byte("tracked one\ntracked two\n"))
	runGit(t, root, "add", "tracked added.txt")
	writeGitFixture(t, root, "untracked file.txt", []byte("new one\nnew two\n"))
	writeGitFixture(t, root, ".spec.md", []byte("active contract\n"))
	writeGitFixture(t, root, "binary.bin", []byte{0, 1, 9, 3})

	changes, err := Changes(root, base)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := changePaths(changes), []string{"binary.bin", "deleted.txt", "modified.txt", "new name.txt", "tracked added.txt", "untracked file.txt"}; !equalStrings(got, want) {
		t.Fatalf("change paths = %v, want %v", got, want)
	}
	assertFileChange(t, changes, "binary.bin", ChangeModified, "", 0, 0, true)
	assertFileChange(t, changes, "deleted.txt", ChangeDeleted, "", 0, 2, false)
	assertFileChange(t, changes, "modified.txt", ChangeModified, "", 2, 1, false)
	assertFileChange(t, changes, "new name.txt", ChangeRenamed, "old name.txt", 0, 0, false)
	assertFileChange(t, changes, "tracked added.txt", ChangeAdded, "", 2, 0, false)
	assertFileChange(t, changes, "untracked file.txt", ChangeAdded, "", 2, 0, false)

	modified := findFileChange(t, changes, "modified.txt")
	hunks, err := Hunks(root, base, modified)
	if err != nil || len(hunks) != 1 || hunks[0].OldStart != 1 || hunks[0].NewStart != 1 {
		t.Fatalf("modified hunks = %+v, %v", hunks, err)
	}
	if additions, deletions := countDiffLines(hunks); additions != 2 || deletions != 1 {
		t.Fatalf("modified hunk lines = +%d -%d", additions, deletions)
	}
	untracked := findFileChange(t, changes, "untracked file.txt")
	hunks, err = Hunks(root, base, untracked)
	if err != nil || len(hunks) != 1 || hunks[0].NewCount != 2 {
		t.Fatalf("untracked hunks = %+v, %v", hunks, err)
	}
}

func writeGitFixture(t *testing.T, root, path string, content []byte) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, content, 0o644); err != nil {
		t.Fatal(err)
	}
}

func changePaths(changes []FileChange) []string {
	paths := make([]string, len(changes))
	for index, change := range changes {
		paths[index] = change.Path
	}
	return paths
}

func equalStrings(left, right []string) bool {
	return bytes.Equal([]byte(strings.Join(left, "\x00")), []byte(strings.Join(right, "\x00")))
}

func findFileChange(t *testing.T, changes []FileChange, path string) FileChange {
	t.Helper()
	for _, change := range changes {
		if change.Path == path {
			return change
		}
	}
	t.Fatalf("change %q not found: %+v", path, changes)
	return FileChange{}
}

func assertFileChange(t *testing.T, changes []FileChange, path string, kind ChangeKind, oldPath string, additions, deletions int, binary bool) {
	t.Helper()
	change := findFileChange(t, changes, path)
	if change.Kind != kind || change.OldPath != oldPath || change.Additions != additions || change.Deletions != deletions || change.Binary != binary {
		t.Fatalf("change %q = %+v", path, change)
	}
}

func countDiffLines(hunks []DiffHunk) (int, int) {
	additions, deletions := 0, 0
	for _, hunk := range hunks {
		for _, line := range hunk.Lines {
			switch line.Kind {
			case DiffAddition:
				additions++
			case DiffDeletion:
				deletions++
			}
		}
	}
	return additions, deletions
}

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

func TestWorktreeFingerprintTracksContentAndIgnoresActiveSpec(t *testing.T) {
	root := t.TempDir()
	runGit(t, root, "init")
	if err := os.WriteFile(filepath.Join(root, "file.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, root, "add", "file.txt")
	runGit(t, root, "-c", "user.name=Spec Test", "-c", "user.email=spec@example.invalid", "commit", "-m", "base")
	if _, err := EnsureActiveSpecExcluded(root); err != nil {
		t.Fatal(err)
	}
	initial, err := WorktreeFingerprint(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".spec.md"), []byte("ignored\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ignored, err := WorktreeFingerprint(root)
	if err != nil || ignored != initial {
		t.Fatalf("ignored fingerprint = %q, initial = %q, err = %v", ignored, initial, err)
	}
	if err := os.WriteFile(filepath.Join(root, "new.txt"), []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	first, err := WorktreeFingerprint(root)
	if err != nil || first == initial {
		t.Fatalf("first fingerprint = %q, err = %v", first, err)
	}
	if err := os.WriteFile(filepath.Join(root, "new.txt"), []byte("two\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	second, err := WorktreeFingerprint(root)
	if err != nil || second == first {
		t.Fatalf("second fingerprint = %q, err = %v", second, err)
	}
}

func runGit(t *testing.T, root string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
}
