package main

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"

	"github.com/TaylorEdgerton/spec-cli/internal/config"
)

func cmdConfigure(args []string) error {
	if err := noArgs(args, "spec configure"); err != nil {
		return err
	}
	directory, err := openConfigurationDirectory()
	if err != nil {
		return err
	}
	fmt.Printf("Opened configuration folder: %s\n", directory)
	return nil
}

func openConfigurationDirectory() (string, error) {
	directory, err := config.Directory()
	if err != nil {
		return "", err
	}
	info, err := os.Stat(directory)
	if os.IsNotExist(err) {
		return "", fmt.Errorf("configuration folder does not exist: %s; run `spec init` or reinstall Spec", directory)
	}
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("configuration path is not a folder: %s", directory)
	}
	command, err := directoryOpenCommand(directory)
	if err != nil {
		return "", fmt.Errorf("configuration is ready at %s; open it manually: %w", directory, err)
	}
	if err := command.Start(); err != nil {
		return "", fmt.Errorf("configuration is ready at %s; open it manually: %w", directory, err)
	}
	_ = command.Process.Release()
	return directory, nil
}

func directoryOpenCommand(directory string) (*exec.Cmd, error) {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", directory), nil
	case "windows":
		return exec.Command("explorer", directory), nil
	default:
		if path, err := exec.LookPath("xdg-open"); err == nil {
			return exec.Command(path, directory), nil
		}
		if path, err := exec.LookPath("gio"); err == nil {
			return exec.Command(path, "open", directory), nil
		}
		if path, err := exec.LookPath("explorer.exe"); err == nil {
			return exec.Command(path, directory), nil
		}
		return nil, fmt.Errorf("no folder-opening command is available")
	}
}
