package tui

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Framehood/framehood-cli/internal/mcp"
)

// TestHeader_ShowsEmail confirms the header shows the signed-in email (not the
// generic "signed in").
func TestHeader_ShowsEmail(t *testing.T) {
	m := newTestModel() // email: kirill@framehood.ai
	out := m.View()
	if !strings.Contains(out, "kirill@framehood.ai") {
		t.Errorf("header should show the email, got:\n%s", out)
	}

	// With no email, it falls back to "signed in".
	m2 := newTestModel()
	m2.email = ""
	if !strings.Contains(m2.View(), "signed in") {
		t.Error("with no email the header should fall back to 'signed in'")
	}
}

// TestBalanceMsg_CapturesEmail verifies the email from billing(balance) is
// adopted into the model when it didn't have one (fixes the "signed in" header).
func TestBalanceMsg_CapturesEmail(t *testing.T) {
	m := newTestModel()
	m.email = "" // login flow didn't capture it
	nm, _ := m.Update(balanceMsg{text: "1640 credits", email: "found@framehood.ai"})
	got := nm.(model)
	if got.email != "found@framehood.ai" {
		t.Errorf("balanceMsg email = %q, want it adopted", got.email)
	}
	if got.balance != "1640 credits" {
		t.Errorf("balance = %q, want '1640 credits'", got.balance)
	}

	// An existing email is NOT overwritten.
	m2 := newTestModel() // email: kirill@framehood.ai
	nm2, _ := m2.Update(balanceMsg{text: "10 credits", email: "other@x.io"})
	if nm2.(model).email != "kirill@framehood.ai" {
		t.Error("an existing email must not be overwritten by balanceMsg")
	}
}

// TestFormatBalance_TextAndEmail checks the balance string + email extraction.
func TestFormatBalance_TextAndEmail(t *testing.T) {
	text, email := formatBalance(json.RawMessage(`{"balance":1640,"role":"owner","email":"a@x.io"}`))
	if text != "1640 credits" {
		t.Errorf("text = %q, want '1640 credits'", text)
	}
	if email != "a@x.io" {
		t.Errorf("email = %q, want a@x.io", email)
	}
}

// TestRenderReadable_UsesFormatterThenFallback verifies the studio READ panel
// renders a known shape readably and falls back to pretty JSON otherwise.
func TestRenderReadable_UsesFormatter(t *testing.T) {
	out := renderReadable("org·info", json.RawMessage(`{"name":"Acme","your_role":"owner","is_personal":false,"member_count":2}`))
	if !strings.Contains(out, "Acme") || !strings.Contains(out, "owner") {
		t.Errorf("renderReadable should format org·info readably, got:\n%s", out)
	}
	if strings.Contains(out, "{") {
		t.Errorf("renderReadable(org·info) should not be raw JSON:\n%s", out)
	}

	// Unknown label → pretty JSON fallback (no panic, shows the data).
	fb := renderReadable("mystery·thing", json.RawMessage(`{"k":1}`))
	if !strings.Contains(fb, "\"k\": 1") {
		t.Errorf("unknown label should fall back to pretty JSON, got:\n%s", fb)
	}

	// A label without a separator → fallback.
	if got := renderReadable("nolabel", json.RawMessage(`{"k":2}`)); !strings.Contains(got, "\"k\": 2") {
		t.Errorf("separatorless label should fall back, got:\n%s", got)
	}
}

// TestImmediateResult_RendersReadable drives the full immediate-read message
// path and confirms the READ panel data is the readable form (not raw JSON).
func TestImmediateResult_RendersReadable(t *testing.T) {
	m := newTestModel()
	m.client = mcp.New("https://example/mcp", nil) // handler drops results when client is nil
	m.phase = phaseWorking                         // an immediate read is in flight
	m.status = "running"
	raw := json.RawMessage(`{"name":"Acme","your_role":"admin","is_personal":false,"member_count":5}`)
	nm, _ := m.Update(immediateResultMsg{label: "org·info", raw: raw})
	got := nm.(model)
	if got.phase != phaseDone {
		t.Fatalf("phase = %v, want phaseDone", got.phase)
	}
	if !strings.Contains(got.readData, "Acme") || !strings.Contains(got.readData, "admin") {
		t.Errorf("readData should be readable, got:\n%s", got.readData)
	}
	if strings.Contains(got.readData, "{") {
		t.Errorf("readData should not be raw JSON:\n%s", got.readData)
	}
}

// TestSplitLabel parses tool·action labels.
func TestSplitLabel(t *testing.T) {
	tool, action, ok := splitLabel("org·info")
	if !ok || tool != "org" || action != "info" {
		t.Errorf("splitLabel(org·info) = (%q,%q,%v)", tool, action, ok)
	}
	if _, _, ok := splitLabel("noseparator"); ok {
		t.Error("a label without · should not split")
	}
}

// TestRunWhoami_FetchesPlan confirms /whoami dispatches the billing·plan read
// (so role/plan/balance land in the READ panel) and notices the email.
func TestRunWhoami_FetchesPlan(t *testing.T) {
	m := newTestModel()
	m.client = mcp.New("https://example/mcp", nil)
	nm, cmd := m.runWhoami()
	got := nm.(model)
	if got.notice == "" || !strings.Contains(got.notice, "kirill@framehood.ai") {
		t.Errorf("whoami notice should carry the email, got %q", got.notice)
	}
	if got.inflightLabel != "billing·plan" {
		t.Errorf("whoami should fetch billing·plan, inflightLabel=%q", got.inflightLabel)
	}
	if cmd == nil {
		t.Error("whoami should return a command (spinner tick + plan fetch)")
	}
}
