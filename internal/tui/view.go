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
	sections = append(sections, m.composerView(w))

	// Palette overlay sits directly below the composer.
	if m.palette.isOpen() {
		sections = append(sections, m.palette.View(w))
	}

	if body := m.statusView(w); body != "" {
		sections = append(sections, body)
	}
	if h := m.historyView(); h != "" {
		sections = append(sections, h)
	}

	sel, selOK := m.selectedItem()
	hc := helpContext{
		keys:            m.keys,
		focus:           m.focus,
		paletteOpen:     m.palette.isOpen(),
		working:         m.phase == phaseWorking,
		hasResult:       selOK && sel.url != "",
		hasRows:         len(m.rows) > 0,
		hasInputHistory: len(m.inputHist.entries) > 0,
	}
	sections = append(sections, "\n"+m.help.View(hc))

	out := lipgloss.JoinVertical(lipgloss.Left, sections...)
	return lipgloss.NewStyle().Padding(1, 1).Render(out)
}

// Header: title on the left, account/balance (or signed-out) on the right.
func (m model) headerView(w int) string {
	// Active work-action chip (Shift+Tab cycles it).
	actionChip := styChipActive.Render(m.action.tool + " · " + m.action.action)

	left := styTitle.Render("✦ Framehood") + styDim.Render(" studio") +
		styDim.Render("  ") + actionChip

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

// composerView: the prompt input (or form in form mode). The active action is
// shown as an eyebrow label.
func (m model) composerView(w int) string {
	box := styPanel
	if m.focus == zoneInput && m.phase != phaseWorking && !m.palette.isOpen() {
		box = styPanelActive
	}
	if m.setdirMode {
		label := styEyebrow.Render("OUTPUT DIRECTORY") +
			styDim.Render("   enter = set · empty = show current · esc = cancel")
		field := box.Width(w - 4).Render(m.input.View())
		return "\n" + label + "\n" + field
	}
	if len(m.formFields) > 0 {
		return m.formComposerView(w, box)
	}
	label := styEyebrow.Render(strings.ToUpper(m.action.tool + " · " + m.action.action))
	field := box.Width(w - 4).Render(m.input.View())
	return "\n" + label + "\n" + field
}

// formComposerView stacks the form fields, the active one showing the live input.
func (m model) formComposerView(w int, box lipgloss.Style) string {
	label := styEyebrow.Render(strings.ToUpper(m.action.tool+" · "+m.action.action)) +
		styDim.Render(fmt.Sprintf("   field %d/%d", m.formIdx+1, len(m.formFields)))
	var rows []string
	for i, f := range m.formFields {
		name := styDim.Render(f.label)
		if i == m.formIdx {
			rows = append(rows, styAcc.Render("▸ ")+name)
			rows = append(rows, box.Width(w-6).Render(m.input.View()))
			continue
		}
		val := m.formVals[f.name]
		if val == "" {
			val = styDim.Render("—")
		} else {
			val = styText.Render(truncate(val, w-10))
		}
		rows = append(rows, styDim.Render("  ")+name+styDim.Render(": ")+val)
	}
	hint := styDim.Render("enter/↓ next · ↑ back · esc cancel")
	return "\n" + label + "\n" + lipgloss.JoinVertical(lipgloss.Left, rows...) + "\n" + hint
}

// Status: working spinner, done result panel, or an error line.
func (m model) statusView(w int) string {
	switch m.phase {
	case phaseWorking:
		return "\n" + m.workingView()

	case phaseDone:
		// Immediate read action (files·list, org·info, billing·balance, …): show
		// the fetched DATA, not a media URL. No open/copy/save — it's information.
		if m.readData != "" {
			hdr := "READ"
			if m.readHdr != "" {
				hdr = strings.ToUpper(m.readHdr)
			}
			inner := lipgloss.JoinVertical(lipgloss.Left,
				styGreen.Render("✓ "+hdr),
				styText.Render(truncateBlock(m.readData, w-8, 18)),
				styDim.Render("enter = clear · / = new command"))
			return "\n" + styEyebrow.Render(hdr) + "\n" +
				styPanel.BorderForeground(colGreen).Width(w-4).Render(inner)
		}
		it, ok := m.selectedItem()
		head := styGreen.Render("✓ done")
		if ok && it.failed {
			head = styRed.Render("✗ failed")
		}
		lines := []string{head}
		if ok && it.url != "" {
			lines = append(lines, styText.Render(it.url))
			lines = append(lines, styDim.Render("o open · c copy url · s save"))
		}
		if m.notice != "" {
			lines = append(lines, m.notice)
		}
		inner := lipgloss.JoinVertical(lipgloss.Left, lines...)
		return "\n" + styEyebrow.Render("RESULT") + "\n" +
			styPanel.BorderForeground(colGreen).Width(w-4).Render(inner)

	case phaseError:
		return "\n" + styPanel.BorderForeground(colRed).Width(w-4).Render(
			styRed.Render("✗ ")+styText.Render(m.errMsg))
	}
	// Show notice even in idle state (e.g. after copy/save from palette).
	if m.notice != "" {
		return "\n" + m.notice
	}
	return ""
}

// workingView renders the working phase as one of two visually-distinct states:
//
//   - submitting — the request is in flight, no job_id yet. A subtle dot spinner
//     and a "submitting" label; the brief pre-job moment.
//   - generating — a job is running and we're polling. A lively marching wave in
//     the indigo palette, with the provider's status label, elapsed M:SS and the
//     short job_id.
func (m model) workingView() string {
	elapsed := fmtDur(time.Since(m.started).Round(time.Second))

	if !m.generating() {
		// No job to poll yet: calm dot spinner. This covers both the pre-job
		// submit moment ("submitting") and an immediate read fetch ("running").
		label := "submitting"
		if m.status == "running" {
			label = "running"
		}
		return fmt.Sprintf("%s %s %s",
			styAcc.Render(m.spin.View()),
			styText.Render(label),
			styDim.Render("· "+elapsed))
	}

	// Generating: the lively wave + provider status + elapsed + job id.
	status := m.status
	if status == "" || status == "submitting" {
		status = "generating"
	}
	line := fmt.Sprintf("%s  %s %s %s",
		genWaveView(m.genFrame),
		styText.Render("generating"),
		styDim.Render("· "+status),
		styDim.Render("· "+elapsed))
	if m.jobID != "" {
		line += styDim.Render(" · " + m.jobID)
	}
	return line
}

// historyView: recent generations table (most recent first), with a paging
// indicator ("RECENT · 7–12 of 143") when there is more than one page.
func (m model) historyView() string {
	if len(m.rows) == 0 {
		return ""
	}
	label := styEyebrow.Render("RECENT")
	if m.focus == zoneOutput {
		label = styAcc.Render("▸ ") + label
	}
	label += styDim.Render("  ·  " + m.historyRangeLabel())
	if m.historyPages() > 1 {
		label += styDim.Render("   ⇞ ⇟ page")
	}
	return "\n" + label + "\n" + m.hist.View()
}

// historyRangeLabel renders the "7–12 of 143" indicator for the current page,
// counting newest-first (entry 1 is the newest).
func (m model) historyRangeLabel() string {
	total := len(m.history)
	if total == 0 {
		return "0 of 0"
	}
	lo, hi := m.pageBounds()
	return fmt.Sprintf("%d–%d of %d", lo+1, hi, total)
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

// truncateBlock clamps a multi-line string to at most maxLines lines, each at
// most width runes wide, so a large read payload (e.g. a long files·list) can't
// blow out the result panel. A trailing "…" line marks truncation.
func truncateBlock(s string, width, maxLines int) string {
	if width < 8 {
		width = 8
	}
	lines := strings.Split(s, "\n")
	clipped := false
	if len(lines) > maxLines {
		lines = lines[:maxLines]
		clipped = true
	}
	for i, ln := range lines {
		if len([]rune(ln)) > width {
			lines[i] = string([]rune(ln)[:width-1]) + "…"
		}
	}
	if clipped {
		lines = append(lines, styDim.Render("…"))
	}
	return strings.Join(lines, "\n")
}
