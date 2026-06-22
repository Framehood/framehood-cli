package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// commandKind controls what running a palette command does.
type commandKind int

const (
	cmdImmediate   commandKind = iota // execute right away (meta commands)
	cmdNeedsPrompt                    // close palette, set action, wait for text + enter
	cmdNeedsForm                      // close palette, open the per-param form flow
)

// paletteCmd is a single selectable entry in the slash-command palette.
type paletteCmd struct {
	id    string      // e.g. "image·create" or "/help"
	title string      // display label
	spec  *actionSpec // nil for meta commands
	kind  commandKind
	meta  string // for meta commands: name of the action to take
}

// metaCmds are the built-in slash commands that don't map to catalog actions.
// Service commands (login/logout/whoami) live in the `/` palette ONLY — they
// are never part of the Shift+Tab work-action ring.
var metaCmds = []paletteCmd{
	{id: "/help", title: "help", kind: cmdImmediate, meta: "help"},
	{id: "/new", title: "new", kind: cmdImmediate, meta: "new"},
	{id: "/open", title: "open", kind: cmdImmediate, meta: "open"},
	{id: "/copy", title: "copy url", kind: cmdImmediate, meta: "copy"},
	{id: "/save", title: "save", kind: cmdImmediate, meta: "save"},
	{id: "/login", title: "login", kind: cmdImmediate, meta: "login"},
	{id: "/logout", title: "logout", kind: cmdImmediate, meta: "logout"},
	{id: "/whoami", title: "whoami", kind: cmdImmediate, meta: "whoami"},
	{id: "/quit", title: "quit", kind: cmdImmediate, meta: "quit"},
	// TODO: /upgrade  — leave hook; implement later
	// TODO: /setdir   — leave hook; implement later
	// TODO: /history  — leave hook; implement later
}

// buildPaletteCmds flattens catalog + meta into the full command list.
//
// Routing rules (in priority order):
//  1. spec.immediate  → cmdImmediate: executes right away, no user input needed.
//  2. spec.hasForm()  → cmdNeedsForm: opens the per-parameter form flow.
//  3. spec.runnable() → cmdNeedsPrompt: closes palette, user types a prompt + enter.
//  4. fallback        → cmdNeedsPrompt: non-form, non-runnable (e.g. get_status·check
//     which needs a job_id typed by the user).
func buildPaletteCmds() []paletteCmd {
	var cmds []paletteCmd
	cmds = append(cmds, metaCmds...)
	for i := range catalog {
		g := &catalog[i]
		for j := range g.actions {
			a := &g.actions[j]
			id := a.tool + "·" + a.action
			title := a.tool + " " + a.action
			switch {
			case a.immediate:
				// No suffix — runs without any further input.
				cmds = append(cmds, paletteCmd{id: id, title: title, spec: a, kind: cmdImmediate})
			case a.hasForm():
				// The › suffix is part of the title; truncation must keep it.
				cmds = append(cmds, paletteCmd{id: id, title: title + " ›", spec: a, kind: cmdNeedsForm})
			default:
				cmds = append(cmds, paletteCmd{id: id, title: title, spec: a, kind: cmdNeedsPrompt})
			}
		}
	}
	return cmds
}

// allPaletteCmds is the static master list (built once at startup).
var allPaletteCmds = buildPaletteCmds()

// paletteState holds all mutable state for the slash-command palette overlay.
type paletteState struct {
	open    bool
	query   string // text after the leading /
	matches []int  // indices into allPaletteCmds
	sel     int    // selected index within matches
	cols    int    // number of columns last computed
	colW    int    // width per cell (fixed)
}

// open returns true when the palette is visible.
func (p paletteState) isOpen() bool { return p.open }

// openPalette initialises (or re-opens) the palette from a fresh "/" key.
func openPaletteState() paletteState {
	p := paletteState{open: true, query: ""}
	p.refilter()
	return p
}

// refilter rebuilds the matches slice for the current query (case-insensitive
// substring on id + title) and clamps the selection.
func (p *paletteState) refilter() {
	q := strings.ToLower(p.query)
	p.matches = p.matches[:0]
	for i, c := range allPaletteCmds {
		if q == "" || strings.Contains(strings.ToLower(c.id), q) ||
			strings.Contains(strings.ToLower(c.title), q) {
			p.matches = append(p.matches, i)
		}
	}
	if p.sel >= len(p.matches) {
		p.sel = 0
	}
}

// selected returns the currently highlighted command, or nil if the palette is
// empty.
func (p paletteState) selected() *paletteCmd {
	if len(p.matches) == 0 {
		return nil
	}
	return &allPaletteCmds[p.matches[p.sel]]
}

