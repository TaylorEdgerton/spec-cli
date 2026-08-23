package secrets

import (
	"os"
	"path/filepath"
	"testing"
)

func TestScanIncludesIgnoredSecretsAndSkipsTemplates(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{".env", ".env.example", ".spec.md", "certs/dev.key", "secrets/token"} {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("value"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	findings, err := Scan(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 3 {
		t.Fatalf("findings: %+v", findings)
	}
	for _, finding := range findings {
		if finding.Path == ".env.example" {
			t.Fatal("template was reported")
		}
		if finding.Path == ".spec.md" {
			t.Fatal("active specification was reported")
		}
	}
}
