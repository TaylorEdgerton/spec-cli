package gitutil

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

type ChangeKind string

const (
	ChangeAdded    ChangeKind = "added"
	ChangeModified ChangeKind = "modified"
	ChangeDeleted  ChangeKind = "deleted"
	ChangeRenamed  ChangeKind = "renamed"
)

type FileChange struct {
	Path      string
	OldPath   string
	Kind      ChangeKind
	Additions int
	Deletions int
	Binary    bool
}

type DiffLineKind string

const (
	DiffContext  DiffLineKind = "context"
	DiffAddition DiffLineKind = "addition"
	DiffDeletion DiffLineKind = "deletion"
	DiffMeta     DiffLineKind = "meta"
)

type DiffLine struct {
	Kind    DiffLineKind
	Text    string
	OldLine int
	NewLine int
}

type DiffHunk struct {
	Header   string
	OldStart int
	OldCount int
	NewStart int
	NewCount int
	Lines    []DiffLine
}

func Changes(root, base string) ([]FileChange, error) {
	raw, err := outputRaw(root, "diff", "--name-status", "--find-renames", "-z", base, "--")
	if err != nil {
		return nil, err
	}
	tokens := strings.Split(raw, "\x00")
	changes := make([]FileChange, 0, len(tokens)/2)
	seen := make(map[string]bool)
	for index := 0; index < len(tokens); {
		status := tokens[index]
		index++
		if status == "" {
			continue
		}
		if index >= len(tokens) {
			return nil, fmt.Errorf("parse git name-status: status %q has no path", status)
		}
		change := FileChange{}
		switch status[0] {
		case 'A':
			change.Kind = ChangeAdded
		case 'D':
			change.Kind = ChangeDeleted
		case 'R':
			change.Kind = ChangeRenamed
			change.OldPath = normalizeGitPath(tokens[index])
			index++
			if index >= len(tokens) {
				return nil, fmt.Errorf("parse git name-status: rename %q has no destination", change.OldPath)
			}
		default:
			change.Kind = ChangeModified
		}
		change.Path = normalizeGitPath(tokens[index])
		index++
		if excludedChangePath(change.Path) {
			continue
		}
		if err := populateTrackedStats(root, base, &change); err != nil {
			return nil, err
		}
		seen[change.Path] = true
		changes = append(changes, change)
	}

	untracked, err := outputRaw(root, "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return nil, err
	}
	for _, token := range strings.Split(untracked, "\x00") {
		path := normalizeGitPath(token)
		if path == "" || seen[path] || excludedChangePath(path) {
			continue
		}
		data, readErr := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
		if readErr != nil {
			return nil, readErr
		}
		change := FileChange{Path: path, Kind: ChangeAdded, Binary: bytes.IndexByte(data, 0) >= 0}
		if !change.Binary {
			change.Additions = contentLineCount(data)
		}
		changes = append(changes, change)
	}
	sort.SliceStable(changes, func(left, right int) bool { return changes[left].Path < changes[right].Path })
	return changes, nil
}

func populateTrackedStats(root, base string, change *FileChange) error {
	paths := []string{change.Path}
	if change.OldPath != "" {
		paths = append([]string{change.OldPath}, paths...)
	}
	args := []string{"diff", "--numstat", "--find-renames", base, "--"}
	args = append(args, paths...)
	text, err := output(root, args...)
	if err != nil {
		return err
	}
	if text == "" {
		return nil
	}
	fields := strings.SplitN(strings.Split(text, "\n")[0], "\t", 3)
	if len(fields) < 2 {
		return fmt.Errorf("parse git numstat for %s: %q", change.Path, text)
	}
	if fields[0] == "-" || fields[1] == "-" {
		change.Binary = true
		return nil
	}
	change.Additions, err = strconv.Atoi(fields[0])
	if err != nil {
		return fmt.Errorf("parse additions for %s: %w", change.Path, err)
	}
	change.Deletions, err = strconv.Atoi(fields[1])
	if err != nil {
		return fmt.Errorf("parse deletions for %s: %w", change.Path, err)
	}
	return nil
}

