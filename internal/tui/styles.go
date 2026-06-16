package tui

import "github.com/charmbracelet/lipgloss"

// Framehood palette — warm amber accent on a near-black canvas.
var (
	colAccent = lipgloss.Color("#E8A33D") // amber
	colText   = lipgloss.Color("#E8E8E8")
	colMuted  = lipgloss.Color("#7A7A7A")
	colGreen  = lipgloss.Color("#5FB87A")
	colRed    = lipgloss.Color("#E5616B")
	colBorder = lipgloss.Color("#333333")

	styTitle = lipgloss.NewStyle().Foreground(colAccent).Bold(true)
	styMuted = lipgloss.NewStyle().Foreground(colMuted)
	styText  = lipgloss.NewStyle().Foreground(colText)
	styGreen = lipgloss.NewStyle().Foreground(colGreen)
	styRed   = lipgloss.NewStyle().Foreground(colRed)

	styPanel = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colBorder).
			Padding(0, 1)

	styChip = lipgloss.NewStyle().
		Padding(0, 2).
		MarginRight(1).
		Foreground(colMuted).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colBorder)

	styChipActive = lipgloss.NewStyle().
			Padding(0, 2).
			MarginRight(1).
			Foreground(lipgloss.Color("#1A1A1A")).
			Background(colAccent).
			Bold(true).
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colAccent)

	styHelp = lipgloss.NewStyle().Foreground(colMuted)
)
