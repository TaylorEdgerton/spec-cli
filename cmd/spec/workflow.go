package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/TaylorEdgerton/spec-cli/internal/aiusage"
	"github.com/TaylorEdgerton/spec-cli/internal/change"
	"github.com/TaylorEdgerton/spec-cli/internal/config"
	"github.com/TaylorEdgerton/spec-cli/internal/gitutil"
	"github.com/TaylorEdgerton/spec-cli/internal/sandbox"
	"github.com/TaylorEdgerton/spec-cli/internal/state"
	verifyrun "github.com/TaylorEdgerton/spec-cli/internal/verify"
)

func currentRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return gitutil.Root(dir)
}

func noArgs(args []string, usage string) error {
	if len(args) != 0 {
		return fmt.Errorf("usage: %s", usage)
	}
	return nil
}

func cmdInit(args []string) error {
	if err := noArgs(args, "spec init"); err != nil {
		return err
	}
	return runInit(os.Stdout)
}

func runInit(output io.Writer) error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	root, created, err := gitutil.EnsureRepository(dir)
	if err != nil {
		return err
	}
	if _, err := gitutil.Status(root); err != nil {
		return fmt.Errorf("Git repository is not ready: %w", err)
	}
	if _, err := gitutil.EnsureActiveSpecExcluded(root); err != nil {
		return fmt.Errorf("configure local Git exclusion: %w", err)
	}
	installedDefaults, err := config.InstallDefaults()
	if err != nil {
		return fmt.Errorf("initialize Spec configuration: %w", err)
	}
	workspace, err := state.Register(root)
	if err != nil {
		return err
	}
	if created {
		fmt.Fprintf(output, "Initialized Git repository in %s\n", root)
	}
	fmt.Fprintf(output, "Registered workspace %s\n", workspace.ID)
	if installedDefaults {
		configurationDirectory, _ := config.Directory()
		fmt.Fprintf(output, "Installed default configuration in %s\n", configurationDirectory)
	}
	if !gitutil.HasBaseline(root) {
		fmt.Fprintln(output, "Warning: this repository has no baseline commit.")
		fmt.Fprintln(output, "Review the files and create an initial commit before you run `spec new`.")
	}
	return nil
}

func cmdVerify(args []string) error {
	if err := noArgs(args, "spec verify"); err != nil {
		return err
	}
	root, err := currentRoot()
	if err != nil {
		return err
	}
	interactive := terminalInput(os.Stdin)
	workspace, err := state.Load(root)
	if err != nil {
		return err
	}
	if !workspace.Active {
		return fmt.Errorf("no active change; run `spec new`")
	}
	if workspace.Setup != nil {
		return fmt.Errorf("Spec setup is incomplete; run `spec` to resume")
	}
	commands, err := config.VerificationCommands(root)
	if err != nil {
		return err
	}
	if len(commands) == 0 && interactive {
		configured, err := configureReadySpec(root, os.Stdin, os.Stdout)
		if err != nil || !configured {
			return err
		}
	}
	return runVerify(root, os.Stdin, os.Stdout, interactive)
}

func runVerify(root string, input io.Reader, output io.Writer, interactive bool) error {
	for {
		result, runErr := verifyrun.Run(root, time.Now())
		if runErr == nil {
			for _, command := range result.Commands {
				fmt.Fprintf(output, "PASS %s\n", command)
			}
			fmt.Fprintln(output, "\nVerification PASS")
			return nil
		}
		for index, command := range result.Commands {
			status := "FAIL"
			if index < result.Completed {
				status = "PASS"
			}
			fmt.Fprintf(output, "%s %s\n", status, command)
			if status == "FAIL" {
				break
			}
		}
		fmt.Fprintln(output, "\nVerification FAILED")
		if !interactive {
			return runErr
		}
		for {
			choice, stopped, err := runChoice(input, output, "Verification FAILED", "Choose the next action.", []string{
				"Copy failure context for AI", "Show full output", "Run again", "Exit",
			})
			if err != nil {
				return err
			}
			if stopped || choice == 3 {
				return runErr
			}
			switch choice {
			case 0:
				if err := copyText(verificationFailurePrompt(result)); err != nil {
					return err
				}
				fmt.Fprintln(output, "Failure context copied to the clipboard.")
			case 1:
				fmt.Fprintln(output, result.Output)
			case 2:
				break
			}
			if choice == 2 {
				break
			}
		}
	}
}

func verificationFailurePrompt(result state.Verification) string {
	return "Read and follow `.spec.md`.\n" +
		"Diagnose the verification failure and make the smallest in-scope correction.\n" +
		"Do not claim verification passes until the configured command is run successfully.\n\n" +
		"Failed command: " + result.FailedCommand + "\n\n" + result.Output + "\n"
}

