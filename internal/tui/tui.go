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
	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

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

	input    textinput.Model
	spin     spinner.Model
	help     help.Model
	hist     table.Model
	nav      list.Model
	keys     keyMap
	focus    focusZone
	action   actionSpec // the NAV-selected tool action
	inflight actionSpec // action captured at submit time (for history attribution)
	groupTop []int      // nav index of each tool group's first action (for 1-9 jumps)
	phase    phase
	status   string
	balance  string
	result   string
	errMsg   string
	jobID    string
	started  time.Time
	history  []historyItem // chronological (append order)
	rows     []historyItem // mirrors the table, newest-first; index by hist.Cursor()
	notice   string        // transient action feedback ("copied", "saved → …")
	width    int
	height   int
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

	nav, groupTop := buildNav()
	m := model{
		client:   client,
		email:    email,
		loggedIn: client != nil,
		input:    ti,
		spin:     sp,
		help:     help.New(),
		hist:     newHistoryTable(),
		nav:      nav,
		groupTop: groupTop,
		action:   catalog[0].actions[0], // image · create (a runnable default)
		keys:     defaultKeys(),
		focus:    zoneInput, // start ready to type
		balance:  "…",
	}
	_, err := tea.NewProgram(m, tea.WithAltScreen()).Run()
	return err
}

// navItem adapts an actionSpec to the bubbles/list item interface.
type navItem struct{ spec actionSpec }

func (n navItem) Title() string {
	if n.spec.runnable() {
		return n.spec.tool + " · " + n.spec.action
	}
	return n.spec.tool + " · " + n.spec.action + " ›" // › = opens a form (later step)
}
func (n navItem) Description() string { return n.spec.summary }
func (n navItem) FilterValue() string {
	return n.spec.tool + " " + n.spec.action + " " + n.spec.summary
}

// buildNav flattens the catalog into the NAV list and records each tool group's
// first index (for the 1-9 group jumps).
func buildNav() (list.Model, []int) {
	var items []list.Item
	var groupTop []int
	for _, g := range catalog {
		groupTop = append(groupTop, len(items))
		for _, a := range g.actions {
			items = append(items, navItem{a})
		}
	}
	d := list.NewDefaultDelegate()
	d.ShowDescription = false
	d.SetSpacing(0)
	d.Styles.SelectedTitle = d.Styles.SelectedTitle.Foreground(colAccent).BorderForeground(colAccent)
	d.Styles.NormalTitle = d.Styles.NormalTitle.Foreground(colText)
	d.Styles.DimmedTitle = d.Styles.DimmedTitle.Foreground(colDim)

	l := list.New(items, d, 0, 0)
	l.Title = "TOOLS"
	l.Styles.Title = styEyebrow
	l.SetShowStatusBar(false)
	l.SetShowHelp(false)
	l.SetFilteringEnabled(true)
	l.KeyMap.Quit.SetEnabled(false) // the studio owns quit/esc
	return l, groupTop
}

