package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/TaylorEdgerton/spec-cli/internal/discovery"
	"github.com/charmbracelet/x/ansi"
)

var discoveryLocationStyle = ansi.NewStyle().ForegroundColor(ansi.BrightGreen).Underline(true)

func openInVSCode(root string, result discovery.Result) error {
	command, err := vsCodeCommand(root, result, exec.LookPath)
	if err != nil {
		return err
	}
	if err := command.Start(); err != nil {
		return err
	}
	return command.Process.Release()
}

func vsCodeCommand(root string, result discovery.Result, lookup func(string) (string, error)) (*exec.Cmd, error) {
	path, err := safeCodePath(root, result.Path)
	if err != nil {
		return nil, err
	}
	editor := ""
	for _, name := range []string{"code", "code-insiders"} {
		if found, lookupErr := lookup(name); lookupErr == nil {
			editor = found
			break
		}
	}
	if editor == "" {
		return nil, fmt.Errorf("VS Code command is unavailable; install `code` on PATH")
	}
	line, column := resultLocation(result)
	location := fmt.Sprintf("%s:%d:%d", path, line, column)
	return exec.Command(editor, "--reuse-window", "--goto", location), nil
}

func styledDiscoveryLocation(result discovery.Result) string {
	return discoveryLocationStyle.Styled(discoveryLocation(result))
}

func discoveryLocation(result discovery.Result) string {
	line, column := resultLocation(result)
	return fmt.Sprintf("%s:%d:%d", result.Path, line, column)
}

func resultLocation(result discovery.Result) (int, int) {
	line, column := result.Line, result.Column
	if line < 1 {
		line = 1
	}
	if column < 1 {
		column = 1
	}
	return line, column
}

func safeCodePath(root, relative string) (string, error) {
	if filepath.IsAbs(relative) {
		return "", fmt.Errorf("discovered path must be relative: %s", relative)
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(filepath.Join(root, filepath.Clean(relative)))
	if err != nil {
		return "", err
	}
	inside, err := filepath.Rel(resolvedRoot, resolved)
	if err != nil || inside == ".." || strings.HasPrefix(inside, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("discovered path is outside the repository: %s", relative)
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.Mode().IsRegular() {
		return "", fmt.Errorf("discovered path is not a regular file: %s", relative)
	}
	return resolved, nil
}
