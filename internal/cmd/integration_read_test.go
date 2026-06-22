//go:build integration

// Package cmd — opt-in LIVE integration smoke test for the immediate read tools.
//
// This file is excluded from the normal build by the `integration` build tag, so
// `go test ./...` never runs it and it cannot affect CI or local unit runs.
//
// WHAT IT DOES
//
//	It connects to the LIVE Framehood MCP using your stored credentials
//	(~/.framehood/credentials.json, the same ones `framehood login` writes) and
//	calls each FREE read tool — billing(balance), files(list), org(info) — through
//	the raw CallTool path. It asserts each returns without a "decode job" error and
//	yields a sane (non-empty, JSON-ish) payload. These are read-only and cost NO
//	credits, so it is safe to run against production.
//
// HOW TO RUN
//
//	# 1. Be logged in (writes ~/.framehood/credentials.json):
//	framehood login
//
//	# 2. Run the live smoke (build tag + env opt-in; both are required):
//	FRAMEHOOD_E2E=1 go test -tags integration -run TestLiveReadTools_NoDecodeError -v ./internal/cmd/
//
// If FRAMEHOOD_E2E is unset, or no credentials are present, the test SKIPS — it
// never fails a machine that simply isn't set up for live testing.
package cmd

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Framehood/framehood-cli/internal/config"
)

func TestLiveReadTools_NoDecodeError(t *testing.T) {
	if os.Getenv("FRAMEHOOD_E2E") == "" {
		t.Skip("set FRAMEHOOD_E2E=1 to run the live read-tool smoke (read-only, no credits spent)")
	}
	cfg, err := config.Load()
	if err != nil {
		t.Skipf("config.Load: %v", err)
	}
	sess, err := NewSession(cfg)
	if err != nil {
		t.Skipf("not logged in (run `framehood login`): %v", err)
	}
	client := sess.Client()

	// Each free read tool + the args the studio sends for its immediate action.
	cases := []struct {
		name string
		tool string
		args map[string]any
	}{
		{"billing·balance", "billing", map[string]any{"action": "balance"}},
		{"files·list", "files", map[string]any{"action": "list"}},
		{"org·info", "org", map[string]any{"action": "info"}},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			// CallTool is the RIGHT call for read tools: it returns the raw content
			// payload. It must NOT be routed through Submit (Job decode), which is
			// exactly the studio bug this whole change fixes. A "decode job" error
			// here would mean a read was wrongly sent down the Job path.
			raw, err := client.CallTool(ctx, tc.tool, tc.args)
			if err != nil {
				if strings.Contains(err.Error(), "decode job") {
					t.Fatalf("%s: a Job decode happened on a read tool: %v", tc.name, err)
				}
				t.Fatalf("%s: CallTool error: %v", tc.name, err)
			}
			if len(strings.TrimSpace(string(raw))) == 0 {
				t.Fatalf("%s: empty payload", tc.name)
			}
			// The payload must be valid JSON (object, array, or bare string) — a
			// sane read response, not an error blob.
			var any2 any
			if err := json.Unmarshal(raw, &any2); err != nil {
				t.Fatalf("%s: payload is not valid JSON: %v\n%s", tc.name, err, truncForLog(raw))
			}
			t.Logf("%s OK: %s", tc.name, truncForLog(raw))
		})
	}
}

func truncForLog(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 200 {
		return s[:200] + "…"
	}
	return s
}
