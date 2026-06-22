package tui

import (
	"context"
	"errors"
	"testing"

	"github.com/Framehood/framehood-cli/internal/mcp"
	tea "github.com/charmbracelet/bubbletea"
)

// stubAuth is a test Authenticator that records calls and returns canned data.
type stubAuth struct {
	loginCalled  bool
	logoutCalled bool
	loginErr     error
	logoutErr    error
	client       *mcp.Client
	email        string
}

func (s *stubAuth) Login(context.Context) (*mcp.Client, string, error) {
	s.loginCalled = true
	return s.client, s.email, s.loginErr
}

func (s *stubAuth) Logout() error {
	s.logoutCalled = true
	return s.logoutErr
}

// metaByID finds a meta paletteCmd by its slash id.
func metaByID(t *testing.T, id string) paletteCmd {
	t.Helper()
	for _, c := range allPaletteCmds {
		if c.id == id {
			return c
		}
	}
	t.Fatalf("palette command %q not found", id)
	return paletteCmd{}
}

// TestPaletteHasAuthCommands checks /login, /logout, /whoami are registered as
// immediate meta commands in the palette.
func TestPaletteHasAuthCommands(t *testing.T) {
	for _, id := range []string{"/login", "/logout", "/whoami"} {
		c := metaByID(t, id)
		if c.kind != cmdImmediate {
			t.Errorf("%s: kind = %v, want cmdImmediate", id, c.kind)
		}
		if c.spec != nil {
			t.Errorf("%s: spec should be nil (meta command), got %+v", id, c.spec)
		}
		if c.meta == "" {
			t.Errorf("%s: meta name should be set", id)
		}
	}
}

// TestAuthCommandsNotInWorkRing ensures the service commands never leak into
// the Shift+Tab work-action ring (they live in the palette only).
func TestAuthCommandsNotInWorkRing(t *testing.T) {
	for _, a := range workActions {
		switch a.action {
		case "login", "logout", "whoami":
			t.Errorf("service command %q must not be in the work ring", a.action)
		}
	}
}

func TestRunLogout_ClearsSession(t *testing.T) {
	auth := &stubAuth{}
	m := newTestModel()
	m.auth = auth
	m.loggedIn = true

	nm, _ := m.runLogout()
	got := nm.(model)
	if !auth.logoutCalled {
		t.Error("runLogout should call Authenticator.Logout")
	}
	if got.loggedIn {
		t.Error("after logout: loggedIn should be false")
	}
	if got.email != "" {
		t.Errorf("after logout: email = %q, want empty", got.email)
	}
	if got.client != nil {
		t.Error("after logout: client should be nil")
	}
	if got.notice == "" {
		t.Error("after logout: a notice should be shown")
	}
}

// TestPollTickAfterLogout_NoNilDeref reproduces the round-3 crash: a job is in
// flight (phaseWorking + a jobID), the user runs /logout (which nil-resets the
// client and tears down the working state), and a previously-scheduled poll
// tick is then delivered. It must not panic and must not issue a poll command
// against the now-nil client.
func TestPollTickAfterLogout_NoNilDeref(t *testing.T) {
	auth := &stubAuth{}
	m := newTestModel()
	m.auth = auth
	m.loggedIn = true
	m.client = mcp.New("https://example/mcp", nil)

	// Simulate an in-flight job.
	m.phase = phaseWorking
	m.jobID = "job_inflight"
	m.status = "working"

	// Log out while the job is still "polling".
	nm, _ := m.runLogout()
	got := nm.(model)
	if got.client != nil {
		t.Fatal("precondition: logout must nil the client")
	}
	if got.phase == phaseWorking || got.jobID != "" {
		t.Fatalf("logout must clear working state: phase=%v jobID=%q", got.phase, got.jobID)
	}

	// Deliver the stale poll tick. Must not panic, must not return a command.
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("pollTickMsg after logout panicked: %v", r)
		}
	}()
	after, cmd := got.Update(pollTickMsg{jobID: "job_inflight"})
	if _, ok := after.(model); !ok {
		t.Fatal("Update did not return a model")
	}
	if cmd != nil {
		t.Error("a stale poll tick after logout must not issue a command")
	}

	// Belt-and-suspenders: the command builders themselves are nil-safe.
	if pollCmd(nil, "job_x") != nil {
		t.Error("pollCmd(nil, …) should be a no-op (nil cmd)")
	}
	if loadBalanceCmd(nil) != nil {
		t.Error("loadBalanceCmd(nil) should be a no-op (nil cmd)")
	}
}

