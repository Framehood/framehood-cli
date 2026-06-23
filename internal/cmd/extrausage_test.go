package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestValidExtraUsageEUR enforces the €5-multiple-≥€5 rule (mirrors the worker's
// isValidExtraUsageAmount), including sub-cent rejection.
func TestValidExtraUsageEUR(t *testing.T) {
	cases := []struct {
		eur     float64
		wantErr bool
	}{
		{5, false},     // exactly the minimum
		{10, false},    // a €5 multiple
		{25, false},    // a €5 multiple
		{500, false},   // large but valid
		{0, true},      // below minimum
		{4.99, true},   // below minimum (and not a multiple)
		{5.01, true},   // not a whole-cent multiple
		{7, true},      // not a €5 multiple
		{5.5, true},    // not a €5 multiple
		{-5, true},     // negative
		{12.5, true},   // not a €5 multiple
		{15.001, true}, // sub-cent precision
	}
	for _, c := range cases {
		err := validExtraUsageEUR(c.eur)
		if (err != nil) != c.wantErr {
			t.Errorf("validExtraUsageEUR(%g) err=%v, wantErr=%v", c.eur, err, c.wantErr)
		}
	}
}

// billingToolServer mocks the worker's /mcp endpoint for the billing tool. It
// records the (action) and the raw arguments of each tools/call and returns a
// canned per-action payload, so a test can assert exactly what the extra-usage
// command sent.
type billingToolServer struct {
	lastAction string
	lastArgs   map[string]any
	payload    string // JSON payload to return as the tool result text
}

func (b *billingToolServer) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Params struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			} `json:"params"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		b.lastArgs = req.Params.Arguments
		if a, ok := req.Params.Arguments["action"].(string); ok {
			b.lastAction = a
		}
		payload := b.payload
		if payload == "" {
			payload = `{"ok":true}`
		}
		inner, _ := json.Marshal(payload)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":%s}]}}`, inner)
	}
}

// runExtraUsage builds the billing command, runs `extra-usage` with args, and
// returns the resulting error. It points the session at the mock server.
func runExtraUsage(t *testing.T, srvURL string, args ...string) error {
	t.Helper()
	cfg := testSessionConfig(t, srvURL)
	cmd := newBillingCmd(cfg)
	// Match the real root's behavior so a RunE error doesn't dump usage/errors to
	// stderr during the test run (the root sets these in Execute()).
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs(append([]string{"extra-usage"}, args...))
	// Cobra reads cmd.Context() in the RunE; SetArgs alone doesn't set one, so
	// Execute installs a background context for us.
	return cmd.Execute()
}

// TestExtraUsage_NoFlagsReads verifies a no-flag invocation performs the read
// (action=extra_usage), not a write.
func TestExtraUsage_NoFlagsReads(t *testing.T) {
	bts := &billingToolServer{payload: `{"extra_usage":{"enabled":false,"trigger_below":200,"refill_amount_cents":null,"extra_usage_cap_cents":5000,"credits_per_eur":80,"min_amount_cents":500,"step_cents":500,"spent_this_cycle_cents":0,"has_card":false,"rate_note":"Extra usage bills at a premium rate."}}`}
	srv := httptest.NewServer(bts.handler())
	defer srv.Close()

	if err := runExtraUsage(t, srv.URL); err != nil {
		t.Fatalf("read should not error: %v", err)
	}
	if bts.lastAction != "extra_usage" {
		t.Fatalf("no-flag invocation must read (action=extra_usage), got %q", bts.lastAction)
	}
}

// TestExtraUsage_EnableSendsOnlySetFields verifies that --enable --amount-eur
// --trigger --cap-eur drive a set_extra_usage carrying exactly those fields
// (and that --cap-eur maps to the euro-denominated extra_usage_cap_eur the MCP
// tool accepts).
func TestExtraUsage_EnableSendsOnlySetFields(t *testing.T) {
	bts := &billingToolServer{payload: `{"ok":true,"extra_usage":{"enabled":true,"trigger_below":200,"refill_amount_cents":500,"extra_usage_cap_cents":5000,"credits_per_eur":80,"spent_this_cycle_cents":0,"has_card":true,"rate_note":"Premium rate."},"rate_note":"Premium rate."}`}
	srv := httptest.NewServer(bts.handler())
	defer srv.Close()

	if err := runExtraUsage(t, srv.URL, "--enable", "--amount-eur", "5", "--trigger", "200", "--cap-eur", "50"); err != nil {
		t.Fatalf("set should not error: %v", err)
	}
	if bts.lastAction != "set_extra_usage" {
		t.Fatalf("flagged invocation must write (action=set_extra_usage), got %q", bts.lastAction)
	}
	if got, ok := bts.lastArgs["enabled"].(bool); !ok || !got {
		t.Errorf("enabled = %v, want true", bts.lastArgs["enabled"])
	}
	// JSON numbers decode as float64.
	if got, _ := bts.lastArgs["amount_eur"].(float64); got != 5 {
		t.Errorf("amount_eur = %v, want 5", bts.lastArgs["amount_eur"])
	}
	if got, _ := bts.lastArgs["trigger_below"].(float64); got != 200 {
		t.Errorf("trigger_below = %v, want 200", bts.lastArgs["trigger_below"])
	}
	if got, _ := bts.lastArgs["extra_usage_cap_eur"].(float64); got != 50 {
		t.Errorf("extra_usage_cap_eur = %v, want 50", bts.lastArgs["extra_usage_cap_eur"])
	}
	// An untouched field must NOT be sent, so the server keeps its value.
	if _, present := bts.lastArgs["extra_usage_cap_cents"]; present {
		t.Errorf("cap should be sent as euros, not cents; got extra_usage_cap_cents in args")
	}
}

