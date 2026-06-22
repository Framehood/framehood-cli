package cmd

import (
	"strings"
	"testing"
)

func TestIdentityRender_AllFields(t *testing.T) {
	id := identity{email: "a@x.io", role: "owner", balance: "1640 credits", plan: "pro"}
	out := id.render()
	for _, want := range []string{
		"Email:   a@x.io",
		"Role:    owner",
		"Balance: 1640 credits",
		"Plan:    pro",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("render missing %q\n--- got ---\n%s", want, out)
		}
	}
	// It must be a labeled block, never raw JSON.
	if strings.Contains(out, "{") || strings.Contains(out, "\"") {
		t.Errorf("whoami render looks like JSON:\n%s", out)
	}
}

func TestIdentityRender_OmitsMissing(t *testing.T) {
	// Missing org (no role/plan) + unknown email.
	id := identity{balance: "10 credits"}
	out := id.render()
	if !strings.Contains(out, "Email:   (unknown email)") {
		t.Errorf("missing email should show placeholder:\n%s", out)
	}
	if strings.Contains(out, "Role:") {
		t.Errorf("absent role must be omitted:\n%s", out)
	}
	if strings.Contains(out, "Plan:") {
		t.Errorf("absent plan must be omitted:\n%s", out)
	}
	if !strings.Contains(out, "Balance: 10 credits") {
		t.Errorf("balance should render:\n%s", out)
	}
}

func TestFirstNonEmpty(t *testing.T) {
	if got := firstNonEmpty("", "fallback"); got != "fallback" {
		t.Errorf("firstNonEmpty(\"\", fallback) = %q", got)
	}
	if got := firstNonEmpty("primary", "fallback"); got != "primary" {
		t.Errorf("firstNonEmpty(primary, fallback) = %q", got)
	}
}
