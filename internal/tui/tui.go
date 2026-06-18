// Package tui implements the interactive Framehood studio — the "beautiful
// terminal client" mode launched when the binary is run with no subcommand.
package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"runtime"
	"strings"
	"time"

	"github.com/Framehood/framehood-cli/internal/mcp"
	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/table"
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
	help    help.Model
	hist    table.Model
	keys    keyMap
	focus   focusZone
	kindIdx int
	phase   phase
	status  string
	balance string
	result  string
	errMsg  string
	jobID   string
	started time.Time
	history []historyItem // chronological (append order)
	rows    []historyItem // mirrors the table, newest-first; index by hist.Cursor()
	notice  string        // transient action feedback ("copied", "saved → …")
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
		help:     help.New(),
		hist:     newHistoryTable(),
		keys:     defaultKeys(),
		focus:    zoneInput, // start ready to type
		balance:  "…",
	}
	_, err := tea.NewProgram(m, tea.WithAltScreen()).Run()
	return err
}

// newHistoryTable builds the OUTPUT-pane history table. Rows are filled later
// from the generation history (newest first).
func newHistoryTable() table.Model {
	t := table.New(
		table.WithColumns([]table.Column{
			{Title: "", Width: 2},
			{Title: "type", Width: 7},
			{Title: "prompt", Width: 44},
		}),
		table.WithFocused(true),
		table.WithHeight(6),
	)
	s := table.DefaultStyles()
	s.Header = s.Header.Foreground(colDim).Bold(true).BorderBottom(false)
	s.Cell = s.Cell.Foreground(colText)
	s.Selected = s.Selected.Foreground(colInk).Background(colAccent).Bold(false)
	t.SetStyles(s)
	return t
}

// rebuildHistory refreshes the table rows (newest first) and the parallel
// `rows` slice used to resolve the selection, keeping the newest row selected.
func (m *model) rebuildHistory() {
	cols := tableWidth(m.width)
	m.hist.SetColumns([]table.Column{
		{Title: "", Width: 2},
		{Title: "type", Width: 7},
		{Title: "prompt", Width: cols},
	})
	rows := make([]table.Row, 0, len(m.history))
	items := make([]historyItem, 0, len(m.history))
	for i := len(m.history) - 1; i >= 0; i-- {
		h := m.history[i]
		dot := "●"
		items = append(items, h)
		rows = append(rows, table.Row{dot, h.kind, truncate(h.prompt, cols-1)})
	}
	m.rows = items
	m.hist.SetRows(rows)
	if len(rows) > 0 {
		m.hist.SetCursor(0) // newest
	}
	h := len(rows)
	if h > 6 {
		h = 6
	}
	if h < 1 {
		h = 1
	}
	m.hist.SetHeight(h + 1) // + header
}

func tableWidth(w int) int {
	pw := w - 2 - 2 - 7 - 6 // margins + status + type cols + padding
	if pw < 20 {
		pw = 20
	}
	if pw > 60 {
		pw = 60
	}
	return pw
}

