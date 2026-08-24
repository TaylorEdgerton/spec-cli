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
	promptbuilder "github.com/TaylorEdgerton/spec-cli/internal/prompt"
	"github.com/TaylorEdgerton/spec-cli/internal/state"
	verifyrun "github.com/TaylorEdgerton/spec-cli/internal/verify"
	"github.com/charmbracelet/x/term"
)

const (
	setupChange       = "change"
	setupOutcome      = "outcome"
	setupLimits       = "limits"
	setupCriteria     = "criteria"
	setupVerification = "verification"
	setupVerifyChange = "change-verification"
	setupVerifyWait   = "waiting-for-verification"
	setupBaseline     = "baseline"
	setupReview       = "review"
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
	setup, err := change.BeginSetup(root, title, time.Now())
	if err != nil {
		return err
	}
	workspace, err := state.Load(root)
	if err != nil {
		return err
	}
	if setup.Editing {
		if _, statErr := os.Stat(change.ActivePath(root)); os.IsNotExist(statErr) {
			choice, stopped, chooseErr := runChoice(input, output, "Active specification is missing", "`.spec.md` was deleted during a paused CLI edit. Spec can recreate the guided sections from saved edit state. Manually added file content cannot be restored.", []string{
				"Recreate .spec.md from saved edit", "Start a replacement Spec", "Exit",
			})
			if chooseErr != nil || stopped || choice == 2 {
				return chooseErr
			}
			if choice == 1 {
				if err := workspace.Abandon(); err != nil {
					return err
				}
				return runNew(nil, input, output, true)
			}
			path, err := change.CreateSetup(root, setup)
			if err != nil {
				return err
			}
			fmt.Fprintf(output, "Recreated active specification from saved edit: %s\n", path)
			fmt.Fprintln(output, "Run `spec` to continue. Verification must run again.")
			return nil
		} else if statErr != nil {
			return statErr
		}
	}
	if _, statErr := os.Stat(change.ActivePath(root)); statErr == nil && workspace.Setup != nil && !setup.Editing {
		if err := workspace.CompleteSetup(setup.Title); err != nil {
			return err
		}
		return offerImplementationPrompt(root, input, output)
	}

	for {
		switch setup.Stage {
		case setupChange:
			value, back, stopped, err := runTextPrompt(input, output, "New Spec", "What are you changing?", firstSetupInput(setup.Input, setup.Title), false, setup.Editing)
			if err != nil {
				return err
			}
			if stopped {
				setup.Input = value
				return saveAndExit(root, setup, output)
			}
			if back {
				setup.Title, setup.Input, setup.Stage = value, "", setupReview
				continue
			}
			setup.Title, setup.Input, setup.Stage = value, "", setupOutcome
		case setupOutcome:
			value, back, stopped, err := runTextPrompt(input, output, "New Spec", "What should happen when finished?", firstSetupInput(setup.Input, setup.Outcome), false, true)
			if err != nil {
				return err
			}
			if stopped {
				setup.Input = value
				return saveAndExit(root, setup, output)
			}
			if back {
				setup.Outcome, setup.Input, setup.Stage = value, "", setupChange
				continue
			}
			setup.Outcome, setup.Input, setup.Stage = value, "", setupLimits
		case setupLimits:
			value, back, stopped, err := runTextPrompt(input, output, "New Spec", "Are there any limits on the solution? (optional)", firstSetupInput(setup.Input, setup.Limits), true, true)
			if err != nil {
				return err
			}
			if stopped {
				setup.Input = value
				return saveAndExit(root, setup, output)
			}
			if back {
				setup.Limits, setup.Input, setup.Stage = value, "", setupOutcome
				continue
			}
			setup.Limits, setup.Input, setup.Stage = value, "", setupCriteria
		case setupCriteria:
			if len(setup.Criteria) == 0 && strings.TrimSpace(setup.Outcome) != "" {
				setup.Criteria = []state.SetupCriterion{{Text: setup.Outcome, Included: true}}
			}
			criteria, back, stopped, err := runSetupCriteria(input, output, setup.Criteria)
			if err != nil {
				return err
			}
			setup.Criteria = criteria
			if stopped {
				return saveAndExit(root, setup, output)
			}
			if back {
				setup.Stage = setupLimits
				continue
			}
			if !hasIncludedCriteria(setup.Criteria) {
				fmt.Fprintln(output, "At least one success criterion is required.")
				if err := saveSetup(root, setup); err != nil {
					return err
				}
				continue
			}
			setup.Stage = setupVerification
		case setupVerification, setupVerifyChange, setupVerifyWait:
			ready, stopped, paused, err := configureVerification(root, &setup, input, output)
			if err != nil {
				return err
			}
			if stopped {
				return saveAndExit(root, setup, output)
			}
			if paused {
				return nil
			}
			if !ready {
				continue
			}
			setup.Stage = setupBaseline
		case setupBaseline:
			commands, err := config.VerificationCommands(root)
			if err != nil {
				return err
			}
			choice, stopped, err := runChoice(input, output, "Verification", "Configured checks:\n"+formatCommands(commands), []string{
				"Run verification baseline", "Continue without a baseline", "Change verification", "Back to success criteria", "Save and exit",
			})
			if err != nil {
				return err
			}
			if stopped || choice == 4 {
				return saveAndExit(root, setup, output)
			}
			switch choice {
			case 0:
				result, runErr := verifyrun.Run(root, time.Now())
				if runErr != nil {
					fmt.Fprintln(output, "Verification baseline: FAIL")
				} else {
					fmt.Fprintf(output, "Verification baseline: PASS (%d check(s))\n", len(result.Commands))
				}
				setup.Stage = setupReview
			case 1:
				setup.Stage = setupReview
			case 2:
				setup.Stage = setupVerifyChange
			case 3:
				setup.Stage = setupCriteria
			}
		case setupReview:
			preview := formatSetupSummary(setup)
			saveLabel := "Create .spec.md"
			if setup.Editing {
				saveLabel = "Save Spec changes"
			}
			choice, stopped, err := runChoice(input, output, "Review Spec", preview, []string{
				saveLabel, "Edit change", "Edit expected outcome", "Edit limits", "Edit success criteria", "Change verification", "Save and exit",
			})
			if err != nil {
				return err
			}
			if stopped || choice == 6 {
				return saveAndExit(root, setup, output)
			}
			switch choice {
			case 1:
				setup.Stage = setupChange
			case 2:
				setup.Stage = setupOutcome
			case 3:
				setup.Stage = setupLimits
			case 4:
				setup.Stage = setupCriteria
			case 5:
				setup.Stage = setupVerifyChange
			default:
				if strings.TrimSpace(setup.Title) == "" {
					setup.Stage = setupChange
					continue
				}
				if strings.TrimSpace(setup.Outcome) == "" {
					setup.Stage = setupOutcome
					continue
				}
				if !hasIncludedCriteria(setup.Criteria) {
					setup.Stage = setupCriteria
					continue
				}
				editing := setup.Editing
				path, err := change.SaveSetup(root, setup)
				if err != nil {
					return err
				}
				if editing {
					fmt.Fprintf(output, "Updated active specification: %s\n", path)
				} else {
					printNewCreated(output, path)
				}
				return offerImplementationPrompt(root, input, output)
			}
		default:
			setup.Stage = setupChange
		}
		if err := saveSetup(root, setup); err != nil {
			return err
		}
	}
}

