package tui

import (
	"strings"
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
	m.action = findAction(t, "image", "edit") // needs image_url → not runnable, has form
	m.input.SetValue("make it pop")

	nm, _ := m.updateInput(tea.KeyMsg{Type: tea.KeyEnter})
	got := nm.(model)
	if got.phase == phaseWorking {
		t.Error("a non-runnable action must not start a job directly from the prompt")
	}
	// New behaviour: pressing enter on a form-driven action enters form mode
	// (rather than routing back to the NAV zone which no longer exists).
	if len(got.formFields) == 0 {
		t.Error("enter on form-driven action should open the form (formFields non-empty)")
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

// TestSubmit_BlocksEmptyPrompt verifies a needs-prompt work action does not
// submit when the prompt box is empty (or whitespace), and sets a "needs:"
// notice instead.
func TestSubmit_BlocksEmptyPrompt(t *testing.T) {
	m := newTestModel()
	m.focus = zoneInput
	m.action = findAction(t, "image", "create") // runnable, needs a prompt
	m.input.SetValue("   ")                     // whitespace only

	nm, cmd := m.updateInput(tea.KeyMsg{Type: tea.KeyEnter})
	got := nm.(model)
	if got.phase == phaseWorking {
		t.Error("must not submit a runnable action with an empty prompt")
	}
	if cmd != nil {
		t.Error("no submit command should be issued for an empty prompt")
	}
	if got.notice == "" {
		t.Error("an empty required prompt should set a 'needs:' notice")
	}
	if !strings.Contains(got.notice, "needs") {
		t.Errorf("notice = %q, want it to mention what's needed", got.notice)
	}

	// A non-empty prompt on the same action DOES submit.
	m2 := newTestModel()
	m2.focus = zoneInput
	m2.action = findAction(t, "image", "create")
	m2.input.SetValue("a red fox")
	nm2, cmd2 := m2.updateInput(tea.KeyMsg{Type: tea.KeyEnter})
	if nm2.(model).phase != phaseWorking || cmd2 == nil {
		t.Error("a non-empty prompt should submit the work action")
	}
}

// TestSubmit_FormBlocksMissingRequired verifies a needs-form action does not
// submit while a required field is empty, and that submitForm itself guards.
func TestSubmit_FormBlocksMissingRequired(t *testing.T) {
	// image.edit needs image_url + prompt.
	m := newTestModel().startForm(findAction(t, "image", "edit"))
	m.formVals = map[string]string{"image_url": "https://x/i.jpg", "prompt": ""}

	nm, cmd := m.submitForm()
	got := nm.(model)
	if got.phase == phaseWorking {
		t.Error("submitForm must not submit with a missing required field")
	}
	if cmd != nil {
		t.Error("submitForm must not issue a command when a field is missing")
	}
	if !strings.Contains(got.notice, "needs") {
		t.Errorf("notice = %q, want a 'needs:' list", got.notice)
	}

	// Fill the missing field → it submits.
	got.formFields = m.formFields // submitForm cleared nothing on the blocked path
	got.formVals["prompt"] = "make it night"
	nm2, cmd2 := got.submitForm()
	if nm2.(model).phase != phaseWorking || cmd2 == nil {
		t.Error("submitForm should submit once all required fields are filled")
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

func TestFormMissing_OptionalAndMediaList(t *testing.T) {
	// animate: image_url required, prompt optional → only image_url needed
	m := newTestModel().startForm(findAction(t, "image", "animate"))
	m.formVals = map[string]string{"image_url": "https://x/i.jpg"}
	if miss := m.formMissing(); miss != "" {
		t.Errorf("animate w/ image_url only: missing=%q, want none (prompt optional)", miss)
	}
	// audio.mix tracks is a required media-list — comma-only is NOT valid
	mix := newTestModel().startForm(findAction(t, "audio", "mix"))
	mix.formVals = map[string]string{"tracks": " , ,"}
	if mix.formMissing() == "" {
		t.Error("comma-only media-list must be flagged missing")
	}
	mix.formVals = map[string]string{"tracks": "https://x/1.mp3"}
	if miss := mix.formMissing(); miss != "" {
		t.Errorf("one valid track should pass, missing=%q", miss)
	}
}

func TestFormSummary(t *testing.T) {
	// a text field → summary is that text (history prompt for form submits)
	edit := newTestModel().startForm(findAction(t, "image", "edit"))
	edit.formVals = map[string]string{"image_url": "https://x/i.jpg", "prompt": "make it night"}
	if s := edit.formSummary(); s != "make it night" {
		t.Errorf("edit summary = %q, want the prompt", s)
	}
	// no text field → summary is the media basename
	up := newTestModel().startForm(findAction(t, "image", "upscale"))
	up.formVals = map[string]string{"image_url": "https://x/pic.jpg?t=1"}
	if s := up.formSummary(); s != "pic.jpg" {
		t.Errorf("upscale summary = %q, want pic.jpg", s)
	}
}

func TestChainSeedsForm(t *testing.T) {
	m := newTestModel()
	m.seedURL = "https://cdn.framehood.ai/prev.mp4"
	// chain into video.upscale (first field is the media video_url)
	m = m.startForm(findAction(t, "video", "upscale"))
	if m.formVals["video_url"] != "https://cdn.framehood.ai/prev.mp4" {
		t.Errorf("first media field not seeded: %v", m.formVals)
	}
	if m.seedURL != "" {
		t.Error("seedURL should be cleared after seeding")
	}
	// field 0 input should show the seeded value
	if m.input.Value() != "https://cdn.framehood.ai/prev.mp4" {
		t.Errorf("field 0 input = %q, want the seed", m.input.Value())
	}
}