func cmdDone(args []string) error {
	root, err := currentRoot()
	if err != nil {
		return err
	}
	return runDone(root, args, os.Stdin, os.Stdout, terminalInput(os.Stdin))
}

func runDone(root string, args []string, input io.Reader, output io.Writer, interactive bool) error {
	workspace, err := state.Load(root)
	if err != nil {
		return err
	}
	if !workspace.Active {
		return fmt.Errorf("no active change; run `spec new`")
	}
	if workspace.Setup != nil {
		return fmt.Errorf("Spec setup is incomplete; run `spec` to resume")
	}
	verification, err := workspace.Verification()
	if err != nil {
		return err
	}
	current, err := verifyrun.Current(root, verification)
	if err != nil {
		return err
	}
	if !current {
		return fmt.Errorf("verification is not current and passing; run `spec verify`")
	}
	specPath := change.ActivePath(root)
	data, err := os.ReadFile(specPath)
	if os.IsNotExist(err) {
		return fmt.Errorf("active specification is missing; run `spec` to recover")
	}
	if err != nil {
		return err
	}
	criteria := change.AcceptanceCriteria(string(data))
	if interactive && len(criteria) > 0 {
		reviewed, stopped, err := runReviewCriteria(input, output, criteria)
		if err != nil {
			return err
		}
		updated, err := change.UpdateAcceptanceCriteria(string(data), reviewed)
		if err != nil {
			return err
		}
		if err := os.WriteFile(specPath, []byte(updated), 0o644); err != nil {
			return err
		}
		criteria = reviewed
		if stopped {
			fmt.Fprintln(output, "Criteria review saved. Run `spec` to resume.")
			return nil
		}
	}
	for _, criterion := range criteria {
		if !criterion.Checked {
			return fmt.Errorf("all success criteria must be reviewed before the Spec can finish")
		}
	}
	files, err := gitutil.ChangedFiles(root, workspace.BaseSHA)
	if err != nil {
		return err
	}
	filtered := make([]string, 0, len(files))
	for _, file := range files {
		if filepath.ToSlash(file) != change.ActiveFilename {
			filtered = append(filtered, file)
		}
	}
	if interactive {
		var detail strings.Builder
		detail.WriteString("Changed files:\n")
		if len(filtered) == 0 {
			detail.WriteString("  none\n")
		} else {
			for _, file := range filtered {
				fmt.Fprintf(&detail, "  %s\n", file)
			}
		}
		fmt.Fprintf(&detail, "\nVerification:\n  PASS\n\nSuccess criteria:\n  %d/%d reviewed", len(criteria), len(criteria))
		choice, stopped, err := runChoice(input, output, "Finish Spec?", detail.String(), []string{"Finish Spec", "Exit"})
		if err != nil {
			return err
		}
		if stopped || choice == 1 {
			return nil
		}
	}
	return finishSpec(root, workspace, args, output)
}

func finishSpec(root string, workspace state.Workspace, args []string, output io.Writer) error {
	var session *state.SandboxSession
	var finalUsage *aiusage.Summary
	if workspace.SandboxSession != nil {
		copy := *workspace.SandboxSession
		session = &copy
		usage := sandbox.Usage(root, time.Now())
		finalUsage = &usage
	}
	record, err := change.DoneWithUsage(root, strings.Join(args, " "), time.Now(), finalUsage)
	if err != nil {
		return err
	}
	fmt.Fprintf(output, "Finished change with %d changed file(s).\n", len(record.ChangedFiles))
	fmt.Fprintf(output, "Archived specification in external state: %s\n", record.SpecArchive)
	if record.Verification == nil {
		fmt.Fprintln(output, "Warning: no verification result was recorded.")
	} else if !record.Verification.Passed {
		fmt.Fprintln(output, "Warning: the latest verification result failed.")
	}
	if record.EndSHA == record.BaseSHA && len(record.ChangedFiles) > 0 {
		fmt.Fprintln(output, "The changes are not in a new commit.")
	}
	if record.AIUsage != nil {
		if record.AIUsage.Available {
			fmt.Fprintln(output, "Captured final AI usage from the Spec sandbox.")
		} else {
			fmt.Fprintf(output, "AI usage unavailable: %s\n", record.AIUsage.UnavailableReason)
		}
		if err := sandbox.StopTelemetry(session); err != nil {
			fmt.Fprintln(output, "Warning: the sandbox telemetry collector could not be stopped.")
		}
	}
	return nil
}

func displayPath(path string) string {
	absolute, err := filepath.Abs(path)
	if err == nil {
		return absolute
	}
	return path
}
