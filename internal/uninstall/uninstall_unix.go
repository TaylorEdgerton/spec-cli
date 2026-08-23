//go:build !windows

package uninstall

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	pathMarkerBegin = "# >>> spec-cli PATH >>>"
	pathMarkerEnd   = "# <<< spec-cli PATH <<<"
)

func pathDescription(_ string, home string) string {
	return fmt.Sprintf("installer-managed PATH block in %s (if present)", filepath.Join(home, ".profile"))
}

func completionMessage() string {
	return "Spec was uninstalled. Restart your shell to refresh PATH."
}

func removeInstallation(executable, home string) error {
	if err := os.Remove(executable); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove binary: %w", err)
	}
	if err := removePathBlock(filepath.Join(home, ".profile")); err != nil {
		return fmt.Errorf("remove installer-managed PATH block: %w", err)
	}
	return nil
}

func removePathBlock(profile string) error {
	data, err := os.ReadFile(profile)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	content := string(data)
	startToken := "\n" + pathMarkerBegin + "\n"
	start := strings.Index(content, startToken)
	if start < 0 {
		if !strings.HasPrefix(content, pathMarkerBegin+"\n") {
			return nil
		}
		startToken = pathMarkerBegin + "\n"
		start = 0
	}
	endStart := start + len(startToken)
	endRelative := strings.Index(content[endStart:], pathMarkerEnd+"\n")
	if endRelative < 0 {
		return nil
	}
	end := endStart + endRelative + len(pathMarkerEnd) + 1
	updated := content[:start] + content[end:]
	info, err := os.Stat(profile)
	if err != nil {
		return err
	}
	temporary := profile + ".spec.tmp"
	if err := os.WriteFile(temporary, []byte(updated), info.Mode().Perm()); err != nil {
		return err
	}
	if err := os.Rename(temporary, profile); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	return nil
}
