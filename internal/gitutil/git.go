package gitutil

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

const ActiveSpecPattern = ".spec.md"

func command(dir string, args ...string) *exec.Cmd {
	all := append([]string{"-C", dir}, args...)
	return exec.Command("git", all...)
}

func output(dir string, args ...string) (string, error) {
	cmd := command(dir, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = err.Error()
		}
		return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), message)
	}
	return strings.TrimSpace(stdout.String()), nil
}

func Root(dir string) (string, error) {
	root, err := output(dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", err
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return "", err
	}
	if resolved, resolveErr := filepath.EvalSymlinks(root); resolveErr == nil {
		root = resolved
	}
	return filepath.Clean(root), nil
}

func EnsureRepository(dir string) (root string, created bool, err error) {
	if root, err = Root(dir); err == nil {
		return root, false, nil
	}
	if _, err = output(dir, "init"); err != nil {
		return "", false, err
	}
	root, err = Root(dir)
	return root, true, err
}

func Head(root string) (string, error) {
	return output(root, "rev-parse", "HEAD")
}

func HasBaseline(root string) bool {
	_, err := Head(root)
	return err == nil
}

func Status(root string) (string, error) {
	return output(root, "status", "--short", "--branch")
}

func LocalExcludePath(root string) (string, error) {
	path, err := output(root, "rev-parse", "--git-path", "info/exclude")
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	return filepath.Clean(path), nil
}

func EnsureActiveSpecExcluded(root string) (bool, error) {
	path, err := LocalExcludePath(root)
	if err != nil {
		return false, err
	}
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return false, err
	}
	if containsLine(string(data), ActiveSpecPattern) {
		return false, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return false, err
	}
	prefix := ""
	if len(data) > 0 {
		if data[len(data)-1] != '\n' {
			prefix = "\n"
		}
		prefix += "\n"
	}
	_, writeErr := fmt.Fprintf(file, "%s# spec-cli\n%s\n", prefix, ActiveSpecPattern)
	closeErr := file.Close()
	if writeErr != nil {
		return false, writeErr
	}
	return true, closeErr
}

func ActiveSpecExcluded(root string) (bool, error) {
	path, err := LocalExcludePath(root)
	if err != nil {
		return false, err
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return containsLine(string(data), ActiveSpecPattern), nil
}

func containsLine(content, expected string) bool {
	for _, line := range strings.Split(content, "\n") {
		if strings.TrimSpace(line) == expected {
			return true
		}
	}
	return false
}

func Diff(root, base string, limit int) (string, bool, error) {
	text, err := output(root, "diff", "--no-ext-diff", "--unified=3", base, "--")
	if err != nil {
		return "", false, err
	}
	if len(text) <= limit {
		return text, false, nil
	}
	return text[:limit] + "\n[diff truncated]", true, nil
}

func ChangedFiles(root, base string) ([]string, error) {
	tracked, err := output(root, "diff", "--name-only", base, "--")
	if err != nil {
		return nil, err
	}
	untracked, err := output(root, "ls-files", "--others", "--exclude-standard")
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool)
	var files []string
	for _, group := range []string{tracked, untracked} {
		for _, file := range strings.Split(group, "\n") {
			file = strings.TrimSpace(file)
			if file != "" && !seen[file] {
				seen[file] = true
				files = append(files, file)
			}
		}
	}
	sort.Strings(files)
	return files, nil
}
