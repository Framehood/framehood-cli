package tui

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/Framehood/framehood-cli/internal/config"
	tea "github.com/charmbracelet/bubbletea"
)

// --- A: parse a typed/pasted slash command ---

func TestResolveSlashInput_TableDriven(t *testing.T) {
	cases := []struct {
		in      string
		wantID  string // palette command id, or "" for no match
		wantRem string
		wantOK  bool
	}{
		{"/files list", "files·list", "", true},
		{"/balance", "billing·balance", "", true},                            // unique action-name alias
		{"/billing balance", "billing·balance", "", true},                    // full two-token name
		{"/setdir ~/out", "/setdir", "~/out", true},                          // meta + arg
		{"/image create a red fox", "image·create", "a red fox", true},       // prompt remainder
		{"/image create a  red   fox", "image·create", "a  red   fox", true}, // internal spacing preserved verbatim
		{"/upgrade", "/upgrade", "", true},
		{"/logout", "/logout", "", true},
		{"/history", "/history", "", true},
		{"/whoami extra ignored words", "/whoami", "extra ignored words", true},
		{"/xyz", "", "", false},                      // unknown
		{"/", "", "", false},                         // bare slash
		{"hello", "", "", false},                     // not a slash command
		{"  /balance ", "billing·balance", "", true}, // surrounding space tolerated
	}
	for _, c := range cases {
		cmd, rem, ok := resolveSlashInput(c.in)
		if ok != c.wantOK {
			t.Errorf("resolveSlashInput(%q) ok = %v, want %v", c.in, ok, c.wantOK)
			continue
		}
		if !ok {
			continue
		}
		if cmd.id != c.wantID {
			t.Errorf("resolveSlashInput(%q) id = %q, want %q", c.in, cmd.id, c.wantID)
		}
		if rem != c.wantRem {
			t.Errorf("resolveSlashInput(%q) remainder = %q, want %q", c.in, rem, c.wantRem)
		}
	}
}

func TestResolveSlashInput_LongestMatchWins(t *testing.T) {
	// "files list" (two tokens) must beat any single-token "files" match.
	cmd, rem, ok := resolveSlashInput("/files list")
	if !ok || cmd.id != "files·list" || rem != "" {
		t.Fatalf("got (%v, %q, %v), want files·list/''/true", cmd, rem, ok)
	}
}

