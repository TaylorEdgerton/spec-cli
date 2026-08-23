package main

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"

	promptbuilder "github.com/TaylorEdgerton/spec-cli/internal/prompt"
)

func cmdPrompt(args []string) error {
	copyOutput, showInfo := false, false
	for _, arg := range args {
		switch arg {
		case "--copy":
			copyOutput = true
		case "--info":
			showInfo = true
		default:
			return fmt.Errorf("usage: spec prompt [--copy] [--info]")
		}
	}
	root, err := currentRoot()
	if err != nil {
		return err
	}
	content, info, err := promptbuilder.Build(root)
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
	var name string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		name = "pbcopy"
	case "windows":
		name = "clip"
	default:
		if _, err := exec.LookPath("wl-copy"); err == nil {
			name = "wl-copy"
		} else {
			name, args = "xclip", []string{"-selection", "clipboard"}
		}
	}
	cmd := exec.Command(name, args...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("clipboard tool is not available: %w", err)
	}
	if _, err := fmt.Fprint(stdin, content); err != nil {
		return err
	}
	if err := stdin.Close(); err != nil {
		return err
	}
	return cmd.Wait()
}
