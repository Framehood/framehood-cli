package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Framehood/framehood-cli/internal/config"
	"github.com/spf13/cobra"
)

// bgCmd returns a cobra command carrying a background context, so helpers that
// call cmd.Context() (e.g. runWorkflowsList) get a usable context outside of an
// Execute call.
func bgCmd() *cobra.Command {
	c := &cobra.Command{}
	c.SetContext(context.Background())
	return c
}

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

// TestIsPublicDownload distinguishes a public files(download) payload (no-auth
// /files/public URL, "public":true) from a private one ("public":false, an
// auth-gated /files/{key} URL the OAuth token can't fetch).
func TestIsPublicDownload(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want bool
	}{
		{"public true", `{"key":"a.png","public":true,"download_url":"https://mcp.framehood.ai/files/public/u/a.png"}`, true},
		{"private false", `{"key":"a.png","public":false,"download_url":"https://mcp.framehood.ai/files/private/a.png"}`, false},
		{"missing flag", `{"download_url":"https://x"}`, false},
		{"garbage", `not json`, false},
	}
	for _, c := range cases {
		if got := isPublicDownload(json.RawMessage(c.raw)); got != c.want {
			t.Errorf("%s: isPublicDownload = %v, want %v", c.name, got, c.want)
		}
	}
}

// filesToolServer mocks the worker's /mcp endpoint for the files tool. It
// records the (action) of each tools/call and returns a canned payload per
// action, so a test can assert the call sequence runFileDownload drives.
type filesToolServer struct {
	actions  []string
	download string // JSON payload for action=download
	publish  string // JSON payload for action=publish
}

func (f *filesToolServer) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Params struct {
				Name      string `json:"name"`
				Arguments struct {
					Action string `json:"action"`
				} `json:"arguments"`
			} `json:"params"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		action := req.Params.Arguments.Action
		f.actions = append(f.actions, action)
		var payload string
		switch action {
		case "download":
			payload = f.download
		case "publish":
			payload = f.publish
		case "unpublish":
			payload = `{"ok":true,"key":"clip.mp4","location":"private"}`
		default:
			payload = `{"ok":true}`
		}
		// Wrap the tool payload as an MCP tools/call result (content[0].text).
		inner, _ := json.Marshal(payload)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":%s}]}}`, inner)
	}
}

// TestRunFileDownload_PrivatePublishesAndRestores is the regression guard for the
// 401 bug: a private file (which the OAuth token can't fetch at /files/{key})
// must be transiently published, then unpublished to restore state. We make the
// published URL non-Framehood so saveURLToFile fails fast (no network), and
// assert the unpublish still ran — a failed fetch must never leave the file
// public.
func TestRunFileDownload_PrivatePublishesAndRestores(t *testing.T) {
	fts := &filesToolServer{
		download: `{"key":"clip.mp4","public":false,"download_url":"https://mcp.framehood.ai/files/private/clip.mp4"}`,
		// Non-Framehood host → saveURLToFile refuses, exercising the failure path.
		publish: `{"ok":true,"public_url":"https://not-framehood.example/clip.mp4"}`,
	}
	srv := httptest.NewServer(fts.handler())
	defer srv.Close()

	cfg := testSessionConfig(t, srv.URL)
	out := filepath.Join(t.TempDir(), "clip.mp4")
	err := runFileDownload(bgCmd(), cfg, "clip.mp4", out)
	if err == nil {
		t.Fatal("expected an error when the published URL is non-Framehood")
	}
	// download → publish → unpublish, in that order; unpublish must run despite
	// the fetch failure so the file is restored to private.
	want := []string{"download", "publish", "unpublish"}
	if len(fts.actions) != len(want) {
		t.Fatalf("action sequence = %v, want %v", fts.actions, want)
	}
	for i, a := range want {
		if fts.actions[i] != a {
			t.Fatalf("action[%d] = %q, want %q (full: %v)", i, fts.actions[i], a, fts.actions)
		}
	}
}

// TestRestorePrivate_RunsAfterParentCancelled is the regression guard for the
// cancel-mid-download leak: the restore (unpublish) of a transiently-published
// file must still reach the server even when the parent context is ALREADY
// cancelled (ctrl-c / timeout mid-download) — otherwise a cancelled download
// would leave the file PUBLIC. restorePrivate detaches via WithoutCancel.
func TestRestorePrivate_RunsAfterParentCancelled(t *testing.T) {
	fts := &filesToolServer{}
	srv := httptest.NewServer(fts.handler())
	defer srv.Close()

	cfg := testSessionConfig(t, srv.URL)
	sess, err := NewSession(cfg)
	if err != nil {
		t.Fatal(err)
	}

	// A parent context that is cancelled BEFORE the restore runs — exactly the
	// mid-download ctrl-c case. A naive client.CallTool(parent, …) would fail with
	// "context canceled" and never reach the server.
	parent, cancel := context.WithCancel(context.Background())
	cancel()

	restorePrivate(parent, sess.Client(), "clip.mp4")

	if len(fts.actions) != 1 || fts.actions[0] != "unpublish" {
		t.Fatalf("restore must unpublish despite the cancelled parent context; actions = %v", fts.actions)
	}
}

