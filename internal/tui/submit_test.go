package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

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
