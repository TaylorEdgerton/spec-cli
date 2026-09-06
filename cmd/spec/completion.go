package main

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// completionModel is the final human sign-off boundary. Merely entering or
// navigating this screen is read-only; the root archives only after the
// explicit Complete and archive Spec action is activated.
type completionModel struct {
	snap          reviewSnapshot
	cursor        int
	width, height int
	viewport      int
	help          bool
	nav           string
	status        string
}

func newCompletionModel(snapshot reviewSnapshot) *completionModel {
	return &completionModel{snap: snapshot}
}

func (m *completionModel) Init() tea.Cmd { return nil }

func (m *completionModel) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch message := message.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = message.Width, message.Height
	case tea.KeyPressMsg:
		switch message.Keystroke() {
		case "up", "k", "left", "shift+tab":
			m.cursor = wrap(m.cursor-1, 2)
		case "down", "j", "right", "tab":
			m.cursor = wrap(m.cursor+1, 2)
		case "enter":
			if m.cursor == 0 {
				m.nav = actionComplete
			} else {
				m.nav = actionChanges
			}
			return m, tea.Quit
		case "?":
			m.help = !m.help
		case "b", "esc":
			m.nav = actionBack
			return m, tea.Quit
		case "ctrl+c":
			m.nav = actionQuit
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m *completionModel) View() tea.View {
	width, height := defaultSize(m.width, m.height)
	header := uiSplit("Complete · Sign-off", fmt.Sprintf("%s · %s", m.snap.SpecID, shortSHA(m.snap.Baseline)), max(1, width-4))
	var body string
	if m.help {
		body = uiHelpOverlayWidth(m.hints(), max(20, width-8))
	} else {
		body = strings.Join(m.body(max(20, width-8)), "\n")
	}
	if m.status != "" {
		body += "\n" + uiLineStyle.Render(m.status)
	}
	body, m.viewport = uiViewportBody(body, uiWorkflowBodyHeight(height, header), m.viewport, m.selectedLabel())
	return tea.NewView(uiAppShell(width, height, header, body, uiKeyHints(m.hints(), "  ")))
}

func (m *completionModel) hints() [][2]string {
	return [][2]string{{"←/→", "choose"}, {"enter", "confirm"}, {"?", "help"}, {"b", "back"}, {"g", "home"}}
}

func (m *completionModel) body(width int) []string {
	stats := m.snap.Projection.Stats
	lines := []string{
		uiTitleStyle.Render("Original intent") + "  " + ansi.Truncate(emptyAs(strings.TrimSpace(m.snap.Intent), "No intent was recorded."), max(10, width-20), "…"),
		uiTitleStyle.Render("Final change") + fmt.Sprintf("  Files %d · Lines +%d/-%d · Reviewability %s", stats.Files, stats.Additions, stats.Deletions, strings.ToUpper(string(m.snap.Projection.Reviewability))),
	}
	if m.snap.Plan == nil {
		lines = append(lines, uiMutedStyle.Render("Implementation plan  No implementation plan was submitted."))
	} else {
		drift := m.snap.Projection.Drift
		lines = append(lines, fmt.Sprintf("Implementation plan  Matched %d · Additional %d · Untouched %d", drift.Matched, drift.Additional, drift.Untouched))
	}

	additional, needsAttention := 0, 0
	for _, item := range projectReviewAttention(m.snap) {
		if item.Kind == attentionNeutral {
			additional++
		} else {
			needsAttention++
		}
	}
	lines = append(lines,
		uiTitleStyle.Render("Review attention"),
		fmt.Sprintf("  Additional scope  %d item(s)", additional),
		fmt.Sprintf("  Needs attention   %d item(s)", needsAttention),
	)

	reviewed := 0
	for _, criterion := range m.snap.Criteria {
		if criterion.Checked {
			reviewed++
		}
	}
	lines = append(lines, uiTitleStyle.Render("Acceptance review")+fmt.Sprintf("  %d / %d reviewed", reviewed, len(m.snap.Criteria)))
	for _, criterion := range m.snap.Criteria {
		mark := "○"
		if criterion.Checked {
			mark = "✓"
		}
		lines = append(lines, "  "+mark+" "+ansi.Truncate(criterion.Text, max(10, width-6), "…"))
	}
	if len(m.snap.Evidence.Items) == 0 {
		lines = append(lines, uiTitleStyle.Render("Evidence")+"  "+uiMutedStyle.Render("No evidence recorded; explicit human acknowledgement is still available."))
	} else {
		lines = append(lines, uiTitleStyle.Render("Evidence")+"  "+m.evidenceSummary())
	}
	lines = append(lines, "", m.actionLine(0, "Complete and archive Spec"), m.actionLine(1, "Return to implementation"))
	return lines
}

func (m *completionModel) evidenceSummary() string {
	summary := m.snap.Evidence.Summary
	return fmt.Sprintf("Before %d · Reproduced %d · Added %d · Modified %d · Manual %d",
		summary.Existing, summary.FailThenPass, summary.NewTests, summary.ModifiedExisting, summary.Manual)
}

func (m *completionModel) actionLine(index int, label string) string {
	row := "  [ " + label + " ]"
	if m.cursor == index {
		return uiSelectedRow("> [ "+label+" ]", 0)
	}
	return row
}

func (m *completionModel) selectedLabel() string {
	if m.cursor == 0 {
		return "Complete and archive Spec"
	}
	return "Return to implementation"
}
