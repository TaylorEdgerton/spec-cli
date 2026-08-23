package documents

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"text/template"

	"github.com/TaylorEdgerton/spec-cli/internal/config"
)

var adrPattern = regexp.MustCompile(`^(\d+)-`)

type templateData struct {
	Title  string
	Number string
}

func README(directory, documentPath string) (created bool, prompt string, err error) {
	path := filepath.Join(directory, "README.md")
	data, readErr := os.ReadFile(path)
	if os.IsNotExist(readErr) {
		content, err := renderTemplate(config.README, templateData{})
		if err != nil {
			return false, "", err
		}
		return true, "", os.WriteFile(path, content, 0o644)
	}
	if readErr != nil {
		return false, "", readErr
	}
	prompt = updatePrompt(documentPath, string(data), "Keep this directory's purpose, setup, use, and verification instructions accurate and concise.")
	return false, prompt, nil
}

func Runbook(root, title string) (path string, created bool, prompt string, err error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return "", false, "", fmt.Errorf("usage: spec runbook \"Scenario title\"")
	}
	name := slug(title)
	if name == "" {
		return "", false, "", fmt.Errorf("runbook title must contain a letter or number")
	}
	path = filepath.Join(root, "docs", "runbooks", name+".md")
	data, readErr := os.ReadFile(path)
	if os.IsNotExist(readErr) {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return path, false, "", err
		}
		content, err := renderTemplate(config.Runbook, templateData{Title: title})
		if err != nil {
			return path, false, "", err
		}
		return path, true, "", os.WriteFile(path, content, 0o644)
	}
	if readErr != nil {
		return path, false, "", readErr
	}
	relative, _ := filepath.Rel(root, path)
	prompt = updatePrompt(filepath.ToSlash(relative), string(data), "Keep diagnosis, recovery, verification, and dangerous operations practical.")
	return path, false, prompt, nil
}

func Runbooks(root string) ([]string, error) {
	var paths []string
	legacy := filepath.Join(root, "docs", "runbook.md")
	if info, err := os.Stat(legacy); err == nil && !info.IsDir() {
		paths = append(paths, "docs/runbook.md")
	} else if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	directory := filepath.Join(root, "docs", "runbooks")
	entries, err := os.ReadDir(directory)
	if os.IsNotExist(err) {
		return paths, nil
	}
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if !entry.IsDir() && strings.EqualFold(filepath.Ext(entry.Name()), ".md") {
			paths = append(paths, filepath.ToSlash(filepath.Join("docs", "runbooks", entry.Name())))
		}
	}
	sort.Strings(paths)
	return paths, nil
}

func ADR(root, title string) (string, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return "", fmt.Errorf("usage: spec adr \"Decision title\"")
	}
	titleSlug := slug(title)
	if titleSlug == "" {
		return "", fmt.Errorf("ADR title must contain a letter or number")
	}
	dir := filepath.Join(root, "docs", "adr")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	next := 1
	for _, entry := range entries {
		match := adrPattern.FindStringSubmatch(entry.Name())
		if len(match) == 2 {
			value, _ := strconv.Atoi(match[1])
			if value >= next {
				next = value + 1
			}
		}
	}
	name := fmt.Sprintf("%04d-%s.md", next, titleSlug)
	path := filepath.Join(dir, name)
	content, err := renderTemplate(config.ADR, templateData{Title: title, Number: fmt.Sprintf("%04d", next)})
	if err != nil {
		return "", err
	}
	return path, os.WriteFile(path, content, 0o644)
}

func renderTemplate(name config.TemplateName, data templateData) ([]byte, error) {
	content, err := config.Template(name)
	if err != nil {
		return nil, err
	}
	parsed, err := template.New(string(name)).Option("missingkey=error").Parse(string(content))
	if err != nil {
		return nil, fmt.Errorf("parse template %s: %w", name, err)
	}
	var rendered bytes.Buffer
	if err := parsed.Execute(&rendered, data); err != nil {
		return nil, fmt.Errorf("render template %s: %w", name, err)
	}
	return rendered.Bytes(), nil
}

func updatePrompt(path, content, instruction string) string {
	const maxDocumentBytes = 128 * 1024
	if len(content) > maxDocumentBytes {
		content = content[:maxDocumentBytes] + "\n[file truncated]"
	}
	return fmt.Sprintf("Update %s.\n%s\nPreserve correct project-specific content.\n\n## Current file\n\n```markdown\n%s\n```\n", path, instruction, strings.TrimSpace(content))
}

func slug(value string) string {
	value = strings.ToLower(value)
	return strings.Join(strings.FieldsFunc(value, func(r rune) bool {
		return !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9')
	}), "-")
}
