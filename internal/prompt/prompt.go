package prompt

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/TaylorEdgerton/spec-cli/internal/change"
	"github.com/TaylorEdgerton/spec-cli/internal/discovery"
	"github.com/TaylorEdgerton/spec-cli/internal/gitutil"
	"github.com/TaylorEdgerton/spec-cli/internal/state"
)

const (
	maxFileBytes       = 64 * 1024
	maxFileTotal       = 128 * 1024
	maxFiles           = 12
	maxDiffBytes       = 64 * 1024
	maxDiscoveredFiles = 5
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

type Kind string

const (
	Implementation Kind = "implementation"
	Plan           Kind = "plan"
)

type contextFile struct {
	path       string
	reasons    []string
	symbols    []discovery.Symbol
	discovered bool
}

var findContext = discovery.Find

func Build(root string, includeFiles bool) (string, Info, error) {
	return BuildKind(root, includeFiles, Implementation)
}

func BuildKind(root string, includeFiles bool, kind Kind) (string, Info, error) {
	if kind != Implementation && kind != Plan {
		return "", Info{}, fmt.Errorf("unknown prompt kind %q", kind)
	}
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
	if kind == Plan {
		builder.WriteString("Investigate and plan only. Do not implement or modify repository files.\n\n")
	}
	builder.WriteString("Read and follow `.spec.md`.\n\n")
	builder.WriteString("Use the current Git workspace and stay within the defined scope.\n")
	builder.WriteString("Run or report verification only through the configured verification workflow.\n")
	builder.WriteString("Prefer existing project conventions.\n")
	builder.WriteString("Make the smallest understandable change.\n")
	builder.WriteString("Do not claim verification was run unless it actually was.\n")
	builder.WriteString("Surface conflicting requirements rather than guessing.\n\n")
	if setup, setupErr := change.SetupFromMarkdown(current); setupErr == nil {
		builder.WriteString("## Change contract\n\n")
		builder.WriteString("Intent: ")
		builder.WriteString(setup.Title)
		builder.WriteByte('\n')
		if strings.TrimSpace(setup.Outcome) != "" {
			builder.WriteString("Scope / expected behaviour: ")
			builder.WriteString(setup.Outcome)
			builder.WriteByte('\n')
		}
		if len(setup.Criteria) > 0 {
			builder.WriteString("Acceptance criteria:\n")
			for _, criterion := range setup.Criteria {
				if criterion.Included && strings.TrimSpace(criterion.Text) != "" {
					builder.WriteString("- ")
					builder.WriteString(criterion.Text)
					builder.WriteByte('\n')
				}
			}
		}
		builder.WriteByte('\n')
	}
	builder.WriteString("Discovery context below is advisory and may be absent.\n\n")
	if kind == Plan {
		writePlanInstructions(&builder)
		fmt.Fprintf(&builder, "\nStarting state: commit %s. Existing work may predate this Spec.\n", workspace.BaseSHA)
		stored, err := workspace.Plan()
		if err != nil {
			return "", Info{}, err
		}
		if stored != nil {
			current, err := json.MarshalIndent(stored.Plan, "", "  ")
			if err != nil {
				return "", Info{}, err
			}
			builder.WriteString("\n## Current AI plan\n\nPropose a complete replacement, including unchanged items. Explain the correction or scope change in the summary. Spec saves an edit before repository changes, or an amendment preserving the original after changes.\n\n```json\n")
			builder.Write(current)
			builder.WriteString("\n```\n")
		}
	} else if err := writeImplementationInstructions(&builder, workspace); err != nil {
		return "", Info{}, err
	}
	info := Info{}
	files := promptFiles(root, current)

	totalFileBytes := 0
	wroteList := ""
	for _, context := range files {
		relative := context.path
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
			section := "Relevant files"
			if context.discovered {
				section = "Likely change area"
			}
			if wroteList != section {
				builder.WriteString("\n## ")
				builder.WriteString(section)
				builder.WriteByte('\n')
				wroteList = section
			}
			builder.WriteString("\n- `")
			builder.WriteString(relative)
			builder.WriteString("`\n")
			for _, reason := range context.reasons {
				builder.WriteString("  - ")
				builder.WriteString(reason)
				builder.WriteByte('\n')
			}
			for _, symbol := range context.symbols {
				builder.WriteString("  - `")
				builder.WriteString(symbol.Name)
				fmt.Fprintf(&builder, "` at line %d\n", symbol.Line)
				for _, reason := range symbol.Reasons {
					builder.WriteString("    - ")
					builder.WriteString(reason)
					builder.WriteByte('\n')
				}
				for _, related := range symbol.Related {
					builder.WriteString("    - ")
					builder.WriteString(related.Relation)
					fmt.Fprintf(&builder, " at line %d\n", related.Line)
				}
			}
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
		builder.WriteByte('\n')
		if len(context.reasons) > 0 {
			builder.WriteString("\nDiscovery: ")
			builder.WriteString(strings.Join(context.reasons, "; "))
			builder.WriteByte('\n')
		}
		if len(context.symbols) > 0 {
			builder.WriteString("Symbols: ")
			for index, symbol := range context.symbols {
				if index > 0 {
					builder.WriteString(", ")
				}
				fmt.Fprintf(&builder, "%s (line %d)", symbol.Name, symbol.Line)
			}
			builder.WriteByte('\n')
		}
		builder.WriteString("\n```text\n")
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

const changePlanExample = `{
  "summary": "short implementation summary",
  "files": [{"path": "repository/relative/path", "action": "modify", "reason": "why"}],
  "integration_points": [{"existing_symbol": "symbol", "planned_change": "change", "relationship": "relationship"}],
  "verification": [{"behaviour": "expected behaviour", "likely_location": "optional path"}],
  "uncertainties": ["open question"]
}`

func writePlanInstructions(builder *strings.Builder) {
	builder.WriteString("## Planning task\n\n")
	builder.WriteString("Your task is to investigate the repository and propose the smallest implementation plan that satisfies the change contract. Do not implement the change or modify repository files.\n")
	builder.WriteString("Allowed file actions are `create`, `modify`, and `delete`. Use repository-relative paths and identify existing integration relationships and verification behaviours.\n\n")
	builder.WriteString("## ChangePlan format\n\nSubmit one complete JSON object, not a partial patch. `summary` is a required non-empty string. Optional arrays: `files` (path, action, optional reason); `integration_points` (existing_symbol, planned_change, relationship); `verification` (behaviour, optional likely_location); `uncertainties` (strings). Use only these fields. Empty arrays are allowed. Paths must stay inside the repository; absolute paths and .. escapes are rejected. Paths are cleaned and backslashes normalized to slashes; duplicate normalized paths are rejected. Text is trimmed. Describe verification behaviours and reuse existing project components and styling.\n\n")
	builder.WriteString("Choose exactly one return route. Run the command in this Spec's repository; do not edit Spec's storage files directly.\n\n")
	builder.WriteString("If you can run terminal commands, submit the plan directly with this safe stdin form:\n\n")
	builder.WriteString("spec plan submit --stdin <<'SPEC_PLAN'\n")
	builder.WriteString(changePlanExample)
	builder.WriteString("\nSPEC_PLAN\n\n")
	builder.WriteString("After successful submission, report success briefly to the human; do not also emit a spec-plan block. If submission fails, correct the reported validation error and retry. Never claim success after a failed command.\n\n")
	builder.WriteString("If you cannot run that command, return exactly one fenced `spec-plan` JSON block and no second plan block:\n\n")
	builder.WriteString("```spec-plan\n")
	builder.WriteString(changePlanExample)
	builder.WriteString("\n```\n\n")
	builder.WriteString("The opening fence must be three backticks followed immediately by spec-plan; put JSON on the next line and close with three backticks on their own line. The human copies your complete response, returns to Implement → AI Plan, and selects Paste AI plan. They paste your response, not this planning prompt.\n\n")
	builder.WriteString("Stop after the plan is submitted or returned. Wait for human approval before implementation.\n")
}

func writeImplementationInstructions(builder *strings.Builder, workspace state.Workspace) error {
	builder.WriteString("## Implementation task\n\n")
	stored, err := workspace.Plan()
	if err != nil {
		return err
	}
	if stored == nil {
		builder.WriteString("No accepted ChangePlan is present; implement the change directly from the change contract and repository evidence. Planning is optional, so do not manufacture or claim an accepted plan.\n\n")
		return nil
	}
	canonical, err := json.MarshalIndent(stored.Plan, "", "  ")
	if err != nil {
		return err
	}
	builder.WriteString("Use the accepted plan below as implementation guidance. Reconcile it with the current repository and surface necessary plan drift.\n\n")
	builder.WriteString("## Accepted ChangePlan\n\n```json\n")
	builder.Write(canonical)
	builder.WriteString("\n```\n\n")
	return nil
}

func promptFiles(root, markdown string) []contextFile {
	var files []contextFile
	seen := make(map[string]bool)
	for _, path := range relevantFiles(markdown) {
		key := filepath.ToSlash(filepath.Clean(path))
		if !seen[key] {
			seen[key] = true
			files = append(files, contextFile{path: path})
		}
	}
	setup, err := change.SetupFromMarkdown(markdown)
	if err != nil {
		return files
	}
	query := discovery.Query{Intent: setup.Title, Outcome: setup.Outcome}
	for _, criterion := range setup.Criteria {
		if criterion.Included && strings.TrimSpace(criterion.Text) != "" {
			query.Criteria = append(query.Criteria, criterion.Text)
		}
	}
	results, err := findContext(root, query)
	if err != nil {
		return files
	}
	discovered := 0
	for _, result := range results {
		key := filepath.ToSlash(filepath.Clean(result.Path))
		if seen[key] {
			continue
		}
		seen[key] = true
		files = append(files, contextFile{
			path: result.Path, reasons: result.Reasons, symbols: result.Symbols, discovered: true,
		})
		discovered++
		if discovered == maxDiscoveredFiles {
			break
		}
	}
	return files
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
