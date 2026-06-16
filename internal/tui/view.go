package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

func (m model) View() string {
	var b strings.Builder

	// Header: title + account + balance (or a signed-out hint).
	title := styTitle.Render("✦ Framehood")
	var right string
	if !m.loggedIn {
		right = styRed.Render("not signed in")
	} else {
		acct := m.email
		if acct == "" {
			acct = "signed in"
		}
		right = styMuted.Render(fmt.Sprintf("%s · %s", acct, m.balance))
	}
	b.WriteString(headerRow(title, right, m.width))
	b.WriteString("\n\n")

	// Type chips.
	var chips []string
	for i, k := range kinds {
		if i == m.kindIdx {
			chips = append(chips, styChipActive.Render(k))
		} else {
			chips = append(chips, styChip.Render(k))
		}
	}
	b.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, chips...))
	b.WriteString("\n\n")

	// Prompt input panel.
	inWidth := max(m.width-4, 20)
	b.WriteString(styPanel.Width(inWidth).Render(m.input.View()))
	b.WriteString("\n\n")

	// Status / result area.
	switch m.phase {
	case phaseWorking:
		st := m.status
		if st == "" {
			st = "working"
		}
		elapsed := time.Since(m.started).Round(time.Second)
		fmt.Fprintf(&b, "%s %s",
			m.spin.View(),
			styMuted.Render(fmt.Sprintf("%s · %s", st, elapsed)))
		if m.jobID != "" {
			b.WriteString(styMuted.Render("  (" + m.jobID + ")"))
		}
		b.WriteString("\n")
	case phaseDone:
		b.WriteString(styGreen.Render("✓ done"))
		b.WriteString("\n")
		if m.result != "" {
			b.WriteString(styText.Render(m.result))
			b.WriteString("\n")
			b.WriteString(styMuted.Render("press o to open in browser"))
			b.WriteString("\n")
		}
	case phaseError:
		b.WriteString(styRed.Render("✗ " + m.errMsg))
		b.WriteString("\n")
	}

	// History (most recent first, max 5).
	if len(m.history) > 0 {
		b.WriteString("\n")
		b.WriteString(styMuted.Render("Recent"))
		b.WriteString("\n")
		n := len(m.history)
		for i := n - 1; i >= 0 && i >= n-5; i-- {
			h := m.history[i]
			mark := styGreen.Render("•")
			if h.failed {
				mark = styRed.Render("•")
			}
			line := fmt.Sprintf("%s %s  %s", mark, styMuted.Render("["+h.kind+"]"), truncate(h.prompt, 48))
			b.WriteString(line + "\n")
		}
	}

	// Footer help.
	b.WriteString("\n")
	b.WriteString(styHelp.Render(helpText(m.phase)))
	return b.String()
}

func headerRow(left, right string, width int) string {
	if width <= 0 {
		return left + "  " + right
	}
	gap := max(width-lipgloss.Width(left)-lipgloss.Width(right), 1)
	return left + strings.Repeat(" ", gap) + right
}

func helpText(p phase) string {
	switch p {
	case phaseWorking:
		return "esc quit"
	case phaseDone:
		return "o open · ⇥ switch type · enter new · esc quit"
	default:
		return "⇥ switch type · enter generate · esc quit"
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
