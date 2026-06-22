// Package tui implements the interactive Framehood studio — the "beautiful
// terminal client" mode launched when the binary is run with no subcommand.
package tui

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/Framehood/framehood-cli/internal/config"
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
	at     time.Time // when the generation completed (for persistence + display)
}

// toPersisted / fromPersisted convert between the in-memory item and its
// on-disk form.
func (h historyItem) toPersisted() persistedEntry {
	return persistedEntry{Time: h.at, Kind: h.kind, Prompt: h.prompt, URL: h.url, Failed: h.failed}
}

func fromPersisted(e persistedEntry) historyItem {
	return historyItem{kind: e.Kind, prompt: e.Prompt, url: e.URL, failed: e.Failed, at: e.Time}
}

// composerPlaceholder is the prompt-box hint shown in the default (non-form)
// composer state.
const composerPlaceholder = "type a prompt · / for commands · ⇧⇥ to change action"

// signedOutMsg is shown when an action needs auth but the studio is signed out.
// It points at the in-TUI /login (no need to quit the studio anymore).
const signedOutMsg = "You're not signed in. Run /login to sign in."

// serviceTools are the catalog groups Shift+Tab must NOT cycle through: account
// reads/management and job status. Their actions reach the studio only through
// the `/` command palette, never the work-action ring.
var serviceTools = map[string]bool{
	"billing":    true,
	"org":        true,
	"files":      true,
	"get_status": true,
}

// workActions is the ordered ring Shift+Tab cycles through: every
// generation / manipulation / QA action across image, video, audio, qa and
// actor — excluding service groups (billing, org, files, get_status) and the
// immediate read-actions (e.g. actor·list/get, which return information rather
// than producing media). Built once from the catalog so it stays in sync.
var workActions = buildWorkActions()

// buildWorkActions flattens the catalog into the Shift+Tab ring, preserving
// catalog order and skipping service groups + immediate read-actions.
func buildWorkActions() []actionSpec {
	var out []actionSpec
	for _, g := range catalog {
		if serviceTools[g.tool] {
			continue
		}
		for _, a := range g.actions {
			if a.immediate || a.kind == kindRead {
				continue // read-only info actions stay out of the work ring
			}
			out = append(out, a)
		}
	}
	return out
}

// workActionIndex returns the position of spec in the workActions ring, or -1
// if spec is not a work action (e.g. a service/read action picked from the
// palette).
func workActionIndex(spec actionSpec) int {
	for i, a := range workActions {
		if a.tool == spec.tool && a.action == spec.action {
			return i
		}
	}
	return -1
}

type model struct {
	client   *mcp.Client
	auth     Authenticator // browser login/logout for /login + /logout (may be nil in tests)
	email    string
	loggedIn bool

	input         textinput.Model
	spin          spinner.Model
	help          help.Model
	hist          table.Model
	keys          keyMap
	focus         focusZone
	action        actionSpec // currently selected action (shown in composer header)
	inflight      actionSpec // action captured at submit time (for history attribution)
	inflightLabel string     // the submitted prompt/summary, for the history row

	// palette state
	palette paletteState

	// form mode: when formFields is non-empty the prompt box is a sequential
	// per-parameter field editor for a form-driven action.
	formFields []paramSpec
	formIdx    int
	formVals   map[string]string
	seedURL    string // a result URL to chain into the next form's first media field

	// inputHist is the shell-style prompt-recall buffer driven by ↑/↓ in the
	// compose box (palette closed, input focused). Session-only.
	inputHist inputHistory

	phase    phase
	status   string
	balance  string
	result   string
	readData string // pretty-printed output of an immediate read action (files·list, org·info, …)
	readHdr  string // header label for readData ("files·list" etc.)
	errMsg   string
	jobID    string
	genFrame int // animation frame for the "generating" wave; advances each tick
	started  time.Time
	history  []historyItem // chronological (append order); ALL entries

	// Paginated RECENT view over `history` (displayed newest-first).
	histPath string        // history.json path; "" disables persistence (tests)
	histPage int           // current page (0 = newest page)
	rows     []historyItem // the CURRENT page's items, newest-first; index by hist.Cursor()

	// Output directory for saved results. cfg persists user settings; outputDir
	// is the resolved absolute dir ("" = current working directory). setdirMode
	// turns the compose box into a one-field "output directory" prompt.
	cfg        config.Config
	outputDir  string
	setdirMode bool

	// version is the build-time CLI version, used by the /upgrade command.
	version string
	// upgrading guards against concurrent /upgrade self-replace attempts.
	upgrading bool

	notice string // transient action feedback ("copied", "saved → …")
	width  int
	height int
}

