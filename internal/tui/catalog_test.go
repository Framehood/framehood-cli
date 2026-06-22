package tui

import "testing"

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

	// Runnable catalog actions must be cmdNeedsPrompt.
	// Form-only catalog actions must be cmdNeedsForm.
	for _, c := range cmds {
		if c.spec == nil {
			continue // meta command
		}
		switch {
		case c.spec.runnable():
			if c.kind != cmdNeedsPrompt {
				t.Errorf("%s: runnable but kind=%v, want cmdNeedsPrompt", c.id, c.kind)
			}
		case c.spec.hasForm():
			if c.kind != cmdNeedsForm {
				t.Errorf("%s: hasForm but kind=%v, want cmdNeedsForm", c.id, c.kind)
			}
		default:
			// read/manage actions without a form — kind is cmdNeedsPrompt (immediate)
			if c.kind != cmdNeedsPrompt {
				t.Errorf("%s: immediate action but kind=%v, want cmdNeedsPrompt", c.id, c.kind)
			}
		}
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
