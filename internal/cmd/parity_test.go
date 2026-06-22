package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Framehood/framehood-cli/internal/config"
	"github.com/spf13/cobra"
)

// TestDownloadURLFrom checks the files(download) payload extraction: it prefers
// download_url, then public_url, and returns "" for an unrecognized shape.
func TestDownloadURLFrom(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"download_url", `{"download_url":"https://cdn.framehood.ai/x","public":false}`, "https://cdn.framehood.ai/x"},
		{"public_url fallback", `{"public_url":"https://cdn.framehood.ai/p"}`, "https://cdn.framehood.ai/p"},
		{"prefers download_url", `{"download_url":"https://a","public_url":"https://b"}`, "https://a"},
		{"unknown shape", `{"ok":true}`, ""},
	}
	for _, c := range cases {
		if got := downloadURLFrom(json.RawMessage(c.raw)); got != c.want {
			t.Errorf("%s: downloadURLFrom = %q, want %q", c.name, got, c.want)
		}
	}
}

// TestFramehoodHost restricts downloads to framehood.ai and its subdomains.
func TestFramehoodHost(t *testing.T) {
	cases := []struct {
		host string
		want bool
	}{
		{"framehood.ai", true},
		{"cdn.framehood.ai", true},
		{"mcp.framehood.ai:443", true},
		{"FRAMEHOOD.AI", true},
		{"evil.com", false},
		{"framehood.ai.evil.com", false},
		{"notframehood.ai", false},
	}
	for _, c := range cases {
		if got := framehoodHost(c.host); got != c.want {
			t.Errorf("framehoodHost(%q) = %v, want %v", c.host, got, c.want)
		}
	}
}

// TestPathSeg escapes a path segment so a name can't break out of /v1/...
func TestPathSeg(t *testing.T) {
	if got := pathSeg("flux_schnell"); got != "flux_schnell" {
		t.Errorf("pathSeg(flux_schnell) = %q", got)
	}
	if got := pathSeg("a/b"); got != "a%2Fb" {
		t.Errorf("pathSeg(a/b) = %q, want a%%2Fb (slash escaped)", got)
	}
}

// TestNewBillingCmd_Subcommands verifies the billing group exposes every action
// the parity work added (alongside the kept reads).
func TestNewBillingCmd_Subcommands(t *testing.T) {
	cfg := config.Config{}
	cmd := newBillingCmd(cfg)
	want := []string{"balance", "plan", "plans", "transactions", "preview", "change", "cancel"}
	have := subcommandNames(cmd)
	for _, w := range want {
		if !have[w] {
			t.Errorf("billing missing subcommand %q (have %v)", w, keys(have))
		}
	}
}

// TestNewJobsCmd_Subcommands verifies jobs exposes list + cancel.
func TestNewJobsCmd_Subcommands(t *testing.T) {
	have := subcommandNames(newJobsCmd(config.Config{}))
	for _, w := range []string{"list", "cancel"} {
		if !have[w] {
			t.Errorf("jobs missing subcommand %q", w)
		}
	}
}

// TestNewFilesCmd_Subcommands verifies the complete files group.
func TestNewFilesCmd_Subcommands(t *testing.T) {
	have := subcommandNames(newFilesCmd(config.Config{}))
	for _, w := range []string{"list", "upload", "delete", "publish", "unpublish", "download"} {
		if !have[w] {
			t.Errorf("files missing subcommand %q", w)
		}
	}
}

// TestNewKeysCmd_Subcommands verifies the api_keys-backed group.
func TestNewKeysCmd_Subcommands(t *testing.T) {
	have := subcommandNames(newKeysCmd(config.Config{}))
	for _, w := range []string{"list", "create", "delete"} {
		if !have[w] {
			t.Errorf("keys missing subcommand %q", w)
		}
	}
}

// subcommandNames returns the set of immediate subcommand names (first word of
// each child's Use) for a cobra command.
func subcommandNames(cmd *cobra.Command) map[string]bool {
	names := map[string]bool{}
	for _, c := range cmd.Commands() {
		names[strings.Fields(c.Use)[0]] = true
	}
	return names
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
