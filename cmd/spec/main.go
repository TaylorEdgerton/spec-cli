package main

import (
	"fmt"
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
	{"new", "create and activate a change specification", cmdNew},
	{"prompt", "create a provider-neutral engineering prompt", cmdPrompt},
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
		usage()
		return nil
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
	fmt.Printf("%s %s - structured AI-assisted engineering\n\n", brand.Command, version)
	fmt.Printf("usage: %s <command> [args]\n\ncommands:\n", brand.Command)
	for _, item := range commands {
		fmt.Printf("  %-10s %s\n", item.name, item.summary)
	}
}
