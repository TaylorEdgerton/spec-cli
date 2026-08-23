package main

import (
	"fmt"
	"os"

	"github.com/TaylorEdgerton/spec-cli/internal/uninstall"
)

func cmdUninstall(args []string) error {
	options := uninstall.Options{Input: os.Stdin, Output: os.Stdout}
	for _, arg := range args {
		switch arg {
		case "--yes", "-y":
			options.AssumeYes = true
		case "--purge":
			options.Purge = true
		case "--help", "-h":
			fmt.Println("usage: spec uninstall [--yes] [--purge]")
			return nil
		default:
			return fmt.Errorf("usage: spec uninstall [--yes] [--purge]")
		}
	}
	return uninstall.Run(options)
}
