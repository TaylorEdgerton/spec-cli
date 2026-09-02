package main

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"

	promptbuilder "github.com/TaylorEdgerton/spec-cli/internal/prompt"
	"github.com/TaylorEdgerton/spec-cli/internal/state"
)

func TestPromptCommandSupportsPlanModeAndBackwardCompatibleImplementationMode(t *testing.T) {
	tests := []struct {
		args []string
		kind promptbuilder.Kind
	}{
		{args: nil, kind: promptbuilder.Implementation},
		{args: []string{"--plan"}, kind: promptbuilder.Plan},
		{args: []string{"--plan", "--include-files", "--info"}, kind: promptbuilder.Plan},
	}
	for _, test := range tests {
		var stdout, stderr bytes.Buffer
		var gotKind promptbuilder.Kind
		err := runPromptCommand("/repo", test.args, &stdout, &stderr, promptCommandServices{
			Build: func(_ string, _ bool, kind promptbuilder.Kind) (string, promptbuilder.Info, error) {
				gotKind = kind
				return "PROMPT", promptbuilder.Info{ApproxTokens: 2}, nil
			},
			Copy: func(string) error { return nil },
		})
		if err != nil || gotKind != test.kind {
			t.Fatalf("args=%v kind=%q err=%v", test.args, gotKind, err)
		}
	}
}

func TestPromptCommandValidatesOptionsAndCopyDelivery(t *testing.T) {
	build := func(_ string, _ bool, _ promptbuilder.Kind) (string, promptbuilder.Info, error) {
		return "PROMPT", promptbuilder.Info{}, nil
	}
	for _, args := range [][]string{{"--wat"}, {"--plan", "--plan"}, {"--copy", "--copy"}} {
		err := runPromptCommand("/repo", args, &bytes.Buffer{}, &bytes.Buffer{}, promptCommandServices{Build: build})
		if err == nil || !strings.Contains(err.Error(), "usage: spec prompt") || !strings.Contains(err.Error(), "--plan") {
			t.Fatalf("args=%v usage error=%v", args, err)
		}
	}
	var copied string
	var stdout, stderr bytes.Buffer
	err := runPromptCommand("/repo", []string{"--plan", "--copy", "--info"}, &stdout, &stderr, promptCommandServices{
		Build: build,
		Copy:  func(content string) error { copied = content; return nil },
	})
	if err != nil || copied != "PROMPT" || stdout.Len() != 0 || !strings.Contains(stderr.String(), "Plan prompt copied") || !strings.Contains(stderr.String(), "Prompt context") {
		t.Fatalf("copied=%q stdout=%q stderr=%q err=%v", copied, stdout.String(), stderr.String(), err)
	}
}

func TestPromptCommandDoesNotRecordClipboardSuccessWhenCopyFails(t *testing.T) {
	called := false
	err := runPromptCommand("/repo", []string{"--plan", "--copy"}, &bytes.Buffer{}, &bytes.Buffer{}, promptCommandServices{
		Build: func(_ string, _ bool, _ promptbuilder.Kind) (string, promptbuilder.Info, error) {
			return "PROMPT", promptbuilder.Info{}, nil
		},
		Copy: func(string) error { return errors.New("clipboard unavailable") },
		Record: func(promptbuilder.Kind, state.TimelineEventType, string) {
			called = true
		},
	})
	if err == nil || called {
		t.Fatalf("failed clipboard delivery err=%v recorded=%v", err, called)
	}
}

func TestCopyTextFallsBackToWindowsClipboardInWSL(t *testing.T) {
	lookedUp := []string{}
	lookup := func(name string) (string, error) {
		lookedUp = append(lookedUp, name)
		if name == "clip.exe" {
			return "/mnt/c/Windows/System32/clip.exe", nil
		}
		return "", errors.New("not found")
	}
	run := func(path string, args []string, content string) error {
		if path != "/mnt/c/Windows/System32/clip.exe" || len(args) != 0 || content != "prompt" {
			return fmt.Errorf("unexpected invocation: %s %v %q", path, args, content)
		}
		return nil
	}
	if err := copyTextWith("linux", "prompt", lookup, run); err != nil {
		t.Fatal(err)
	}
	if strings.Join(lookedUp, ",") != "wl-copy,xclip,xsel,clip.exe" {
		t.Fatalf("lookup order = %v", lookedUp)
	}
}

func TestCopyTextTriesNextAvailableToolAfterFailure(t *testing.T) {
	lookup := func(name string) (string, error) {
		if name == "wl-copy" || name == "xclip" {
			return "/usr/bin/" + name, nil
		}
		return "", errors.New("not found")
	}
	runs := []string{}
	run := func(path string, _ []string, _ string) error {
		runs = append(runs, path)
		if strings.HasSuffix(path, "wl-copy") {
			return errors.New("Wayland display is unavailable")
		}
		return nil
	}
	if err := copyTextWith("linux", "prompt", lookup, run); err != nil {
		t.Fatal(err)
	}
	if strings.Join(runs, ",") != "/usr/bin/wl-copy,/usr/bin/xclip" {
		t.Fatalf("run order = %v", runs)
	}
}

func TestCopyTextReportsSupportedToolsWhenNoneAreInstalled(t *testing.T) {
	err := copyTextWith("linux", "prompt", func(string) (string, error) {
		return "", errors.New("not found")
	}, func(string, []string, string) error {
		t.Fatal("run should not be called")
		return nil
	})
	for _, expected := range []string{"no clipboard tool", "wl-copy", "xclip", "xsel", "clip.exe"} {
		if err == nil || !strings.Contains(err.Error(), expected) {
			t.Fatalf("error = %v; expected %q", err, expected)
		}
	}
}
