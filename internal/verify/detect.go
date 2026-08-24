package verify

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

func Detect(root string) []string {
	var commands []string
	if regularFile(filepath.Join(root, "go.mod")) {
		commands = append(commands, "go test ./...")
	}
	if regularFile(filepath.Join(root, "Cargo.toml")) {
		commands = append(commands, "cargo test")
	}
	if command := detectPython(root); command != "" {
		commands = append(commands, command)
	}
	commands = append(commands, detectNode(root)...)
	return commands
}

func Languages(root string) []string {
	var languages []string
	if regularFile(filepath.Join(root, "go.mod")) {
		languages = append(languages, "Go")
	}
	if regularFile(filepath.Join(root, "Cargo.toml")) {
		languages = append(languages, "Rust")
	}
	if regularFile(filepath.Join(root, "pyproject.toml")) || regularFile(filepath.Join(root, "requirements.txt")) || hasExtension(root, ".py") {
		languages = append(languages, "Python")
	}
	if regularFile(filepath.Join(root, "package.json")) {
		languages = append(languages, "JavaScript/TypeScript")
	}
	return languages
}

func detectNode(root string) []string {
	data, err := os.ReadFile(filepath.Join(root, "package.json"))
	if err != nil {
		return nil
	}
	var manifest struct {
		Scripts map[string]string `json:"scripts"`
	}
	if json.Unmarshal(data, &manifest) != nil {
		return nil
	}
	runner := "npm run"
	if regularFile(filepath.Join(root, "pnpm-lock.yaml")) {
		runner = "pnpm"
	} else if regularFile(filepath.Join(root, "yarn.lock")) {
		runner = "yarn"
	} else if regularFile(filepath.Join(root, "bun.lock")) || regularFile(filepath.Join(root, "bun.lockb")) {
		runner = "bun run"
	}
	var commands []string
	if test := strings.TrimSpace(manifest.Scripts["test"]); test != "" && !strings.Contains(test, "Error: no test specified") {
		if runner == "npm run" {
			commands = append(commands, "npm test")
		} else {
			commands = append(commands, runner+" test")
		}
	}
	if strings.TrimSpace(manifest.Scripts["lint"]) != "" {
		commands = append(commands, runner+" lint")
	}
	return commands
}

func regularFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

func detectPython(root string) string {
	tests := filepath.Join(root, "tests")
	entries, err := os.ReadDir(tests)
	if err != nil {
		return ""
	}
	hasTests := false
	for _, entry := range entries {
		name := entry.Name()
		if !entry.IsDir() && strings.HasSuffix(name, ".py") && (strings.HasPrefix(name, "test_") || strings.HasSuffix(name, "_test.py")) {
			hasTests = true
			break
		}
	}
	if !hasTests {
		return ""
	}
	if regularFile(filepath.Join(root, "pytest.ini")) || regularFile(filepath.Join(root, "conftest.py")) {
		return "python -m pytest"
	}
	if data, err := os.ReadFile(filepath.Join(root, "pyproject.toml")); err == nil && strings.Contains(strings.ToLower(string(data)), "pytest") {
		return "python -m pytest"
	}
	return "python -m unittest discover tests"
}

func hasExtension(root, extension string) bool {
	entries, err := os.ReadDir(root)
	if err != nil {
		return false
	}
	for _, entry := range entries {
		if !entry.IsDir() && strings.EqualFold(filepath.Ext(entry.Name()), extension) {
			return true
		}
	}
	return false
}
