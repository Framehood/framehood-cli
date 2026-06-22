package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/Framehood/framehood-cli/internal/config"
	"github.com/Framehood/framehood-cli/internal/render"
	"github.com/spf13/cobra"
)

// maxDownloadBytes caps a `files download -o` fetch, matching the studio's
// result-download ceiling. A larger file is rejected rather than streamed.
const maxDownloadBytes = 1 << 30 // 1 GiB

// getReadable performs an authenticated GET against a REST path under the API
// base and prints the response as human-readable text. tool/action select the
// render formatter; an empty action (or an unknown shape) falls back to pretty
// JSON so nothing is ever hidden. It mirrors callTool's output convention for
// the read endpoints that aren't MCP tools (/v1/models, /v1/workflows, …).
func getReadable(cmd *cobra.Command, cfg config.Config, path, tool, action string) error {
	sess, err := NewSession(cfg)
	if err != nil {
		return err
	}
	raw, err := sess.Client().GetJSON(cmd.Context(), cfg.APIEndpoint(path))
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

// pathSeg percent-encodes a single URL path segment so a model/workflow name
// can't break out of the intended /v1/... path.
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