func Hunks(root, base string, change FileChange) ([]DiffHunk, error) {
	if change.Binary {
		return nil, nil
	}
	if change.Kind == ChangeAdded {
		path := filepath.Join(root, filepath.FromSlash(change.Path))
		tracked, err := output(root, "ls-files", "--error-unmatch", "--", change.Path)
		if err != nil || tracked == "" {
			data, readErr := os.ReadFile(path)
			if readErr != nil {
				return nil, readErr
			}
			return untrackedHunks(data), nil
		}
	}
	paths := []string{change.Path}
	if change.OldPath != "" {
		paths = append([]string{change.OldPath}, paths...)
	}
	args := []string{"diff", "--no-ext-diff", "--no-color", "--unified=3", "--find-renames", base, "--"}
	args = append(args, paths...)
	text, err := outputRaw(root, args...)
	if err != nil {
		return nil, err
	}
	return parseDiffHunks(text)
}

var hunkHeaderPattern = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@`)

func parseDiffHunks(text string) ([]DiffHunk, error) {
	var hunks []DiffHunk
	var current *DiffHunk
	oldLine, newLine := 0, 0
	for _, line := range strings.Split(text, "\n") {
		if matches := hunkHeaderPattern.FindStringSubmatch(line); matches != nil {
			hunk := DiffHunk{Header: line}
			var err error
			if hunk.OldStart, err = strconv.Atoi(matches[1]); err != nil {
				return nil, err
			}
			hunk.OldCount = parseHunkCount(matches[2])
			if hunk.NewStart, err = strconv.Atoi(matches[3]); err != nil {
				return nil, err
			}
			hunk.NewCount = parseHunkCount(matches[4])
			hunks = append(hunks, hunk)
			current = &hunks[len(hunks)-1]
			oldLine, newLine = hunk.OldStart, hunk.NewStart
			continue
		}
		if current == nil || line == "" {
			continue
		}
		diffLine := DiffLine{Text: line}
		switch line[0] {
		case '+':
			diffLine.Kind, diffLine.NewLine, diffLine.Text = DiffAddition, newLine, line[1:]
			newLine++
		case '-':
			diffLine.Kind, diffLine.OldLine, diffLine.Text = DiffDeletion, oldLine, line[1:]
			oldLine++
		case ' ':
			diffLine.Kind, diffLine.OldLine, diffLine.NewLine, diffLine.Text = DiffContext, oldLine, newLine, line[1:]
			oldLine++
			newLine++
		case '\\':
			diffLine.Kind = DiffMeta
		default:
			continue
		}
		current.Lines = append(current.Lines, diffLine)
	}
	return hunks, nil
}

func parseHunkCount(value string) int {
	if value == "" {
		return 1
	}
	count, _ := strconv.Atoi(value)
	return count
}

func untrackedHunks(data []byte) []DiffHunk {
	if len(data) == 0 || bytes.IndexByte(data, 0) >= 0 {
		return nil
	}
	text := strings.TrimSuffix(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	lines := strings.Split(text, "\n")
	hunk := DiffHunk{Header: fmt.Sprintf("@@ -0,0 +1,%d @@", len(lines)), NewStart: 1, NewCount: len(lines)}
	for index, line := range lines {
		hunk.Lines = append(hunk.Lines, DiffLine{Kind: DiffAddition, Text: line, NewLine: index + 1})
	}
	return []DiffHunk{hunk}
}

func contentLineCount(data []byte) int {
	if len(data) == 0 {
		return 0
	}
	count := bytes.Count(data, []byte{'\n'})
	if data[len(data)-1] != '\n' {
		count++
	}
	return count
}

func normalizeGitPath(path string) string {
	if path == "" {
		return ""
	}
	return filepath.ToSlash(filepath.Clean(filepath.FromSlash(path)))
}

func excludedChangePath(path string) bool {
	return path == ActiveSpecPattern
}
