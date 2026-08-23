package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/TaylorEdgerton/spec-cli/internal/change"
	"github.com/TaylorEdgerton/spec-cli/internal/gitutil"
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
	workspace, err := state.Register(root)
	if err != nil {
		return err
	}
	if created {
		fmt.Printf("Initialized Git repository in %s\n", root)
	}
	fmt.Printf("Registered workspace %s\n", workspace.ID)
	if !gitutil.HasBaseline(root) {
		fmt.Println("Warning: this repository has no baseline commit.")
		fmt.Println("Review the files and create an initial commit before you run `spec new`.")
	}
	return nil
}

func cmdNew(args []string) error {
	root, err := currentRoot()
	if err != nil {
		return fmt.Errorf("Git repository is required; run `spec init`")
	}
	path, err := change.New(root, strings.Join(args, " "), time.Now())
	if err != nil {
		return err
	}
	fmt.Printf("Created active specification: %s\n", path)
	fmt.Println("Edit the specification, then run `spec prompt`.")
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
	result, err := verifyrun.Run(root, os.Stdout, os.Stderr, time.Now())
	if err != nil {
		fmt.Println("verify: FAIL")
		return err
	}
	fmt.Printf("verify: PASS (%d command(s))\n", len(result.Commands))
	return nil
}

func cmdDone(args []string) error {
	root, err := currentRoot()
	if err != nil {
		return err
	}
	record, err := change.Done(root, strings.Join(args, " "), time.Now())
	if err != nil {
		return err
	}
	fmt.Printf("Finished change with %d changed file(s).\n", len(record.ChangedFiles))
	fmt.Printf("Archived specification in external state: %s\n", record.SpecArchive)
	if record.Verification == nil {
		fmt.Println("Warning: no verification result was recorded.")
	} else if !record.Verification.Passed {
		fmt.Println("Warning: the latest verification result failed.")
	}
	if record.EndSHA == record.BaseSHA && len(record.ChangedFiles) > 0 {
		fmt.Println("The changes are not in a new commit.")
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