// TestExtraUsage_DisableSendsEnabledFalse verifies --disable sends enabled:false.
func TestExtraUsage_DisableSendsEnabledFalse(t *testing.T) {
	bts := &billingToolServer{payload: `{"ok":true,"extra_usage":{"enabled":false,"trigger_below":200,"extra_usage_cap_cents":5000,"credits_per_eur":80,"spent_this_cycle_cents":0,"has_card":true}}`}
	srv := httptest.NewServer(bts.handler())
	defer srv.Close()

	if err := runExtraUsage(t, srv.URL, "--disable"); err != nil {
		t.Fatalf("disable should not error: %v", err)
	}
	if bts.lastAction != "set_extra_usage" {
		t.Fatalf("--disable must write, got %q", bts.lastAction)
	}
	if got, ok := bts.lastArgs["enabled"].(bool); !ok || got {
		t.Errorf("enabled = %v, want false", bts.lastArgs["enabled"])
	}
}

// TestExtraUsage_BadAmountFailsLocally verifies a non-€5-multiple amount is
// rejected BEFORE any tool call (the server is never reached).
func TestExtraUsage_BadAmountFailsLocally(t *testing.T) {
	bts := &billingToolServer{}
	srv := httptest.NewServer(bts.handler())
	defer srv.Close()

	if err := runExtraUsage(t, srv.URL, "--enable", "--amount-eur", "7"); err == nil {
		t.Fatal("expected a local validation error for a non-€5-multiple amount")
	}
	if bts.lastAction != "" {
		t.Fatalf("a bad amount must fail before any tool call; server saw action %q", bts.lastAction)
	}
}

// TestExtraUsage_EnableAndDisableConflict verifies the mutually-exclusive guard.
func TestExtraUsage_EnableAndDisableConflict(t *testing.T) {
	bts := &billingToolServer{}
	srv := httptest.NewServer(bts.handler())
	defer srv.Close()

	if err := runExtraUsage(t, srv.URL, "--enable", "--disable"); err == nil {
		t.Fatal("expected an error when both --enable and --disable are set")
	}
	if bts.lastAction != "" {
		t.Fatalf("the conflict must fail before any tool call; server saw action %q", bts.lastAction)
	}
}

// TestExtraUsage_ForbiddenSurfaces verifies the NON-isError {error,message}
// body the tool returns for a non-owner is surfaced as a clear CLI error
// (carrying the human message), not silently rendered as success.
func TestExtraUsage_ForbiddenSurfaces(t *testing.T) {
	bts := &billingToolServer{payload: `{"error":"forbidden","message":"Only the org owner can view Extra usage."}`}
	srv := httptest.NewServer(bts.handler())
	defer srv.Close()

	err := runExtraUsage(t, srv.URL)
	if err == nil {
		t.Fatal("expected a forbidden error to surface")
	}
	if err.Error() != "Only the org owner can view Extra usage." {
		t.Fatalf("forbidden message not surfaced; got %q", err.Error())
	}
}

// TestRenderExtraUsage formats both the read shape and the write shape, and
// surfaces the rate note. It checks a few load-bearing substrings rather than
// the exact block.
func TestRenderExtraUsage(t *testing.T) {
	read := `{"extra_usage":{"enabled":true,"trigger_below":200,"refill_amount_cents":500,"extra_usage_cap_cents":5000,"credits_per_eur":80,"spent_this_cycle_cents":250,"has_card":true,"rate_note":"Extra usage bills at a premium rate."}}`
	out, ok := renderExtraUsage(json.RawMessage(read))
	if !ok {
		t.Fatal("renderExtraUsage(read) returned ok=false")
	}
	for _, want := range []string{"Extra usage: on", "€5", "≈400 credits", "200 credits", "€50", "€2.50 used", "card on file", "premium rate"} {
		if !strings.Contains(out, want) {
			t.Errorf("read render missing %q\n%s", want, out)
		}
	}

	// Write shape carries rate_note on the wrapper.
	write := `{"ok":true,"extra_usage":{"enabled":false,"trigger_below":200,"extra_usage_cap_cents":5000,"credits_per_eur":80,"spent_this_cycle_cents":0,"has_card":false},"rate_note":"Premium overflow."}`
	out2, ok := renderExtraUsage(json.RawMessage(write))
	if !ok {
		t.Fatal("renderExtraUsage(write) returned ok=false")
	}
	for _, want := range []string{"Extra usage: off", "no card on file", "Premium overflow."} {
		if !strings.Contains(out2, want) {
			t.Errorf("write render missing %q\n%s", want, out2)
		}
	}

	// A non-config shape must not match.
	if _, ok := renderExtraUsage(json.RawMessage(`{"plan":"pro"}`)); ok {
		t.Error("renderExtraUsage should reject a non-extra-usage payload")
	}
}

// TestBillingError extracts {error,message}, preferring the message, and returns
// nil when there is no error.
func TestBillingError(t *testing.T) {
	if err := billingError(json.RawMessage(`{"ok":true,"extra_usage":{}}`)); err != nil {
		t.Errorf("no-error payload should yield nil, got %v", err)
	}
	if err := billingError(json.RawMessage(`{"error":"card_required","message":"Add a card first."}`)); err == nil || err.Error() != "Add a card first." {
		t.Errorf("message should be preferred; got %v", err)
	}
	// Error with no message falls back to the (underscores→spaces) code.
	if err := billingError(json.RawMessage(`{"error":"amount_invalid"}`)); err == nil || err.Error() != "amount invalid" {
		t.Errorf("bare error should map underscores to spaces; got %v", err)
	}
}