func configureVerification(root string, setup *state.Setup, input io.Reader, output io.Writer) (bool, bool, bool, error) {
	commands, err := config.VerificationCommands(root)
	if err != nil {
		return false, false, false, err
	}
	if len(commands) > 0 && setup.Stage != setupVerifyChange {
		fmt.Fprintln(output, "Verification")
		fmt.Fprintln(output)
		fmt.Fprintln(output, formatCommands(commands))
		fmt.Fprintln(output, "Using existing workspace verification.")
		return true, false, false, nil
	}
	selected, waiting, stopped, err := selectVerification(root, input, output, verificationPrompt(root, *setup, verifyrun.Detect(root)), &setup.Input)
	if err != nil || stopped {
		return false, stopped, false, err
	}
	if waiting == "back" {
		setup.Input = ""
		if setup.Editing {
			setup.Stage = setupReview
		} else {
			setup.Stage = setupCriteria
		}
		return false, false, false, nil
	}
	if waiting != "" {
		setup.Stage = setupVerifyWait
		if waiting == "ai" {
			setup.Stage = setupVerifyChange
		}
		if err := saveSetup(root, *setup); err != nil {
			return false, false, false, err
		}
		fmt.Fprintln(output, "Setup saved. Run `spec` to resume after verification is prepared.")
		return false, false, true, nil
	}
	if err := saveVerificationCommands(root, selected); err != nil {
		return false, false, false, err
	}
	setup.Input = ""
	return true, false, false, nil
}

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

func offerImplementationPrompt(root string, input io.Reader, output io.Writer) error {
	choice, stopped, err := runChoice(input, output, "Spec is ready.", "Choose the next action.", []string{"Copy implementation prompt", "Print implementation prompt", "Finish"})
	if err != nil || stopped || choice == 2 {
		return err
	}
	content, _, err := promptbuilder.Build(root, false)
	if err != nil {
		return err
	}
	if choice == 0 {
		if err := copyText(content); err != nil {
			return err
		}
		fmt.Fprintln(output, "Prompt copied to the clipboard.")
	} else {
		fmt.Fprint(output, content)
	}
	return nil
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

func firstSetupInput(current, saved string) string {
	if current != "" {
		return current
	}
	return saved
}

func hasIncludedCriteria(criteria []state.SetupCriterion) bool {
	for _, criterion := range criteria {
		if criterion.Included && strings.TrimSpace(criterion.Text) != "" {
			return true
		}
	}
	return false
}

func formatCommands(commands []string) string {
	var builder strings.Builder
	for _, command := range commands {
		builder.WriteString("✓ ")
		builder.WriteString(command)
		builder.WriteByte('\n')
	}
	return strings.TrimSpace(builder.String())
}

func formatSetupSummary(setup state.Setup) string {
	limits := strings.TrimSpace(setup.Limits)
	if limits == "" {
		limits = "none"
	}
	criteria := 0
	for _, criterion := range setup.Criteria {
		if criterion.Included && strings.TrimSpace(criterion.Text) != "" {
			criteria++
		}
	}
	return fmt.Sprintf("Change: %s\nExpected outcome: %s\nLimits: %s\nSuccess criteria: %d",
		boundedSummary(setup.Title), boundedSummary(setup.Outcome), boundedSummary(limits), criteria)
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
