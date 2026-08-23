package change

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/TaylorEdgerton/spec-cli/internal/gitutil"
	"github.com/TaylorEdgerton/spec-cli/internal/state"
)

const ActiveFilename = ".spec.md"

func ActivePath(root string) string {
	return filepath.Join(root, ActiveFilename)
}

func New(root, title string, now time.Time) (string, error) {
	base, err := gitutil.Head(root)
	if err != nil {
		return "", fmt.Errorf("a baseline commit is required; commit the current project before `spec new`")
	}
	workspace, err := state.Load(root)
	if err != nil {
		return "", err
	}
	title = strings.TrimSpace(title)
	path := ActivePath(root)
	if _, err := os.Stat(path); err == nil {
		if workspace.Active {
			return "", fmt.Errorf("%s already exists; finish it with `spec done` or remove it to abandon the change", ActiveFilename)
		}
		return "", fmt.Errorf("%s already exists without active metadata; remove it before `spec new`", ActiveFilename)
	} else if !os.IsNotExist(err) {
		return "", err
	}
	if workspace.Active {
		if err := workspace.Abandon(); err != nil {
			return "", fmt.Errorf("clear abandoned change metadata: %w", err)
		}
	}
	heading := title
	if heading == "" {
		heading = "Change"
	}
	content := "# " + heading + "\n\n## Intent\n\n"
	if title != "" {
		content += title + "\n"
	}
	content += "\n## Scope\n\n## Constraints\n\n## Acceptance Criteria\n\n## Relevant Files\n\n## Notes\n"
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return "", err
	}
	_, writeErr := file.WriteString(content)
	closeErr := file.Close()
	if writeErr != nil {
		_ = os.Remove(path)
		return "", writeErr
	}
	if closeErr != nil {
		_ = os.Remove(path)
		return "", closeErr
	}
	if err := workspace.Start(title, base, now.UTC()); err != nil {
		_ = os.Remove(path)
		return "", err
	}
	return path, nil
}

func Done(root, summary string, now time.Time) (state.History, error) {
	workspace, err := state.Load(root)
	if err != nil {
		return state.History{}, err
	}
	if !workspace.Active {
		return state.History{}, fmt.Errorf("no active change; run `spec new`")
	}
	path := ActivePath(root)
	current, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return state.History{}, fmt.Errorf("active specification is missing: %s", path)
	}
	if err != nil {
		return state.History{}, err
	}
	files, err := gitutil.ChangedFiles(root, workspace.BaseSHA)
	if err != nil {
		return state.History{}, err
	}
	files = withoutActiveSpec(files)
	end, _ := gitutil.Head(root)
	verification, err := workspace.Verification()
	if err != nil {
		return state.History{}, err
	}
	title := workspace.Title
	if title == "" {
		title = sectionSummary(string(current), "Intent")
	}
	historyVerification := verification
	if verification != nil {
		copy := *verification
		copy.Output = ""
		historyVerification = &copy
	}
	record := state.History{
		Title: title, StartedAt: workspace.StartedAt, BaseSHA: workspace.BaseSHA,
		FinishedAt: now.UTC(), EndSHA: end, ChangedFiles: files,
		Verification: historyVerification, Summary: strings.TrimSpace(summary),
	}
	record, err = workspace.Finish(record, current, path)
	if err != nil {
		return state.History{}, err
	}
	return record, nil
}

func withoutActiveSpec(files []string) []string {
	result := files[:0]
	for _, file := range files {
		if filepath.ToSlash(file) != ActiveFilename {
			result = append(result, file)
		}
	}
	return result
}

func sectionSummary(markdown, heading string) string {
	lines := strings.Split(markdown, "\n")
	inSection := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "## ") {
			if inSection {
				break
			}
			inSection = strings.EqualFold(strings.TrimSpace(strings.TrimPrefix(trimmed, "## ")), heading)
			continue
		}
		if inSection && trimmed != "" {
			return strings.TrimLeft(trimmed, "-* ")
		}
	}
	return "Untitled change"
}
