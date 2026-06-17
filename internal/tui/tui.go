// Package tui implements the interactive Framehood studio — the "beautiful
// terminal client" mode launched when the binary is run with no subcommand.
package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/Framehood/framehood-cli/internal/mcp"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// kinds are the generation types selectable in the studio.
var kinds = []string{"image", "video", "audio"}

type phase int

const (
	phaseIdle phase = iota
	phaseWorking
	phaseDone
	phaseError
)

// focusZone is the pane currently receiving keys. This is the fix for the old
// always-focused-input model: keys are routed by zone, so text editing, tab
// switching, and result actions never collide.
type focusZone int

const (
	zoneTabs   focusZone = iota // image/video/audio selector — h/l or ←/→ switches
	zoneInput                   // the prompt field — text editing happens ONLY here
	zoneOutput                  // the result/history — o/c act here (input is blurred)
)

const numZones = 3

type historyItem struct {
	kind   string
	prompt string
	url    string
	failed bool
}

type model struct {
	client   *mcp.Client
	email    string
	loggedIn bool

	input   textinput.Model
	spin    spinner.Model
	focus   focusZone
	kindIdx int
	phase   phase
	status  string
	balance string
	result  string
	errMsg  string
	jobID   string
	started time.Time
	history []historyItem
	width   int
	height  int
}

// Run starts the interactive studio.
func Run(client *mcp.Client, email string) error {
	ti := textinput.New()
	ti.Placeholder = "Describe what to create…"
	ti.Focus()
	ti.CharLimit = 1000
	ti.Prompt = "› "

	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = lipgloss.NewStyle().Foreground(colAccent)

	m := model{
		client:   client,
		email:    email,
		loggedIn: client != nil,
		input:    ti,
		spin:     sp,
		focus:    zoneInput, // start ready to type
		balance:  "…",
	}
	_, err := tea.NewProgram(m, tea.WithAltScreen()).Run()
	return err
}

func (m model) Init() tea.Cmd {
	if !m.loggedIn {
		return m.spin.Tick // signed-out: no balance to load
	}
	return tea.Batch(m.spin.Tick, loadBalanceCmd(m.client))
}

// --- messages ---

type balanceMsg struct{ text string }
type submittedMsg struct {
	job mcp.Job
	err error
}
type polledMsg struct {
	job mcp.Job
	err error
}
type pollTickMsg struct{ jobID string }

// setFocus moves focus to z, keeping the textinput's Focus/Blur in sync so the
// raw key stream only reaches the input when the input zone is active.
func (m model) setFocus(z focusZone) model {
	m.focus = z
	if z == zoneInput {
		m.input.Focus()
	} else {
		m.input.Blur()
	}
	return m
}

func (m model) cycleFocus(dir int) model {
	return m.setFocus(focusZone((int(m.focus) + dir + numZones) % numZones))
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.input.Width = msg.Width - 6

	case tea.KeyMsg:
		// Global keys (any zone).
		switch msg.String() {
		case "ctrl+c":
			return m, tea.Quit
		case "tab":
			return m.cycleFocus(1), nil
		case "shift+tab":
			return m.cycleFocus(-1), nil
		}
		// Zone-routed keys.
		switch m.focus {
		case zoneTabs:
			return m.updateTabs(msg)
		case zoneOutput:
			return m.updateOutput(msg)
		default: // zoneInput
			return m.updateInput(msg)
		}

	case balanceMsg:
		m.balance = msg.text
		return m, nil

	case submittedMsg:
		if msg.err != nil {
			m.phase = phaseError
			m.errMsg = msg.err.Error()
			return m.setFocus(zoneOutput), nil
		}
		return m.handleJob(msg.job)

	case polledMsg:
		if msg.err != nil {
			m.phase = phaseError
			m.errMsg = msg.err.Error()
			return m.setFocus(zoneOutput), nil
		}
		return m.handleJob(msg.job)

	case pollTickMsg:
		return m, pollCmd(m.client, msg.jobID)

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd
	}

	// Non-key messages while editing still feed the input (e.g. paste).
	if m.focus == zoneInput && m.phase != phaseWorking {
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		return m, cmd
	}
	return m, nil
}

// updateTabs: the image/video/audio selector is focused.
func (m model) updateTabs(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "esc":
		return m, tea.Quit
	case "left", "h":
		if m.phase != phaseWorking {
			m.kindIdx = (m.kindIdx - 1 + len(kinds)) % len(kinds)
		}
	case "right", "l":
		if m.phase != phaseWorking {
			m.kindIdx = (m.kindIdx + 1) % len(kinds)
		}
	case "enter":
		return m.setFocus(zoneInput), nil // jump straight to typing
	}
	return m, nil
}

