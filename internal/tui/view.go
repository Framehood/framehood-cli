package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

// contentWidth clamps the studio to a comfortable reading width.
func (m model) contentWidth() int {
	w := m.width
	if w <= 0 {
		w = 74
	}
	w -= 2 // outer margin
	if w > 84 {
		w = 84
	}
	if w < 36 {
		w = 36
	}
	return w
}

func (m model) View() string {
	w := m.contentWidth()
	var sections []string

	sections = append(sections, m.headerView(w))
	sections = append(sections, m.kindsView())
	sections = append(sections, m.composerView(w))

	if body := m.statusView(w); body != "" {
		sections = append(sections, body)
	}
	if h := m.historyView(); h != "" {
		sections = append(sections, h)
	}
	hc := helpContext{
		keys:      m.keys,
		focus:     m.focus,
		working:   m.phase == phaseWorking,
		hasResult: m.phase == phaseDone && m.result != "",
	}
	sections = append(sections, "\n"+m.help.View(hc))

	out := lipgloss.JoinVertical(lipgloss.Left, sections...)
	return lipgloss.NewStyle().Padding(1, 1).Render(out)
}

// Header: title on the left, account/balance (or signed-out) on the right, then a rule.
func (m model) headerView(w int) string {
	left := styTitle.Render("✦ Framehood") + styDim.Render(" studio")
	var right string
	if !m.loggedIn {
		right = styRed.Render("● not signed in")
	} else {
		acct := m.email
		if acct == "" {
			acct = "signed in"
		}
		right = styMuted.Render(acct) + styDim.Render("  ·  ") + styAcc.Render(m.balance)
	}
	gap := w - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		gap = 1
	}
	bar := left + strings.Repeat(" ", gap) + right
	rule := styDim.Render(strings.Repeat("─", w))
	return bar + "\n" + rule
}

// Type selector chips. A leading marker shows when this zone has focus.
func (m model) kindsView() string {
	chips := make([]string, len(kinds))
	for i, k := range kinds {
		if i == m.kindIdx {
			chips[i] = styChipActive.Render(k)
		} else {
			chips[i] = styChip.Render(k)
		}
	}
	marker := styDim.Render("  ")
	if m.focus == zoneTabs {
		marker = styAcc.Render("▸ ")
	}
	return "\n" + marker + lipgloss.JoinHorizontal(lipgloss.Top, chips...)
}

// Composer: eyebrow label + bordered prompt input. The accent border lights up
// only while the input zone is focused (and not mid-job).
func (m model) composerView(w int) string {
	label := styEyebrow.Render("DESCRIBE YOUR SHOT")
	box := styPanel
	if m.focus == zoneInput && m.phase != phaseWorking {
		box = styPanelActive
	}
	field := box.Width(w - 4).Render(m.input.View())
	return "\n" + label + "\n" + field
}

// Status: working spinner, done result panel, or an error line.
func (m model) statusView(w int) string {
	switch m.phase {
	case phaseWorking:
		st := m.status
		if st == "" {
			st = "working"
		}
		elapsed := time.Since(m.started).Round(time.Second)
		line := fmt.Sprintf("%s %s %s",
			styAcc.Render(m.spin.View()),
			styText.Render(st),
			styDim.Render("· "+fmtDur(elapsed)))
		if m.jobID != "" {
			line += styDim.Render(" · " + m.jobID)
		}
		return "\n" + line

	case phaseDone:
		head := styGreen.Render("✓ done")
		lines := []string{head}
		if m.result != "" {
			lines = append(lines, styText.Render(m.result))
			lines = append(lines, styDim.Render("press o to open in your browser"))
		}
		inner := lipgloss.JoinVertical(lipgloss.Left, lines...)
		return "\n" + styEyebrow.Render("RESULT") + "\n" +
			styPanel.BorderForeground(colGreen).Width(w-4).Render(inner)

	case phaseError:
		return "\n" + styPanel.BorderForeground(colRed).Width(w-4).Render(
			styRed.Render("✗ ")+styText.Render(m.errMsg))
	}
	return ""
}

// Recent generations (most recent first, up to 5).
func (m model) historyView() string {
	if len(m.history) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n" + styEyebrow.Render("RECENT") + "\n")
	n := len(m.history)
	for i := n - 1; i >= 0 && i >= n-5; i-- {
		h := m.history[i]
		dot := styGreen.Render("●")
		if h.failed {
			dot = styRed.Render("●")
		}
		b.WriteString(fmt.Sprintf("%s %s %s\n",
			dot, styDim.Render("["+h.kind+"]"), styMuted.Render(truncate(h.prompt, 52))))
	}
	return strings.TrimRight(b.String(), "\n")
}

func fmtDur(d time.Duration) string {
	s := int(d.Seconds())
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
