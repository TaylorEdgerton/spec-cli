package documents

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

const readmeTemplate = `# Project

Describe the purpose of this project.

## Requirements

List the required tools and services.

## Use

Show the main commands and examples.

## Verify

Show how to verify a change.
`

const runbookTemplate = `# Runbook

## Purpose

## Dependencies

## Failure Symptoms

## Diagnosis

## Logs and Metrics

## Recovery

## Verification

## Dangerous Operations
`

var adrPattern = regexp.MustCompile(`^(\d+)-`)

func README(directory, documentPath string) (created bool, prompt string, err error) {
	path := filepath.Join(directory, "README.md")
	data, readErr := os.ReadFile(path)
	if os.IsNotExist(readErr) {
		return true, "", os.WriteFile(path, []byte(readmeTemplate), 0o644)
	}
	if readErr != nil {
		return false, "", readErr
	}
	prompt = updatePrompt(documentPath, string(data), "Keep this directory's purpose, setup, use, and verification instructions accurate and concise.")
	return false, prompt, nil
}

func Runbook(root string) (path string, created bool, prompt string, err error) {
	path = filepath.Join(root, "docs", "runbook.md")
	data, readErr := os.ReadFile(path)
	if os.IsNotExist(readErr) {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return path, false, "", err
		}
		return path, true, "", os.WriteFile(path, []byte(runbookTemplate), 0o644)
	}
	if readErr != nil {
		return path, false, "", readErr
	}
	prompt = updatePrompt("docs/runbook.md", string(data), "Keep diagnosis, recovery, verification, and dangerous operations practical.")
	return path, false, prompt, nil
}

func ADR(root, title string) (string, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return "", fmt.Errorf("usage: spec adr \"Decision title\"")
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
	name := fmt.Sprintf("%04d-%s.md", next, slug(title))
	path := filepath.Join(dir, name)
	content := fmt.Sprintf("# ADR-%04d: %s\n\n## Status\n\nProposed\n\n## Context\n\n## Decision\n\n## Alternatives\n\n## Consequences\n", next, title)
	return path, os.WriteFile(path, []byte(content), 0o644)
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
