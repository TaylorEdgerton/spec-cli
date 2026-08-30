package main

import (
	"fmt"
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// uiColumns lays two blocks side by side at a fixed left width without allowing
// a long left row to soft-wrap and shift the surrounding layout.
func uiColumns(left string, leftWidth int, right string) string {
	leftLines, rightLines := strings.Split(left, "\n"), strings.Split(right, "\n")
	rows := make([]string, max(len(leftLines), len(rightLines)))
	for index := range rows {
		text := ""
		if index < len(leftLines) {
			text = ansi.Truncate(leftLines[index], leftWidth, "…")
		}
		rows[index] = text + strings.Repeat(" ", max(0, leftWidth-lipgloss.Width(text)))
		if index < len(rightLines) {
			rows[index] += " " + rightLines[index]
		}
	}
	return strings.Join(rows, "\n")
}

func uiSplit(left, right string, width int) string {
	width = max(1, width)
	right = ansi.Truncate(right, max(1, width/2), "…")
	left = ansi.Truncate(left, max(1, width-lipgloss.Width(right)-1), "…")
	return left + strings.Repeat(" ", max(1, width-lipgloss.Width(left)-lipgloss.Width(right))) + right
}

func uiPanel(width, height int, border color.Color, title, right, body string) string {
	return uiPanelWith(lipgloss.RoundedBorder(), width, height, border, title, right, body)
}

// uiPanelWith draws a bordered panel whose title and right-hand label sit in
// the top border rule, which Lip Gloss has no primitive for.
func uiPanelWith(runes lipgloss.Border, width, height int, border color.Color, title, right, body string) string {
	rendered := lipgloss.NewStyle().
		Border(runes).
		BorderForeground(border).
		Padding(0, 1).
		Width(max(1, width)).
		Height(max(1, height)).
		MaxWidth(max(1, width)).
		Render(body)
	if title == "" && right == "" {
		return rendered
	}
	lines := strings.Split(rendered, "\n")
	inner := max(2, lipgloss.Width(lines[0])-2)
	rule := lipgloss.NewStyle().Foreground(border)
	if title != "" {
		title = " " + title + " "
	}
	if right != "" {
		right = " " + right + " "
	}
	if lipgloss.Width(title)+lipgloss.Width(right)+2 > inner {
		right = ""
	}
	if lipgloss.Width(title)+2 > inner {
		title = ansi.Truncate(title, max(0, inner-2), "…")
	}
	gap := max(0, inner-lipgloss.Width(title)-lipgloss.Width(right)-1)
	lines[0] = rule.Render(runes.TopLeft+runes.Top) + title +
		rule.Render(strings.Repeat(runes.Top, gap)) + right + rule.Render(runes.TopRight)
	return strings.Join(lines, "\n")
}

func uiBodyHeight(height int) int { return max(3, height-8) }

func uiViewportBody(body string, height, offset int, anchor string) (string, int) {
	lines := strings.Split(body, "\n")
	selectedLine := -1
	if anchor != "" {
		for index, line := range lines {
			if strings.Contains(ansi.Strip(line), anchor) {
				selectedLine = index
				break
			}
		}
	}
	visible, next := (screenViewport{Height: height, Offset: offset}).visible(lines, selectedLine)
	return strings.Join(visible, "\n"), next
}

func uiWorkflowBodyHeight(height int, header string) int {
	return max(3, height-len(strings.Split(header, "\n"))-7)
}

func defaultSize(width, height int) (int, int) {
	if width <= 0 {
		width = 100
	}
	if height <= 0 {
		height = 32
	}
	return width, height
}

func uiAppShell(width, height int, header, body, footer string) string {
	width, height = max(1, width), max(1, height)
	if height < 9 {
		return strings.Join([]string{
			ansi.Truncate(header, width, "…"),
			ansi.Truncate(body, width, "…"),
			ansi.Truncate(footer, width, "…"),
		}, "\n")
	}
	bodyHeight := uiBodyHeight(height)
	// A body line wider than the panel soft-wraps and pushes the shell past the
	// terminal height, so cut every line to the panel's inner width first.
	bodyLines := strings.Split(body, "\n")
	for index, line := range bodyLines {
		bodyLines[index] = ansi.Truncate(line, max(1, width-4), "…")
	}
	body = strings.Join(bodyLines, "\n")
	headerLines := strings.Split(header, "\n")
	for index, line := range headerLines {
		headerLines[index] = ansi.Truncate(line, max(1, width-4), "…")
	}
	header = strings.Join(headerLines, "\n")
	footer = ansi.Truncate(footer, max(1, width-4), "…")
	headerHeight := max(3, len(headerLines)+2)
	bodyHeight = max(3, height-headerHeight-5)
	return lipgloss.JoinVertical(lipgloss.Left,
		uiPanel(width, headerHeight, uiBlue, "", "", uiTitleStyle.Render(header)),
		uiPanel(width, bodyHeight, uiBorder, "", "", body),
		uiPanel(width, 3, uiBorder, "", "", footer),
	)
}

func uiMinimumSize(width, height int) string {
	return uiAppShell(width, height, "Terminal is too small",
		fmt.Sprintf("Spec needs at least %dx%d; this terminal is %dx%d.", homeMinWidth, homeMinHeight, width, height),
		"Resize the terminal to continue.")
}

func uiEmptyState(title, reason string) string {
	var lines []string
	if strings.TrimSpace(title) != "" {
		lines = append(lines, uiTitleStyle.Render(title))
	}
	if strings.TrimSpace(reason) == "" {
		reason = "Nothing to show."
	}
	lines = append(lines, uiMutedStyle.Render(reason))
	return strings.Join(lines, "\n")
}

func uiHelpOverlay(pairs [][2]string) string {
	lines := []string{uiTitleStyle.Render("Keys"), ""}
	for _, pair := range pairs {
		lines = append(lines, "  "+uiKeyStyle.Render(pair[0])+"  "+uiMutedStyle.Render(pair[1]))
	}
	return strings.Join(append(lines, "", uiMutedStyle.Render("  ? closes this help.")), "\n")
}

func uiKeyHints(pairs [][2]string, separator string) string {
	hints := make([]string, 0, len(pairs))
	for _, pair := range pairs {
		hints = append(hints, uiKeyStyle.Render(pair[0])+" "+uiMutedStyle.Render(pair[1]))
	}
	return strings.Join(hints, separator)
}

func uiTabs(labels []string, active int) string {
	tabs := make([]string, len(labels))
	for index, label := range labels {
		if index == active {
			tabs[index] = uiTitleStyle.Render("[" + label + "]")
		} else {
			tabs[index] = uiMutedStyle.Render(label)
		}
	}
	return strings.Join(tabs, "   ")
}

func uiBadge(label string, foreground color.Color) string {
	return lipgloss.NewStyle().Background(uiSurface).Foreground(foreground).Bold(true).Render(" " + label + " ")
}

func uiSelectedRow(text string, width int) string {
	style := uiSelectedStyle
	if width > 0 {
		style = style.Width(width)
	}
	return style.Render(text)
}

func uiCodeLine(number int, current bool, code string, width int) string {
	marker := " "
	numberText := fmt.Sprintf("%d", max(1, number))
	if current {
		marker = uiLineStyle.Render("›")
		numberText = uiLineStyle.Render(numberText)
	} else {
		numberText = uiMutedStyle.Render(numberText)
	}
	gutter := marker + " " + numberText + " " + uiMutedStyle.Render("│") + " "
	line := gutter + ansi.Truncate(code, max(1, width-lipgloss.Width(gutter)), "…")
	if current {
		line = uiCurrentStyle.Width(max(1, width)).Render(line)
	}
	return line
}
