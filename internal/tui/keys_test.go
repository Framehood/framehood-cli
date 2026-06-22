package tui

import "testing"

// helpListed reports whether a binding with the given primary key is present
// in the short help for the given context.
func helpListed(hc helpContext, primary string) bool {
	for _, b := range hc.ShortHelp() {
		for _, k := range b.Keys() {
			if k == primary {
				return true
			}
		}
	}
	return false
}

func TestShortHelp_FocusAware(t *testing.T) {
	k := defaultKeys()

	// Input zone (primary surface): must advertise /, shift+tab, enter.
	// q must NOT appear (not a bound key here).
	in := helpContext{keys: k, focus: zoneInput}
	if helpListed(in, "q") {
		t.Error("input zone help must not advertise q")
	}
	if !helpListed(in, "/") {
		t.Error("input zone help should list / (open palette)")
	}
	if !helpListed(in, "shift+tab") {
		t.Error("input zone help should list shift+tab (next action)")
	}
	if !helpListed(in, "tab") {
		t.Error("input zone help should list tab (prev action — reverse cycle)")
	}
	if !helpListed(in, "enter") {
		t.Error("input zone help should list enter (generate)")
	}

	// Palette open: must advertise enter (run) and esc (close).
	pal := helpContext{keys: k, paletteOpen: true}
	if !helpListed(pal, "enter") {
		t.Error("palette help should list enter (run command)")
	}
	if !helpListed(pal, "esc") {
		t.Error("palette help should list esc (close palette)")
	}

	// Output zone: o (open) only when there is a result.
	noResult := helpContext{keys: k, focus: zoneOutput, hasResult: false}
	if helpListed(noResult, "o") {
		t.Error("output zone without a result must not advertise o")
	}
	withResult := helpContext{keys: k, focus: zoneOutput, hasResult: true}
	if !helpListed(withResult, "o") {
		t.Error("output zone with a result should advertise o")
	}
}
