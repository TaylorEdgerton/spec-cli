package main

import "charm.land/lipgloss/v2"

var (
	uiBlue    = lipgloss.Color("#67B7E1")
	uiPurple  = lipgloss.Color("#C58AF9")
	uiTeal    = lipgloss.Color("#6FD3C4")
	uiGreen   = lipgloss.Color("#89D185")
	uiYellow  = lipgloss.Color("#E8C547")
	uiMuted   = lipgloss.Color("#8393A7")
	uiBorder  = lipgloss.Color("#52647A")
	uiSurface = lipgloss.Color("#252B35")
	uiViolet  = lipgloss.Color("#4B3A75")

	uiTitleStyle    = lipgloss.NewStyle().Foreground(uiBlue).Bold(true)
	uiSymbolStyle   = lipgloss.NewStyle().Foreground(uiPurple)
	uiMutedStyle    = lipgloss.NewStyle().Foreground(uiMuted)
	uiSelectedStyle = lipgloss.NewStyle().Background(uiViolet).Foreground(lipgloss.BrightWhite).Bold(true)
	uiCurrentStyle  = lipgloss.NewStyle().Background(uiSurface)
	uiLineStyle     = lipgloss.NewStyle().Foreground(uiYellow).Bold(true)
	uiKeyStyle      = lipgloss.NewStyle().Foreground(uiBlue).Bold(true)
	uiEvidenceStyle = lipgloss.NewStyle().Foreground(uiGreen)

	// Lip Gloss has no dashed border preset, so keep the established runes here.
	uiDashedBorder = lipgloss.Border{
		Top: "┄", Bottom: "┄", Left: "┆", Right: "┆",
		TopLeft: "╭", TopRight: "╮", BottomLeft: "╰", BottomRight: "╯",
	}
)
