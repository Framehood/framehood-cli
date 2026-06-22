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

type phase int

const (
	phaseIdle phase = iota
	phaseWorking
	phaseDone
	phaseError
)

// focusZone is the pane currently receiving keys.
// The palette is a transient overlay on zoneInput, not a separate zone.
type focusZone int

const (
	zoneInput  focusZone = iota // the prompt field — text editing + palette
	zoneOutput                  // the result/history — o/c/s/u act here
)

type historyItem struct {
	kind   string
	prompt string
	url    string
	failed bool
}

// typeIndex is the cycling position for Shift+Tab type selection.
// 0 = image, 1 = video, 2 = audio.
type typeIndex int

const (
	typeImage typeIndex = iota
	typeVideo
	typeAudio
	numTypes = 3
)

// defaultActionForType maps a typeIndex to the catalog action to set.
var defaultActionForType = [numTypes]struct{ tool, action string }{
	typeImage: {"image", "create"},
	typeVideo: {"video", "scene"},
	typeAudio: {"audio", "speak"},
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
	action  actionSpec // currently selected action (shown in composer header)
	inflight      actionSpec // action captured at submit time (for history attribution)
	inflightLabel string     // the submitted prompt/summary, for the history row

	// palette state
	palette paletteState

	// current generation type (cycles with shift+tab)
	genType typeIndex

	// form mode: when formFields is non-empty the prompt box is a sequential
	// per-parameter field editor for a form-driven action.
	formFields []paramSpec
	formIdx    int
	formVals   map[string]string
	seedURL    string // a result URL to chain into the next form's first media field

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
	ti.Placeholder = "type a prompt · / for commands · ⇧⇥ to change type"
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
		action:   catalog[0].actions[0], // image · create (a runnable default)
		keys:     defaultKeys(),
		focus:    zoneInput, // start ready to type
		genType:  typeImage,
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
// `rows` slice used to resolve the selection. selectNewest moves the cursor to
// the newest row (job completion); otherwise the current selection is preserved.
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

// setFocus moves focus to z, keeping the textinput's Focus/Blur in sync.
func (m model) setFocus(z focusZone) model {
	m.focus = z
	if z == zoneInput {
		m.input.Focus()
	} else {
		m.input.Blur()
	}
	return m
}

// findAction looks up an actionSpec by tool + action name.
func findCatalogAction(tool, action string) (actionSpec, bool) {
	for _, g := range catalog {
		for _, a := range g.actions {
			if a.tool == tool && a.action == action {
				return a, true
			}
		}
	}
	return actionSpec{}, false
}

// applyGenType sets m.action to the default action for the current genType.
func (m model) applyGenType() model {
	t := defaultActionForType[m.genType]
	if a, ok := findCatalogAction(t.tool, t.action); ok {
		m.action = a
	}
	return m
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.input.Width = msg.Width - 6
		m.help.Width = msg.Width - 4
		m.rebuildHistory(false)

	case tea.KeyMsg:
		// ctrl+c always quits regardless of state.
		if key.Matches(msg, m.keys.ForceQuit) {
			return m, tea.Quit
		}

		// Palette is open — route all keys there.
		if m.palette.isOpen() {
			return m.updatePalette(msg)
		}

		// Zone-routed keys (palette closed).
		switch m.focus {
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

// updatePalette handles all key events while the palette overlay is open.
func (m model) updatePalette(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keys.Esc):
		// Close palette, restore empty input.
		m.palette = paletteState{}
		m.input.SetValue("")
		return m.setFocus(zoneInput), nil

	case key.Matches(msg, m.keys.Generate): // enter → run selected command
		cmd := m.palette.selected()
		if cmd == nil {
			return m, nil
		}
		return m.runPaletteCmd(cmd)

	case key.Matches(msg, m.keys.PaletteLeft):
		m.palette.moveLeft()
		return m, nil

	case key.Matches(msg, m.keys.PaletteRight):
		m.palette.moveRight()
		return m, nil

	case key.Matches(msg, m.keys.PaletteUp):
		m.palette.moveUp()
		return m, nil

	case key.Matches(msg, m.keys.PaletteDown):
		m.palette.moveDown()
		return m, nil

	default:
		// Any printable character refines the filter query.
		s := msg.String()
		switch s {
		case "backspace", "ctrl+h":
			if len(m.palette.query) > 0 {
				runes := []rune(m.palette.query)
				m.palette.query = string(runes[:len(runes)-1])
				m.palette.refilter()
			}
		default:
			if len(s) == 1 {
				m.palette.query += s
				m.palette.refilter()
			}
		}
		return m, nil
	}
}

// runPaletteCmd executes the selected palette command.
func (m model) runPaletteCmd(cmd *paletteCmd) (tea.Model, tea.Cmd) {
	// Close the palette first.
	m.palette = paletteState{}
	m.input.SetValue("")
	m.notice = ""

	if cmd.meta != "" {
		// Meta (immediate) commands.
		switch cmd.meta {
		case "help":
			m.help.ShowAll = !m.help.ShowAll
			return m.setFocus(zoneInput), nil
		case "new":
			if m.phase != phaseWorking {
				m.phase = phaseIdle
				m.result, m.errMsg, m.jobID, m.notice = "", "", "", ""
			}
			return m.setFocus(zoneInput), nil
		case "open":
			if it, ok := m.selectedItem(); ok && it.url != "" {
				if err := openBrowser(it.url); err != nil {
					m.notice = styRed.Render("couldn't open: " + err.Error())
				} else {
					m.notice = styGreen.Render("opened in browser")
				}
			}
			return m.setFocus(zoneInput), nil
		case "copy":
			if it, ok := m.selectedItem(); ok && it.url != "" {
				if err := copyToClipboard(it.url); err != nil {
					m.notice = styRed.Render("copy failed: " + err.Error())
				} else {
					m.notice = styGreen.Render("copied url to clipboard")
				}
			}
			return m.setFocus(zoneInput), nil
		case "save":
			if it, ok := m.selectedItem(); ok && it.url != "" {
				m.notice = styDim.Render("saving…")
				return m.setFocus(zoneInput), saveCmd(it.url)
			}
			return m.setFocus(zoneInput), nil
		case "quit":
			return m, tea.Quit
		}
		return m.setFocus(zoneInput), nil
	}

	// Catalog action.
	if cmd.spec == nil {
		return m.setFocus(zoneInput), nil
	}
	m.action = *cmd.spec

	switch cmd.kind {
	case cmdImmediate:
		// Read-only, no-required-arg action — submit immediately without prompting.
		if !m.loggedIn {
			m.phase = phaseError
			m.errMsg = "You're not signed in. Quit and run `framehood login` first."
			return m.setFocus(zoneOutput), nil
		}
		m.phase = phaseWorking
		m.status = "submitting"
		m.result, m.errMsg, m.jobID = "", "", ""
		m.inflight = m.action
		m.inflightLabel = m.action.tool + "·" + m.action.action
		m.started = time.Now()
		// Immediate actions have no promptField; build minimal args directly.
		tool, args := argsForImmediateAction(m.action)
		return m, tea.Batch(m.spin.Tick, submitCmd(m.client, tool, args))
	case cmdNeedsForm:
		return m.startForm(*cmd.spec), nil
	default: // cmdNeedsPrompt
		m.formFields = nil
		m.input.Placeholder = "Describe what to create…"
		return m.setFocus(zoneInput), nil
	}
}

// updateOutput: the history table is focused — ↑↓ select a past generation
// and o/c/s act on it.
func (m model) updateOutput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keys.Esc):
		return m.setFocus(zoneInput), nil
	case key.Matches(msg, m.keys.Help):
		m.help.ShowAll = !m.help.ShowAll
		return m, nil
	case key.Matches(msg, m.keys.Up), key.Matches(msg, m.keys.Down):
		m.notice = ""
		m.hist, _ = m.hist.Update(msg)
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
	case key.Matches(msg, m.keys.Use): // chain this result into a new action
		if it, ok := m.selectedItem(); ok && it.url != "" {
			m.seedURL = it.url
			m.notice = styAcc.Render("chaining result → pick an action")
			m.palette = openPaletteState()
			return m.setFocus(zoneInput), nil
		}
		return m, nil
	case key.Matches(msg, m.keys.Generate): // enter → go back to input (start over)
		if m.phase != phaseWorking {
			m.phase = phaseIdle
			m.result, m.errMsg, m.jobID, m.notice = "", "", "", ""
			m.input.SetValue("")
			return m.setFocus(zoneInput), nil
		}
	}
	return m, nil
}

