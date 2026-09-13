package main

import (
	"fmt"
	"io"
	"os"
	"time"
)

func cmdPlan(args []string) error {
	root, err := currentRoot()
	if err != nil {
		return err
	}
	return runPlanCommand(root, args, os.Stdin, os.Stdout, time.Now())
}
func runPlanCommand(root string, args []string, input io.Reader, output io.Writer, now time.Time) error {
	if len(args) != 2 || args[0] != "submit" || args[1] != "--stdin" {
		return fmt.Errorf("usage: spec plan submit --stdin")
	}
	return runPlanSubmit(root, input, output, now)
}
