package tui

import "github.com/charmbracelet/lipgloss"

// Framehood brand palette — indigo accent (matches the logo) on a terminal that
// is usually dark. AdaptiveColor keeps text legible on light terminals too.
var (
	colAccent  = lipgloss.Color("#6c6ff2") // brand indigo (a touch brighter for terminals)
	colAccent2 = lipgloss.Color("#9aa0ff") // lighter indigo for keys/highlights
	colText    = lipgloss.AdaptiveColor{Light: "#1a1a22", Dark: "#e9e9ef"}
	colMuted   = lipgloss.Color("#8b8b97")
	colDim     = lipgloss.Color("#5b5b66")
	colGreen   = lipgloss.Color("#4cc38a")
	colRed     = lipgloss.Color("#f0697e")
	colBorder  = lipgloss.Color("#34343f")
	colInk     = lipgloss.Color("#0b0b10") // text on the accent fill

	styTitle = lipgloss.NewStyle().Foreground(colAccent).Bold(true)
	styAcc   = lipgloss.NewStyle().Foreground(colAccent)
	styText  = lipgloss.NewStyle().Foreground(colText)
	styMuted = lipgloss.NewStyle().Foreground(colMuted)
	styDim   = lipgloss.NewStyle().Foreground(colDim)
	styGreen = lipgloss.NewStyle().Foreground(colGreen)
	styRed   = lipgloss.NewStyle().Foreground(colRed)
	styKey   = lipgloss.NewStyle().Foreground(colAccent2)

	// Small section label (eyebrow).
	styEyebrow = lipgloss.NewStyle().Foreground(colDim).Bold(true)

	// Bordered content panel.
	styPanel = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colBorder).
			Padding(0, 1)

	// Focused panel (e.g. the composer while typing).
	styPanelActive = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colAccent).
			Padding(0, 1)

	// Active work-action chip in the header.
	styChipActive = lipgloss.NewStyle().
			Padding(0, 2).
			MarginRight(1).
			Foreground(colInk).
			Background(colAccent).
			Bold(true)

	styHelp = lipgloss.NewStyle().Foreground(colDim)
)
