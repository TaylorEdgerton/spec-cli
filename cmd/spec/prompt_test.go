package main

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

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
