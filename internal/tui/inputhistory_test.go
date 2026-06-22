package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// --- pure inputHistory unit tests ---

func TestInputHistory_AddDedupAndSkipEmpty(t *testing.T) {
	var h inputHistory

	h.add("alpha")
	h.add("beta")
	h.add("beta") // consecutive duplicate → skipped
	h.add("")     // empty → skipped
	h.add("   ")  // whitespace → skipped
	h.add("gamma")
	h.add("beta") // non-consecutive duplicate → kept

	want := []string{"alpha", "beta", "gamma", "beta"}
	if len(h.entries) != len(want) {
		t.Fatalf("entries = %v, want %v", h.entries, want)
	}
	for i := range want {
		if h.entries[i] != want[i] {
			t.Errorf("entries[%d] = %q, want %q", i, h.entries[i], want[i])
		}
	}
	if h.navigating() {
		t.Error("add must leave navigation on the live line")
	}
}

func TestInputHistory_Cap(t *testing.T) {
	var h inputHistory
	for i := 0; i < maxInputHistory+50; i++ {
		// Unique prompts so none are deduped.
		h.add(string(rune('a'+i%26)) + itoa(i))
	}
	if len(h.entries) != maxInputHistory {
		t.Fatalf("entries len = %d, want cap %d", len(h.entries), maxInputHistory)
	}
	// The newest entry must be the last one added.
	last := h.entries[len(h.entries)-1]
	wantLast := string(rune('a'+(maxInputHistory+49)%26)) + itoa(maxInputHistory+49)
	if last != wantLast {
		t.Errorf("newest entry = %q, want %q", last, wantLast)
	}
}

// itoa is a tiny dependency-free int→string for the cap test.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

func TestInputHistory_PrevWalksNewestToOldestAndClamps(t *testing.T) {
	var h inputHistory
	h.add("one")
	h.add("two")
	h.add("three") // newest

	// First ↑ saves the draft and recalls the newest.
	v, ok := h.prev("draft-in-progress")
	if !ok || v != "three" {
		t.Fatalf("first prev = (%q,%v), want (three,true)", v, ok)
	}
	if !h.navigating() {
		t.Error("after first prev, should be navigating")
	}
	if h.draft != "draft-in-progress" {
		t.Errorf("draft = %q, want the in-progress text", h.draft)
	}

	// ↑ again → older.
	if v, _ := h.prev(""); v != "two" {
		t.Errorf("2nd prev = %q, want two", v)
	}
	if v, _ := h.prev(""); v != "one" {
		t.Errorf("3rd prev = %q, want one", v)
	}
	// ↑ at the oldest clamps.
	if v, _ := h.prev(""); v != "one" {
		t.Errorf("clamp prev = %q, want one (oldest)", v)
	}
}

func TestInputHistory_NextRestoresDraftPastNewest(t *testing.T) {
	var h inputHistory
	h.add("one")
	h.add("two")

	// ↓ on the live line is a no-op.
	if _, ok := h.next(); ok {
		t.Error("next on live line should be a no-op (ok=false)")
	}

	// ↑ twice → "one", then ↓ walks back to "two", then ↓ restores the draft.
	h.prev("DRAFT") // → "two"
	h.prev("")      // → "one"
	if v, _ := h.next(); v != "two" {
		t.Errorf("next from oldest = %q, want two", v)
	}
	v, ok := h.next() // past the newest → restore draft, exit nav
	if !ok || v != "DRAFT" {
		t.Errorf("next past newest = (%q,%v), want (DRAFT,true)", v, ok)
	}
	if h.navigating() {
		t.Error("after restoring draft, navigation should be off")
	}
}

func TestInputHistory_PrevEmpty(t *testing.T) {
	var h inputHistory
	if _, ok := h.prev("x"); ok {
		t.Error("prev on empty history should report ok=false")
	}
}

func TestInputHistory_ResetReturnsToLiveLine(t *testing.T) {
	var h inputHistory
	h.add("a")
	h.add("b")
	h.prev("draft") // start navigating
	if !h.navigating() {
		t.Fatal("precondition: should be navigating")
	}
	h.reset()
	if h.navigating() {
		t.Error("reset should return to the live line")
	}
	if h.draft != "" {
		t.Error("reset should clear the draft")
	}
	// A fresh ↑ after reset starts again from the newest.
	if v, _ := h.prev("new-draft"); v != "b" {
		t.Errorf("prev after reset = %q, want b (newest)", v)
	}
}

// --- integration: ↑/↓ routed through updateInput / Update ---

// submitPrompt runs the studio's submit path for `prompt` (the way pressing
// Enter does), so the prompt lands in inputHist exactly as in production.
func submitPrompt(t *testing.T, m model, prompt string) model {
	t.Helper()
	m.action = findAction(t, "image", "create") // a runnable prompt action
	m.input.SetValue(prompt)
	nm, _ := m.updateInput(tea.KeyMsg{Type: tea.KeyEnter})
	got := nm.(model)
	// The submit clears working state for the test (we only care about history).
	got.phase = phaseIdle
	got.input.SetValue("")
	return got
}

