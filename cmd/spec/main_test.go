package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestCommandUsageSeparatesPlanningFromImplementation(t *testing.T) {
	var output bytes.Buffer
	usageTo(&output)
	text := output.String()
	for _, expected := range []string{
		"run spec without a command to open or resume the interactive workflow",
		"spec prompt --plan [--copy]",
		"create the optional planning prompt",
		"spec plan submit --stdin",
		"validate and store plan JSON from stdin",
		"spec prompt [--copy]",
		"create the implementation prompt",
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("usage missing %q:\n%s", expected, text)
		}
	}
	if strings.Index(text, "spec prompt --plan") > strings.Index(text, "spec prompt [--copy]") {
		t.Fatalf("usage did not present planning before implementation:\n%s", text)
	}
}
