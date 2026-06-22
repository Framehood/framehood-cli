package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestArgsForAction(t *testing.T) {
	find := func(tool, action string) actionSpec {
		for _, g := range catalog {
			for _, a := range g.actions {
				if a.tool == tool && a.action == action {
					return a
				}
			}
		}
		t.Fatalf("action %s.%s not in catalog", tool, action)
		return actionSpec{}
	}
	// image.create: prompt → {action:create, prompt, out}
	tool, args := argsForAction(find("image", "create"), "a fox")
	if tool != "image" || args["action"] != "create" || args["prompt"] != "a fox" || args["out"] != "image.jpg" {
		t.Errorf("image.create args = %v (tool %s)", args, tool)
	}
	// audio.speak uses the `text` field, not prompt
	tool, args = argsForAction(find("audio", "speak"), "hello")
	if tool != "audio" || args["action"] != "speak" || args["text"] != "hello" {
		t.Errorf("audio.speak args = %v", args)
	}
	if _, hasPrompt := args["prompt"]; hasPrompt {
		t.Error("audio.speak must use text, not prompt")
	}
}

func TestRunnableGate(t *testing.T) {
	runnable, gated := 0, 0
	for _, g := range catalog {
		for _, a := range g.actions {
			if a.runnable() {
				if a.promptField == "" {
					t.Errorf("%s.%s runnable but no promptField", a.tool, a.action)
				}
				runnable++
			} else {
				if a.promptField != "" {
					t.Errorf("%s.%s has promptField but reported not runnable", a.tool, a.action)
				}
				gated++
			}
		}
	}
	if runnable < 5 {
		t.Errorf("expected ≥5 runnable create actions, got %d", runnable)
	}
	if gated == 0 {
		t.Error("expected some form-gated actions")
	}
}

func TestBuildPaletteCmds(t *testing.T) {
	cmds := buildPaletteCmds()
	if len(cmds) == 0 {
		t.Fatal("palette has no commands")
	}

	// Every catalog action must appear in the palette (meta cmds + catalog).
	catalogTotal := 0
	for _, g := range catalog {
		catalogTotal += len(g.actions)
	}
	// palette = metaCmds + catalog actions (all included, none skipped)
	want := len(metaCmds) + catalogTotal
	if len(cmds) != want {
		t.Errorf("palette len = %d, want %d (meta=%d + catalog=%d)",
			len(cmds), want, len(metaCmds), catalogTotal)
	}

	// Verify routing rules for each catalog command (priority: immediate > form > prompt).
	for _, c := range cmds {
		if c.spec == nil {
			continue // meta command — kind is set explicitly, not derived
		}
		switch {
		case c.spec.immediate:
			if c.kind != cmdImmediate {
				t.Errorf("%s: immediate=true but kind=%v, want cmdImmediate", c.id, c.kind)
			}
		case c.spec.hasForm():
			if c.kind != cmdNeedsForm {
				t.Errorf("%s: hasForm but kind=%v, want cmdNeedsForm", c.id, c.kind)
			}
		default:
			if c.kind != cmdNeedsPrompt {
				t.Errorf("%s: fallback action but kind=%v, want cmdNeedsPrompt", c.id, c.kind)
			}
		}
	}

	// The read-only immediate allowlist must be exactly cmdImmediate.
	immediateWant := []string{
		"billing·balance", "billing·plans", "billing·plan",
		"files·list",
		"org·info", "org·members", "org·spend",
	}
	byID := map[string]paletteCmd{}
	for _, c := range cmds {
		byID[c.id] = c
	}
	for _, id := range immediateWant {
		c, ok := byID[id]
		if !ok {
			t.Errorf("immediate action %q not found in palette", id)
			continue
		}
		if c.kind != cmdImmediate {
			t.Errorf("%s: want cmdImmediate, got %v", id, c.kind)
		}
	}

	// Generation action (image·create) must be cmdNeedsPrompt, NOT cmdImmediate.
	if c, ok := byID["image·create"]; !ok {
		t.Error("image·create missing from palette")
	} else if c.kind != cmdNeedsPrompt {
		t.Errorf("image·create: want cmdNeedsPrompt, got %v", c.kind)
	}

	// IDs must be unique.
	seen := map[string]bool{}
	for _, c := range cmds {
		if seen[c.id] {
			t.Errorf("duplicate palette command id: %q", c.id)
		}
		seen[c.id] = true
	}
}

