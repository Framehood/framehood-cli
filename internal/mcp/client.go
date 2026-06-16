// Package mcp is a minimal MCP (Model Context Protocol) client speaking the
// Streamable HTTP transport against the Framehood worker's /mcp endpoint.
//
// The worker rebuilds a fresh, stateless MCP server per request, so the client
// does not maintain a session: each tools/call is a self-contained POST that
// carries the negotiated protocol version header. Responses arrive either as a
// single application/json body or as a text/event-stream (SSE) frame; both are
// handled.
package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
)

// ProtocolVersion is the MCP protocol revision the client negotiates. It must
// be sent on every non-initialize request to the streamable HTTP transport.
const ProtocolVersion = "2025-06-18"

// Tokens supplies (and can refresh) the bearer token for requests.
type Tokens interface {
	// Access returns the current access token.
	Access() string
	// Refresh obtains a new access token, persists it, and returns it. Called
	// once on a 401 before the request is retried.
	Refresh(ctx context.Context) (string, error)
}

// Client talks JSON-RPC to a single MCP endpoint.
type Client struct {
	Endpoint string
	Tokens   Tokens
	HTTP     *http.Client

	id int64
}

// New builds a client for endpoint using tok for authorization.
func New(endpoint string, tok Tokens) *Client {
	return &Client{Endpoint: endpoint, Tokens: tok, HTTP: http.DefaultClient}
}

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int64  `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type rpcError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *rpcError) Error() string {
	if e == nil {
		return ""
	}
	return fmt.Sprintf("MCP error %d: %s", e.Code, e.Message)
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int64          `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *rpcError       `json:"error"`
}

// Call invokes a JSON-RPC method and returns the raw result. On a 401 it
// refreshes the token once and retries.
func (c *Client) Call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	res, status, err := c.do(ctx, method, params, c.Tokens.Access())
	if err != nil {
		return nil, err
	}
	if status == http.StatusUnauthorized {
		newTok, rerr := c.Tokens.Refresh(ctx)
		if rerr != nil {
			return nil, fmt.Errorf("unauthorized and refresh failed: %w", rerr)
		}
		res, status, err = c.do(ctx, method, params, newTok)
		if err != nil {
			return nil, err
		}
	}
	if status == http.StatusUnauthorized {
		return nil, fmt.Errorf("unauthorized — run `framehood login`")
	}
	if status >= 300 {
		return nil, fmt.Errorf("MCP request failed: HTTP %d: %s", status, strings.TrimSpace(string(res.Result)))
	}
	if res.Error != nil {
		return nil, res.Error
	}
	return res.Result, nil
}

// do performs a single POST and decodes the matching JSON-RPC response. It
// returns the HTTP status so callers can implement refresh-and-retry.
func (c *Client) do(ctx context.Context, method string, params any, token string) (rpcResponse, int, error) {
	reqID := atomic.AddInt64(&c.id, 1)
	body, _ := json.Marshal(rpcRequest{JSONRPC: "2.0", ID: reqID, Method: method, Params: params})

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Endpoint, bytes.NewReader(body))
	if err != nil {
		return rpcResponse{}, 0, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json, text/event-stream")
	httpReq.Header.Set("MCP-Protocol-Version", ProtocolVersion)
	if token != "" {
		httpReq.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := c.HTTP.Do(httpReq)
	if err != nil {
		return rpcResponse{}, 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return rpcResponse{}, resp.StatusCode, nil
	}

	ct := resp.Header.Get("Content-Type")
	if strings.HasPrefix(ct, "text/event-stream") {
		rr, err := parseSSE(resp.Body, reqID)
		return rr, resp.StatusCode, err
	}
	// Plain JSON (or an error page).
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return rpcResponse{Result: raw}, resp.StatusCode, nil
	}
	var rr rpcResponse
	if err := json.Unmarshal(raw, &rr); err != nil {
		return rpcResponse{}, resp.StatusCode, fmt.Errorf("decode response: %w (body: %s)", err, truncate(raw, 200))
	}
	return rr, resp.StatusCode, nil
}

// parseSSE reads an SSE stream and returns the JSON-RPC response whose id
// matches wantID. Other events (notifications, progress) are skipped.
func parseSSE(r io.Reader, wantID int64) (rpcResponse, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	var data strings.Builder
	flush := func() (rpcResponse, bool, error) {
		if data.Len() == 0 {
			return rpcResponse{}, false, nil
		}
		payload := data.String()
		data.Reset()
		var rr rpcResponse
		if err := json.Unmarshal([]byte(payload), &rr); err != nil {
			return rpcResponse{}, false, nil // not a JSON-RPC frame — ignore
		}
		if rr.ID != nil && *rr.ID == wantID {
			return rr, true, nil
		}
		return rpcResponse{}, false, nil
	}
	for sc.Scan() {
		line := sc.Text()
		if line == "" { // end of one SSE event
			if rr, ok, err := flush(); ok || err != nil {
				return rr, err
			}
			continue
		}
		if d, ok := strings.CutPrefix(line, "data:"); ok {
			data.WriteString(strings.TrimSpace(d))
		}
		// `event:`, `id:`, `:` comments are ignored.
	}
	if err := sc.Err(); err != nil {
		return rpcResponse{}, err
	}
	// Stream ended — try a final flush in case there was no trailing blank line.
	if rr, ok, _ := flush(); ok {
		return rr, nil
	}
	return rpcResponse{}, fmt.Errorf("no matching response in stream")
}

func truncate(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "…"
}
