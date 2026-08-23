package prompt

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/TaylorEdgerton/spec-cli/internal/change"
	"github.com/TaylorEdgerton/spec-cli/internal/gitutil"
	"github.com/TaylorEdgerton/spec-cli/internal/state"
)

const (
	maxFileBytes = 64 * 1024
	maxFileTotal = 128 * 1024
	maxFiles     = 12
	maxDiffBytes = 64 * 1024
)

var headingPattern = regexp.MustCompile(`(?m)^##\s+(.+?)\s*$`)

type Info struct {
	Files          []string
	MissingFiles   []string
	IncludedDiff   bool
	DiffTruncated  bool
	IncludedError  bool
	FilesTruncated bool
	ApproxTokens   int
}

func Build(root string, includeFiles bool) (string, Info, error) {
	workspace, err := state.Load(root)
	if err != nil {
		return "", Info{}, err
	}
	if !workspace.Active {
		return "", Info{}, fmt.Errorf("no active change; run `spec new`")
	}
	currentData, err := os.ReadFile(change.ActivePath(root))
	if os.IsNotExist(err) {
		return "", Info{}, fmt.Errorf("active specification is missing: %s", change.ActivePath(root))
	}
	if err != nil {
		return "", Info{}, err
	}
	current := string(currentData)
	var builder strings.Builder
	builder.WriteString("Read and follow `.spec.md`.\n\n")
	builder.WriteString("Use the current Git workspace and stay within the defined scope.\n")
	builder.WriteString("Run or report verification only through the configured verification workflow.\n")
	builder.WriteString("Prefer existing project conventions.\n")
	builder.WriteString("Make the smallest understandable change.\n")
	builder.WriteString("Do not claim verification was run unless it actually was.\n")
	builder.WriteString("Surface conflicting requirements rather than guessing.\n\n")
	info := Info{}

	totalFileBytes := 0
	wroteFileList := false
	for _, relative := range relevantFiles(current) {
		if filepath.ToSlash(filepath.Clean(relative)) == change.ActiveFilename {
			continue
		}
		if len(info.Files) >= maxFiles || totalFileBytes >= maxFileTotal {
			info.FilesTruncated = true
			break
		}
		path, ok := safePath(root, relative)
		if !ok {
			info.MissingFiles = append(info.MissingFiles, relative)
			continue
		}
		if !includeFiles {
			fileInfo, statErr := os.Stat(path)
			if statErr != nil || fileInfo.IsDir() {
				info.MissingFiles = append(info.MissingFiles, relative)
				continue
			}
			info.Files = append(info.Files, relative)
			if !wroteFileList {
				builder.WriteString("\n## Relevant files\n")
				wroteFileList = true
			}
			builder.WriteString("\n- `")
			builder.WriteString(relative)
			builder.WriteString("`\n")
			continue
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			info.MissingFiles = append(info.MissingFiles, relative)
			continue
		}
		info.Files = append(info.Files, relative)
		remaining := maxFileTotal - totalFileBytes
		limitSize := maxFileBytes
		if remaining < limitSize {
			limitSize = remaining
		}
		if len(data) > limitSize {
			data = append(data[:limitSize], []byte("\n[file truncated]\n")...)
			info.FilesTruncated = true
		}
		totalFileBytes += len(data)
		builder.WriteString("\n## Relevant file: ")
		builder.WriteString(relative)
		builder.WriteString("\n\n```text\n")
		builder.Write(data)
		if len(data) == 0 || data[len(data)-1] != '\n' {
			builder.WriteByte('\n')
		}
		builder.WriteString("```\n")
	}

	if includeFiles {
		diff, truncated, err := gitutil.Diff(root, workspace.BaseSHA, maxDiffBytes)
		if err != nil {
			return "", Info{}, err
		}
		if diff != "" {
			info.IncludedDiff, info.DiffTruncated = true, truncated
			builder.WriteString("\n## Current Git diff\n\n```diff\n")
			builder.WriteString(diff)
			builder.WriteString("\n```\n")
		}
	} else if workspace.BaseSHA != "" {
		builder.WriteString("\nInspect changes since the Spec began with `git diff ")
		builder.WriteString(workspace.BaseSHA)
		builder.WriteString(" --`.\n")
	}
	verification, err := workspace.Verification()
	if err != nil {
		return "", Info{}, err
	}
	if verification != nil && !verification.Passed {
		if includeFiles {
			info.IncludedError = true
			builder.WriteString("\n## Latest verification failure\n\n```text\n")
			builder.WriteString(limit(verification.Output, 32*1024))
			builder.WriteString("\n```\n")
		} else {
			builder.WriteString("\nThe latest verification failed. Run `spec verify` to reproduce it.\n")
		}
	}
	result := builder.String()
	info.ApproxTokens = (len(result) + 3) / 4
	if err := workspace.SavePrompt(result); err != nil {
		return "", Info{}, err
	}
	return result, info, nil
}

func relevantFiles(markdown string) []string {
	matches := headingPattern.FindAllStringSubmatchIndex(markdown, -1)
	for index, match := range matches {
		if !strings.EqualFold(strings.TrimSpace(markdown[match[2]:match[3]]), "Relevant Files") {
			continue
		}
		start, end := match[1], len(markdown)
		if index+1 < len(matches) {
			end = matches[index+1][0]
		}
		var files []string
		for _, line := range strings.Split(markdown[start:end], "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "* ") {
				file := strings.Trim(strings.TrimSpace(line[2:]), "`")
				if file != "" {
					files = append(files, file)
				}
			}
		}
		return files
	}
	return nil
}

func safePath(root, relative string) (string, bool) {
	if filepath.IsAbs(relative) {
		return "", false
	}
	clean := filepath.Clean(relative)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", false
	}
	path := filepath.Join(root, clean)
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", false
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", false
	}
	rel, err := filepath.Rel(resolvedRoot, resolved)
	return resolved, err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func limit(value string, size int) string {
	if len(value) <= size {
		return value
	}
	return value[:size] + "\n[output truncated]"
}

func FormatInfo(info Info) string {
	parts := []string{fmt.Sprintf("approximate tokens: %d", info.ApproxTokens)}
	parts = append(parts, fmt.Sprintf("relevant files: %d", len(info.Files)))
	if info.IncludedDiff {
		parts = append(parts, "Git diff: included")
	}
	if info.IncludedError {
		parts = append(parts, "verification failure: included")
	}
	if info.FilesTruncated {
		parts = append(parts, "relevant file content: truncated")
	}
	if info.DiffTruncated {
		parts = append(parts, "Git diff: truncated")
	}
	if len(info.MissingFiles) > 0 {
		parts = append(parts, "unavailable files: "+strings.Join(info.MissingFiles, ", "))
	}
	return strings.Join(parts, "\n")
}
