package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/TaylorEdgerton/spec-cli/internal/change"
	"github.com/TaylorEdgerton/spec-cli/internal/config"
	"github.com/TaylorEdgerton/spec-cli/internal/documents"
	"github.com/TaylorEdgerton/spec-cli/internal/gitutil"
	"github.com/TaylorEdgerton/spec-cli/internal/sandbox"
	"github.com/TaylorEdgerton/spec-cli/internal/secrets"
	"github.com/TaylorEdgerton/spec-cli/internal/state"
)

func cmdADR(args []string) error {
	root, err := currentRoot()
	if err != nil {
		return err
	}
	path, err := documents.ADR(root, strings.Join(args, " "))
	if err != nil {
		return err
	}
	fmt.Printf("Created %s\n", displayPath(path))
	return nil
}

func cmdREADME(args []string) error {
	if err := noArgs(args, "spec readme"); err != nil {
		return err
	}
	root, err := currentRoot()
	if err != nil {
		return err
	}
	created, output, err := documents.README(root)
	if err != nil {
		return err
	}
	if created {
		fmt.Printf("Created %s\n", filepath.Join(root, "README.md"))
		return nil
	}
	if workspace, loadErr := state.Load(root); loadErr == nil {
		_ = workspace.SavePrompt(output)
	}
	fmt.Print(output)
	return nil
}

func cmdRunbook(args []string) error {
	if err := noArgs(args, "spec runbook"); err != nil {
		return err
	}
	root, err := currentRoot()
	if err != nil {
		return err
	}
	path, created, output, err := documents.Runbook(root)
	if err != nil {
		return err
	}
	if created {
		fmt.Printf("Created %s\n", path)
		return nil
	}
	if workspace, loadErr := state.Load(root); loadErr == nil {
		_ = workspace.SavePrompt(output)
	}
	fmt.Print(output)
	return nil
}

func cmdSandbox(args []string) error {
	agent, assumeYes := "shell", false
	seenAgent := false
	for _, arg := range args {
		if arg == "--yes" {
			assumeYes = true
		} else if strings.HasPrefix(arg, "-") || seenAgent {
			return fmt.Errorf("usage: spec sandbox [agent] [--yes]")
		} else {
			agent, seenAgent = arg, true
		}
	}
	root, err := currentRoot()
	if err != nil {
		return err
	}
	return sandbox.Launch(root, agent, assumeYes, os.Stdin, os.Stdout)
}

func cmdCheck(args []string) error {
	if err := noArgs(args, "spec check"); err != nil {
		return err
	}
	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	root, gitErr := gitutil.Root(dir)
	if gitErr != nil {
		fmt.Println("FAIL Git repository is not present")
		return fmt.Errorf("workspace is not ready")
	}
	ok := true
	printCheck(true, "Git repository is present")
	hasBaseline := gitutil.HasBaseline(root)
	printCheck(hasBaseline, "baseline commit is present")
	ok = ok && hasBaseline
	excluded, excludeErr := gitutil.ActiveSpecExcluded(root)
	localExcludeReady := excludeErr == nil && excluded
	printCheck(localExcludeReady, ".git/info/exclude excludes .spec.md")
	ok = ok && localExcludeReady
	status, statusErr := gitutil.Status(root)
	if statusErr != nil {
		printCheck(false, "Git status is available")
		ok = false
	} else if strings.Contains(status, "\n") {
		fmt.Println("INFO Git working tree has changes")
	} else {
		fmt.Println("PASS Git working tree is clean")
	}
	commands, configErr := config.VerificationCommands(root)
	configured := configErr == nil && len(commands) > 0
	printCheck(configured, "verification is configured")
	ok = ok && configured
	workspace, stateErr := state.Load(root)
	registered := stateErr == nil
	printCheck(registered, "external Spec state is accessible")
	ok = ok && registered
	_, specErr := os.Stat(change.ActivePath(root))
	specExists := specErr == nil
	if specErr != nil && !os.IsNotExist(specErr) {
		fmt.Printf("FAIL active specification cannot be inspected: %v\n", specErr)
		ok = false
	} else if specExists && registered && workspace.Active {
		fmt.Println("INFO active specification: .spec.md")
	} else if !specExists && registered && !workspace.Active {
		fmt.Println("INFO no active specification")
	} else if specExists {
		fmt.Println("FAIL .spec.md exists but external state is not active")
		ok = false
	} else if registered && workspace.Active {
		fmt.Println("FAIL external state is active but .spec.md is missing")
		ok = false
	} else {
		fmt.Println("INFO no active specification")
	}
	if _, err := exec.LookPath("sbx"); err == nil {
		fmt.Println("PASS Docker Sandbox is available")
	} else {
		fmt.Println("INFO Docker Sandbox is not available")
	}
	findings, scanErr := secrets.Scan(root)
	if scanErr != nil {
		fmt.Printf("WARN sensitive-file scan failed: %v\n", scanErr)
	} else if len(findings) == 0 {
		fmt.Println("PASS no obvious sensitive files found")
	} else {
		fmt.Printf("WARN %d potential AI-readable sensitive file(s) found\n", len(findings))
		for _, finding := range findings {
			fmt.Printf("     %s (%s)\n", finding.Path, finding.Reason)
		}
	}
	if !ok {
		return fmt.Errorf("workspace is not ready")
	}
	return nil
}

func printCheck(pass bool, text string) {
	if pass {
		fmt.Println("PASS " + text)
	} else {
		fmt.Println("FAIL " + text)
	}
}