// selectedItem returns the history row the OUTPUT cursor points at.
func (m model) selectedItem() (historyItem, bool) {
	i := m.hist.Cursor()
	if i < 0 || i >= len(m.rows) {
		return historyItem{}, false
	}
	return m.rows[i], true
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
type savedMsg struct {
	path string
	err  error
}

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
		m.help.Width = msg.Width - 4
		m.rebuildHistory() // reflow the prompt column to the new width

	case tea.KeyMsg:
		// Global keys (any zone). Only non-typed keys live here, so they never
		// steal characters from the prompt field.
		switch {
		case key.Matches(msg, m.keys.ForceQuit):
			return m, tea.Quit
		case key.Matches(msg, m.keys.Tab):
			return m.cycleFocus(1), nil
		case key.Matches(msg, m.keys.ShiftTab):
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

	case savedMsg:
		if msg.err != nil {
			m.notice = styRed.Render("save failed: " + msg.err.Error())
		} else {
			m.notice = styGreen.Render("saved → " + msg.path)
		}
		return m, nil

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
	switch {
	case key.Matches(msg, m.keys.Quit, m.keys.Esc):
		return m, tea.Quit
	case key.Matches(msg, m.keys.Help):
		m.help.ShowAll = !m.help.ShowAll
	case key.Matches(msg, m.keys.Left):
		if m.phase != phaseWorking {
			m.kindIdx = (m.kindIdx - 1 + len(kinds)) % len(kinds)
		}
	case key.Matches(msg, m.keys.Right):
		if m.phase != phaseWorking {
			m.kindIdx = (m.kindIdx + 1) % len(kinds)
		}
	case key.Matches(msg, m.keys.Write): // enter
		return m.setFocus(zoneInput), nil // jump straight to typing
	}
	return m, nil
}

// updateOutput: the history table is focused — ↑↓ select a past generation and
// o/c/s act on it. The input is blurred, so these are verbs, not typed text.
func (m model) updateOutput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keys.Quit, m.keys.Esc):
		return m, tea.Quit
	case key.Matches(msg, m.keys.Help):
		m.help.ShowAll = !m.help.ShowAll
		return m, nil
	case key.Matches(msg, m.keys.Up, m.keys.Down):
		m.notice = ""
		m.hist, _ = m.hist.Update(msg) // let the table move its cursor
		return m, nil
	case key.Matches(msg, m.keys.Open):
		if it, ok := m.selectedItem(); ok && it.url != "" {
			if err := openBrowser(it.url); err != nil {
				m.notice = styRed.Render("couldn't open: " + err.Error())
			} else {
				m.notice = styGreen.Render("opened in browser")
			}
		}
		return m, nil
	case key.Matches(msg, m.keys.Copy):
		if it, ok := m.selectedItem(); ok && it.url != "" {
			if err := copyToClipboard(it.url); err != nil {
				m.notice = styRed.Render("copy failed: " + err.Error())
			} else {
				m.notice = styGreen.Render("copied url to clipboard")
			}
		}
		return m, nil
	case key.Matches(msg, m.keys.Save):
		if it, ok := m.selectedItem(); ok && it.url != "" {
			m.notice = styDim.Render("saving…")
			return m, saveCmd(it.url)
		}
		return m, nil
	case key.Matches(msg, m.keys.New): // enter
		if m.phase != phaseWorking {
			m.phase = phaseIdle
			m.result, m.errMsg, m.jobID, m.notice = "", "", "", ""
			m.input.SetValue("")
			return m.setFocus(zoneInput), nil
		}
	}
	return m, nil
}

// updateInput: the prompt field is focused — text editing happens here, and the
// only control keys are esc (leave) and enter (submit).
func (m model) updateInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keys.Esc):
		return m.setFocus(zoneTabs), nil
	case key.Matches(msg, m.keys.Generate): // enter
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
		m.rebuildHistory()
		return m.setFocus(zoneOutput), loadBalanceCmd(m.client)
	}
	m.phase = phaseDone
	m.result = j.ResultURL()
	m.notice = ""
	m.history = append(m.history, historyItem{kind: kinds[m.kindIdx], prompt: m.input.Value(), url: m.result})
	m.rebuildHistory()
	// Auto-focus the output so o/c/s act on the just-finished (newest) row.
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

// copyToClipboard writes s to the OS clipboard via the platform tool (no extra
// dependency). On Linux this needs xclip or xsel installed.
func copyToClipboard(s string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("pbcopy")
	case "windows":
		cmd = exec.Command("clip")
	default:
		if _, err := exec.LookPath("xclip"); err == nil {
			cmd = exec.Command("xclip", "-selection", "clipboard")
		} else if _, err := exec.LookPath("xsel"); err == nil {
			cmd = exec.Command("xsel", "--clipboard", "--input")
		} else {
			return fmt.Errorf("no clipboard tool (install xclip or xsel)")
		}
	}
	cmd.Stdin = strings.NewReader(s)
	return cmd.Run()
}

// saveCmd downloads the result URL into the current directory, off the UI thread.
func saveCmd(rawURL string) tea.Cmd {
	return func() tea.Msg {
		path, err := saveResult(rawURL)
		return savedMsg{path: path, err: err}
	}
}

// outputFilename derives a local filename from a result URL — the path's base,
// with query/fragment dropped — falling back to a generic name.
func outputFilename(rawURL string) string {
	name := ""
	if u, err := url.Parse(rawURL); err == nil {
		name = path.Base(u.Path)
	}
	if name == "" || name == "." || name == "/" || name == ".." {
		return "framehood_output"
	}
	return name
}

// saveResult downloads rawURL to ./<basename>, returning the written path.
func saveResult(rawURL string) (string, error) {
	name := outputFilename(rawURL)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("http %d", resp.StatusCode)
	}
	f, err := os.Create(name)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := io.Copy(f, resp.Body); err != nil {
		return "", err
	}
	return name, nil
}