// startForm enters per-parameter form mode for a form-driven action, reusing
// the prompt box as a sequential field editor.
func (m model) startForm(spec actionSpec) model {
	m.action = spec
	m.formFields = spec.form()
	m.formIdx = 0
	m.formVals = map[string]string{}
	// Chaining: drop a carried-over result URL into the first media field.
	if m.seedURL != "" {
		for _, f := range m.formFields {
			if f.isMedia() {
				m.formVals[f.name] = m.seedURL
				break
			}
		}
		m.seedURL = ""
	}
	m.notice = ""
	m.input.SetValue(m.formVals[m.formFields[0].name])
	m.input.Placeholder = formPlaceholder(m.formFields[0])
	return m.setFocus(zoneInput)
}

func formPlaceholder(p paramSpec) string {
	if p.isMedia() {
		return p.label + " — paste a URL (ctrl+r = latest result)"
	}
	return p.label
}

func (m model) latestResultURL() string {
	for i := len(m.history) - 1; i >= 0; i-- {
		if m.history[i].url != "" {
			return m.history[i].url
		}
	}
	return ""
}

func (m model) formMissing() string {
	var miss []string
	for _, f := range m.formFields {
		if !f.required {
			continue
		}
		raw := strings.TrimSpace(m.formVals[f.name])
		if f.kind == pMediaList {
			valid := 0
			for _, p := range strings.Split(raw, ",") {
				if strings.TrimSpace(p) != "" {
					valid++
				}
			}
			if valid == 0 {
				miss = append(miss, f.label)
			}
			continue
		}
		if raw == "" {
			miss = append(miss, f.label)
		}
	}
	return strings.Join(miss, ", ")
}

