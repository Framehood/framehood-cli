package cmd

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Framehood/framehood-cli/internal/config"
	"github.com/Framehood/framehood-cli/internal/render"
	"github.com/spf13/cobra"
)

// maxDownloadBytes caps a `files download -o` fetch, matching the studio's
// result-download ceiling. A larger file is rejected rather than streamed.
const maxDownloadBytes = 1 << 30 // 1 GiB

// readReadable reads an MCP resource (resources/read over /mcp) and prints the
// response as human-readable text. tool/action select the render formatter; an
// empty action (or an unknown shape) falls back to pretty JSON so nothing is
// ever hidden. It mirrors callTool's output convention for the catalog reads
// (models, workflows, skills).
//
// These reads go over /mcp — not a raw GET against /v1/... — because the CLI's
// credential is an OAuth-provider access token that only the /mcp endpoint
// (wrapped by the worker's OAuthProvider) accepts; the /v1/... routes are
// authenticated separately and reject it with a 401. Reading the equivalent
// zvs:// resource carries the same token and the same refresh-on-401 retry as
// every working command.
func readReadable(cmd *cobra.Command, cfg config.Config, uri, tool, action string) error {
	sess, err := NewSession(cfg)
	if err != nil {
		return err
	}
	raw, err := sess.Client().ReadResource(cmd.Context(), uri)
	if err != nil {
		return err
	}
	if out, ok := render.Readable(tool, action, raw); ok {
		fmt.Println(out)
	} else {
		fmt.Println(render.PrettyJSON(raw))
	}
	return nil
}

// downloadURLFrom extracts the download_url (or public_url) from a files tool
// payload. Returns "" when the shape is unrecognized.
func downloadURLFrom(raw json.RawMessage) string {
	var v struct {
		DownloadURL string `json:"download_url"`
		PublicURL   string `json:"public_url"`
	}
	if jsonUnmarshal(raw, &v) != nil {
		return ""
	}
	if v.DownloadURL != "" {
		return v.DownloadURL
	}
	return v.PublicURL
}

// isPublicDownload reports whether a files(download) payload describes a public
// file — one whose download_url is the no-auth /files/public/... path. The
// worker sets "public":true only for those; a private file is "public":false
// and its URL is the auth-gated /files/{key} route the OAuth token can't fetch.
func isPublicDownload(raw json.RawMessage) bool {
	var v struct {
		Public bool `json:"public"`
	}
	if jsonUnmarshal(raw, &v) != nil {
		return false
	}
	return v.Public
}

// saveURLToFile fetches rawURL and writes the body to out. The fetch is
// restricted to Framehood hosts (the worker/CDN origin the files tool returns)
// and carries the caller's bearer token so authenticated /files/ URLs for
// private files succeed. The body is size-capped.
func saveURLToFile(ctx context.Context, rawURL, out, token string) error {
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme != "https" || !framehoodHost(u.Host) {
		return fmt.Errorf("refusing to fetch a non-Framehood URL")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download failed: HTTP %d", resp.StatusCode)
	}
	f, err := os.Create(out)
	if err != nil {
		return err
	}
	n, err := io.Copy(f, io.LimitReader(resp.Body, maxDownloadBytes+1))
	if err != nil {
		f.Close()
		os.Remove(out)
		return err
	}
	if n > maxDownloadBytes {
		f.Close()
		os.Remove(out)
		return fmt.Errorf("file exceeds %d bytes", maxDownloadBytes)
	}
	if err := f.Close(); err != nil {
		os.Remove(out)
		return err
	}
	return nil
}

// framehoodHost reports whether host is framehood.ai or a subdomain of it
// (the worker, CDN, and API origins all live under it).
func framehoodHost(host string) bool {
	host = strings.ToLower(host)
	if i := strings.IndexByte(host, ':'); i >= 0 {
		host = host[:i]
	}
	return host == "framehood.ai" || strings.HasSuffix(host, ".framehood.ai")
}

// pathSeg percent-encodes a single URL/URI path segment so a model/workflow
// name can't break out of the intended zvs://model/{name} resource path.
func pathSeg(s string) string { return url.PathEscape(s) }

// checkLimit validates an explicitly-set --limit against the documented
// [min,max] range, returning a clear CLI error when out of bounds.
func checkLimit(n, min, max int) error {
	if n < min || n > max {
		return fmt.Errorf("--limit must be between %d and %d (got %d)", min, max, n)
	}
	return nil
}

// jsonUnmarshal is a tiny alias kept local so command files don't each import
// encoding/json just for one decode.
func jsonUnmarshal(raw json.RawMessage, v any) error { return json.Unmarshal(raw, v) }

// newIdempotencyKey returns a per-invocation Idempotency-Key for a money write
// (the billing top-up). A CLI run is one logical request, so one key per run is
// enough: a transient timeout+retry within the same invocation reuses it and the
// worker collapses the calls to a single Stripe invoice instead of double-billing.
// It prefers crypto/rand; if that ever fails it falls back to a timestamp so the
// command can still proceed (a distinct, if non-collapsing, key).
func newIdempotencyKey() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "cli-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	}
	return "cli-" + hex.EncodeToString(b)
}
