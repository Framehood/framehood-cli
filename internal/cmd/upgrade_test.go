package cmd

import "testing"

func TestDisplayVersion(t *testing.T) {
	cases := map[string]string{
		"1.2.3":  "v1.2.3",
		"v1.2.3": "v1.2.3",
		"dev":    "dev",
		"":       "",
	}
	for in, want := range cases {
		if got := displayVersion(in); got != want {
			t.Errorf("displayVersion(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestUpgradeCmd_HasUpdateAlias(t *testing.T) {
	cmd := newUpgradeCmd()
	if cmd.Use != "upgrade" {
		t.Errorf("Use = %q, want upgrade", cmd.Use)
	}
	found := false
	for _, a := range cmd.Aliases {
		if a == "update" {
			found = true
		}
	}
	if !found {
		t.Error("upgrade command should have an `update` alias")
	}
}