// formSummary is a short human label for a form submission.
func (m model) formSummary() string {
	var texts []string
	for _, f := range m.formFields {
		if f.kind == pText {
			if v := strings.TrimSpace(m.formVals[f.name]); v != "" {
				texts = append(texts, v)
			}
		}
	}
	if len(texts) > 0 {
		return strings.Join(texts, " · ")
	}
	for _, f := range m.formFields {
		if v := strings.TrimSpace(m.formVals[f.name]); v != "" {
			return outputFilename(v)
		}
	}
	return m.action.action
}

// updateForm drives the sequential field editor.
func (m model) updateForm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	cur := m.formFields[m.formIdx]
	save := func() { m.formVals[cur.name] = strings.TrimSpace(m.input.Value()) }
	gotoField := func(i int) {
		m.formIdx = i
		m.input.SetValue(m.formVals[m.formFields[i].name])
		m.input.Placeholder = formPlaceholder(m.formFields[i])
	}
	switch msg.String() {
	case "esc":
		m.formFields, m.formVals = nil, nil
		m.input.SetValue("")
		m.input.Placeholder = "type a prompt · / for commands · ⇧⇥ to change type"
		return m.setFocus(zoneInput), nil
	case "ctrl+r":
		if cur.isMedia() {
			if u := m.latestResultURL(); u != "" {
				m.input.SetValue(u)
			}
		}
		return m, nil
	case "up":
		save()
		if m.formIdx > 0 {
			gotoField(m.formIdx - 1)
		}
		return m, nil
	case "enter", "down":
		save()
		if m.formIdx < len(m.formFields)-1 {
			gotoField(m.formIdx + 1)
			return m, nil
		}
		if miss := m.formMissing(); miss != "" {
			m.notice = styRed.Render("fill in: " + miss)
			return m, nil
		}
		return m.submitForm()
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

// submitForm builds the args from collected values and dispatches the job.
func (m model) submitForm() (tea.Model, tea.Cmd) {
	if !m.loggedIn {
		m.phase = phaseError
		m.errMsg = "You're not signed in. Quit and run `framehood login` first."
		m.formFields = nil
		return m.setFocus(zoneOutput), nil
	}
	tool, args := argsForForm(m.action, m.formVals)
	m.phase = phaseWorking
	m.status = "submitting"
	m.result, m.errMsg, m.jobID = "", "", ""
	m.inflight = m.action
	m.inflightLabel = m.formSummary()
	m.started = time.Now()
	m.formFields = nil
	m.input.SetValue("")
	m.input.Placeholder = "type a prompt · / for commands · ⇧⇥ to change type"
	return m, tea.Batch(m.spin.Tick, submitCmd(m.client, tool, args))
}

// updateInput: the prompt field is focused — text editing happens here.
// `/` at start of input (or on empty input) opens the palette.
// Shift+Tab cycles the generation type.
// Enter submits with the active action.
func (m model) updateInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if len(m.formFields) > 0 {
		return m.updateForm(msg)
	}

	switch {
	case key.Matches(msg, m.keys.ShiftTab):
		// Cycle generation type: image → video → audio → image.
		m.genType = typeIndex((int(m.genType) + 1) % numTypes)
		m = m.applyGenType()
		m.input.Placeholder = "type a prompt · / for commands · ⇧⇥ to change type"
		return m, nil

	case msg.String() == "/" && strings.TrimSpace(m.input.Value()) == "":
		// Open the palette when the input is empty or the typed char is '/'.
		m.input.SetValue("")
		m.palette = openPaletteState()
		return m, nil

	case key.Matches(msg, m.keys.Esc):
		// If output zone has rows, move focus there; otherwise just clear.
		if len(m.rows) > 0 {
			return m.setFocus(zoneOutput), nil
		}
		return m, nil

	case key.Matches(msg, m.keys.Help):
		m.help.ShowAll = !m.help.ShowAll
		return m, nil

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
			// Selection moved to a form-driven action — open the form.
			return m.startForm(m.action), nil
		}
		m.phase = phaseWorking
		m.status = "submitting"
		m.result, m.errMsg, m.jobID = "", "", ""
		m.inflight = m.action
		m.inflightLabel = prompt
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
		return m, tea.Batch(m.spin.Tick, tea.Tick(2*time.Second, func(time.Time) tea.Msg {
			return pollTickMsg{jobID: j.ID}
		}))
	}
	label := m.inflight.tool + "·" + m.inflight.action
	if label == "·" {
		label = m.action.tool + "·" + m.action.action
	}
	prompt := m.inflightLabel
	if prompt == "" {
		prompt = m.input.Value()
	}
	if j.Status == "failed" {
		m.phase = phaseError
		m.errMsg = "job failed: " + strings.TrimSpace(string(j.Error))
		m.history = append(m.history, historyItem{kind: label, prompt: prompt, failed: true})
		m.rebuildHistory(true)
		return m.setFocus(zoneOutput), loadBalanceCmd(m.client)
	}
	m.phase = phaseDone
	m.result = j.ResultURL()
	m.notice = ""
	m.history = append(m.history, historyItem{kind: label, prompt: prompt, url: m.result})
	m.rebuildHistory(true)
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
	// Only open our own https result URLs — never a server-supplied file://,
	// javascript:, or off-CDN (phishing) URL reaches open / rundll32 / xdg-open.
	if u, err := url.Parse(target); err != nil || u.Scheme != "https" || !resultHostAllowed(u.Host) {
		return fmt.Errorf("refusing to open a non-Framehood URL")
	}
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", target).Start()
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", target).Start()
	default:
		return exec.Command("xdg-open", target).Start()
	}
}

