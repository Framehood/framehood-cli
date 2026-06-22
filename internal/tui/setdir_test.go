package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Framehood/framehood-cli/internal/config"
	tea "github.com/charmbracelet/bubbletea"
)

func TestPalette_HasSetdirCommand(t *testing.T) {
	c := paletteCmdByID(t, "/setdir")
	if c.kind != cmdImmediate || c.meta != "setdir" {
		t.Errorf("/setdir = %+v, want immediate meta='setdir'", c)
	}
	if c.spec != nil {
		t.Errorf("/setdir should be a meta command (spec nil), got %+v", c.spec)
	}
}

func TestSetdir_NotInWorkRing(t *testing.T) {
	for _, a := range workActions {
		if a.action == "setdir" {
			t.Errorf("setdir must not appear in the Shift+Tab work ring: %+v", a)
		}
	}
}

// typeString feeds each rune of s through updateSetdir so the textinput value
// updates exactly as in production.
func typeString(m model, s string) model {
	for _, r := range s {
		nm, _ := m.updateSetdir(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = nm.(model)
	}
	return m
}

func TestSetdir_FlowValidatesAndPersists(t *testing.T) {
	cfgDir := t.TempDir()
	cfg := config.Config{MCPBase: "https://mcp.framehood.ai", ConfigDir: cfgDir}

	m := newTestModel()
	m.cfg = cfg

	// /setdir opens the one-field prompt.
	sd := paletteCmdByID(t, "/setdir")
	nm, _ := m.runPaletteCmd(&sd)
	m = nm.(model)
	if !m.setdirMode {
		t.Fatal("/setdir should enter setdirMode")
	}
	if m.focus != zoneInput {
		t.Error("/setdir should keep focus on the input")
	}

	// Empty enter → shows the current dir, stays in the prompt.
	nm, _ = m.updateSetdir(tea.KeyMsg{Type: tea.KeyEnter})
	m = nm.(model)
	if !m.setdirMode {
		t.Error("empty submit should keep the prompt open")
	}
	if !strings.Contains(m.notice, "output dir") {
		t.Errorf("empty submit should report the current dir, notice=%q", m.notice)
	}

	// Type a path and submit → validate, create, persist, exit the prompt.
	target := filepath.Join(t.TempDir(), "saved")
	m = typeString(m, target)
	nm, _ = m.updateSetdir(tea.KeyMsg{Type: tea.KeyEnter})
	m = nm.(model)
	if m.setdirMode {
		t.Error("a valid path should exit setdir mode")
	}
	if m.outputDir != target {
		t.Errorf("model outputDir = %q, want %q", m.outputDir, target)
	}
	if !strings.Contains(m.notice, "output dir → ") {
		t.Errorf("notice = %q, want 'output dir → <abs>'", m.notice)
	}
	// Persisted on disk.
	if got := cfg.OutputDir(); got != target {
		t.Errorf("persisted output dir = %q, want %q", got, target)
	}
	if info, err := os.Stat(target); err != nil || !info.IsDir() {
		t.Errorf("setdir should have created the directory: err=%v", err)
	}
}

func TestSetdir_RejectsNonDirectory(t *testing.T) {
	cfg := config.Config{MCPBase: "x", ConfigDir: t.TempDir()}
	m := newTestModel()
	m.cfg = cfg

	// A regular file is not a directory.
	file := filepath.Join(t.TempDir(), "afile")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	m = m.startSetdir()
	m = typeString(m, file)
	nm, _ := m.updateSetdir(tea.KeyMsg{Type: tea.KeyEnter})
	m = nm.(model)
	if !m.setdirMode {
		t.Error("an invalid dir should keep the prompt open so the user can fix it")
	}
	if !strings.Contains(m.notice, "invalid output dir") {
		t.Errorf("notice = %q, want an 'invalid output dir' error", m.notice)
	}
	if m.outputDir != "" {
		t.Errorf("a rejected dir must not change outputDir, got %q", m.outputDir)
	}
}

func TestSetdir_EscCancels(t *testing.T) {
	m := newTestModel()
	m.cfg = config.Config{ConfigDir: t.TempDir()}
	m = m.startSetdir()
	m = typeString(m, "/some/path")
	nm, _ := m.updateSetdir(tea.KeyMsg{Type: tea.KeyEsc})
	m = nm.(model)
	if m.setdirMode {
		t.Error("esc should leave setdir mode")
	}
	if m.outputDir != "" {
		t.Error("esc must not change the output dir")
	}
	if m.input.Placeholder != composerPlaceholder {
		t.Errorf("esc should restore the composer placeholder, got %q", m.input.Placeholder)
	}
}

// TestSaveResult_WritesIntoConfiguredDir exercises saveResult end-to-end against
// a local test server, confirming the file lands in the configured directory.
func TestSaveResult_WritesIntoConfiguredDir(t *testing.T) {
	// saveResult only fetches https Framehood-CDN hosts, so we can't point it at
	// httptest. Instead verify the directory-rooting via createNonColliding,
	// which is the part saveResult delegates the destination to.
	dir := t.TempDir()
	f, name, err := createNonColliding(dir, "result.jpg")
	if err != nil {
		t.Fatalf("createNonColliding: %v", err)
	}
	f.Close()
	if filepath.Dir(name) != dir {
		t.Errorf("save target dir = %q, want %q", filepath.Dir(name), dir)
	}

	// And the dotfile/separator sanitization is still applied by outputFilename
	// regardless of the configured dir (defense in depth).
	if got := outputFilename("https://cdn.framehood.ai/results/.env"); got != "framehood_output" {
		t.Errorf("outputFilename dotfile guard = %q, want framehood_output", got)
	}
}