// histPageSize is the number of generation rows shown per RECENT page.
const histPageSize = 6

// generating reports whether the working phase has advanced past "submitting"
// into the actual job (a job_id exists and we're polling). It is the
// sub-state discriminator for the status animation: false = submitting (request
// in flight, no job yet), true = generating (job running, lively animation).
func (m model) generating() bool {
	return m.phase == phaseWorking && m.jobID != ""
}

// Run starts the interactive studio. auth wires the `/login` and `/logout`
// palette commands (may be nil). cfg locates the persisted history + settings
// (output dir); its config dir may be empty, which disables persistence.
// version is the build-time CLI version, used by /upgrade.
func Run(client *mcp.Client, email string, auth Authenticator, cfg config.Config, version string) error {
	// The work-action ring is built from the catalog; an empty ring would mean
	// a broken/empty catalog. Fail clearly rather than index workActions[0].
	if len(workActions) == 0 {
		return fmt.Errorf("studio: no work actions available (empty catalog)")
	}

	ti := textinput.New()
	ti.Placeholder = composerPlaceholder
	ti.Focus()
	ti.CharLimit = 1000
	ti.Prompt = "› "

	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = lipgloss.NewStyle().Foreground(colAccent)

	historyPath := ""
	if cfg.ConfigDir != "" {
		historyPath = cfg.HistoryPath()
	}

	m := model{
		client:    client,
		auth:      auth,
		email:     email,
		loggedIn:  client != nil,
		input:     ti,
		spin:      sp,
		help:      help.New(),
		hist:      newHistoryTable(),
		action:    workActions[0], // image · create — the first work action
		keys:      defaultKeys(),
		focus:     zoneInput, // start ready to type
		balance:   "…",
		histPath:  historyPath,
		cfg:       cfg,
		outputDir: cfg.OutputDir(), // "" = current working directory
		version:   version,
	}
	// Load past generations so they appear immediately. A missing/corrupt file
	// loads as empty (loadHistory never errors).
	for _, e := range loadHistory(historyPath) {
		m.history = append(m.history, fromPersisted(e))
	}
	m.rebuildHistory(true)

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

// historyPages is the total number of RECENT pages (at least 1).
func (m model) historyPages() int {
	if len(m.history) == 0 {
		return 1
	}
	return (len(m.history) + histPageSize - 1) / histPageSize
}

// rebuildHistory refreshes the table with the CURRENT page of `history`
// (displayed newest-first) and the parallel `rows` slice used to resolve the
// selection. selectNewest jumps to page 0 and selects the newest row (a fresh
// generation); otherwise the page and in-page selection are preserved (clamped).
func (m *model) rebuildHistory(selectNewest bool) {
	prev := m.hist.Cursor()
	cols := tableWidth(m.width)
	m.hist.SetColumns([]table.Column{
		{Title: "", Width: 2},
		{Title: "type", Width: 7},
		{Title: "prompt", Width: cols},
	})

	pages := m.historyPages()
	if selectNewest {
		m.histPage = 0
	}
	if m.histPage < 0 {
		m.histPage = 0
	}
	if m.histPage >= pages {
		m.histPage = pages - 1
	}

	// The newest-first display index range for this page.
	lo, hi := m.pageBounds()

	rows := make([]table.Row, 0, hi-lo)
	items := make([]historyItem, 0, hi-lo)
	for d := lo; d < hi; d++ {
		h := m.history[len(m.history)-1-d] // d-th newest
		items = append(items, h)
		rows = append(rows, table.Row{"●", h.kind, truncate(h.prompt, cols-1)})
	}
	m.rows = items
	m.hist.SetRows(rows)

	if len(rows) > 0 {
		switch {
		case selectNewest:
			m.hist.SetCursor(0) // newest on page 0
		case prev >= len(rows):
			m.hist.SetCursor(len(rows) - 1)
		case prev < 0:
			m.hist.SetCursor(0)
		default:
			m.hist.SetCursor(prev)
		}
	}
	h := len(rows)
	if h > histPageSize {
		h = histPageSize
	}
	if h < 1 {
		h = 1
	}
	m.hist.SetHeight(h + 1) // + header
}

// pageBounds returns the [lo, hi) newest-first display indices covered by the
// current page (lo inclusive, hi exclusive; 0 = the newest entry).
func (m model) pageBounds() (int, int) {
	lo := m.histPage * histPageSize
	hi := lo + histPageSize
	if hi > len(m.history) {
		hi = len(m.history)
	}
	if lo > len(m.history) {
		lo = len(m.history)
	}
	return lo, hi
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

// immediateResultMsg carries the outcome of an immediate read action
// (files·list, org·info, billing·balance, …). These tools return their DATA —
// a list/object/string via the MCP tool-call content — NOT a job envelope, so
// they are fetched with the raw CallTool path and never decoded as a Job.
type immediateResultMsg struct {
	label string          // "files·list" etc., for the result header
	raw   json.RawMessage // the tool's raw content payload
	err   error
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

// loginResultMsg carries the outcome of the off-thread `/login` browser flow.
type loginResultMsg struct {
	client *mcp.Client
	email  string
	err    error
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

// cycleWorkAction advances the active action to the next (dir=+1) or previous
// (dir=-1) entry in the workActions ring, wrapping at both ends. If the current
// action is not a work action (it was picked from the palette as a service/read
// action), the ring resumes from its first/last entry.
func (m model) cycleWorkAction(dir int) model {
	n := len(workActions)
	if n == 0 {
		return m
	}
	cur := workActionIndex(m.action)
	var next int
	switch {
	case cur < 0 && dir > 0:
		next = 0
	case cur < 0: // dir < 0
		next = n - 1
	default:
		next = (cur + dir%n + n) % n
	}
	m.action = workActions[next]
	return m
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.input.Width = msg.Width - 6
		m.help.Width = msg.Width - 4
		m.rebuildHistory(false)
		if m.palette.isOpen() {
			m.palette.layout(m.contentWidth()) // keep grid nav correct after resize
		}

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
		// Drop a result that belongs to no current job — e.g. an in-flight submit
		// that lands after /logout (client nil-ed, working state torn down). The
		// submit response is what assigns the job id, so we can't match on id yet;
		// gate on client + working phase instead.
		if m.client == nil || m.phase != phaseWorking {
			return m, nil
		}
		if msg.err != nil {
			// Submit failed before a job existed — recover to a usable compose
			// state (input focused, esc/enter dismisses) rather than parking the
			// user in the output zone behind a stale error.
			m.phase = phaseError
			m.errMsg = msg.err.Error()
			m.jobID = ""
			return m.setFocus(zoneInput), nil
		}
		return m.handleJob(msg.job)

	case immediateResultMsg:
		// Result of an immediate read action. There is no job and nothing to poll;
		// whatever the outcome, the studio must land in a usable, recoverable
		// state (input focusable, esc/enter dismisses) — never stuck in
		// phaseWorking. Drop a late result delivered after logout / a new action.
		if m.client == nil || m.phase != phaseWorking {
			return m, nil
		}
		m.jobID = ""
		if msg.err != nil {
			m.phase = phaseError
			m.errMsg = msg.err.Error()
			m.readData, m.readHdr = "", ""
			return m.setFocus(zoneInput), nil
		}
		m.phase = phaseDone
		m.readData = prettyJSON(msg.raw)
		m.readHdr = msg.label
		m.result, m.errMsg = "", ""
		m.notice = ""
		// Read results are informational — keep focus on the input so the user can
		// immediately read the output and type the next command (esc/enter also
		// dismiss cleanly via the normal input path).
		return m.setFocus(zoneInput), nil

	case polledMsg:
		// Ignore a poll result delivered after logout / a new action (no client
		// or not working anymore).
		if m.client == nil || m.phase != phaseWorking {
			return m, nil
		}
		if msg.err != nil {
			// A poll failure must not strand the user in phaseWorking. Recover to a
			// usable compose state (input focused, esc/enter dismisses).
			m.phase = phaseError
			m.errMsg = msg.err.Error()
			return m.setFocus(zoneInput), nil
		}
		// Success result must be for the job we're tracking — drop a stale poll
		// for a previous job id.
		if msg.job.ID != m.jobID {
			return m, nil
		}
		return m.handleJob(msg.job)

	case pollTickMsg:
		// A logout (or any client teardown) may have nil-ed the client while a
		// poll tick was already scheduled — drop the stale tick instead of
		// dereferencing a nil client. Also drop ticks for a job we're no longer
		// tracking.
		if m.client == nil || m.phase != phaseWorking || msg.jobID != m.jobID {
			return m, nil
		}
		return m, pollCmd(m.client, msg.jobID)

	case savedMsg:
		if msg.err != nil {
			m.notice = styRed.Render("save failed: " + msg.err.Error())
		} else {
			m.notice = styGreen.Render("saved → " + msg.path)
		}
		return m, nil

	case loginResultMsg:
		return m.handleLoginResult(msg)

	case upgradeResultMsg:
		return m.handleUpgradeResult(msg)

	case spinner.TickMsg:
		// Advance the generating-wave animation on the same cadence as the
		// spinner (so we don't schedule a second ticker). Only while actually
		// generating; submitting keeps the calmer dot spinner.
		if m.generating() {
			m.genFrame++
		}
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
		// Close palette, restore empty input. A pending chain (seedURL from
		// "use as input") is abandoned here — clear it so it can't leak into an
		// unrelated form opened later.
		m.palette = paletteState{}
		m.input.SetValue("")
		m.seedURL = ""
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
		// Meta commands never consume a chained result — abandon any pending one
		// so it can't prefill a future form.
		m.seedURL = ""
		// Meta (immediate) commands.
		switch cmd.meta {
		case "help":
			m.help.ShowAll = !m.help.ShowAll
			return m.setFocus(zoneInput), nil
		case "new":
			if m.phase != phaseWorking {
				m.phase = phaseIdle
				m.result, m.errMsg, m.jobID, m.notice = "", "", "", ""
				m.readData, m.readHdr = "", ""
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
				return m.setFocus(zoneInput), saveCmd(it.url, m.outputDir)
			}
			return m.setFocus(zoneInput), nil
		case "login":
			return m.runLogin()
		case "logout":
			return m.runLogout()
		case "whoami":
			return m.runWhoami()
		case "history":
			// Focus the RECENT view at the newest page so the user can page
			// through all persisted generations with ⇞/⇟ + o/c/s/u.
			if len(m.history) == 0 {
				m.notice = styDim.Render("no generations yet")
				return m.setFocus(zoneInput), nil
			}
			m.rebuildHistory(true) // newest page, newest selected
			return m.setFocus(zoneOutput), nil
		case "setdir":
			return m.startSetdir(), nil
		case "upgrade":
			return m.runUpgrade()
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
		// Read-only action: doesn't consume a chained result — drop any pending one.
		m.seedURL = ""
		if !m.loggedIn {
			m.phase = phaseError
			m.errMsg = signedOutMsg
			return m.setFocus(zoneOutput), nil
		}
		// Immediate read actions return DATA, not a job. They MUST go through the
		// raw CallTool path (immediateCmd), never Submit's Job-decode path — a list
		// or bare string would otherwise either fail to decode ("decode job: …") or
		// silently produce an empty job and wedge the studio in an endless poll.
		// This is a one-shot fetch, NOT a pollable job: no jobID, no phaseWorking.
		m.phase = phaseWorking // brief "running" indicator only (no job to poll)
		m.status = "running"
		m.result, m.errMsg, m.jobID = "", "", ""
		m.readData, m.readHdr = "", ""
		m.genFrame = 0
		label := m.action.tool + "·" + m.action.action
		m.inflight = m.action
		m.inflightLabel = label
		m.started = time.Now()
		tool, args := argsForImmediateAction(m.action)
		return m, tea.Batch(m.spin.Tick, immediateCmd(m.client, label, tool, args))
	case cmdNeedsForm:
		// The only consumer of a chained result — startForm seeds the first
		// media field and clears seedURL itself.
		return m.startForm(*cmd.spec), nil
	default: // cmdNeedsPrompt
		// Prompt-only action takes no media URL — drop any pending chain.
		m.seedURL = ""
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
	case key.Matches(msg, m.keys.PageOlder): // pgdn → older page
		m.notice = ""
		if m.histPage < m.historyPages()-1 {
			m.histPage++
			m.hist.SetCursor(0) // newest row on the new page
			m.rebuildHistory(false)
		}
		return m, nil
	case key.Matches(msg, m.keys.PageNewer): // pgup → newer page
		m.notice = ""
		if m.histPage > 0 {
			m.histPage--
			m.hist.SetCursor(0)
			m.rebuildHistory(false)
		}
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
			return m, saveCmd(it.url, m.outputDir)
		}
		return m, nil
	case key.Matches(msg, m.keys.Use): // chain this result into a new action
		if it, ok := m.selectedItem(); ok && it.url != "" {
			m.seedURL = it.url
			m.notice = styAcc.Render("chaining result → pick an action")
			m.palette = openPaletteState()
			m.palette.layout(m.contentWidth()) // persist cols for grid ↑/↓ nav
			return m.setFocus(zoneInput), nil
		}
		return m, nil
	case key.Matches(msg, m.keys.Generate): // enter → go back to input (start over)
		if m.phase != phaseWorking {
			m.phase = phaseIdle
			m.result, m.errMsg, m.jobID, m.notice = "", "", "", ""
			m.readData, m.readHdr = "", ""
			m.input.SetValue("")
			return m.setFocus(zoneInput), nil
		}
	}
	return m, nil
}

// startForm enters per-parameter form mode for a form-driven action, reusing
// the prompt box as a sequential field editor.
//
// Some ring actions are neither prompt-runnable nor form-backed yet
// (e.g. video·scene, actor·create). For those, spec.form() is empty: entering
// form mode would index an empty slice and panic. Instead we fail safe —
// set the action, show a "not yet available" notice listing its required
// inputs, and stay on the input with no form open.
func (m model) startForm(spec actionSpec) model {
	m.action = spec
	fields := spec.form()
	if len(fields) == 0 {
		// No form to consume a chained result — abandon any pending one.
		m.seedURL = ""
		m.formFields = nil
		m.input.Placeholder = composerPlaceholder
		m.notice = styRed.Render(specUnavailableNotice(spec))
		return m.setFocus(zoneInput)
	}
	m.formFields = fields
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
		m.input.Placeholder = composerPlaceholder
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
			m.notice = styRed.Render("needs: " + miss)
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
	// Required-field validation: never submit a form with empty required fields.
	if miss := m.formMissing(); miss != "" {
		m.notice = styRed.Render("needs: " + miss)
		return m, nil
	}
	if !m.loggedIn {
		m.phase = phaseError
		m.errMsg = signedOutMsg
		m.formFields = nil
		return m.setFocus(zoneOutput), nil
	}
	tool, args := argsForForm(m.action, m.formVals)
	m.phase = phaseWorking
	m.status = "submitting"
	m.result, m.errMsg, m.jobID = "", "", ""
	m.readData, m.readHdr = "", ""
	m.genFrame = 0
	m.inflight = m.action
	m.inflightLabel = m.formSummary()
	m.started = time.Now()
	m.formFields = nil
	m.input.SetValue("")
	m.input.Placeholder = composerPlaceholder
	return m, tea.Batch(m.spin.Tick, submitCmd(m.client, tool, args))
}

// updateInput: the prompt field is focused — text editing happens here.
// `/` at start of input (or on empty input) opens the palette.
// Shift+Tab advances the work-action ring (Tab reverses it).
// Enter submits with the active action.
func (m model) updateInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.setdirMode {
		return m.updateSetdir(msg)
	}
	if len(m.formFields) > 0 {
		return m.updateForm(msg)
	}

	// Recoverable dismissal: when an error notice or an immediate read result is
	// showing, Esc (or Enter on an empty prompt) clears it back to a clean idle
	// compose state. This guarantees the studio never traps the user behind a
	// stale error/result panel — there's always a key that returns control.
	if m.phase == phaseError || (m.phase == phaseDone && m.readData != "") {
		dismiss := key.Matches(msg, m.keys.Esc) ||
			(key.Matches(msg, m.keys.Generate) && strings.TrimSpace(m.input.Value()) == "")
		if dismiss {
			m.phase = phaseIdle
			m.errMsg, m.result, m.notice = "", "", ""
			m.readData, m.readHdr = "", ""
			return m.setFocus(zoneInput), nil
		}
	}

	switch {
	case key.Matches(msg, m.keys.ShiftTab):
		// Next work action (e.g. image·create → image·edit → … → actor·batch → wrap).
		m = m.cycleWorkAction(+1)
		m.notice = ""
		return m, nil

	case key.Matches(msg, m.keys.Tab):
		// Previous work action (reverse of Shift+Tab).
		m = m.cycleWorkAction(-1)
		m.notice = ""
		return m, nil

	case key.Matches(msg, m.keys.HistPrev): // ↑ — recall an older prompt
		// Single-line textinput doesn't use ↑/↓ for cursor movement, so we own
		// them here for shell-style history recall. Always return — never fall
		// through to textinput.Update.
		if val, ok := m.inputHist.prev(m.input.Value()); ok {
			m.input.SetValue(val)
			m.input.CursorEnd()
		}
		return m, nil

	case key.Matches(msg, m.keys.HistNext): // ↓ — recall newer / restore draft
		if val, ok := m.inputHist.next(); ok {
			m.input.SetValue(val)
			m.input.CursorEnd()
		}
		return m, nil

	case msg.String() == "/" && strings.TrimSpace(m.input.Value()) == "":
		// Open the palette when the input is empty or the typed char is '/'.
		m.input.SetValue("")
		m.palette = openPaletteState()
		m.palette.layout(m.contentWidth()) // persist cols for grid ↑/↓ nav
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
			m.errMsg = signedOutMsg
			return m.setFocus(zoneOutput), nil
		}
		if !m.action.runnable() {
			// Selection moved to a form-driven action — open the form.
			return m.startForm(m.action), nil
		}
		prompt := strings.TrimSpace(m.input.Value())
		if prompt == "" {
			// Required-field validation: a needs-prompt action can't submit empty.
			m.notice = styRed.Render("needs: " + m.actionNeeds())
			return m, nil
		}
		m.inputHist.add(prompt) // record for ↑/↓ recall (dedup + cap inside)
		m.phase = phaseWorking
		m.status = "submitting"
		m.result, m.errMsg, m.jobID = "", "", ""
		m.readData, m.readHdr = "", ""
		m.genFrame = 0
		m.inflight = m.action
		m.inflightLabel = prompt
		m.started = time.Now()
		tool, args := argsForAction(m.action, prompt)
		return m, tea.Batch(m.spin.Tick, submitCmd(m.client, tool, args))
	}

	if m.phase != phaseWorking {
		// Any editing keystroke leaves history-navigation mode, so the next ↑
		// starts from the newest entry again (and the saved draft is dropped).
		m.inputHist.reset()
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		return m, cmd
	}
	return m, nil
}

// actionNeeds is the comma-joined list of required inputs for the active
// action, used in the "needs: …" validation notice. It prefers the catalog's
// declared `needs` list and falls back to the prompt field name.
func (m model) actionNeeds() string {
	if len(m.action.needs) > 0 {
		return strings.Join(m.action.needs, ", ")
	}
	if m.action.promptField != "" {
		return m.action.promptField
	}
	return "input"
}

// specUnavailableNotice is the message shown when a ring action has no prompt
// path and no form yet (form wiring for it is a deliberate follow-up). It names
// the action and its required inputs so the user knows what it will need.
func specUnavailableNotice(spec actionSpec) string {
	label := spec.tool + " " + spec.action
	if len(spec.needs) > 0 {
		return label + " needs: " + strings.Join(spec.needs, ", ") + " — not yet available in the studio"
	}
	return label + " — not yet available in the studio"
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
		m.history = capHistory(append(m.history, historyItem{kind: label, prompt: prompt, failed: true, at: time.Now()}))
		m.rebuildHistory(true)
		return m.setFocus(zoneOutput), tea.Batch(loadBalanceCmd(m.client), m.saveHistoryCmd())
	}
	m.phase = phaseDone
	m.result = j.ResultURL()
	m.notice = ""
	m.history = capHistory(append(m.history, historyItem{kind: label, prompt: prompt, url: m.result, at: time.Now()}))
	m.rebuildHistory(true)
	return m.setFocus(zoneOutput), tea.Batch(loadBalanceCmd(m.client), m.saveHistoryCmd())
}

