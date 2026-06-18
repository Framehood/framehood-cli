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

func TestBuildNavGroups(t *testing.T) {
	_, groupTop := buildNav()
	if len(groupTop) != len(catalog) {
		t.Fatalf("groupTop len %d, want %d", len(groupTop), len(catalog))
	}
	if groupTop[0] != 0 {
		t.Errorf("first group must start at 0, got %d", groupTop[0])
	}
	// monotonic increasing
	for i := 1; i < len(groupTop); i++ {
		if groupTop[i] <= groupTop[i-1] {
			t.Errorf("groupTop not increasing at %d: %v", i, groupTop)
		}
	}
}