// TestRunLogout_ModelSurvivesKeypress verifies that after a real logout (which
// nil-resets m.client) the model handles a subsequent keypress without a nil
// panic. The Enter path on a runnable work action must short-circuit on the
// signed-out guard and never dereference the now-nil client. An immediate
// palette action (billing·balance) routed while signed out must do the same.
func TestRunLogout_ModelSurvivesKeypress(t *testing.T) {
	auth := &stubAuth{}
	m := newTestModel()
	m.auth = auth
	m.loggedIn = true
	m.client = mcp.New("https://example/mcp", nil)

	// Log out: this sets m.client = nil and m.loggedIn = false.
	nm, _ := m.runLogout()
	got := nm.(model)
	if got.client != nil || got.loggedIn {
		t.Fatalf("precondition: logout should clear client+loggedIn (client=%v loggedIn=%v)",
			got.client, got.loggedIn)
	}

	// Pressing Enter on the active (runnable) work action must NOT panic and must
	// NOT start a job — it should surface the signed-out error instead.
	got.action = findAction(t, "image", "create")
	got.input.SetValue("a red fox")
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("keypress after logout panicked: %v", r)
		}
	}()
	after, _ := got.Update(tea.KeyMsg{Type: tea.KeyEnter})
	am := after.(model)
	if am.phase == phaseWorking {
		t.Error("a signed-out model must not start a job on Enter")
	}
	if am.errMsg == "" {
		t.Error("Enter while signed out should set the not-signed-in error")
	}

	// Route an immediate palette action while signed out: also guarded, no panic.
	for _, c := range allPaletteCmds {
		if c.id == "billing·balance" {
			cc := c
			nm2, _ := got.runPaletteCmd(&cc)
			im := nm2.(model)
			if im.phase == phaseWorking {
				t.Error("immediate action while signed out must not start a job")
			}
			if im.errMsg == "" {
				t.Error("immediate action while signed out should set the not-signed-in error")
			}
			break
		}
	}
}

func TestRunLogin_RefreshesSessionOnSuccess(t *testing.T) {
	cl := mcp.New("https://example/mcp", nil)
	auth := &stubAuth{client: cl, email: "new@framehood.ai"}

	m := newTestModel()
	m.auth = auth
	m.loggedIn = false
	m.client = nil
	m.email = ""

	// runLogin returns a tea.Cmd that performs the (stubbed) browser flow.
	nm, cmd := m.runLogin()
	_ = nm.(model)
	if cmd == nil {
		t.Fatal("runLogin should return a command to run the login flow")
	}
	// Execute the command → it should produce a loginResultMsg.
	msg := cmd()
	res, ok := msg.(loginResultMsg)
	if !ok {
		t.Fatalf("login cmd produced %T, want loginResultMsg", msg)
	}
	if !auth.loginCalled {
		t.Error("login cmd should call Authenticator.Login")
	}
	// Feed the result back through the handler.
	nm2, _ := m.handleLoginResult(res)
	got := nm2.(model)
	if !got.loggedIn {
		t.Error("after a successful login: loggedIn should be true")
	}
	if got.client != cl {
		t.Error("after login: client should be the freshly-returned one")
	}
	if got.email != "new@framehood.ai" {
		t.Errorf("after login: email = %q, want new@framehood.ai", got.email)
	}
}

func TestRunLogin_ReportsError(t *testing.T) {
	auth := &stubAuth{loginErr: errors.New("browser closed")}
	m := newTestModel()
	m.auth = auth

	nm, _ := m.handleLoginResult(loginResultMsg{err: auth.loginErr})
	got := nm.(model)
	if got.loggedIn != m.loggedIn {
		t.Error("a failed login must not change the signed-in state")
	}
	if got.notice == "" {
		t.Error("a failed login should surface a notice")
	}
}

func TestRunWhoami_ShowsAccount(t *testing.T) {
	m := newTestModel() // signed in, email set, balance set
	nm, _ := m.runWhoami()
	got := nm.(model)
	if got.notice == "" {
		t.Error("whoami should set a notice with the account/balance")
	}

	// Signed-out whoami still produces a (different) notice and does not panic.
	out := newTestModel()
	out.loggedIn = false
	nm2, _ := out.runWhoami()
	if nm2.(model).notice == "" {
		t.Error("signed-out whoami should set a notice")
	}
}

// TestAuthCommands_NilAuthenticator confirms the studio degrades gracefully
// when no Authenticator was wired (e.g. an embedding that didn't provide one).
func TestAuthCommands_NilAuthenticator(t *testing.T) {
	m := newTestModel()
	m.auth = nil

	if nm, _ := m.runLogin(); nm.(model).notice == "" {
		t.Error("runLogin with nil auth should show an 'unavailable' notice")
	}
	if nm, _ := m.runLogout(); nm.(model).notice == "" {
		t.Error("runLogout with nil auth should show an 'unavailable' notice")
	}
}

// Compile-time assertion that the stub satisfies the interface.
var _ Authenticator = (*stubAuth)(nil)
