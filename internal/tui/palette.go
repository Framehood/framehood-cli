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
	{id: "/history", title: "history", kind: cmdImmediate, meta: "history"},
	{id: "/setdir", title: "set output dir", kind: cmdImmediate, meta: "setdir"},
	{id: "/upgrade", title: "upgrade", kind: cmdImmediate, meta: "upgrade"},
	{id: "/quit", title: "quit", kind: cmdImmediate, meta: "quit"},
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

// commandNames is the lookup used to resolve a typed/pasted slash command to a
// palette command. It maps a normalized name (lowercase, space-joined tokens)
// to the command index in allPaletteCmds. Built once from:
//   - meta commands: their id without the leading "/" ("help", "setdir", …);
//   - catalog commands: the two-token "tool action" ("files list", "image
//     create", …) AND a single-token alias = the action name when that action
//     name is unique across the whole catalog ("balance" → billing·balance).
//
// Multi-name entries let "/balance" and "/billing balance" both resolve.
var commandNames = buildCommandNames()

func buildCommandNames() map[string]int {
	names := make(map[string]int)

	// Count action-name occurrences across catalog so we only alias unique ones.
	actionCount := map[string]int{}
	for i := range allPaletteCmds {
		if s := allPaletteCmds[i].spec; s != nil {
			actionCount[s.action]++
		}
	}

	for i := range allPaletteCmds {
		c := &allPaletteCmds[i]
		if c.spec == nil {
			// Meta command: "/setdir" → "setdir".
			name := strings.ToLower(strings.TrimPrefix(c.id, "/"))
			if name != "" {
				names[name] = i
			}
			continue
		}
		// Catalog command: "files list".
		full := strings.ToLower(c.spec.tool + " " + c.spec.action)
		names[full] = i
		// Single-word alias only when the action name is globally unique.
		if actionCount[c.spec.action] == 1 {
			alias := strings.ToLower(c.spec.action)
			if _, taken := names[alias]; !taken {
				names[alias] = i
			}
		}
	}
	return names
}

// resolveSlashInput parses a compose-box value that starts with "/" into a
// palette command plus the remainder (the text after the matched command name,
// used as a prompt/arg). It matches the LONGEST leading token sequence (up to
// two tokens) against commandNames. Returns ok=false when the text doesn't
// resolve to a command (callers then fall back to the highlighted item).
func resolveSlashInput(input string) (cmd *paletteCmd, remainder string, ok bool) {
	s := strings.TrimSpace(input)
	if !strings.HasPrefix(s, "/") {
		return nil, "", false
	}
	body := strings.TrimSpace(s[1:])
	if body == "" {
		return nil, "", false
	}
	fields := strings.Fields(body)
	// Try the longest leading name first (two tokens), then one token.
	for n := 2; n >= 1; n-- {
		if len(fields) < n {
			continue
		}
		name := strings.ToLower(strings.Join(fields[:n], " "))
		if idx, found := commandNames[name]; found {
			rem := strings.TrimSpace(strings.Join(fields[n:], " "))
			return &allPaletteCmds[idx], rem, true
		}
	}
	return nil, "", false
}

// paletteState holds all mutable state for the slash-command palette overlay.
type paletteState struct {
	open    bool
	query   string // text after the leading /
	matches []int  // indices into allPaletteCmds
	sel     int    // selected index within matches
	cols    int    // persisted column count — grid nav (moveUp/Down) keys off this
}

// open returns true when the palette is visible.
func (p paletteState) isOpen() bool { return p.open }

// openPalette initialises (or re-opens) the palette from a fresh "/" key.
func openPaletteState() paletteState {
	p := paletteState{open: true, query: ""}
	p.refilter()
	return p
}

// columnsFor returns how many fixed-width cells fit in the given content width
// (always ≥1). It is the single source of truth for the grid column count,
// shared by layout() (which persists it for navigation) and View() (rendering).
func columnsFor(width int) int {
	available := width - 4 // box padding (Padding(0,1)=2) + 2 border chars
	if available < paletteCellTotal {
		available = paletteCellTotal
	}
	cols := available / paletteCellTotal
	if cols < 1 {
		cols = 1
	}
	return cols
}

// layout recomputes and PERSISTS the column count for the current width. It must
// be called from the Update path (where the real model is mutated), because
// model.View has a value receiver — computing cols inside View would only mutate
// a throwaway copy, leaving the real model's cols at 0 and breaking ↑/↓ nav.
func (p *paletteState) layout(width int) {
	p.cols = columnsFor(width)
}

// syncFromInput derives the palette filter query from the live compose-box
// value (which holds the leading "/" + the typed text) and refilters. The
// query is the text after the leading "/", with a leading space trimmed so
// "/  files" still filters on "files".
func (p *paletteState) syncFromInput(input string) {
	q := input
	if i := strings.IndexByte(q, '/'); i >= 0 {
		q = q[i+1:]
	}
	p.query = strings.TrimLeft(q, " ")
	p.refilter()
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
//
// View derives its column count locally for rendering and does NOT persist it:
// model.View has a value receiver, so any mutation here lands on a copy. The
// authoritative cols used by grid navigation is set by layout() on the Update
// path. The two agree because both go through columnsFor.
func (p paletteState) View(width int) string {
	cols := columnsFor(width)

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
