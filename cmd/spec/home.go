package main

import (
	"fmt"
	"io"
	"os"

	"github.com/TaylorEdgerton/spec-cli/internal/change"
	"github.com/TaylorEdgerton/spec-cli/internal/config"
	"github.com/TaylorEdgerton/spec-cli/internal/discovery"
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
		choice, stopped, err := runChoice(input, output, "Spec", "", homeMenuItems(workspace.Active))
		if err != nil || stopped || choice == 4 {
			return err
		}
		switch choice {
		case 0:
			if workspace.Active {
				return resumeActiveSpec(root, input, output)
			}
			return runNew(nil, input, output, true)
		case 1:
			if stopped, exploreErr := runExploreCodebase(root, input, output); exploreErr != nil || stopped {
				return exploreErr
			}
		case 2:
			if stopped, recentErr := runRecentChanges(root, input, output); recentErr != nil || stopped {
				return recentErr
			}
		case 3:
			if stopped, documentErr := runCreateDocument(root, input, output); documentErr != nil || stopped {
				return documentErr
			}
		}
	}
}

func homeMenuItems(active bool) []string {
	primary := "Create a spec"
	if active {
		primary = "Resume Spec"
	}
	return []string{primary, "Explore codebase", "Recent changes", "Create a doc", "Exit"}
}

func resumeActiveSpec(root string, input io.Reader, output io.Writer) error {
	workspace, err := state.Load(root)
	if err != nil {
		return err
	}
	if !workspace.Active {
		return nil
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
	_, err = runShell(root, screenOverview, input, output)
	return err
}

func runExploreCodebase(root string, input io.Reader, output io.Writer) (bool, error) {
	return runContextExplorer(root, "", nil, input, output)
}

func findCodebaseContext(root, query string) ([]discovery.Result, error) {
	return discovery.Find(root, discovery.Query{Intent: query})
}

func runRecentChanges(root string, input io.Reader, output io.Writer) (bool, error) {
	for {
		choice, stopped, err := runChoice(input, output, "Recent Changes", "", recentChangesMenuItems())
		if err != nil || stopped {
			return stopped, err
		}
		if choice == 2 {
			return false, nil
		}
		// Both entries open the same completed-Spec history; "Code changes" simply
		// starts with its stored file and line statistics already showing.
		action, err := runHistory(root, choice == 1, input, output)
		if err != nil || action == actionQuit {
			return action == actionQuit, err
		}
	}
}

func recentChangesMenuItems() []string {
	return []string{"Spec history", "Code changes", "Back"}
}

func runCreateDocument(root string, input io.Reader, output io.Writer) (bool, error) {
	for {
		choice, stopped, err := runChoice(input, output, "Create a Doc", "", createDocumentMenuItems())
		if err != nil || stopped {
			return stopped, err
		}
		switch choice {
		case 0:
			if err := runREADME(nil, output); err != nil {
				return false, err
			}
		case 1:
			title, back, stopped, err := runTextPrompt(input, output, "Create a Runbook", "Scenario title:", "", false, true)
			if err != nil || stopped {
				return stopped, err
			}
			if back {
				continue
			}
			if err := runRunbook(root, []string{title}, output); err != nil {
				return false, err
			}
		case 2:
			return false, nil
		}
	}
}

func createDocumentMenuItems() []string {
	return []string{"README", "Runbook", "Back"}
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
