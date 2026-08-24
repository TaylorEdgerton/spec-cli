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
		choice, stopped, err := runChoice(input, output, "Spec", "This directory is not a Git workspace.", []string{"Initialize workspace", "Exit"})
		if err != nil || stopped || choice == 1 {
			return err
		}
		return runInit(output)
	}
	for {
		workspace, err := state.Load(root)
		if err != nil {
			choice, stopped, chooseErr := runChoice(input, output, "Spec", "This Git workspace is not registered.", []string{"Initialize workspace", "Exit"})
			if chooseErr != nil || stopped || choice == 1 {
				return chooseErr
			}
			return runInit(output)
		}
		if !workspace.Active {
			choice, stopped, err := runChoice(input, output, "Spec", "No Spec is active.", []string{"Start a new Spec", "Exit"})
			if err != nil || stopped || choice == 1 {
				return err
			}
			return runNew(nil, input, output, true)
		}
		if workspace.Setup != nil {
			return runNew(nil, input, output, true)
		}
		if _, err := os.Stat(change.ActivePath(root)); os.IsNotExist(err) {
			choice, stopped, chooseErr := runChoice(input, output, "Spec", "The active specification is missing.", []string{"Start a replacement Spec", "Exit"})
			if chooseErr != nil || stopped || choice == 1 {
				return chooseErr
			}
			return runNew(nil, input, output, true)
		} else if err != nil {
			return err
		}
		commands, err := config.VerificationCommands(root)
		if err != nil {
			return err
		}
		if len(commands) == 0 {
			configured, err := configureReadySpec(root, input, output)
			if err != nil || !configured {
				return err
			}
			commands, err = config.VerificationCommands(root)
			if err != nil {
				return err
			}
		}
		verification, err := workspace.Verification()
		if err != nil {
			return err
		}
		current, err := verifyrun.Current(root, verification)
		if err != nil {
			return err
		}
		freshFailure := false
		if verification != nil && !verification.Passed && verification.Fingerprint != "" {
			fingerprint, fingerprintErr := verifyrun.Fingerprint(root, commands)
			freshFailure = fingerprintErr == nil && fingerprint == verification.Fingerprint
		}
		if freshFailure {
			choice, stopped, err := runChoice(input, output, "Verification FAILED", "The latest failure matches the current workspace.", []string{
				"Run verification again", "Edit Spec", "Copy failure context for AI", "Show full output", "Copy implementation prompt", "Exit",
			})
			if err != nil || stopped || choice == 5 {
				return err
			}
			switch choice {
			case 0:
				if err := runVerify(root, input, output, true); err != nil {
					return err
				}
				continue
			case 1:
				return editActiveSpec(root, input, output)
			case 2:
				if err := copyText(verificationFailurePrompt(*verification)); err != nil {
					return err
				}
				fmt.Fprintln(output, "Failure context copied to the clipboard.")
			case 3:
				fmt.Fprintln(output, verification.Output)
			case 4:
				return offerImplementationPrompt(root, input, output)
			}
			continue
		}
		if current {
			choice, stopped, err := runChoice(input, output, "Spec", "Verification PASS · current workspace", []string{
				"Review and finish Spec", "Edit Spec", "Copy implementation prompt", "Run verification again", "Exit",
			})
			if err != nil || stopped || choice == 4 {
				return err
			}
			switch choice {
			case 0:
				return runDone(root, nil, input, output, true)
			case 1:
				return editActiveSpec(root, input, output)
			case 2:
				return offerImplementationPrompt(root, input, output)
			case 3:
				if err := runVerify(root, input, output, true); err != nil {
					return err
				}
				continue
			}
		}
		choice, stopped, err := runChoice(input, output, "Spec", "Verification is missing or stale for the current workspace.", []string{
			"Run verification", "Edit Spec", "Copy implementation prompt", "Exit",
		})
		if err != nil || stopped || choice == 3 {
			return err
		}
		if choice == 0 {
			if err := runVerify(root, input, output, true); err != nil {
				return err
			}
			continue
		}
		if choice == 1 {
			return editActiveSpec(root, input, output)
		}
		return offerImplementationPrompt(root, input, output)
	}
}

func editActiveSpec(root string, input io.Reader, output io.Writer) error {
	if _, err := change.BeginEdit(root); err != nil {
		return err
	}
	return runNew(nil, input, output, true)
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
