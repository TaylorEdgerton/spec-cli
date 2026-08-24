package main

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"

	promptbuilder "github.com/TaylorEdgerton/spec-cli/internal/prompt"
)

func cmdPrompt(args []string) error {
	copyOutput, showInfo, includeFiles := false, false, false
	for _, arg := range args {
		switch arg {
		case "--copy":
			copyOutput = true
		case "--info":
			showInfo = true
		case "--include-files":
			includeFiles = true
		default:
			return fmt.Errorf("usage: spec prompt [--copy] [--info] [--include-files]")
		}
	}
	root, err := currentRoot()
	if err != nil {
		return err
	}
	content, info, err := promptbuilder.Build(root, includeFiles)
	if err != nil {
		return err
	}
	if copyOutput {
		if err := copyText(content); err != nil {
			return err
		}
		fmt.Fprintln(os.Stderr, "Prompt copied to the clipboard.")
	} else {
		fmt.Print(content)
	}
	if showInfo {
		fmt.Fprintln(os.Stderr, "\nPrompt context:")
		fmt.Fprintln(os.Stderr, promptbuilder.FormatInfo(info))
	}
	return nil
}

func copyText(content string) error {
	return copyTextWith(runtime.GOOS, content, exec.LookPath, runClipboardTool)
}

type clipboardTool struct {
	name string
	args []string
}

func clipboardTools(goos string) []clipboardTool {
	switch goos {
	case "darwin":
		return []clipboardTool{{name: "pbcopy"}}
	case "windows":
		return []clipboardTool{{name: "clip.exe"}, {name: "clip"}}
	default:
		return []clipboardTool{
			{name: "wl-copy"},
			{name: "xclip", args: []string{"-selection", "clipboard"}},
			{name: "xsel", args: []string{"--clipboard", "--input"}},
			{name: "clip.exe"},
			{name: "termux-clipboard-set"},
		}
	}
}

func copyTextWith(goos, content string, lookup func(string) (string, error), run func(string, []string, string) error) error {
	var failures []string
	available := false
	tools := clipboardTools(goos)
	for _, tool := range tools {
		path, err := lookup(tool.name)
		if err != nil {
			continue
		}
		available = true
		if err := run(path, tool.args, content); err == nil {
			return nil
		} else {
			failures = append(failures, tool.name+": "+err.Error())
		}
	}
	if available {
		return fmt.Errorf("clipboard copy failed with available tools: %s", strings.Join(failures, "; "))
	}
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		names = append(names, tool.name)
	}
	return fmt.Errorf("no clipboard tool is available; install one of: %s", strings.Join(names, ", "))
}

func runClipboardTool(path string, args []string, content string) error {
	command := exec.Command(path, args...)
	command.Stdin = strings.NewReader(content)
	output, err := command.CombinedOutput()
	if err == nil {
		return nil
	}
	if detail := strings.TrimSpace(string(output)); detail != "" {
		return fmt.Errorf("%w: %s", err, detail)
	}
	return err
}
