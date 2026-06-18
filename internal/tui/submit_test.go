package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func keyEnter() tea.KeyMsg    { return tea.KeyMsg{Type: tea.KeyEnter} }
func enterForm(m model) model { nm, _ := m.updateForm(keyEnter()); return nm.(model) }

func findAction(t *testing.T, tool, action string) actionSpec {
	t.Helper()
	for _, g := range catalog {
		for _, a := range g.actions {
			if a.tool == tool && a.action == action {
				return a
			}
		}
	}
	t.Fatalf("%s.%s missing from catalog", tool, action)
	return actionSpec{}
}

func TestSubmit_GuardsNonRunnable(t *testing.T) {
	m := newTestModel()
	m.focus = zoneInput
	m.action = findAction(t, "image", "edit") // needs image_url → not runnable
	m.input.SetValue("make it pop")

	nm, _ := m.updateInput(tea.KeyMsg{Type: tea.KeyEnter})
	got := nm.(model)
	if got.phase == phaseWorking {
		t.Error("a non-runnable action must not start a job")
	}
	if got.focus != zoneTabs {
		t.Errorf("should route back to NAV, focus=%v", got.focus)
	}
}

func TestSubmit_RunnableCapturesInflight(t *testing.T) {
	m := newTestModel()
	m.focus = zoneInput
	m.action = findAction(t, "audio", "speak")
	m.input.SetValue("hello there")

	nm, cmd := m.updateInput(tea.KeyMsg{Type: tea.KeyEnter})
	got := nm.(model)
	if got.phase != phaseWorking {
		t.Fatal("a runnable action with a prompt should start working")
	}
	if got.inflight.tool != "audio" || got.inflight.action != "speak" {
		t.Errorf("inflight = %s.%s, want audio.speak", got.inflight.tool, got.inflight.action)
	}
	if cmd == nil {
		t.Error("expected a submit command")
	}
}

func TestArgsForForm(t *testing.T) {
	// two media fields → flat args + default out
	tool, args := argsForForm(findAction(t, "video", "lipsync"),
		map[string]string{"video_url": "https://x/v.mp4", "audio_url": "https://x/a.mp3"})
	if tool != "video" || args["action"] != "lipsync" || args["video_url"] != "https://x/v.mp4" ||
		args["audio_url"] != "https://x/a.mp3" || args["out"] != "video.mp4" {
		t.Errorf("lipsync args = %v", args)
	}
	// media-list field splits + trims on commas → []string
	_, a2 := argsForForm(findAction(t, "audio", "mix"),
		map[string]string{"tracks": "https://x/1.mp3, https://x/2.mp3 ,"})
	list, ok := a2["tracks"].([]string)
	if !ok || len(list) != 2 || list[0] != "https://x/1.mp3" || list[1] != "https://x/2.mp3" {
		t.Errorf("tracks = %#v", a2["tracks"])
	}
	// empty optional field is omitted
	_, a3 := argsForForm(findAction(t, "image", "animate"),
		map[string]string{"image_url": "https://x/i.jpg", "prompt": ""})
	if _, has := a3["prompt"]; has {
		t.Error("empty field must be omitted from args")
	}
}

func TestFormFlow(t *testing.T) {
	m := newTestModel().startForm(findAction(t, "image", "edit")) // image_url + prompt
	if len(m.formFields) != 2 || m.formIdx != 0 {
		t.Fatalf("startForm: fields=%d idx=%d", len(m.formFields), m.formIdx)
	}
	m.input.SetValue("https://x/i.jpg")
	m = enterForm(m) // advance to field 1
	if m.formIdx != 1 || m.formVals["image_url"] != "https://x/i.jpg" {
		t.Fatalf("advance: idx=%d vals=%v", m.formIdx, m.formVals)
	}
	// last field empty → must not submit
	m.input.SetValue("")
	m = enterForm(m)
	if m.phase == phaseWorking {
		t.Error("must not submit with a missing required field")
	}
	// fill it → submits + exits form mode
	m.input.SetValue("make it night")
	nm, cmd := m.updateForm(keyEnter())
	m = nm.(model)
	if m.phase != phaseWorking || cmd == nil {
		t.Error("should submit when complete")
	}
	if len(m.formFields) != 0 {
		t.Error("form mode should end after submit")
	}
}
