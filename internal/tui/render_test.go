package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/help"
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
	nav, groupTop := buildNav()
	m := model{
		email: "kirill@framehood.ai", loggedIn: true, input: ti, spin: sp,
		help: help.New(), hist: newHistoryTable(), keys: defaultKeys(),
		nav: nav, groupTop: groupTop, action: catalog[0].actions[0],
		balance: "1,640 credits", width: 78,
		history: []historyItem{{kind: "image·create", prompt: "a red fox in the snow"}},
	}
	m.nav.SetSize(74, 8)
	m.rebuildHistory(true)
	return m
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
	url := "https://cdn.framehood.ai/job_abc.jpg"
	// A finished job: a history row carrying the result URL, selected (newest).
	m.history = []historyItem{{kind: "image", prompt: "a red fox in the snow", url: url}}
	m.rebuildHistory(true)
	m.phase = phaseDone
	m.result = url
	out := m.View()
	if !strings.Contains(out, "done") {
		t.Error("done view missing 'done'")
	}
	if !strings.Contains(out, url) {
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
