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
	verifyrun "github.com/TaylorEdgerton/spec-cli/internal/verify"
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

func BeginSetup(root, title string, now time.Time) (state.Setup, error) {
	workspace, err := state.Load(root)
	if err != nil {
		return state.Setup{}, err
	}
	path := ActivePath(root)
	if workspace.Active {
		if workspace.Setup != nil {
			return *workspace.Setup, nil
		}
		if _, statErr := os.Stat(path); statErr == nil {
			return state.Setup{}, fmt.Errorf("%s already exists; finish it with `spec done`", ActiveFilename)
		} else if !os.IsNotExist(statErr) {
			return state.Setup{}, statErr
		}
		if err := workspace.Abandon(); err != nil {
			return state.Setup{}, fmt.Errorf("clear abandoned change metadata: %w", err)
		}
	}
	if _, err := os.Stat(path); err == nil {
		return state.Setup{}, fmt.Errorf("%s already exists without active metadata; remove it before `spec new`", ActiveFilename)
	} else if !os.IsNotExist(err) {
		return state.Setup{}, err
	}
	base, err := gitutil.Head(root)
	if err != nil {
		return state.Setup{}, fmt.Errorf("a baseline commit is required; commit the current project before `spec new`")
	}
	setup := state.Setup{Stage: "change", Title: strings.TrimSpace(title)}
	if setup.Title != "" {
		setup.Stage = "outcome"
	}
	if err := workspace.BeginSetup(base, now.UTC(), setup); err != nil {
		return state.Setup{}, err
	}
	return setup, nil
}

func BeginEdit(root string) (state.Setup, error) {
	workspace, err := state.Load(root)
	if err != nil {
		return state.Setup{}, err
	}
	if !workspace.Active {
		return state.Setup{}, fmt.Errorf("no active change; run `spec new`")
	}
	if workspace.Setup != nil {
		return *workspace.Setup, nil
	}
	data, err := os.ReadFile(ActivePath(root))
	if err != nil {
		return state.Setup{}, err
	}
	setup, err := SetupFromMarkdown(string(data))
	if err != nil {
		return state.Setup{}, err
	}
	setup.Stage = "review"
	setup.Editing = true
	if err := workspace.BeginEdit(setup); err != nil {
		return state.Setup{}, err
	}
	return setup, nil
}

func CreateSetup(root string, setup state.Setup) (string, error) {
	workspace, err := state.Load(root)
	if err != nil {
		return "", err
	}
	if !workspace.Active || workspace.Setup == nil {
		return "", fmt.Errorf("no Spec setup is active; run `spec new`")
	}
	path := ActivePath(root)
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return "", err
	}
	_, writeErr := file.WriteString(RenderSetup(setup))
	closeErr := file.Close()
	if writeErr != nil {
		_ = os.Remove(path)
		return "", writeErr
	}
	if closeErr != nil {
		_ = os.Remove(path)
		return "", closeErr
	}
	if err := workspace.CompleteSetup(setup.Title); err != nil {
		return "", err
	}
	return path, nil
}

func SaveSetup(root string, setup state.Setup) (string, error) {
	if !setup.Editing {
		return CreateSetup(root, setup)
	}
	workspace, err := state.Load(root)
	if err != nil {
		return "", err
	}
	if !workspace.Active || workspace.Setup == nil {
		return "", fmt.Errorf("no Spec edit is active; run `spec`")
	}
	path := ActivePath(root)
	current, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "", fmt.Errorf("active specification was deleted during the CLI edit; run `spec` to recover it")
	}
	if err != nil {
		return "", err
	}
	updated, err := UpdateSetup(string(current), setup)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
		return "", err
	}
	if err := workspace.CompleteSetup(setup.Title); err != nil {
		return "", err
	}
	return path, nil
}

func SetupFromMarkdown(markdown string) (state.Setup, error) {
	title := documentTitle(markdown)
	if title == "" {
		title = sectionText(markdown, "Intent")
	}
	if title == "" {
		return state.Setup{}, fmt.Errorf("active specification has no title or Intent")
	}
	setup := state.Setup{
		Title:   title,
		Outcome: sectionText(markdown, "Scope"),
		Limits:  sectionText(markdown, "Constraints"),
		Editing: true,
	}
	for _, criterion := range AcceptanceCriteria(markdown) {
		setup.Criteria = append(setup.Criteria, state.SetupCriterion{Text: criterion.Text, Included: true})
	}
	return setup, nil
}

func UpdateSetup(markdown string, setup state.Setup) (string, error) {
	updated, err := updateDocumentTitle(markdown, setup.Title)
	if err != nil {
		return "", err
	}
	for _, section := range []struct {
		name string
		body string
	}{
		{"Intent", strings.TrimSpace(setup.Title)},
		{"Scope", strings.TrimSpace(setup.Outcome)},
		{"Constraints", listBody([]string{setup.Limits})},
		{"Acceptance Criteria", criteriaBody(setup.Criteria)},
	} {
		updated, err = updateSection(updated, section.name, section.body)
		if err != nil {
			return "", err
		}
	}
	return updated, nil
}