// navSelected returns the actionSpec under the NAV cursor.
func (m model) navSelected() (actionSpec, bool) {
	it, ok := m.nav.SelectedItem().(navItem)
	if !ok {
		return actionSpec{}, false
	}
	return it.spec, true
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
// `rows` slice used to resolve the selection. selectNewest moves the cursor to
// the newest row (job completion); otherwise the current selection is preserved
// (e.g. a resize reflow must not yank the user off the row they were viewing).
func (m *model) rebuildHistory(selectNewest bool) {
	prev := m.hist.Cursor()
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
		switch {
		case selectNewest:
			m.hist.SetCursor(0) // newest
		case prev >= len(rows):
			m.hist.SetCursor(len(rows) - 1)
		case prev < 0:
			m.hist.SetCursor(0)
		default:
			m.hist.SetCursor(prev)
		}
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
		navH := msg.Height - 10
		if navH < 4 {
			navH = 4
		}
		m.nav.SetSize(msg.Width-4, navH)
		m.rebuildHistory(false) // reflow but keep the user's selected row

	case tea.KeyMsg:
		// While the NAV filter input is active it owns every key (so typing a
		// filter never triggers Tab / quit / jumps). ctrl+c still quits.
		if m.focus == zoneTabs && m.nav.SettingFilter() {
			if key.Matches(msg, m.keys.ForceQuit) {
				return m, tea.Quit
			}
			var cmd tea.Cmd
			m.nav, cmd = m.nav.Update(msg)
			return m, cmd
		}
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

// updateTabs: the NAV list is focused — browse/filter the tool→action catalog.
// (Not in filter mode here; filtering is handled before zone routing.)
func (m model) updateTabs(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keys.Quit, m.keys.Esc):
		return m, tea.Quit
	case key.Matches(msg, m.keys.Help):
		m.help.ShowAll = !m.help.ShowAll
		return m, nil
	case msg.String() >= "1" && msg.String() <= "9":
		if g := int(msg.String()[0] - '1'); g < len(m.groupTop) {
			m.nav.Select(m.groupTop[g])
		}
		return m, nil
	case key.Matches(msg, m.keys.Write): // enter → pick this action
		if spec, ok := m.navSelected(); ok {
			m.action = spec
			m.notice = ""
			if spec.runnable() {
				return m.setFocus(zoneInput), nil // go type the prompt
			}
			// Form-driven action: not yet submittable from the prompt box.
			m.notice = styDim.Render("needs " + strings.Join(spec.needs, ", ") +
				" — the input form arrives in the next update")
		}
		return m, nil
	}
	// Everything else (↑↓ j/k, `/` to filter, page keys) drives the list.
	var cmd tea.Cmd
	m.nav, cmd = m.nav.Update(msg)
	return m, cmd
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
		if !m.action.runnable() {
			// Selection moved to a form-driven action — can't submit from the
			// prompt box yet. Send the user back to pick a runnable one.
			m.notice = styDim.Render("needs " + strings.Join(m.action.needs, ", ") +
				" — pick a runnable action")
			return m.setFocus(zoneTabs), nil
		}
		m.phase = phaseWorking
		m.status = "submitting"
		m.result, m.errMsg, m.jobID = "", "", ""
		m.inflight = m.action // attribute the result to the action as it was at submit
		m.started = time.Now()
		tool, args := argsForAction(m.action, prompt)
		return m, tea.Batch(m.spin.Tick, submitCmd(m.client, tool, args))
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
	label := m.inflight.tool + "·" + m.inflight.action
	if label == "·" { // no captured action (shouldn't happen) → fall back
		label = m.action.tool + "·" + m.action.action
	}
	if j.Status == "failed" {
		m.phase = phaseError
		m.errMsg = "job failed: " + strings.TrimSpace(string(j.Error))
		m.history = append(m.history, historyItem{kind: label, prompt: m.input.Value(), failed: true})
		m.rebuildHistory(true)
		return m.setFocus(zoneOutput), loadBalanceCmd(m.client)
	}
	m.phase = phaseDone
	m.result = j.ResultURL()
	m.notice = ""
	m.history = append(m.history, historyItem{kind: label, prompt: m.input.Value(), url: m.result})
	m.rebuildHistory(true)
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

func submitCmd(c *mcp.Client, tool string, args map[string]any) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
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

// saveResult downloads rawURL to a non-colliding path in the current directory,
// returning the written path. It never overwrites an existing file.
func saveResult(rawURL string) (string, error) {
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

	// Create exclusively, picking the next free "name", "name-1", … so repeated
	// saves never clobber an earlier file.
	f, name, err := createNonColliding(outputFilename(rawURL))
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		os.Remove(name)
		return "", err
	}
	if err := f.Close(); err != nil { // surface the final flush error
		os.Remove(name)
		return "", err
	}
	return name, nil
}

// createNonColliding O_EXCL-creates the first free of base, base-1, base-2, …
// in the current directory, returning the open file and its name.
func createNonColliding(base string) (*os.File, string, error) {
	ext := path.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	for i := 0; i < 1000; i++ {
		name := base
		if i > 0 {
			name = fmt.Sprintf("%s-%d%s", stem, i, ext)
		}
		f, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err == nil {
			return f, name, nil
		}
		if !os.IsExist(err) {
			return nil, "", err
		}
	}
	return nil, "", fmt.Errorf("could not find a free filename for %q", base)
}
