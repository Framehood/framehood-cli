package tui

import (
	"context"
	"time"

	"github.com/Framehood/framehood-cli/internal/mcp"
	tea "github.com/charmbracelet/bubbletea"
)

// Authenticator lets the studio run the browser sign-in / sign-out flows for
// the `/login` and `/logout` palette commands without the tui package importing
// the auth + config packages directly (which would create an import cycle with
// cmd). The cmd package supplies a concrete implementation; tests inject a stub.
type Authenticator interface {
	// Login runs the interactive browser OAuth flow, persists the credentials,
	// and returns a fresh MCP client bound to the new session plus the signed-in
	// email. It is invoked off the Bubble Tea UI thread.
	Login(ctx context.Context) (client *mcp.Client, email string, err error)

	// Logout clears the stored credentials.
	Logout() error
}

// runLogin handles the `/login` palette command. It runs the browser OAuth flow
// off the UI thread (as a tea.Cmd) and reports a notice while it waits.
func (m model) runLogin() (tea.Model, tea.Cmd) {
	if m.auth == nil {
		m.notice = styRed.Render("login isn't available in this session")
		return m.setFocus(zoneInput), nil
	}
	m.notice = styDim.Render("opening your browser to sign in…")
	return m.setFocus(zoneInput), loginCmd(m.auth)
}

// loginCmd runs the browser sign-in off the Bubble Tea UI thread.
func loginCmd(a Authenticator) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		client, email, err := a.Login(ctx)
		return loginResultMsg{client: client, email: email, err: err}
	}
}

// handleLoginResult swaps in the freshly-authenticated client on success and
// reloads the balance; on failure it surfaces the error as a notice and leaves
// the existing (signed-in or signed-out) state untouched.
func (m model) handleLoginResult(msg loginResultMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.notice = styRed.Render("login failed: " + msg.err.Error())
		return m, nil
	}
	if msg.client == nil {
		// Defensive: a nil client on the success path is not a usable session.
		m.notice = styRed.Render("login failed: no session returned")
		return m, nil
	}

	// Success: adopt the new session. These assignments are intentionally
	// confined to this branch so a future partial-success can't leave the model
	// claiming it's signed in without a client.
	m.client = msg.client
	m.email = msg.email
	m.loggedIn = true
	m.balance = "…"
	who := m.email
	if who == "" {
		who = "your account"
	}
	m.notice = styGreen.Render("signed in as " + who)
	return m, loadBalanceCmd(m.client)
}

// runLogout handles the `/logout` palette command: it clears stored
// credentials (the same path the `logout` cobra command uses) and resets the
// model to a signed-out state.
func (m model) runLogout() (tea.Model, tea.Cmd) {
	if m.auth == nil {
		m.notice = styRed.Render("logout isn't available in this session")
		return m.setFocus(zoneInput), nil
	}
	if err := m.auth.Logout(); err != nil {
		m.notice = styRed.Render("logout failed: " + err.Error())
		return m.setFocus(zoneInput), nil
	}
	m.client = nil
	m.email = ""
	m.loggedIn = false
	m.balance = "—"
	// Tear down any in-flight job state so a scheduled poll tick (which would
	// otherwise fire against the now-nil client) becomes a harmless no-op.
	m.phase = phaseIdle
	m.jobID = ""
	m.status = ""
	m.result = ""
	m.errMsg = ""
	m.notice = styGreen.Render("signed out · /login to sign back in")
	return m.setFocus(zoneInput), nil
}

// runWhoami handles the `/whoami` palette command: it shows the signed-in
// account and current balance as an immediate notice.
func (m model) runWhoami() (tea.Model, tea.Cmd) {
	if !m.loggedIn {
		m.notice = styRed.Render("not signed in · /login to sign in")
		return m.setFocus(zoneInput), nil
	}
	who := m.email
	if who == "" {
		who = "signed in"
	}
	m.notice = styAcc.Render(who) + styDim.Render("  ·  ") + styAcc.Render(m.balance)
	return m.setFocus(zoneInput), nil
}