// TestArgsForImmediateAction verifies that argsForImmediateAction builds
// minimal args (just the action field, no prompt).
func TestArgsForImmediateAction(t *testing.T) {
	find := func(tool, action string) actionSpec {
		for _, g := range catalog {
			for _, a := range g.actions {
				if a.tool == tool && a.action == action {
					return a
				}
			}
		}
		t.Fatalf("%s.%s not in catalog", tool, action)
		return actionSpec{}
	}
	tool, args := argsForImmediateAction(find("billing", "balance"))
	if tool != "billing" {
		t.Errorf("tool = %q, want billing", tool)
	}
	if args["action"] != "balance" {
		t.Errorf("action = %v, want balance", args["action"])
	}
	if _, hasPrompt := args["prompt"]; hasPrompt {
		t.Error("immediate action args must not include prompt field")
	}
}

// TestImmediateFlagAllowlist checks that only the explicit allowlist carries
// immediate=true, and that generation actions do not.
func TestImmediateFlagAllowlist(t *testing.T) {
	allowlist := map[string]bool{
		"billing.balance": true, "billing.plans": true, "billing.plan": true,
		"files.list": true,
		"org.info": true, "org.members": true, "org.spend": true,
	}
	for _, g := range catalog {
		for _, a := range g.actions {
			key := a.tool + "." + a.action
			wantImmediate := allowlist[key]
			if a.immediate != wantImmediate {
				t.Errorf("%s: immediate=%v, want %v", key, a.immediate, wantImmediate)
			}
		}
	}
}

// TestShiftTabCyclesType verifies that shift+tab advances the genType and
// updates the action to the appropriate default.
func TestShiftTabCyclesType(t *testing.T) {
	m := newTestModel()
	// Should start on image (typeImage).
	if m.genType != typeImage {
		t.Fatalf("initial genType = %v, want typeImage", m.genType)
	}
	if m.action.tool != "image" || m.action.action != "create" {
		t.Fatalf("initial action = %s.%s, want image.create", m.action.tool, m.action.action)
	}

	// Shift+Tab once → video.
	nm, _ := m.updateInput(tea.KeyMsg{Type: tea.KeyShiftTab})
	m = nm.(model)
	if m.genType != typeVideo {
		t.Errorf("after 1 shift+tab: genType = %v, want typeVideo", m.genType)
	}
	if m.action.tool != "video" || m.action.action != "scene" {
		t.Errorf("after 1 shift+tab: action = %s.%s, want video.scene", m.action.tool, m.action.action)
	}

	// Shift+Tab again → audio.
	nm, _ = m.updateInput(tea.KeyMsg{Type: tea.KeyShiftTab})
	m = nm.(model)
	if m.genType != typeAudio {
		t.Errorf("after 2 shift+tab: genType = %v, want typeAudio", m.genType)
	}
	if m.action.tool != "audio" || m.action.action != "speak" {
		t.Errorf("after 2 shift+tab: action = %s.%s, want audio.speak", m.action.tool, m.action.action)
	}

	// Shift+Tab again → wraps back to image.
	nm, _ = m.updateInput(tea.KeyMsg{Type: tea.KeyShiftTab})
	m = nm.(model)
	if m.genType != typeImage {
		t.Errorf("after 3 shift+tab: genType = %v, want typeImage (wrap)", m.genType)
	}
	if m.action.tool != "image" || m.action.action != "create" {
		t.Errorf("after 3 shift+tab: action = %s.%s, want image.create", m.action.tool, m.action.action)
	}
}
