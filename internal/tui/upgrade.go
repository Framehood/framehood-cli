package tui

import (
	"context"
	"time"

	"github.com/Framehood/framehood-cli/internal/selfupdate"
	tea "github.com/charmbracelet/bubbletea"
)

// upgradeResultMsg carries the outcome of the off-thread `/upgrade` flow.
type upgradeResultMsg struct {
	res selfupdate.Result
	err error
}

// runUpgrade handles the `/upgrade` palette command: it runs the self-update
// off the Bubble Tea UI thread and reports a notice while it works.
func (m model) runUpgrade() (tea.Model, tea.Cmd) {
	if m.upgrading {
		m.notice = styDim.Render("upgrade already in progress…")
		return m.setFocus(zoneInput), nil
	}
	m.upgrading = true
	m.notice = styDim.Render("checking for updates…")
	return m.setFocus(zoneInput), upgradeCmd(m.version)
}

// upgradeCmd runs selfupdate.Upgrade off the UI thread.
func upgradeCmd(version string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		res, err := selfupdate.Upgrade(ctx, version)
		return upgradeResultMsg{res: res, err: err}
	}
}

// handleUpgradeResult turns the upgrade outcome into a studio notice.
func (m model) handleUpgradeResult(msg upgradeResultMsg) (tea.Model, tea.Cmd) {
	m.upgrading = false
	if msg.err != nil {
		m.notice = styRed.Render("upgrade failed: " + msg.err.Error())
		return m, nil
	}
	switch msg.res.Outcome {
	case selfupdate.OutcomeUpToDate:
		m.notice = styGreen.Render("already on the latest (" + msg.res.To + ")")
	case selfupdate.OutcomeUpgraded:
		m.notice = styGreen.Render("upgraded " + msg.res.From + " → " + msg.res.To + " · restart to use it")
	case selfupdate.OutcomeManagedRan:
		// The PM command ran, but the installed version isn't confirmed (the
		// formula/npm index can lag the release), so don't claim a version.
		m.notice = styGreen.Render(msg.res.Manager + " upgrade command completed · restart to confirm the new version")
	case selfupdate.OutcomeManaged:
		m.notice = styAcc.Render("update available (" + msg.res.To + "): " + msg.res.Advice)
	}
	return m, nil
}
