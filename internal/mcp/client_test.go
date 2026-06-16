package mcp

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// staticTokens is a Tokens that returns a fixed access token and, on Refresh,
// switches to a second token (to exercise the 401 retry path).
type staticTokens struct {
	access    string
	refreshed string
	calls     int32
}

func (s *staticTokens) Access() string { return s.access }
func (s *staticTokens) Refresh(context.Context) (string, error) {
	atomic.AddInt32(&s.calls, 1)
	s.access = s.refreshed
	return s.refreshed, nil
}

// sseBody renders a single JSON-RPC response as one SSE event.
func sseBody(id int, result string) string {
	return fmt.Sprintf("event: message\ndata: {\"jsonrpc\":\"2.0\",\"id\":%d,\"result\":%s}\n\n", id, result)
}

func TestCall_SSEResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("MCP-Protocol-Version") != ProtocolVersion {
			t.Errorf("missing protocol version header")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, sseBody(1, `{"ok":true}`))
	}))
	defer srv.Close()

	c := New(srv.URL, &staticTokens{access: "tok"})
	res, err := c.Call(context.Background(), "tools/list", nil)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if string(res) != `{"ok":true}` {
		t.Fatalf("unexpected result: %s", res)
	}
}

func TestCall_RefreshesOn401(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&hits, 1)
		if n == 1 {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer tok2" {
			t.Errorf("retry used wrong token: %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"jsonrpc":"2.0","id":1,"result":{"ok":true}}`)
	}))
	defer srv.Close()

	tok := &staticTokens{access: "tok1", refreshed: "tok2"}
	c := New(srv.URL, tok)
	if _, err := c.Call(context.Background(), "tools/list", nil); err != nil {
		t.Fatalf("Call: %v", err)
	}
	if tok.calls != 1 {
		t.Fatalf("expected exactly one refresh, got %d", tok.calls)
	}
}

func TestCallTool_SurfacesToolError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// tools/call result whose content carries an "Error: …" + isError.
		fmt.Fprint(w, `{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"Error: prompt required"}],"isError":true}}`)
	}))
	defer srv.Close()

	c := New(srv.URL, &staticTokens{access: "tok"})
	_, err := c.CallTool(context.Background(), "image", map[string]any{"action": "create"})
	if err == nil || err.Error() != "prompt required" {
		t.Fatalf("expected tool error 'prompt required', got %v", err)
	}
}

func TestParseSSE_SkipsUnrelatedEvents(t *testing.T) {
	stream := "event: message\ndata: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/progress\"}\n\n" +
		sseBody(7, `{"done":true}`)
	rr, err := parseSSE(strings.NewReader(stream), 7)
	if err != nil {
		t.Fatalf("parseSSE: %v", err)
	}
	if string(rr.Result) != `{"done":true}` {
		t.Fatalf("unexpected: %s", rr.Result)
	}
}
