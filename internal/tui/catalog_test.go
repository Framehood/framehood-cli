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
		"org.info":   true, "org.members": true, "org.spend": true,
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

// TestWorkActionsRing verifies the Shift+Tab ring is built from the catalog,
// excludes every service/read/immediate action, and contains the expected
// generation/manipulation/QA actions.
func TestWorkActionsRing(t *testing.T) {
	if len(workActions) == 0 {
		t.Fatal("workActions ring is empty")
	}

	// The ring must NEVER include a service group or any read/immediate action.
	for _, a := range workActions {
		if serviceTools[a.tool] {
			t.Errorf("work ring contains service action %s·%s (group %q is service-only)",
				a.tool, a.action, a.tool)
		}
		if a.immediate {
			t.Errorf("work ring contains immediate read-action %s·%s", a.tool, a.action)
		}
		if a.kind == kindRead {
			t.Errorf("work ring contains read action %s·%s", a.tool, a.action)
		}
	}

	// First entry is image·create (the startup default).
	if workActions[0].tool != "image" || workActions[0].action != "create" {
		t.Errorf("workActions[0] = %s·%s, want image·create",
			workActions[0].tool, workActions[0].action)
	}

	// A representative sample of actions that MUST be present.
	wantPresent := []string{
		"image·create", "image·edit", "image·upscale", "image·animate", "image·actor_sheet",
		"video·create", "video·edit", "video·lipsync", "video·scene",
		"audio·speak", "audio·sfx", "audio·music", "audio·mix", "audio·concat",
		"actor·create", "actor·update", "actor·batch",
		"qa·full", "qa·person", "qa·transcript", "qa·image",
	}
	have := map[string]bool{}
	for _, a := range workActions {
		have[a.tool+"·"+a.action] = true
	}
	for _, id := range wantPresent {
		if !have[id] {
			t.Errorf("work ring missing expected action %q", id)
		}
	}

	// Actions that MUST NOT be present (service + read).
	wantAbsent := []string{
		"billing·balance", "billing·plans", "billing·plan", "billing·subscribe",
		"org·info", "org·members", "org·spend", "org·invite",
		"files·list", "files·upload", "get_status·check",
		"actor·list", "actor·get", // kindRead
	}
	for _, id := range wantAbsent {
		if have[id] {
			t.Errorf("work ring must not contain %q", id)
		}
	}
}

// TestTabCyclesWorkActions verifies plain Tab advances FORWARD through the work
// ring (the primary, no-Shift cycle), Shift+Tab reverses it, both wrap, and
// neither ever lands on a service/billing/org/files/get_status or read action.
func TestTabCyclesWorkActions(t *testing.T) {
	m := newTestModel()
	// Startup default is the first work action: image·create.
	if m.action.tool != "image" || m.action.action != "create" {
		t.Fatalf("initial action = %s·%s, want image·create", m.action.tool, m.action.action)
	}

	// Walk the entire ring forward via plain Tab; it must return to the start
	// after len(workActions) presses and never visit a service/read action.
	n := len(workActions)
	seen := map[string]bool{}
	cur := m
	for i := 0; i < n; i++ {
		nm, _ := cur.updateInput(tea.KeyMsg{Type: tea.KeyTab})
		cur = nm.(model)
		a := cur.action
		if serviceTools[a.tool] || a.immediate || a.kind == kindRead {
			t.Fatalf("after %d tab: landed on non-work action %s·%s", i+1, a.tool, a.action)
		}
		seen[a.tool+"·"+a.action] = true
	}
	if cur.action.tool != "image" || cur.action.action != "create" {
		t.Errorf("after a full forward loop: action = %s·%s, want image·create (wrap)",
			cur.action.tool, cur.action.action)
	}
	if len(seen) != n {
		t.Errorf("forward loop visited %d distinct actions, want %d (the whole ring)", len(seen), n)
	}

	// One Tab forward then one Shift+Tab back returns to image·create.
	fwd, _ := m.updateInput(tea.KeyMsg{Type: tea.KeyTab})
	mf := fwd.(model)
	if mf.action.tool == "image" && mf.action.action == "create" {
		t.Fatalf("tab did not advance off image·create: %s·%s", mf.action.tool, mf.action.action)
	}
	back, _ := mf.updateInput(tea.KeyMsg{Type: tea.KeyShiftTab})
	mb := back.(model)
	if mb.action.tool != "image" || mb.action.action != "create" {
		t.Errorf("tab then shift+tab = %s·%s, want image·create", mb.action.tool, mb.action.action)
	}

	// Shift+Tab from the first action wraps backward to the LAST work action.
	back2, _ := m.updateInput(tea.KeyMsg{Type: tea.KeyShiftTab})
	mb2 := back2.(model)
	last := workActions[n-1]
	if mb2.action.tool != last.tool || mb2.action.action != last.action {
		t.Errorf("shift+tab from first action = %s·%s, want last %s·%s",
			mb2.action.tool, mb2.action.action, last.tool, last.action)
	}
}