// runTypedCommand opens the palette, sets the input to `text`, and presses Enter
// through updatePalette — the production path for a typed/pasted command.
func runTypedCommand(t *testing.T, m model, text string) model {
	t.Helper()
	m.input.Focus()
	m.input.SetValue("/")
	op, _ := m.updateInput(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m = op.(model)
	m.input.SetValue(text) // simulate the fully-typed/pasted value
	m.palette.syncFromInput(text)
	nm, _ := m.updatePalette(tea.KeyMsg{Type: tea.KeyEnter})
	return nm.(model)
}

func TestTypedCommand_ImmediateRead_RunsViaCallTool(t *testing.T) {
	// /files list is an immediate read → should enter the immediate (running)
	// path, not a Job submit. We only assert it leaves the palette and starts a
	// read (phaseWorking, status running, no jobID), since the network call is
	// stubbed elsewhere.
	m := newTestModel()
	got := runTypedCommand(t, m, "/files list")
	if got.palette.isOpen() {
		t.Error("a resolved command should close the palette")
	}
	if got.action.tool != "files" || got.action.action != "list" {
		t.Errorf("action = %s·%s, want files·list", got.action.tool, got.action.action)
	}
	if got.phase != phaseWorking || got.status != "running" {
		t.Errorf("immediate read should be phaseWorking/running, got %v/%q", got.phase, got.status)
	}
	if got.jobID != "" {
		t.Errorf("immediate read must not set a jobID, got %q", got.jobID)
	}
}

func TestTypedCommand_BalanceAlias(t *testing.T) {
	m := newTestModel()
	got := runTypedCommand(t, m, "/balance")
	if got.action.tool != "billing" || got.action.action != "balance" {
		t.Errorf("/balance resolved to %s·%s, want billing·balance", got.action.tool, got.action.action)
	}
}

func TestTypedCommand_ImageCreateWithPrompt_Submits(t *testing.T) {
	m := newTestModel()
	got := runTypedCommand(t, m, "/image create a red fox")
	if got.palette.isOpen() {
		t.Error("a resolved generation command should close the palette")
	}
	if got.action.tool != "image" || got.action.action != "create" {
		t.Errorf("action = %s·%s, want image·create", got.action.tool, got.action.action)
	}
	if got.phase != phaseWorking || got.status != "submitting" {
		t.Errorf("a prompt submit should be phaseWorking/submitting, got %v/%q", got.phase, got.status)
	}
	if got.inflightLabel != "a red fox" {
		t.Errorf("inflightLabel = %q, want 'a red fox'", got.inflightLabel)
	}
}

func TestTypedCommand_SetdirWithArg_Persists(t *testing.T) {
	cfgDir := t.TempDir()
	cfg := config.Config{MCPBase: "x", ConfigDir: cfgDir}
	target := filepath.Join(t.TempDir(), "out")

	m := newTestModel()
	m.cfg = cfg
	got := runTypedCommand(t, m, "/setdir "+target)
	if got.palette.isOpen() {
		t.Error("/setdir <path> should close the palette")
	}
	if got.setdirMode {
		t.Error("/setdir with an inline path should NOT enter the setdir prompt")
	}
	if got.outputDir != target {
		t.Errorf("outputDir = %q, want %q", got.outputDir, target)
	}
	if !strings.Contains(got.notice, "output dir → ") {
		t.Errorf("notice = %q, want 'output dir → …'", got.notice)
	}
	// Persisted on disk.
	if cfg.OutputDir() != target {
		t.Errorf("persisted output dir = %q, want %q", cfg.OutputDir(), target)
	}
}

func TestTypedCommand_NonMatching_FallsBackToHighlighted(t *testing.T) {
	m := newTestModel()
	m.input.Focus()
	m.input.SetValue("/")
	op, _ := m.updateInput(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m = op.(model)

	// Type a non-matching command and select a known highlighted item.
	m.input.SetValue("/xyzzy")
	m.palette.syncFromInput("/xyzzy")
	// Force the highlight to a deterministic command (/help) for the assertion.
	for i, idx := range m.palette.matches {
		if allPaletteCmds[idx].id == "/help" {
			m.palette.sel = i
			break
		}
	}
	// If "/help" isn't in the (empty) match set, point matches at it directly.
	if len(m.palette.matches) == 0 {
		for i := range allPaletteCmds {
			if allPaletteCmds[i].id == "/help" {
				m.palette.matches = []int{i}
				m.palette.sel = 0
			}
		}
	}
	before := m.help.ShowAll
	nm, _ := m.updatePalette(tea.KeyMsg{Type: tea.KeyEnter})
	got := nm.(model)
	// /help toggles ShowAll — proof the HIGHLIGHTED item ran (not the unparsed text).
	if got.help.ShowAll == before {
		t.Error("a non-matching slash command should run the highlighted palette item")
	}
}

// TestPastedCommand_RunsFromClosedComposeBox simulates pasting a full slash
// command into the compose box (palette NOT open) and pressing Enter — it must
// resolve and run, not be submitted as a literal prompt.
func TestPastedCommand_RunsFromClosedComposeBox(t *testing.T) {
	m := newTestModel()
	m.input.Focus()
	if m.palette.isOpen() {
		t.Fatal("precondition: palette should be closed")
	}
	// Paste lands the whole string in the input (palette stays closed).
	m.input.SetValue("/files list")
	nm, _ := m.updateInput(tea.KeyMsg{Type: tea.KeyEnter})
	got := nm.(model)
	if got.action.tool != "files" || got.action.action != "list" {
		t.Errorf("pasted /files list resolved to %s·%s, want files·list", got.action.tool, got.action.action)
	}
	if got.phase != phaseWorking || got.status != "running" {
		t.Errorf("pasted immediate read should run (phaseWorking/running), got %v/%q", got.phase, got.status)
	}
	if got.inflightLabel == "/files list" {
		t.Error("the slash command must not be submitted as a literal prompt")
	}
}

func TestSlashInput_LivesInComposeBox(t *testing.T) {
	m := newTestModel()
	m.input.Focus()
	m.input.SetValue("")
	op, _ := m.updateInput(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m = op.(model)
	// The "/" is shown/edited in the compose input (not just a hidden query).
	if m.input.Value() != "/" {
		t.Errorf("input after '/' = %q, want '/'", m.input.Value())
	}
	if !m.palette.isOpen() {
		t.Error("'/' should open the palette")
	}
}

// --- B: double ctrl+c to quit ---

func TestDoubleCtrlC_ArmsThenQuits(t *testing.T) {
	m := newTestModel()

	// First ctrl+c: arms + notices, does NOT quit (no tea.Quit cmd).
	nm, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	got := nm.(model)
	if !got.quitArmed {
		t.Error("first ctrl+c should arm the quit")
	}
	if got.notice == "" || !strings.Contains(got.notice, "ctrl+c again") {
		t.Errorf("first ctrl+c should notice; got %q", got.notice)
	}
	if cmd != nil && isQuitCmd(cmd) {
		t.Error("first ctrl+c must NOT quit")
	}

	// Second ctrl+c (while armed): quits.
	_, cmd2 := got.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd2 == nil || !isQuitCmd(cmd2) {
		t.Error("second ctrl+c should quit")
	}
}

func TestDoubleCtrlC_OtherKeyDisarms(t *testing.T) {
	m := newTestModel()
	m.input.Focus()

	// Arm with one ctrl+c.
	nm, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	m = nm.(model)
	if !m.quitArmed {
		t.Fatal("precondition: should be armed")
	}

	// Any other key disarms (and clears the notice).
	nm2, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	got := nm2.(model)
	if got.quitArmed {
		t.Error("an interleaved key should disarm the quit")
	}

	// A subsequent single ctrl+c now only re-arms (does not quit).
	_, cmd := got.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd != nil && isQuitCmd(cmd) {
		t.Error("after disarming, a single ctrl+c must not quit")
	}
}

// isQuitCmd reports whether a tea.Cmd resolves to tea.Quit (a tea.QuitMsg).
func isQuitCmd(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	msg := cmd()
	_, ok := msg.(tea.QuitMsg)
	return ok
}
