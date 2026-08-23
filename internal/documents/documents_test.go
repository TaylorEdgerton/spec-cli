package documents

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDocumentHelpersPreserveExistingFiles(t *testing.T) {
	root := t.TempDir()
	created, prompt, err := README(root)
	if err != nil || !created || prompt != "" {
		t.Fatalf("README create: %v %q %v", created, prompt, err)
	}
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("# Existing\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	created, prompt, err = README(root)
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
