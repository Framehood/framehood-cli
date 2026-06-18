package tui

import "testing"

// helpListed reports whether a binding with the given primary key is present.
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

	// Input zone: q and ? are typed into the prompt, so the bar must NOT list
	// them as commands (regression guard for the typed-key conflict).
	in := helpContext{keys: k, focus: zoneInput}
	if helpListed(in, "q") {
		t.Error("input zone help must not advertise q (it types a literal q)")
	}
	if helpListed(in, "?") {
		t.Error("input zone help must not advertise ? (it types a literal ?)")
	}
	if !helpListed(in, "enter") || !helpListed(in, "esc") {
		t.Error("input zone help should list enter + esc")
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

	// Tabs zone lists the type switcher.
	tabs := helpContext{keys: k, focus: zoneTabs}
	if !helpListed(tabs, "right") {
		t.Error("tabs zone help should list the ←/→ type switch")
	}
}