// updateOutput: the result/history is focused — action keys live here, and the
// input is blurred, so 'o' finally opens the browser instead of typing "o".
func (m model) updateOutput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "esc":
		return m, tea.Quit
	case "o":
		if m.phase == phaseDone && m.result != "" {
			_ = openBrowser(m.result)
		}
	case "enter":
		// Start a fresh prompt.
		if m.phase != phaseWorking {
			m.phase = phaseIdle
			m.result, m.errMsg, m.jobID = "", "", ""
			m.input.SetValue("")
			return m.setFocus(zoneInput), nil
		}
	}
	return m, nil
}

// updateInput: the prompt field is focused — text editing happens here, and the
// only control keys are esc (leave) and enter (submit).
func (m model) updateInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		return m.setFocus(zoneTabs), nil
	case "enter":
		if m.phase == phaseWorking {
			return m, nil
		}
		if !m.loggedIn {
			m.phase = phaseError
			m.errMsg = "You're not signed in. Quit and run `framehood login` first."
			return m.setFocus(zoneOutput), nil
		}
		prompt := strings.TrimSpace(m.input.Value())
		if prompt == "" {
			return m, nil
		}
		m.phase = phaseWorking
		m.status = "submitting"
		m.result, m.errMsg, m.jobID = "", "", ""
		m.started = time.Now()
		return m, tea.Batch(m.spin.Tick, submitCmd(m.client, kinds[m.kindIdx], prompt))
	}
	if m.phase != phaseWorking {
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		return m, cmd
	}
	return m, nil
}

// handleJob advances the state machine from a freshly observed job.
func (m model) handleJob(j mcp.Job) (tea.Model, tea.Cmd) {
	m.jobID = j.ID
	if !j.Terminal() {
		m.status = j.Status
		if m.status == "" {
			m.status = "working"
		}
		// Schedule the next poll after a short delay.
		return m, tea.Batch(m.spin.Tick, tea.Tick(2*time.Second, func(time.Time) tea.Msg {
			return pollTickMsg{jobID: j.ID}
		}))
	}
	if j.Status == "failed" {
		m.phase = phaseError
		m.errMsg = "job failed: " + strings.TrimSpace(string(j.Error))
		m.history = append(m.history, historyItem{kind: kinds[m.kindIdx], prompt: m.input.Value(), failed: true})
		return m.setFocus(zoneOutput), loadBalanceCmd(m.client)
	}
	m.phase = phaseDone
	m.result = j.ResultURL()
	m.history = append(m.history, historyItem{kind: kinds[m.kindIdx], prompt: m.input.Value(), url: m.result})
	// Auto-focus the output so 'o' opens the result immediately.
	return m.setFocus(zoneOutput), loadBalanceCmd(m.client)
}

// --- commands ---

func loadBalanceCmd(c *mcp.Client) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		raw, err := c.Balance(ctx)
		if err != nil {
			return balanceMsg{text: "—"}
		}
		return balanceMsg{text: formatBalance(raw)}
	}
}

func submitCmd(c *mcp.Client, kind, prompt string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		tool, args := argsFor(kind, prompt)
		job, err := c.Submit(ctx, tool, args)
		return submittedMsg{job: job, err: err}
	}
}

func pollCmd(c *mcp.Client, jobID string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		job, err := c.GetStatus(ctx, jobID)
		return polledMsg{job: job, err: err}
	}
}

// argsFor maps a studio kind + prompt to an MCP tool call.
func argsFor(kind, prompt string) (string, map[string]any) {
	switch kind {
	case "audio":
		return "audio", map[string]any{"action": "speak", "text": prompt, "out": "audio.mp3"}
	case "video":
		return "video", map[string]any{"action": "create", "prompt": prompt, "out": "video.mp4"}
	default:
		return "image", map[string]any{"action": "create", "prompt": prompt, "out": "image.jpg"}
	}
}

func formatBalance(raw json.RawMessage) string {
	var v map[string]any
	if err := json.Unmarshal(raw, &v); err != nil {
		return strings.TrimSpace(string(raw))
	}
	if b, ok := v["balance"]; ok {
		return fmt.Sprintf("%v credits", b)
	}
	return strings.TrimSpace(string(raw))
}

func openBrowser(target string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", target).Start()
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", target).Start()
	default:
		return exec.Command("xdg-open", target).Start()
	}
}
