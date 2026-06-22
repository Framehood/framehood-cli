package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// setdirPlaceholder is the compose-box hint while collecting an output directory.
const setdirPlaceholder = "output directory path…  (empty = show current · esc = cancel)"

// startSetdir switches the compose box into the one-field "output directory"
// prompt. The box starts empty; submitting it empty shows the current dir, a
// non-empty path is validated, created, and persisted.
func (m model) startSetdir() model {
	m.setdirMode = true
	m.formFields = nil // never mix with the per-parameter form
	m.input.SetValue("")
	m.input.Placeholder = setdirPlaceholder
	m.notice = styDim.Render("current output dir: " + m.outputDirLabel())
	return m.setFocus(zoneInput)
}

// outputDirLabel renders the active output directory for display ("current
// working directory" when unset).
func (m model) outputDirLabel() string {
	if m.outputDir == "" {
		return "current working directory"
	}
	return m.outputDir
}

// exitSetdir leaves the output-directory prompt and restores the normal composer.
func (m model) exitSetdir() model {
	m.setdirMode = false
	m.input.SetValue("")
	m.input.Placeholder = composerPlaceholder
	return m
}

// updateSetdir drives the one-field output-directory prompt. Enter on an empty
// box just reports the current dir; enter on a path validates + persists it;
// esc cancels.
func (m model) updateSetdir(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m = m.exitSetdir()
		m.notice = styDim.Render("output dir unchanged: " + m.outputDirLabel())
		return m, nil
	case "enter":
		path := strings.TrimSpace(m.input.Value())
		if path == "" {
			// Empty submit → just show the current directory, stay in the prompt.
			m.notice = styAcc.Render("output dir: " + m.outputDirLabel())
			return m, nil
		}
		abs, err := m.cfg.SetOutputDir(path)
		if err != nil {
			m.notice = styRed.Render("invalid output dir: " + err.Error())
			return m, nil // keep the prompt open so the user can fix it
		}
		m.outputDir = abs
		m = m.exitSetdir()
		m.notice = styGreen.Render("output dir → " + abs)
		return m, nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}