// capHistory keeps the in-memory generation history bounded to the same limit as
// the persisted file, so a long session can't grow it without bound.
func capHistory(h []historyItem) []historyItem {
	if len(h) > maxPersistedHistory {
		return h[len(h)-maxPersistedHistory:]
	}
	return h
}

// saveHistoryCmd persists the current generation history off the UI thread.
// Persistence is best-effort: a write error is swallowed (returns a no-op msg)
// so it can never crash or block the studio. A no-op when persistence is off.
func (m model) saveHistoryCmd() tea.Cmd {
	if m.histPath == "" {
		return nil
	}
	// Snapshot the entries so the goroutine doesn't race the model.
	entries := make([]persistedEntry, len(m.history))
	for i, h := range m.history {
		entries[i] = h.toPersisted()
	}
	path := m.histPath
	return func() tea.Msg {
		_ = saveHistory(path, entries) // best-effort; never surfaced
		return nil
	}
}

// --- commands ---

func loadBalanceCmd(c *mcp.Client) tea.Cmd {
	if c == nil {
		return nil // signed out — nothing to load, no-op
	}
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

// immediateCmd runs an immediate read action (files·list, org·info,
// billing·balance, …). It calls the RAW tool path (CallTool) and returns the
// data untouched — these tools answer with a list/object/string, not a job
// envelope, so they must NOT go through Submit's Job decode.
func immediateCmd(c *mcp.Client, label, tool string, args map[string]any) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		raw, err := c.CallTool(ctx, tool, args)
		return immediateResultMsg{label: label, raw: raw, err: err}
	}
}

