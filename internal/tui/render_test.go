package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// newTestModel builds a signed-in model for view tests. Color is stripped so
// assertions can match the rendered text directly.
func newTestModel() model {
	lipgloss.SetColorProfile(termenv.Ascii)
	ti := textinput.New()
	ti.SetValue("a red fox in the snow")
	sp := spinner.New()
	return model{
		email: "kirill@framehood.ai", loggedIn: true, input: ti, spin: sp,
		balance: "1,640 credits", width: 78, kindIdx: 0,
		history: []historyItem{{kind: "image", prompt: "a red fox in the snow"}},
	}
}

func TestView_AllStatesRender(t *testing.T) {
	base := newTestModel()
	for name, ph := range map[string]phase{"idle": phaseIdle, "working": phaseWorking, "done": phaseDone, "error": phaseError} {
		m := base
		m.phase = ph
		if out := m.View(); out == "" {
			t.Errorf("%s state rendered empty view", name)
		}
	}
}

func TestView_Done_ShowsResult(t *testing.T) {
	m := newTestModel()
	m.phase = phaseDone
	m.result = "https://cdn.framehood.ai/job_abc.jpg"
	out := m.View()
	if !strings.Contains(out, "done") {
		t.Error("done view missing 'done'")
	}
	if !strings.Contains(out, m.result) {
		t.Error("done view missing the result URL")
	}
}

func TestView_SignedOut_ShowsHint(t *testing.T) {
	m := newTestModel()
	m.loggedIn = false
	m.email = ""
	if !strings.Contains(m.View(), "not signed in") {
		t.Error("signed-out view should show 'not signed in'")
	}
}

func TestView_Working_ShowsElapsed(t *testing.T) {
	m := newTestModel()
	m.phase = phaseWorking
	m.status = "running"
	m.started = time.Now().Add(-12 * time.Second)
	out := m.View()
	if !strings.Contains(out, "running") {
		t.Error("working view should show status")
	}
}
