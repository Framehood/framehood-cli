package tui

import (
	"errors"
	"strings"
	"testing"

	"github.com/Framehood/framehood-cli/internal/selfupdate"
)

func TestPalette_HasUpgradeCommand(t *testing.T) {
	c := paletteCmdByID(t, "/upgrade")
	if c.kind != cmdImmediate || c.meta != "upgrade" {
		t.Errorf("/upgrade = %+v, want immediate meta='upgrade'", c)
	}
	if c.spec != nil {
		t.Errorf("/upgrade should be a meta command (spec nil), got %+v", c.spec)
	}
}

func TestUpgrade_NotInWorkRing(t *testing.T) {
	for _, a := range workActions {
		if a.action == "upgrade" {
			t.Errorf("upgrade must not appear in the Shift+Tab work ring: %+v", a)
		}
	}
}

func TestRunUpgrade_SetsCheckingNoticeAndCmd(t *testing.T) {
	m := newTestModel()
	m.version = "v1.0.0"
	nm, cmd := m.runUpgrade()
	got := nm.(model)
	if got.notice == "" || !strings.Contains(got.notice, "checking") {
		t.Errorf("runUpgrade should show a 'checking…' notice, got %q", got.notice)
	}
	if cmd == nil {
		t.Error("runUpgrade should return a command to run the update off-thread")
	}
	if got.focus != zoneInput {
		t.Errorf("runUpgrade should keep input focus, got %v", got.focus)
	}
}

func TestHandleUpgradeResult_Notices(t *testing.T) {
	m := newTestModel()

	// up-to-date
	nm, _ := m.handleUpgradeResult(upgradeResultMsg{
		res: selfupdate.Result{Outcome: selfupdate.OutcomeUpToDate, To: "v1.2.3"},
	})
	if n := nm.(model).notice; !strings.Contains(n, "latest") || !strings.Contains(n, "v1.2.3") {
		t.Errorf("up-to-date notice = %q, want 'already on the latest (v1.2.3)'", n)
	}

	// upgraded
	nm, _ = m.handleUpgradeResult(upgradeResultMsg{
		res: selfupdate.Result{Outcome: selfupdate.OutcomeUpgraded, From: "v1.2.0", To: "v1.2.3"},
	})
	if n := nm.(model).notice; !strings.Contains(n, "upgraded") || !strings.Contains(n, "v1.2.3") {
		t.Errorf("upgraded notice = %q, want 'upgraded v1.2.0 → v1.2.3 …'", n)
	}

	// managed → advice
	nm, _ = m.handleUpgradeResult(upgradeResultMsg{
		res: selfupdate.Result{Outcome: selfupdate.OutcomeManaged, To: "v1.2.3", Advice: "brew upgrade framehood"},
	})
	if n := nm.(model).notice; !strings.Contains(n, "brew upgrade framehood") {
		t.Errorf("managed notice = %q, want it to carry the advice", n)
	}

	// managed-ran → the PM command completed, but we must NOT claim a version
	// landed (the formula/npm index can lag the release).
	nm, _ = m.handleUpgradeResult(upgradeResultMsg{
		res: selfupdate.Result{Outcome: selfupdate.OutcomeManagedRan, From: "v1.2.0", Manager: "Homebrew"},
	})
	if n := nm.(model).notice; !strings.Contains(n, "Homebrew") || !strings.Contains(n, "restart to confirm") {
		t.Errorf("managed-ran notice = %q, want it to mention the manager and 'restart to confirm'", n)
	}

	// error
	nm, _ = m.handleUpgradeResult(upgradeResultMsg{err: errors.New("network down")})
	if n := nm.(model).notice; !strings.Contains(n, "upgrade failed") || !strings.Contains(n, "network down") {
		t.Errorf("error notice = %q, want 'upgrade failed: network down'", n)
	}
}