// copyToClipboard writes s to the OS clipboard via the platform tool.
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

// outputFilename derives a local filename from a result URL.
func outputFilename(rawURL string) string {
	name := ""
	if u, err := url.Parse(rawURL); err == nil {
		name = path.Base(u.Path)
	}
	if name == "" || name == "." || name == "/" || name == ".." ||
		strings.HasPrefix(name, ".") || strings.ContainsAny(name, `\/:`) {
		return "framehood_output"
	}
	return name
}

// maxResultDownloadBytes caps one saved result so a rogue server can't fill the disk.
const maxResultDownloadBytes = 1 << 30 // 1 GiB

// resultHostAllowed restricts result fetches/opens to our CDN.
func resultHostAllowed(host string) bool {
	host = strings.ToLower(host)
	if i := strings.IndexByte(host, ':'); i >= 0 {
		host = host[:i]
	}
	return host == "framehood.ai" || strings.HasSuffix(host, ".framehood.ai")
}

// saveResult downloads rawURL to a non-colliding path in the current directory.
func saveResult(rawURL string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme != "https" || !resultHostAllowed(u.Host) {
		return "", fmt.Errorf("refusing to fetch a non-Framehood result URL")
	}
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

	f, name, err := createNonColliding(outputFilename(rawURL))
	if err != nil {
		return "", err
	}
	n, err := io.Copy(f, io.LimitReader(resp.Body, maxResultDownloadBytes+1))
	if err != nil {
		f.Close()
		os.Remove(name)
		return "", err
	}
	if n > maxResultDownloadBytes {
		f.Close()
		os.Remove(name)
		return "", fmt.Errorf("result exceeds %d bytes", maxResultDownloadBytes)
	}
	if err := f.Close(); err != nil {
		os.Remove(name)
		return "", err
	}
	return name, nil
}

// createNonColliding O_EXCL-creates the first free of base, base-1, base-2, …
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
