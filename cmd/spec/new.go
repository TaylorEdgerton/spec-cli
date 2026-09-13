package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/TaylorEdgerton/spec-cli/internal/change"
	"github.com/TaylorEdgerton/spec-cli/internal/config"
	"github.com/TaylorEdgerton/spec-cli/internal/discovery"
	"github.com/TaylorEdgerton/spec-cli/internal/state"
	verifyrun "github.com/TaylorEdgerton/spec-cli/internal/verify"
	"github.com/charmbracelet/x/term"
)

func cmdNew(args []string) error {
	return runNew(args, os.Stdin, os.Stdout, terminalInput(os.Stdin))
}

func runNew(args []string, input io.Reader, output io.Writer, interactive bool) error {
	root, err := currentRoot()
	if err != nil {
		return fmt.Errorf("Git repository is required; run `spec init`")
	}
	title := strings.TrimSpace(strings.Join(args, " "))
	if !interactive {
		path, err := change.New(root, title, time.Now())
		if err != nil {
			return err
		}
		printNewCreated(output, path)
		return nil
	}
	if _, err := change.BeginSetup(root, title, time.Now()); err != nil {
		return err
	}
	_, err = runWorkflowApp(root, screenDefinition, input, output)
	return err
}

// selectVerification remains shared by the non-interactive `spec verify`
// command when a workspace has no configured checks. Interactive workflow
// screens themselves are owned by workflowApp.
func selectVerification(root string, input io.Reader, output io.Writer, aiPrompt string, draft *string) ([]string, string, bool, error) {
	for {
		detected := verifyrun.Detect(root)
		reusable, err := config.ReusableVerification(root)
		if err != nil {
			return nil, "", false, err
		}
		var items, actions []string
		if len(detected) > 0 {
			items = append(items, "Use detected project checks: "+strings.Join(detected, ", "))
			actions = append(actions, "detected")
		}
		items = append(items, "Ask AI to create verification", "Enter a verification command")
		actions = append(actions, "ai", "enter")
		if len(reusable) > 0 {
			items = append(items, "Reuse verification from another workspace")
			actions = append(actions, "reuse")
		}
		items = append(items, "Edit configuration manually", "Back", "Save and exit")
		actions = append(actions, "edit", "back", "exit")
		selected, stopped, err := runChoice(input, output, "Verification is required but is not configured.", "How should this change be verified?", items)
		if err != nil || stopped || actions[selected] == "exit" {
			return nil, "", stopped || actions[selected] == "exit", err
		}
		switch actions[selected] {
		case "back":
			return nil, "back", false, nil
		case "detected":
			return detected, "", false, nil
		case "enter":
			command, back, interrupted, err := runTextPrompt(input, output, "Verification", "Enter one deterministic verification command", *draft, false, true)
			*draft = command
			if err != nil || interrupted {
				return nil, "", interrupted, err
			}
			if back {
				continue
			}
			return []string{command}, "", false, nil
		case "reuse":
			labels := make([]string, len(reusable))
			for index, option := range reusable {
				labels[index] = option.Root + ": " + strings.Join(option.Commands, ", ")
			}
			choice, interrupted, err := runChoice(input, output, "Reuse verification", "Choose a workspace", labels)
			if err != nil || interrupted {
				return nil, "", interrupted, err
			}
			return reusable[choice].Commands, "", false, nil
		case "ai":
			workspace, err := state.Load(root)
			if err != nil {
				return nil, "", false, err
			}
			if err := workspace.SavePrompt(aiPrompt); err != nil {
				return nil, "", false, err
			}
			choice, interrupted, err := runChoice(input, output, "AI verification prompt", "Create permanent project tests, then run `spec` to resume.", []string{"Copy prompt", "Print prompt", "Back"})
			if err != nil || interrupted {
				return nil, "", interrupted, err
			}
			if choice == 2 {
				continue
			}
			if choice == 0 {
				if err := copyText(aiPrompt); err != nil {
					return nil, "", false, err
				}
				fmt.Fprintln(output, "Prompt copied to the clipboard.")
			} else {
				fmt.Fprintln(output, aiPrompt)
			}
			return nil, "ai", false, nil
		case "edit":
			if err := saveVerificationCommands(root, nil); err != nil {
				return nil, "", false, err
			}
			path, err := openConfigurationDirectory()
			if err != nil {
				return nil, "", false, err
			}
			fmt.Fprintf(output, "Opened configuration folder: %s\n", path)
			return nil, "edit", false, nil
		}
	}
}

func saveVerificationCommands(root string, commands []string) error {
	workspace, err := state.Load(root)
	if err != nil {
		return err
	}
	return workspace.SetVerificationCommands(commands)
}

func discoveryQuery(setup state.Setup) discovery.Query {
	query := discovery.Query{Intent: setup.Title, Outcome: setup.Outcome}
	for _, criterion := range setup.Criteria {
		if criterion.Included && strings.TrimSpace(criterion.Text) != "" {
			query.Criteria = append(query.Criteria, criterion.Text)
		}
	}
	return query
}

func verificationPrompt(root string, setup state.Setup, detected []string) string {
	var builder strings.Builder
	builder.WriteString("Create permanent deterministic tests for this change.\n\n")
	fmt.Fprintf(&builder, "Change: %s\n", setup.Title)
	fmt.Fprintf(&builder, "Expected result: %s\n", setup.Outcome)
	if setup.Limits != "" {
		fmt.Fprintf(&builder, "Limits: %s\n", setup.Limits)
	}
	builder.WriteString("\nSuccess criteria:\n")
	for _, criterion := range setup.Criteria {
		if criterion.Included {
			fmt.Fprintf(&builder, "- %s\n", criterion.Text)
		}
	}
	if len(detected) > 0 {
		fmt.Fprintf(&builder, "\nDetected project checks: %s\n", strings.Join(detected, ", "))
	}
	if languages := verifyrun.Languages(root); len(languages) > 0 {
		fmt.Fprintf(&builder, "Detected languages: %s\n", strings.Join(languages, ", "))
	}
	builder.WriteString("\nFollow existing project conventions. Add tests to the repository. Do not implement the product change. Return one deterministic command that runs the tests.\n")
	return builder.String()
}

func saveSetup(root string, setup state.Setup) error {
	workspace, err := state.Load(root)
	if err != nil {
		return err
	}
	return workspace.SaveSetup(setup)
}

func saveAndExit(root string, setup state.Setup, output io.Writer) error {
	if err := saveSetup(root, setup); err != nil {
		return err
	}
	if setup.Editing {
		fmt.Fprintln(output, "Spec edit saved. Run `spec` to resume.")
	} else {
		fmt.Fprintln(output, "Setup saved. Run `spec` to resume.")
	}
	return nil
}

func boundedSummary(value string) string {
	value = strings.Join(strings.Fields(value), " ")
	runes := []rune(value)
	if len(runes) <= 72 {
		return value
	}
	return string(runes[:71]) + "…"
}

func terminalInput(input *os.File) bool {
	return term.IsTerminal(input.Fd())
}

func printNewCreated(output io.Writer, path string) {
	absolute, err := filepath.Abs(path)
	if err == nil {
		path = absolute
	}
	fmt.Fprintf(output, "Created active specification: %s\n", path)
}