// prettyJSON renders a raw tool payload for the result panel: valid JSON is
// indented for readability; anything else (a bare string, plain text) is shown
// as-is, trimmed. It never errors — the worst case is the original text.
func prettyJSON(raw json.RawMessage) string {
	s := strings.TrimSpace(string(raw))
	if s == "" {
		return "(empty)"
	}
	// A bare JSON string ("…") reads better unquoted.
	var str string
	if json.Unmarshal(raw, &str) == nil {
		return strings.TrimSpace(str)
	}
	var buf bytes.Buffer
	if err := json.Indent(&buf, raw, "", "  "); err == nil {
		return buf.String()
	}
	return s
}

func pollCmd(c *mcp.Client, jobID string) tea.Cmd {
	if c == nil {
		return nil // signed out — drop the poll, no-op
	}
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

// saveCmd downloads the result URL into dir (or the current directory when dir
// is ""), off the UI thread.
func saveCmd(rawURL, dir string) tea.Cmd {
	return func() tea.Msg {
		path, err := saveResult(rawURL, dir)
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

// saveResult downloads rawURL to a non-colliding path inside dir (or the
// current working directory when dir is ""). The filename is always derived by
// the sanitizing outputFilename — dir only changes where the file lands, never
// the server-controlled basename.
func saveResult(rawURL, dir string) (string, error) {
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

	f, name, err := createNonColliding(dir, outputFilename(rawURL))
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
// inside dir (or the current working directory when dir is ""). base is the
// already-sanitized filename; dir is the configured output directory.
func createNonColliding(dir, base string) (*os.File, string, error) {
	ext := path.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	for i := 0; i < 1000; i++ {
		name := base
		if i > 0 {
			name = fmt.Sprintf("%s-%d%s", stem, i, ext)
		}
		full := name
		if dir != "" {
			full = filepath.Join(dir, name)
		}
		f, err := os.OpenFile(full, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err == nil {
			return f, full, nil
		}
		if !os.IsExist(err) {
			return nil, "", err
		}
	}
	return nil, "", fmt.Errorf("could not find a free filename for %q", base)
}
