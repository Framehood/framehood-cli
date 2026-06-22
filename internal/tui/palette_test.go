package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// TestPaletteRefilter checks that the filter query correctly narrows results.
func TestPaletteRefilter(t *testing.T) {
	p := openPaletteState()

	// No query → all commands visible.
	if len(p.matches) != len(allPaletteCmds) {
		t.Fatalf("empty query: got %d matches, want %d", len(p.matches), len(allPaletteCmds))
	}

	// Filter to "image" → only image-related commands.
	p.query = "image"
	p.refilter()
	for _, idx := range p.matches {
		c := allPaletteCmds[idx]
		if !strings.Contains(strings.ToLower(c.id), "image") &&
			!strings.Contains(strings.ToLower(c.title), "image") {
			t.Errorf("'image' filter returned non-matching command: id=%q title=%q", c.id, c.title)
		}
	}
	if len(p.matches) == 0 {
		t.Error("'image' filter should return at least one result")
	}

	// Filter with no matches.
	p.query = "zzznomatch"
	p.refilter()
	if len(p.matches) != 0 {
		t.Errorf("'zzznomatch' filter: got %d matches, want 0", len(p.matches))
	}
}

// TestPaletteNavigation verifies arrow-key movement within the grid.
func TestPaletteNavigation(t *testing.T) {
	p := openPaletteState()
	p.cols = 4 // simulate a 4-column grid

	// Start at 0.
	if p.sel != 0 {
		t.Fatalf("initial sel = %d, want 0", p.sel)
	}

	p.moveRight()
	if p.sel != 1 {
		t.Errorf("after moveRight: sel=%d, want 1", p.sel)
	}

	p.moveDown()
	if p.sel != 5 {
		t.Errorf("after moveDown from col1: sel=%d, want 5", p.sel)
	}

	p.moveLeft()
	if p.sel != 4 {
		t.Errorf("after moveLeft: sel=%d, want 4", p.sel)
	}

	p.moveUp()
	if p.sel != 0 {
		t.Errorf("after moveUp to first row: sel=%d, want 0", p.sel)
	}

	// Wrap left at 0.
	p.sel = 0
	p.moveLeft()
	if p.sel != len(p.matches)-1 {
		t.Errorf("left-wrap: sel=%d, want %d", p.sel, len(p.matches)-1)
	}
}

