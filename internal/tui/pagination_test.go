package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// makeHistoryModel builds a model with n generation entries (prompt "p0".."p{n-1}",
// appended chronologically so p{n-1} is the newest) on the newest page.
func makeHistoryModel(n int) model {
	m := newTestModel()
	m.history = nil
	for i := 0; i < n; i++ {
		m.history = append(m.history, historyItem{
			kind:   "image·create",
			prompt: "p" + itoa(i),
			url:    "https://cdn.framehood.ai/" + itoa(i) + ".jpg",
		})
	}
	m.rebuildHistory(true) // newest page, newest selected
	m = m.setFocus(zoneOutput)
	return m
}

func TestPagination_PagesAndIndicator(t *testing.T) {
	// 15 entries, page size 6 → 3 pages: [1–6],[7–12],[13–15] (newest-first).
	m := makeHistoryModel(15)

	if got := m.historyPages(); got != 3 {
		t.Fatalf("historyPages = %d, want 3", got)
	}
	// Page 0 (newest): 1–6 of 15.
	if got := m.historyRangeLabel(); got != "1–6 of 15" {
		t.Errorf("page 0 indicator = %q, want '1–6 of 15'", got)
	}
	// Newest entry (p14) is selected on page 0.
	if it, ok := m.selectedItem(); !ok || it.prompt != "p14" {
		t.Fatalf("page 0 selection = %+v, want p14 (newest)", it)
	}

	// PgDn → older page (page 1): 7–12 of 15.
	nm, _ := m.updateOutput(tea.KeyMsg{Type: tea.KeyPgDown})
	m = nm.(model)
	if m.histPage != 1 {
		t.Fatalf("after pgdn: histPage = %d, want 1", m.histPage)
	}
	if got := m.historyRangeLabel(); got != "7–12 of 15" {
		t.Errorf("page 1 indicator = %q, want '7–12 of 15'", got)
	}
	// Top of page 1 is the 7th-newest = p8.
	if it, ok := m.selectedItem(); !ok || it.prompt != "p8" {
		t.Errorf("page 1 top selection = %+v, want p8", it)
	}

	// PgDn again → last page (page 2): 13–15 of 15.
	nm, _ = m.updateOutput(tea.KeyMsg{Type: tea.KeyPgDown})
	m = nm.(model)
	if got := m.historyRangeLabel(); got != "13–15 of 15" {
		t.Errorf("page 2 indicator = %q, want '13–15 of 15'", got)
	}
	if len(m.rows) != 3 {
		t.Errorf("last page rows = %d, want 3 (15 mod 6)", len(m.rows))
	}

	// PgDn at the last page clamps (no change).
	nm, _ = m.updateOutput(tea.KeyMsg{Type: tea.KeyPgDown})
	m = nm.(model)
	if m.histPage != 2 {
		t.Errorf("pgdn at last page: histPage = %d, want clamped at 2", m.histPage)
	}

	// PgUp walks back toward newer pages.
	nm, _ = m.updateOutput(tea.KeyMsg{Type: tea.KeyPgUp})
	m = nm.(model)
	if m.histPage != 1 || m.historyRangeLabel() != "7–12 of 15" {
		t.Errorf("after pgup: page %d %q, want page 1 '7–12 of 15'", m.histPage, m.historyRangeLabel())
	}
	// PgUp to page 0, then clamp.
	nm, _ = m.updateOutput(tea.KeyMsg{Type: tea.KeyPgUp})
	m = nm.(model)
	nm, _ = m.updateOutput(tea.KeyMsg{Type: tea.KeyPgUp})
	m = nm.(model)
	if m.histPage != 0 {
		t.Errorf("pgup at first page: histPage = %d, want clamped at 0", m.histPage)
	}
}

func TestPagination_SinglePage(t *testing.T) {
	m := makeHistoryModel(3)
	if m.historyPages() != 1 {
		t.Errorf("3 entries → %d pages, want 1", m.historyPages())
	}
	if got := m.historyRangeLabel(); got != "1–3 of 3" {
		t.Errorf("indicator = %q, want '1–3 of 3'", got)
	}
	// PgDn on a single page is a no-op.
	nm, _ := m.updateOutput(tea.KeyMsg{Type: tea.KeyPgDown})
	if nm.(model).histPage != 0 {
		t.Error("pgdn with one page must not change the page")
	}
}

