package tui

import "strings"

// maxInputHistory caps the per-session prompt-recall ring so a long session
// can't grow it without bound.
const maxInputHistory = 200

// inputHistory is the shell-style prompt-recall buffer for the compose box.
// Entries are stored oldest-first (index 0 = oldest, len-1 = newest), exactly
// the order a shell's `history` prints.
//
// Navigation model (mirrors bash/zsh line editing):
//   - pos == len(entries)  → "live" line: not navigating; the box shows the
//     user's in-progress text (the draft).
//   - 0 <= pos < len       → recalling entries[pos].
//
// draft holds the in-progress text captured on the first ↑ so ↓ past the newest
// entry can restore it. navigating reports whether a draft is currently held.
type inputHistory struct {
	entries []string
	pos     int    // navigation cursor; == len(entries) when on the live line
	draft   string // in-progress text saved on the first ↑
}

// add records a freshly-submitted prompt. Empty/whitespace-only prompts and a
// consecutive duplicate of the most recent entry are skipped (shell-style).
// Any add resets navigation back to the live line.
func (h *inputHistory) add(prompt string) {
	h.reset()
	if strings.TrimSpace(prompt) == "" {
		return
	}
	if n := len(h.entries); n > 0 && h.entries[n-1] == prompt {
		// Consecutive duplicate — leave the ring unchanged.
		h.pos = len(h.entries)
		return
	}
	h.entries = append(h.entries, prompt)
	if len(h.entries) > maxInputHistory {
		// Drop the oldest, keep the cap.
		h.entries = h.entries[len(h.entries)-maxInputHistory:]
	}
	h.pos = len(h.entries) // back to the live line
}

// reset returns navigation to the live line and clears the saved draft.
// Call this whenever the user edits the box (any non-arrow key), so the next ↑
// starts recalling from the newest entry again.
func (h *inputHistory) reset() {
	h.pos = len(h.entries)
	h.draft = ""
}

// navigating reports whether we are currently recalling history (a draft is
// held). It is false on the live line.
func (h *inputHistory) navigating() bool { return h.pos < len(h.entries) }

// prev recalls the previous (older) entry. `current` is the box's live text,
// saved as the draft on the first ↑ so it can be restored later. It returns the
// text to show and ok=false when there is nothing to recall (empty history).
// At the oldest entry it clamps (returns the oldest, no error).
func (h *inputHistory) prev(current string) (string, bool) {
	if len(h.entries) == 0 {
		return "", false
	}
	if !h.navigating() {
		// First ↑: stash the in-progress line, jump to the newest entry.
		h.draft = current
		h.pos = len(h.entries) - 1
		return h.entries[h.pos], true
	}
	if h.pos > 0 {
		h.pos--
	}
	// At pos 0 we clamp on the oldest entry.
	return h.entries[h.pos], true
}

// next moves toward newer entries. Stepping past the newest entry restores the
// saved draft and exits navigation (returns the draft, ok=true). It returns
// ok=false when not currently navigating (↓ on the live line is a no-op here).
func (h *inputHistory) next() (string, bool) {
	if !h.navigating() {
		return "", false
	}
	h.pos++
	if h.pos >= len(h.entries) {
		// Past the newest entry → back to the live line, restore the draft.
		draft := h.draft
		h.reset()
		return draft, true
	}
	return h.entries[h.pos], true
}
