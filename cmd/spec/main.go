package main

import (
	"fmt"
	"io"
	"os"

	"github.com/TaylorEdgerton/spec-cli/internal/brand"
)

var version = "dev"

type command struct {
	name    string
	summary string
	run     func([]string) error
}

var commands = []command{
	{"init", "register the current Git workspace", cmdInit},
	{"configure", "open global configuration and templates", cmdConfigure},
	{"new", "start or resume a change specification", cmdNew},
	{"prompt", "create a provider-neutral engineering prompt", cmdPrompt},
	{"plan", "submit an optional implementation plan", cmdPlan},
	{"verify", "run deterministic project checks", cmdVerify},
	{"done", "finish the active change", cmdDone},
	{"adr", "create an architecture decision record", cmdADR},
	{"readme", "create or prepare README.md in the current directory", cmdREADME},
	{"runbook", "list or prepare scenario runbooks", cmdRunbook},
	{"sandbox", "run the workspace in Docker Sandbox", cmdSandbox},
	{"usage", "report AI usage for the active Spec sandbox", cmdUsage},
	{"check", "report workspace readiness", cmdCheck},
	{"uninstall", "remove Spec from this computer", cmdUninstall},
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", brand.Command, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return runHome(os.Stdin, os.Stdout, terminalInput(os.Stdin))
	}
	switch args[0] {
	case "-h", "--help", "help":
		usage()
		return nil
	case "-v", "--version", "version":
		fmt.Println(brand.Command, version)
		return nil
	}
	for _, item := range commands {
		if item.name == args[0] {
			return item.run(args[1:])
		}
	}
	usage()
	return fmt.Errorf("unknown command %q", args[0])
}

func usage() {
	usageTo(os.Stdout)
}

func usageTo(output io.Writer) {
	fmt.Fprintf(output, "%s %s - structured AI-assisted engineering\n\n", brand.Command, version)
	fmt.Fprintf(output, "usage: %s [command] [args]\n", brand.Command)
	fmt.Fprintf(output, "run %s without a command to open or resume the interactive workflow\n\ncommands:\n", brand.Command)
	for _, item := range commands {
		fmt.Fprintf(output, "  %-10s %s\n", item.name, item.summary)
	}
	fmt.Fprintln(output, "\nplanning:")
	fmt.Fprintf(output, "  %-30s %s\n", "spec prompt --plan [--copy]", "create the optional planning prompt")
	fmt.Fprintf(output, "  %-30s %s\n", "spec plan submit --stdin", "validate and store plan JSON from stdin")
	fmt.Fprintf(output, "  %-30s %s\n", "spec prompt [--copy]", "create the implementation prompt")
}