// moveRight / moveLeft / moveUp / moveDown navigate within the columns grid.
func (p *paletteState) moveRight() {
	if len(p.matches) == 0 {
		return
	}
	p.sel = (p.sel + 1) % len(p.matches)
}

func (p *paletteState) moveLeft() {
	if len(p.matches) == 0 {
		return
	}
	p.sel = (p.sel - 1 + len(p.matches)) % len(p.matches)
}

func (p *paletteState) moveDown() {
	if p.cols <= 0 || len(p.matches) == 0 {
		p.moveRight()
		return
	}
	next := p.sel + p.cols
	if next >= len(p.matches) {
		// wrap: go to the same (or last) column in the first row
		col := p.sel % p.cols
		if col >= len(p.matches) {
			col = len(p.matches) - 1
		}
		p.sel = col
	} else {
		p.sel = next
	}
}

func (p *paletteState) moveUp() {
	if p.cols <= 0 || len(p.matches) == 0 {
		p.moveLeft()
		return
	}
	next := p.sel - p.cols
	if next < 0 {
		// wrap: find the same column in the last row
		col := p.sel % p.cols
		rows := (len(p.matches) + p.cols - 1) / p.cols
		last := (rows-1)*p.cols + col
		if last >= len(p.matches) {
			last -= p.cols
		}
		p.sel = last
	} else {
		p.sel = next
	}
}

// --- rendering ---

const (
	paletteCellInner = 16                                      // visible text area per cell (title truncated here)
	paletteCellPad   = 2                                       // padding left+right inside the cell
	paletteCellTotal = paletteCellInner + paletteCellPad*2 + 2 // +2 for border chars
)

// truncateRunes truncates s to at most n runes, appending "…" if cut.
// Unlike truncate() in view.go which uses byte length, this counts runes so
// multi-byte characters (e.g. "›") don't cause off-by-one wrapping inside
// lipgloss cells.
func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

var (
	styPaletteCell = lipgloss.NewStyle().
			Width(paletteCellInner).
			Padding(0, paletteCellPad).
			Foreground(colText)

	styPaletteSel = lipgloss.NewStyle().
			Width(paletteCellInner).
			Padding(0, paletteCellPad).
			Foreground(colInk).
			Background(colAccent).
			Bold(true)

	styPaletteBox = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colAccent).
			Padding(0, 1)

	styPaletteQuery = lipgloss.NewStyle().Foreground(colAccent).Bold(true)
	styPaletteLabel = lipgloss.NewStyle().Foreground(colDim).Bold(true)
)

// View renders the palette as a column grid inside a rounded border.
// width is the available content width (same as composerView uses).
func (p *paletteState) View(width int) string {
	// Compute how many columns fit.
	available := width - 4 // account for box padding (Padding(0,1) = 2) + 2 border chars
	if available < paletteCellTotal {
		available = paletteCellTotal
	}
	cols := available / paletteCellTotal
	if cols < 1 {
		cols = 1
	}
	p.cols = cols

	label := styPaletteLabel.Render("COMMANDS")
	queryLine := styPaletteQuery.Render("/") + styText.Render(p.query)
	if p.query == "" {
		queryLine = styPaletteQuery.Render("/") + styDim.Render("type to filter…")
	}

	if len(p.matches) == 0 {
		inner := lipgloss.JoinVertical(lipgloss.Left,
			label,
			queryLine,
			styDim.Render("  no matches"),
		)
		return styPaletteBox.Width(width - 4).Render(inner)
	}

	// Build grid rows.
	var gridRows []string
	for row := 0; ; row++ {
		start := row * cols
		if start >= len(p.matches) {
			break
		}
		end := start + cols
		if end > len(p.matches) {
			end = len(p.matches)
		}
		var cells []string
		for i := start; i < end; i++ {
			cmd := &allPaletteCmds[p.matches[i]]
			title := truncateRunes(cmd.title, paletteCellInner)
			if i == p.sel {
				cells = append(cells, styPaletteSel.Render(title))
			} else {
				cells = append(cells, styPaletteCell.Render(title))
			}
		}
		gridRows = append(gridRows, lipgloss.JoinHorizontal(lipgloss.Top, cells...))
	}

	hint := styDim.Render("← → ↑ ↓ navigate · enter run · esc close")
	inner := lipgloss.JoinVertical(lipgloss.Left,
		label,
		queryLine,
		"",
		lipgloss.JoinVertical(lipgloss.Left, gridRows...),
		"",
		hint,
	)
	return styPaletteBox.Width(width - 4).Render(inner)
}
