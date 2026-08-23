package main

import (
	"fmt"
	"os"
	"time"

	"github.com/TaylorEdgerton/spec-cli/internal/aiusage"
	"github.com/TaylorEdgerton/spec-cli/internal/sandbox"
)

func cmdUsage(args []string) error {
	if err := noArgs(args, "spec usage"); err != nil {
		return err
	}
	root, err := currentRoot()
	if err != nil {
		fmt.Fprintln(os.Stdout, "AI usage")
		fmt.Fprintln(os.Stdout)
		fmt.Fprintln(os.Stdout, "usage unavailable: use a spec-managed sandbox environment for usage stats")
		return nil
	}
	aiusage.Format(os.Stdout, sandbox.Usage(root, time.Now()))
	return nil
}