func TestUpdateInput_RecallThreePrompts(t *testing.T) {
	m := newTestModel()
	m.input.SetValue("")
	m = submitPrompt(t, m, "first")
	m = submitPrompt(t, m, "second")
	m = submitPrompt(t, m, "third")

	if len(m.inputHist.entries) != 3 {
		t.Fatalf("history = %v, want 3 entries", m.inputHist.entries)
	}

	up := func(mm model) model {
		nm, _ := mm.updateInput(tea.KeyMsg{Type: tea.KeyUp})
		return nm.(model)
	}
	down := func(mm model) model {
		nm, _ := mm.updateInput(tea.KeyMsg{Type: tea.KeyDown})
		return nm.(model)
	}

	// Type an in-progress draft first so ↓-past-newest can restore it.
	m.input.SetValue("draft")
	m.inputHist.reset() // editing resets nav (as a keystroke would)

	// ↑ newest-first: third, second, first, then clamp at first.
	m = up(m)
	if m.input.Value() != "third" {
		t.Errorf("1st ↑ = %q, want third", m.input.Value())
	}
	m = up(m)
	if m.input.Value() != "second" {
		t.Errorf("2nd ↑ = %q, want second", m.input.Value())
	}
	m = up(m)
	if m.input.Value() != "first" {
		t.Errorf("3rd ↑ = %q, want first", m.input.Value())
	}
	m = up(m)
	if m.input.Value() != "first" {
		t.Errorf("4th ↑ (clamp) = %q, want first", m.input.Value())
	}

	// ↓ walks back toward newer, then restores the draft past the newest.
	m = down(m)
	if m.input.Value() != "second" {
		t.Errorf("1st ↓ = %q, want second", m.input.Value())
	}
	m = down(m)
	if m.input.Value() != "third" {
		t.Errorf("2nd ↓ = %q, want third", m.input.Value())
	}
	m = down(m)
	if m.input.Value() != "draft" {
		t.Errorf("3rd ↓ (restore draft) = %q, want draft", m.input.Value())
	}
	if m.inputHist.navigating() {
		t.Error("after restoring the draft, navigation should be off")
	}
}

func TestUpdateInput_EditingResetsNavigation(t *testing.T) {
	m := newTestModel()
	m.input.SetValue("")
	m = submitPrompt(t, m, "alpha")
	m = submitPrompt(t, m, "beta")

	// ↑ recalls the newest.
	nm, _ := m.updateInput(tea.KeyMsg{Type: tea.KeyUp})
	m = nm.(model)
	if m.input.Value() != "beta" || !m.inputHist.navigating() {
		t.Fatalf("after ↑: value=%q navigating=%v, want beta/true", m.input.Value(), m.inputHist.navigating())
	}

	// Typing a character exits navigation.
	nm2, _ := m.updateInput(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	m = nm2.(model)
	if m.inputHist.navigating() {
		t.Error("typing should reset history navigation")
	}

	// A fresh ↑ now starts again from the newest entry.
	nm3, _ := m.updateInput(tea.KeyMsg{Type: tea.KeyUp})
	m = nm3.(model)
	if m.input.Value() != "beta" {
		t.Errorf("fresh ↑ after edit = %q, want beta (newest)", m.input.Value())
	}
}

func TestUpdateInput_DedupConsecutiveSubmit(t *testing.T) {
	m := newTestModel()
	m.input.SetValue("")
	m = submitPrompt(t, m, "same")
	m = submitPrompt(t, m, "same") // consecutive duplicate
	if len(m.inputHist.entries) != 1 {
		t.Errorf("consecutive-duplicate submit not deduped: entries = %v", m.inputHist.entries)
	}
}

// TestArrowsInPaletteStillNavigateGrid proves ↑/↓ go to the palette grid (not
// input history) when the palette is open — the multiplexing must not steal
// them. Routed through the top-level Update so the palette-open branch is hit.
func TestArrowsInPaletteStillNavigateGrid(t *testing.T) {
	m := newTestModel()
	m.input.SetValue("")
	m = submitPrompt(t, m, "some prompt") // give history so a leak would be visible

	// Open the palette via the real `/` path (sets the grid column count).
	op, _ := m.updateInput(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m = op.(model)
	if !m.palette.isOpen() {
		t.Fatal("'/' should open the palette")
	}
	startSel := m.palette.sel
	startInput := m.input.Value()

	// ↓ through the TOP-LEVEL Update (which routes to the palette when open).
	nm, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = nm.(model)
	if m.palette.sel == startSel {
		t.Error("↓ with palette open should move the grid selection")
	}
	if m.input.Value() != startInput {
		t.Errorf("↓ with palette open must NOT recall history into the input (got %q)", m.input.Value())
	}
	if m.inputHist.navigating() {
		t.Error("palette ↓ must not start input-history navigation")
	}
}

// TestArrowsInOutputZoneStillMoveTable proves ↑/↓ move the history table cursor
// when the output zone is focused, not the input-history buffer.
func TestArrowsInOutputZoneStillMoveTable(t *testing.T) {
	m := newTestModel()
	// Two rows so the cursor can move.
	m.history = []historyItem{
		{kind: "image", prompt: "first", url: "https://x/1.jpg"},
		{kind: "video", prompt: "second", url: "https://x/2.mp4"},
	}
	m.rebuildHistory(true)
	m = m.setFocus(zoneOutput)
	m.input.SetValue("")
	m = submitPrompt(t, m, "p") // populate input history (must stay untouched)
	m = m.setFocus(zoneOutput)

	startCursor := m.hist.Cursor()
	startInput := m.input.Value()
	nm, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = nm.(model)
	if m.hist.Cursor() == startCursor {
		t.Error("↓ in the output zone should move the table cursor")
	}
	if m.input.Value() != startInput {
		t.Error("↓ in the output zone must not recall input history")
	}
}