// TestPagination_QuickKeysActOnSelectedAcrossPages verifies the o/c/s/u quick
// keys resolve the selected row correctly after paging (selectedItem indexes
// the current page's rows).
func TestPagination_QuickKeysActOnSelectedAcrossPages(t *testing.T) {
	m := makeHistoryModel(15)

	// Page to the middle page and confirm "use as input" seeds the right URL.
	nm, _ := m.updateOutput(tea.KeyMsg{Type: tea.KeyPgDown}) // page 1, top = p8
	m = nm.(model)
	it, ok := m.selectedItem()
	if !ok {
		t.Fatal("expected a selection on page 1")
	}
	nm2, _ := m.updateOutput(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'u'}})
	got := nm2.(model)
	if got.seedURL != it.url {
		t.Errorf("use-as-input seeded %q, want the selected row's url %q", got.seedURL, it.url)
	}
	if !strings.Contains(it.url, "8.jpg") {
		t.Errorf("page-1 top row url = %q, want the p8 entry (8.jpg)", it.url)
	}
}

func TestPagination_NewGenerationReturnsToNewestPage(t *testing.T) {
	m := makeHistoryModel(15)
	// Page away from the newest.
	nm, _ := m.updateOutput(tea.KeyMsg{Type: tea.KeyPgDown})
	nm, _ = nm.(model).updateOutput(tea.KeyMsg{Type: tea.KeyPgDown})
	m = nm.(model)
	if m.histPage != 2 {
		t.Fatalf("precondition: should be on page 2, got %d", m.histPage)
	}
	// A fresh completed generation rebuilds with selectNewest → back to page 0.
	m.history = append(m.history, historyItem{kind: "audio·speak", prompt: "fresh"})
	m.rebuildHistory(true)
	if m.histPage != 0 {
		t.Errorf("after a new generation: histPage = %d, want 0 (newest page)", m.histPage)
	}
	if it, ok := m.selectedItem(); !ok || it.prompt != "fresh" {
		t.Errorf("after a new generation: selected = %+v, want 'fresh'", it)
	}
}

func TestHistoryView_RendersIndicator(t *testing.T) {
	lipgloss.SetColorProfile(termenv.Ascii)
	m := makeHistoryModel(20)
	out := m.historyView()
	if !strings.Contains(out, "1–6 of 20") {
		t.Errorf("history view should show the page indicator: %q", out)
	}
}

func TestPalette_HasHistoryCommand(t *testing.T) {
	var found paletteCmd
	for _, c := range allPaletteCmds {
		if c.id == "/history" {
			found = c
			break
		}
	}
	if found.id != "/history" {
		t.Fatal("/history command not found in the palette")
	}
	if found.kind != cmdImmediate || found.meta != "history" {
		t.Errorf("/history = %+v, want immediate meta='history'", found)
	}
}

func TestRunPalette_HistoryFocusesOutput(t *testing.T) {
	m := makeHistoryModel(10)
	m = m.setFocus(zoneInput) // start from the input

	hist := paletteCmdByID(t, "/history")
	nm, _ := m.runPaletteCmd(&hist)
	got := nm.(model)
	if got.focus != zoneOutput {
		t.Errorf("/history should focus the output zone, got %v", got.focus)
	}
	if got.histPage != 0 {
		t.Errorf("/history should jump to the newest page, got page %d", got.histPage)
	}

	// With no history, /history sets a notice and stays on the input.
	empty := newTestModel()
	empty.history = nil
	empty.rebuildHistory(true)
	he := paletteCmdByID(t, "/history")
	nm2, _ := empty.runPaletteCmd(&he)
	got2 := nm2.(model)
	if got2.focus != zoneInput || got2.notice == "" {
		t.Errorf("/history with no history should notice + stay on input (focus=%v notice=%q)", got2.focus, got2.notice)
	}
}
