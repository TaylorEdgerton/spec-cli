package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"

	promptbuilder "github.com/TaylorEdgerton/spec-cli/internal/prompt"
	"github.com/TaylorEdgerton/spec-cli/internal/state"
)

func cmdPrompt(args []string) error {
	root, err := currentRoot()
	if err != nil {
		return err
	}
	return runPromptCommand(root, args, os.Stdout, os.Stderr, promptCommandServices{
		Record: func(kind promptbuilder.Kind, event state.TimelineEventType, source string) {
			recordPromptDelivery(root, event, kind, source)
		},
	})
}

type promptCommandServices struct {
	Build  func(string, bool, promptbuilder.Kind) (string, promptbuilder.Info, error)
	Copy   func(string) error
	Record func(promptbuilder.Kind, state.TimelineEventType, string)
}

func runPromptCommand(root string, args []string, output, errorOutput io.Writer, services promptCommandServices) error {
	copyOutput, showInfo, includeFiles := false, false, false
	kind := promptbuilder.Implementation
	seen := make(map[string]bool)
	for _, arg := range args {
		if seen[arg] {
			return promptUsageError()
		}
		seen[arg] = true
		switch arg {
		case "--copy":
			copyOutput = true
		case "--info":
			showInfo = true
		case "--include-files":
			includeFiles = true
		case "--plan":
			kind = promptbuilder.Plan
		default:
			return promptUsageError()
		}
	}
	if services.Build == nil {
		services.Build = promptbuilder.BuildKind
	}
	if services.Copy == nil {
		services.Copy = copyText
	}
	content, info, err := services.Build(root, includeFiles, kind)
	if err != nil {
		return err
	}
	if copyOutput {
		if err := services.Copy(content); err != nil {
			return err
		}
		label := "Implementation"
		if kind == promptbuilder.Plan {
			label = "Plan"
		}
		fmt.Fprintf(errorOutput, "%s prompt copied to the clipboard.\n", label)
		if services.Record != nil {
			services.Record(kind, state.TimelinePromptCopied, "clipboard")
		}
	} else {
		fmt.Fprint(output, content)
		if services.Record != nil {
			services.Record(kind, state.TimelinePromptPrinted, "stdout")
		}
	}
	if showInfo {
		fmt.Fprintln(errorOutput, "\nPrompt context:")
		fmt.Fprintln(errorOutput, promptbuilder.FormatInfo(info))
	}
	return nil
}

func promptUsageError() error {
	return fmt.Errorf("usage: spec prompt [--plan] [--copy] [--info] [--include-files]")
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

func readClipboardText() (string, error) {
	var tools []clipboardTool
	switch runtime.GOOS {
	case "darwin":
		tools = []clipboardTool{{name: "pbpaste"}}
	case "windows":
		tools = []clipboardTool{{name: "powershell.exe", args: []string{"-NoProfile", "-Command", "Get-Clipboard -Raw"}}}
	default:
		tools = []clipboardTool{
			{name: "wl-paste", args: []string{"--no-newline"}},
			{name: "xclip", args: []string{"-selection", "clipboard", "-o"}},
			{name: "xsel", args: []string{"--clipboard", "--output"}},
			{name: "termux-clipboard-get"},
		}
	}
	var failures []string
	for _, tool := range tools {
		path, err := exec.LookPath(tool.name)
		if err != nil {
			continue
		}
		output, err := exec.Command(path, tool.args...).CombinedOutput()
		if err == nil {
			return string(output), nil
		}
		failures = append(failures, tool.name+": "+err.Error())
	}
	if len(failures) > 0 {
		return "", fmt.Errorf("clipboard read failed: %s", strings.Join(failures, "; "))
	}
	return "", fmt.Errorf("no clipboard read tool is available")
}
