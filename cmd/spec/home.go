package main

import (
	"fmt"
	"io"
	"os"

	"github.com/TaylorEdgerton/spec-cli/internal/change"
	"github.com/TaylorEdgerton/spec-cli/internal/config"
	"github.com/TaylorEdgerton/spec-cli/internal/state"
	verifyrun "github.com/TaylorEdgerton/spec-cli/internal/verify"
)

func runHome(input io.Reader, output io.Writer, interactive bool) error {
	root, rootErr := currentRoot()
	if !interactive {
		if rootErr != nil {
			fmt.Fprintln(output, "No Git workspace. Run `spec init` or `spec help`.")
			return nil
		}
		return printWorkflowStatus(root, output)
	}
	if rootErr != nil {
		_, err := runWorkflowApp("", screenHome, input, output)
		return err
	}
	_, err := runWorkflowApp(root, screenHome, input, output)
	return err
}

func editDefinition(root string) error {
	workspace, err := state.Load(root)
	if err != nil || !workspace.Active || workspace.Setup != nil {
		return err
	}
	_, err = change.BeginEdit(root)
	return err
}

func configureReadySpec(root string, input io.Reader, output io.Writer) (bool, error) {
	detected := verifyrun.Detect(root)
	workspace, err := state.Load(root)
	if err != nil {
		return false, err
	}
	setup := state.Setup{Title: workspace.Title}
	if data, readErr := os.ReadFile(change.ActivePath(root)); readErr == nil {
		for _, criterion := range change.AcceptanceCriteria(string(data)) {
			setup.Criteria = append(setup.Criteria, state.SetupCriterion{Text: criterion.Text, Included: true})
		}
	}
	draft := ""
	commands, waiting, stopped, err := selectVerification(root, input, output, verificationPrompt(root, setup, detected), &draft)
	if err != nil || waiting != "" || stopped {
		if waiting != "" && waiting != "back" {
			fmt.Fprintln(output, "Run `spec` again after verification is prepared.")
		}
		return false, err
	}
	if err := workspace.SetVerificationCommands(commands); err != nil {
		return false, err
	}
	return true, nil
}

func printWorkflowStatus(root string, output io.Writer) error {
	workspace, err := state.Load(root)
	if err != nil {
		fmt.Fprintln(output, "Workspace is not registered. Run `spec init`.")
		return nil
	}
	if !workspace.Active {
		fmt.Fprintln(output, "No Spec is active. Run `spec new`.")
		return nil
	}
	if workspace.Setup != nil {
		workflow := "setup"
		if workspace.Setup.Editing {
			workflow = "edit"
		}
		fmt.Fprintf(output, "Spec %s is paused at %s. Run `spec` interactively to resume.\n", workflow, workspace.Setup.Stage)
		return nil
	}
	if _, err := os.Stat(change.ActivePath(root)); os.IsNotExist(err) {
		fmt.Fprintln(output, "The active specification is missing. Run `spec` interactively to recover.")
		return nil
	} else if err != nil {
		return err
	}
	verification, err := workspace.Verification()
	if err != nil {
		return err
	}
	current, _ := verifyrun.Current(root, verification)
	status := "missing or stale"
	if current {
		status = "PASS · current workspace"
	} else if verification != nil && !verification.Passed && verification.Fingerprint != "" {
		commands, commandErr := config.VerificationCommands(root)
		fingerprint, fingerprintErr := verifyrun.Fingerprint(root, commands)
		if commandErr == nil && fingerprintErr == nil && fingerprint == verification.Fingerprint {
			status = "FAILED · current workspace"
		}
	}
	fmt.Fprintf(output, "Active Spec: %s\nVerification: %s\n", workspace.Title, status)
	return nil
}