func RenderSetup(setup state.Setup) string {
	setup.Title = strings.TrimSpace(setup.Title)
	heading := setup.Title
	if heading == "" {
		heading = "Change"
	}
	var builder strings.Builder
	fmt.Fprintf(&builder, "# %s\n\n", heading)
	writeSection(&builder, "Intent", setup.Title)
	writeSection(&builder, "Scope", setup.Outcome)
	constraints := []string{setup.Limits}
	writeListSection(&builder, "Constraints", constraints)
	criteria := make([]string, 0, len(setup.Criteria))
	for _, criterion := range setup.Criteria {
		if criterion.Included {
			criteria = append(criteria, criterion.Text)
		}
	}
	writeTaskSection(&builder, "Acceptance Criteria", criteria)
	writeListSection(&builder, "Relevant Files", nil)
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
		if value = strings.TrimSpace(value); value != "" {
			fmt.Fprintf(builder, "- %s\n", value)
		}
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

func documentTitle(markdown string) string {
	for _, line := range strings.Split(markdown, "\n") {
		if strings.HasPrefix(line, "# ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "# "))
		}
	}
	return ""
}

func updateDocumentTitle(markdown, title string) (string, error) {
	lines := strings.SplitAfter(markdown, "\n")
	offset := 0
	for _, line := range lines {
		if strings.HasPrefix(line, "# ") {
			ending := ""
			if strings.HasSuffix(line, "\n") {
				ending = "\n"
			}
			return markdown[:offset] + "# " + strings.TrimSpace(title) + ending + markdown[offset+len(line):], nil
		}
		offset += len(line)
	}
	return "", fmt.Errorf("active specification has no title")
}

func sectionText(markdown, heading string) string {
	start, end := sectionBounds(markdown, heading)
	if start < 0 {
		return ""
	}
	var values []string
	for _, line := range strings.Split(markdown[start:end], "\n") {
		line = strings.TrimSpace(line)
		line = strings.TrimSpace(strings.TrimLeft(line, "-*"))
		if line != "" {
			values = append(values, line)
		}
	}
	return strings.Join(values, "; ")
}

func listBody(values []string) string {
	var builder strings.Builder
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			fmt.Fprintf(&builder, "- %s\n", value)
		}
	}
	return strings.TrimSuffix(builder.String(), "\n")
}

func criteriaBody(criteria []state.SetupCriterion) string {
	var builder strings.Builder
	for _, criterion := range criteria {
		if criterion.Included && strings.TrimSpace(criterion.Text) != "" {
			fmt.Fprintf(&builder, "- [ ] %s\n", strings.TrimSpace(criterion.Text))
		}
	}
	return strings.TrimSuffix(builder.String(), "\n")
}

func updateSection(markdown, heading, body string) (string, error) {
	start, end := sectionBounds(markdown, heading)
	if start < 0 {
		return "", fmt.Errorf("active specification has no %s section", heading)
	}
	replacement := "\n"
	if body = strings.TrimSpace(body); body != "" {
		replacement += body + "\n"
	}
	if end < len(markdown) {
		replacement += "\n"
	}
	return markdown[:start] + replacement + markdown[end:], nil
}

type Criterion struct {
	Text    string
	Checked bool
}

func AcceptanceCriteria(markdown string) []Criterion {
	start, end := sectionBounds(markdown, "Acceptance Criteria")
	if start < 0 {
		return nil
	}
	var criteria []Criterion
	for _, line := range strings.Split(markdown[start:end], "\n") {
		trimmed := strings.TrimSpace(line)
		lower := strings.ToLower(trimmed)
		switch {
		case strings.HasPrefix(lower, "- [x] "):
			criteria = append(criteria, Criterion{Text: strings.TrimSpace(trimmed[6:]), Checked: true})
		case strings.HasPrefix(lower, "- [ ] "):
			criteria = append(criteria, Criterion{Text: strings.TrimSpace(trimmed[6:])})
		case strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* "):
			criteria = append(criteria, Criterion{Text: strings.TrimSpace(trimmed[2:])})
		case trimmed != "":
			criteria = append(criteria, Criterion{Text: trimmed})
		}
	}
	return criteria
}

func UpdateAcceptanceCriteria(markdown string, criteria []Criterion) (string, error) {
	start, end := sectionBounds(markdown, "Acceptance Criteria")
	if start < 0 {
		return "", fmt.Errorf("active specification has no Acceptance Criteria section")
	}
	var replacement strings.Builder
	replacement.WriteByte('\n')
	for _, criterion := range criteria {
		text := strings.TrimSpace(criterion.Text)
		if text == "" {
			continue
		}
		mark := " "
		if criterion.Checked {
			mark = "x"
		}
		fmt.Fprintf(&replacement, "- [%s] %s\n", mark, text)
	}
	if end < len(markdown) && !strings.HasSuffix(replacement.String(), "\n\n") {
		replacement.WriteByte('\n')
	}
	return markdown[:start] + replacement.String() + markdown[end:], nil
}

func sectionBounds(markdown, heading string) (int, int) {
	lines := strings.SplitAfter(markdown, "\n")
	offset, start := 0, -1
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "## ") {
			name := strings.TrimSpace(strings.TrimPrefix(trimmed, "## "))
			if start >= 0 {
				return start, offset
			}
			if strings.EqualFold(name, heading) {
				start = offset + len(line)
			}
		}
		offset += len(line)
	}
	if start >= 0 {
		return start, len(markdown)
	}
	return -1, -1
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
	criteria := AcceptanceCriteria(string(current))
	for _, criterion := range criteria {
		if !criterion.Checked {
			return state.History{}, fmt.Errorf("acceptance criteria are not fully reviewed; run `spec done`")
		}
	}
	verification, err := workspace.Verification()
	if err != nil {
		return state.History{}, err
	}
	currentVerification, err := verifyrun.Current(root, verification)
	if err != nil {
		return state.History{}, err
	}
	if !currentVerification {
		return state.History{}, fmt.Errorf("verification is not current and passing; run `spec verify`")
	}
	files, err := gitutil.ChangedFiles(root, workspace.BaseSHA)
	if err != nil {
		return state.History{}, err
	}
	files = withoutActiveSpec(files)
	end, _ := gitutil.Head(root)
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
