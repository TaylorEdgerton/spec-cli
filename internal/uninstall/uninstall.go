package uninstall

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/TaylorEdgerton/spec-cli/internal/state"
)

type Options struct {
	AssumeYes  bool
	Purge      bool
	Input      io.Reader
	Output     io.Writer
	Executable string
	HomeDir    string
}

func Run(options Options) error {
	if options.Input == nil {
		options.Input = os.Stdin
	}
	if options.Output == nil {
		options.Output = os.Stdout
	}
	if options.Executable == "" {
		executable, err := os.Executable()
		if err != nil {
			return err
		}
		options.Executable = executable
	}
	if options.HomeDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		options.HomeDir = home
	}

	fmt.Fprintln(options.Output, "Spec will remove:")
	fmt.Fprintf(options.Output, "  binary: %s\n", options.Executable)
	fmt.Fprintf(options.Output, "  %s\n", pathDescription(options.Executable, options.HomeDir))
	if options.Purge {
		statePath, err := state.Path()
		if err != nil {
			return err
		}
		fmt.Fprintf(options.Output, "  external state and history: %s\n", statePath)
	} else {
		fmt.Fprintln(options.Output, "External state and history will be preserved.")
	}

	if !options.AssumeYes {
		fmt.Fprint(options.Output, "Are you sure? [y/N] ")
		answer, _ := bufio.NewReader(options.Input).ReadString('\n')
		answer = strings.ToLower(strings.TrimSpace(answer))
		if answer != "y" && answer != "yes" {
			fmt.Fprintln(options.Output, "Uninstall cancelled.")
			return nil
		}
	}

	if options.Purge {
		if err := state.Purge(); err != nil {
			return fmt.Errorf("purge external state: %w", err)
		}
	}
	if err := removeInstallation(options.Executable, options.HomeDir); err != nil {
		return err
	}
	fmt.Fprintln(options.Output, completionMessage())
	return nil
}