// TestPaletteColsPersistForGridNav is the regression test for the value-receiver
// bug: model.View() copies the model, so computing p.cols inside View only
// mutated the copy — the real model's cols stayed 0 and grid ↑/↓ navigation
// silently fell back to ←/→. The fix sets cols via layout() on the Update path.
// This test drives the REAL Update path (open palette via the `/` key) and
// asserts cols persisted (>1) and that ↓ advances the selection by exactly cols.
func TestPaletteColsPersistForGridNav(t *testing.T) {
	m := newTestModel()
	// Apply a wide window so more than one column fits.
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m = m2.(model)

	// Open the palette through the real key path (this is where layout() runs).
	m.input.SetValue("")
	op, _ := m.updateInput(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m = op.(model)
	if !m.palette.isOpen() {
		t.Fatal("'/' should open the palette")
	}

	// cols must be persisted on the REAL model (not lost on a View copy).
	cols := m.palette.cols
	if cols <= 1 {
		t.Fatalf("palette.cols = %d after open at width 100, want > 1 (grid nav needs it)", cols)
	}

	// Down should advance by a full row (cols), proving grid nav uses the
	// persisted cols rather than degrading to a single-step move.
	startSel := m.palette.sel
	dn, _ := m.updatePalette(tea.KeyMsg{Type: tea.KeyDown})
	m = dn.(model)
	if got := m.palette.sel - startSel; got != cols {
		t.Errorf("after ↓: sel advanced by %d, want cols=%d (grid nav broken)", got, cols)
	}

	// A WindowSizeMsg while the palette is open must keep cols consistent.
	wm, _ := m.Update(tea.WindowSizeMsg{Width: 60, Height: 30})
	m = wm.(model)
	if m.palette.cols < 1 {
		t.Errorf("after resize: cols = %d, want ≥1", m.palette.cols)
	}
}

// TestPaletteViewNoPanic is the non-interactive render smoke test described in
// the spec. It constructs a model at a fixed 100×30 window, opens the palette
// with a query, and confirms View() produces a non-empty columns grid without
// panicking.
func TestPaletteViewNoPanic(t *testing.T) {
	lipgloss.SetColorProfile(termenv.Ascii)

	m := newTestModel()
	// Apply the window size (100×30).
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m = m2.(model)

	// Open palette with query "image".
	m.palette = openPaletteState()
	m.palette.query = "image"
	m.palette.refilter()

	if !m.palette.isOpen() {
		t.Fatal("palette should be open")
	}
	if len(m.palette.matches) == 0 {
		t.Fatal("'image' query should have matches")
	}

	// Render — must not panic.
	out := m.View()
	if out == "" {
		t.Fatal("View() returned empty string")
	}

	// Palette region: the grid should be rendered below the composer.
	// With Ascii color profile we can check for plain text.
	if !strings.Contains(out, "COMMANDS") {
		t.Error("palette view should contain 'COMMANDS' label")
	}
	if !strings.Contains(out, "/") {
		t.Error("palette view should contain the / query prefix")
	}
	if !strings.Contains(out, "image") {
		t.Error("palette view should contain 'image' in a cell or the query")
	}

	// Rendered palette section itself.
	w := m.contentWidth()
	pv := m.palette.View(w)
	if pv == "" {
		t.Fatal("palette.View() returned empty")
	}

	t.Logf("=== Palette render at 100×30, query='image' ===\n%s\n", pv)
}

// TestPaletteViewSmallWidth is the small-width render smoke test: it renders the
// palette grid at narrow widths (including below one cell) with an EMPTY query
// and with a no-match query, and confirms View() never panics and always
// produces non-empty output. Guards the columns math (cols clamps to ≥1).
func TestPaletteViewSmallWidth(t *testing.T) {
	lipgloss.SetColorProfile(termenv.Ascii)

	for _, w := range []int{0, 1, 5, 20, paletteCellTotal - 1} {
		// Empty query → all commands visible, must lay out at ≥1 column.
		p := openPaletteState() // query == ""
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("palette.View(width=%d, empty query) panicked: %v", w, r)
				}
			}()
			out := p.View(w)
			if out == "" {
				t.Fatalf("palette.View(width=%d, empty query) returned empty", w)
			}
			// cols is set by the authoritative layout() path (View no longer
			// mutates it, since model.View has a value receiver). layout must
			// clamp to ≥1 at any width so grid nav never divides by zero.
			p.layout(w)
			if p.cols < 1 {
				t.Errorf("width=%d: layout cols = %d, want ≥1", w, p.cols)
			}
			if w == 20 {
				t.Logf("=== Palette render at width=20, empty query (cols=%d) ===\n%s\n", p.cols, out)
			}
		}()

		// No-match query → the empty-state branch must also not panic.
		p2 := openPaletteState()
		p2.query = "zzznomatch"
		p2.refilter()
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("palette.View(width=%d, no matches) panicked: %v", w, r)
				}
			}()
			if out := p2.View(w); out == "" {
				t.Fatalf("palette.View(width=%d, no matches) returned empty", w)
			}
		}()
	}
}

// TestPaletteOpenClose verifies that pressing Esc via updatePalette clears
// the palette and restores input focus.
func TestPaletteOpenClose(t *testing.T) {
	m := newTestModel()
	m.palette = openPaletteState()
	if !m.palette.isOpen() {
		t.Fatal("palette should be open after openPaletteState")
	}

	nm, _ := m.updatePalette(tea.KeyMsg{Type: tea.KeyEsc})
	got := nm.(model)
	if got.palette.isOpen() {
		t.Error("palette should be closed after esc")
	}
	if got.focus != zoneInput {
		t.Errorf("focus should be zoneInput after esc, got %v", got.focus)
	}
	if got.input.Value() != "" {
		t.Errorf("input should be empty after esc, got %q", got.input.Value())
	}
}

// TestPaletteQueryTyping verifies that typing characters in palette mode
// updates the query and refilters.
func TestPaletteQueryTyping(t *testing.T) {
	m := newTestModel()
	m.palette = openPaletteState()
	initial := len(m.palette.matches)

	// Type "v" — should narrow to video + any commands containing "v".
	nm, _ := m.updatePalette(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'v'}})
	got := nm.(model)
	if got.palette.query != "v" {
		t.Errorf("query = %q, want 'v'", got.palette.query)
	}
	if len(got.palette.matches) >= initial {
		t.Errorf("typing 'v' should narrow results: before=%d after=%d", initial, len(got.palette.matches))
	}

	// Backspace should restore the empty query.
	nm2, _ := got.updatePalette(tea.KeyMsg{Type: tea.KeyBackspace})
	got2 := nm2.(model)
	if got2.palette.query != "" {
		t.Errorf("after backspace query = %q, want ''", got2.palette.query)
	}
	if len(got2.palette.matches) != initial {
		t.Errorf("after backspace matches = %d, want %d (initial)", len(got2.palette.matches), initial)
	}
}
