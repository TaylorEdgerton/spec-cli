package change

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/TaylorEdgerton/spec-cli/internal/aiusage"
	"github.com/TaylorEdgerton/spec-cli/internal/gitutil"
	"github.com/TaylorEdgerton/spec-cli/internal/state"
)

const ActiveFilename = ".spec.md"

type GuidedSpec struct {
	Change              string
	Reason              string
	Outcome             string
	MustNotBreak        string
	ImportantConstraint string
	RelevantFiles       []string
	AcceptanceCriteria  []string
}

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

func Populate(root string, guided GuidedSpec) error {
	workspace, err := state.Load(root)
	if err != nil {
		return err
	}
	if !workspace.Active {
		return fmt.Errorf("no active change; run `spec new`")
	}
	path := ActivePath(root)
	if _, err := os.Stat(path); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(guidedMarkdown(guided)), 0o644); err != nil {
		return err
	}
	return workspace.UpdateTitle(guided.Change)
}

func AcceptanceCriteriaPrompt() string {
	return "Read and follow `.spec.md`.\n" +
		"Suggest concise, measurable acceptance criteria for the active change.\n" +
		"Update only the `## Acceptance Criteria` section in `.spec.md`.\n" +
		"Write each criterion as an unchecked Markdown task item (`- [ ]`).\n" +
		"Do not change application code, tests, dependencies, or configuration.\n"
}

func guidedMarkdown(guided GuidedSpec) string {
	guided.Change = strings.TrimSpace(guided.Change)
	heading := guided.Change
	if heading == "" {
		heading = "Change"
	}
	var builder strings.Builder
	fmt.Fprintf(&builder, "# %s\n\n", heading)
	writeSection(&builder, "Intent", firstNonEmpty(guided.Reason, guided.Change))
	writeSection(&builder, "Scope", guided.Change)
	constraints := []string{}
	if value := strings.TrimSpace(guided.MustNotBreak); value != "" {
		constraints = append(constraints, "Must not break: "+value)
	}
	if value := strings.TrimSpace(guided.ImportantConstraint); value != "" {
		constraints = append(constraints, value)
	}
	writeListSection(&builder, "Constraints", constraints)
	criteria := guided.AcceptanceCriteria
	if len(criteria) == 0 {
		if value := strings.TrimSpace(guided.Outcome); value != "" {
			criteria = []string{value}
		}
	}
	writeTaskSection(&builder, "Acceptance Criteria", criteria)
	files := make([]string, 0, len(guided.RelevantFiles))
	for _, file := range guided.RelevantFiles {
		if file = strings.TrimSpace(file); file != "" {
			files = append(files, "`"+file+"`")
		}
	}
	writeListSection(&builder, "Relevant Files", files)
	builder.WriteString("## Notes\n")
	return builder.String()
}

func writeSection(builder *strings.Builder, heading, value string) {
	fmt.Fprintf(builder, "## %s\n\n", heading)
	if value = strings.TrimSpace(value); value != "" {
		builder.WriteString(value)
		builder.WriteString("\n")
	}
	builder.WriteString("\n")
}

func writeListSection(builder *strings.Builder, heading string, values []string) {
	fmt.Fprintf(builder, "## %s\n\n", heading)
	for _, value := range values {
		fmt.Fprintf(builder, "- %s\n", value)
	}
	if len(values) > 0 {
		builder.WriteString("\n")
	}
}

func writeTaskSection(builder *strings.Builder, heading string, values []string) {
	fmt.Fprintf(builder, "## %s\n\n", heading)
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			fmt.Fprintf(builder, "- [ ] %s\n", value)
		}
	}
	if len(values) > 0 {
		builder.WriteString("\n")
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func Done(root, summary string, now time.Time) (state.History, error) {
	return DoneWithUsage(root, summary, now, nil)
}

func DoneWithUsage(root, summary string, now time.Time, usage *aiusage.Summary) (state.History, error) {
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
		Verification: historyVerification, Summary: strings.TrimSpace(summary), AIUsage: usage,
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