// TestRunFileDownload_PrivateNoOutPrintsPayload verifies that without -o a
// private file does NOT publish (no state mutation) — it just prints the
// payload, leaving the file untouched.
func TestRunFileDownload_PrivateNoOutPrintsPayload(t *testing.T) {
	fts := &filesToolServer{
		download: `{"key":"clip.mp4","public":false,"download_url":"https://mcp.framehood.ai/files/private/clip.mp4"}`,
	}
	srv := httptest.NewServer(fts.handler())
	defer srv.Close()

	cfg := testSessionConfig(t, srv.URL)
	if err := runFileDownload(bgCmd(), cfg, "clip.mp4", ""); err != nil {
		t.Fatalf("no-out private download should not error: %v", err)
	}
	if len(fts.actions) != 1 || fts.actions[0] != "download" {
		t.Fatalf("without -o only download should be called, got %v", fts.actions)
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

// TestCheckLimit enforces the documented [min,max] range only when set.
func TestCheckLimit(t *testing.T) {
	cases := []struct {
		n, min, max int
		wantErr     bool
	}{
		{1, 1, 100, false},
		{100, 1, 100, false},
		{50, 1, 50, false},
		{0, 1, 100, true},   // below min
		{101, 1, 100, true}, // above max
		{51, 1, 50, true},   // above transactions max
		{-5, 1, 100, true},
	}
	for _, c := range cases {
		err := checkLimit(c.n, c.min, c.max)
		if (err != nil) != c.wantErr {
			t.Errorf("checkLimit(%d,%d,%d) err=%v, wantErr=%v", c.n, c.min, c.max, err, c.wantErr)
		}
	}
}

// TestNewBillingCmd_Subcommands verifies the billing group exposes every action
// the parity work added (alongside the kept reads).
func TestNewBillingCmd_Subcommands(t *testing.T) {
	cfg := config.Config{}
	cmd := newBillingCmd(cfg)
	want := []string{"balance", "plan", "plans", "transactions", "preview", "change", "cancel", "extra-usage", "topup"}
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

// testSessionConfig writes a non-expired credentials file in a temp dir and
// returns a Config pointing the MCP base at mcpBase, so NewSession loads a
// usable session without touching the real ~/.framehood credentials.
func testSessionConfig(t *testing.T, mcpBase string) config.Config {
	t.Helper()
	dir := t.TempDir()
	creds := `{"access_token":"tok"}` // no expiry → never treated as expired
	if err := os.WriteFile(filepath.Join(dir, "credentials.json"), []byte(creds), 0o600); err != nil {
		t.Fatalf("write credentials: %v", err)
	}
	return config.Config{MCPBase: mcpBase, ConfigDir: dir}
}

// workflowResource renders a resources/read result whose body is a workflow
// skill payload ({name, type, content}).
func workflowResource(name, content string) string {
	body, _ := json.Marshal(map[string]string{"name": name, "type": "workflow", "content": content})
	inner, _ := json.Marshal(string(body))
	return fmt.Sprintf(
		`{"jsonrpc":"2.0","id":1,"result":{"contents":[{"uri":"zvs://workflow/%s","mimeType":"application/json","text":%s}]}}`,
		name, inner,
	)
}

// TestRunWorkflowsList_AllReadsFail is the regression guard for the CodeRabbit
// finding: when every workflow read fails, the command must return the
// underlying error rather than silently rendering an empty ("No workflows.")
// catalog that looks like success.
func TestRunWorkflowsList_AllReadsFail(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// Every resources/read returns a server error → ReadResource errors.
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, "boom")
	}))
	defer srv.Close()

	cfg := testSessionConfig(t, srv.URL)
	err := runWorkflowsList(bgCmd(), cfg)
	if err == nil {
		t.Fatal("expected an error when every workflow read fails, got nil")
	}
}

// TestRunWorkflowsList_PartialSuccess verifies partial tolerance: if some reads
// succeed, those render and a failing read does not abort the listing or
// surface an error.
func TestRunWorkflowsList_PartialSuccess(t *testing.T) {
	// Only the first known workflow resolves; the rest 500.
	first := knownWorkflows[0]
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Params struct {
				URI string `json:"uri"`
			} `json:"params"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.Params.URI == "zvs://workflow/"+pathSeg(first) {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, workflowResource(first, "# First\n\nThe first workflow."))
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, "boom")
	}))
	defer srv.Close()

	cfg := testSessionConfig(t, srv.URL)
	if err := runWorkflowsList(bgCmd(), cfg); err != nil {
		t.Fatalf("partial success must not error, got: %v", err)
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
