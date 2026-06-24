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
	t          *testing.T
	expectAct  string // the billing action this call must carry (e.g. "extra_usage")
	lastAction string
	lastArgs   map[string]any
	payload    string // JSON payload to return as the tool result text
	gotRequest bool   // set once a well-formed expected request arrives
}

// handler returns an httptest handler that asserts the request is a well-formed
// MCP tools/call POST to /mcp targeting the "billing" tool with the expected
// action, then returns the canned payload. Any deviation (wrong method/path,
// decode error, wrong tool target or action) fails the test — so broken wiring
// can't pass green.
func (b *billingToolServer) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			b.t.Errorf("mock: method = %q, want POST", r.Method)
			http.Error(w, "bad method", http.StatusMethodNotAllowed)
			return
		}
		if r.URL.Path != "/mcp" {
			b.t.Errorf("mock: path = %q, want /mcp", r.URL.Path)
			http.Error(w, "bad path", http.StatusNotFound)
			return
		}
		var req struct {
			Method string `json:"method"`
			Params struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			} `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			b.t.Errorf("mock: decode request body: %v", err)
			http.Error(w, "bad body", http.StatusBadRequest)
			return
		}
		if req.Method != "tools/call" {
			b.t.Errorf("mock: rpc method = %q, want tools/call", req.Method)
			http.Error(w, "bad method", http.StatusBadRequest)
			return
		}
		if req.Params.Name != "billing" {
			b.t.Errorf("mock: tool target = %q, want billing", req.Params.Name)
			http.Error(w, "wrong tool", http.StatusBadRequest)
			return
		}
		action, _ := req.Params.Arguments["action"].(string)
		if b.expectAct != "" && action != b.expectAct {
			b.t.Errorf("mock: action = %q, want %q", action, b.expectAct)
			http.Error(w, "wrong action", http.StatusBadRequest)
			return
		}
		b.gotRequest = true
		b.lastArgs = req.Params.Arguments
		b.lastAction = action
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
	bts := &billingToolServer{t: t, expectAct: "extra_usage", payload: `{"extra_usage":{"enabled":false,"trigger_below":200,"refill_amount_cents":null,"extra_usage_cap_cents":5000,"credits_per_eur":80,"min_amount_cents":500,"step_cents":500,"spent_this_cycle_cents":0,"has_card":false,"rate_note":"Extra usage bills at a premium rate."}}`}
	srv := httptest.NewServer(bts.handler())
	defer srv.Close()

	if err := runExtraUsage(t, srv.URL); err != nil {
		t.Fatalf("read should not error: %v", err)
	}
	if !bts.gotRequest {
		t.Fatal("no-flag invocation must reach the billing tool")
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
	bts := &billingToolServer{t: t, expectAct: "set_extra_usage", payload: `{"ok":true,"extra_usage":{"enabled":true,"trigger_below":200,"refill_amount_cents":500,"extra_usage_cap_cents":5000,"credits_per_eur":80,"spent_this_cycle_cents":0,"has_card":true,"rate_note":"Premium rate."},"rate_note":"Premium rate."}`}
	srv := httptest.NewServer(bts.handler())
	defer srv.Close()

	if err := runExtraUsage(t, srv.URL, "--enable", "--amount-eur", "5", "--trigger", "200", "--cap-eur", "50"); err != nil {
		t.Fatalf("set should not error: %v", err)
	}
	if !bts.gotRequest {
		t.Fatal("flagged invocation must reach the billing tool")
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
	bts := &billingToolServer{t: t, expectAct: "set_extra_usage", payload: `{"ok":true,"extra_usage":{"enabled":false,"trigger_below":200,"extra_usage_cap_cents":5000,"credits_per_eur":80,"spent_this_cycle_cents":0,"has_card":true}}`}
	srv := httptest.NewServer(bts.handler())
	defer srv.Close()

	if err := runExtraUsage(t, srv.URL, "--disable"); err != nil {
		t.Fatalf("disable should not error: %v", err)
	}
	if !bts.gotRequest {
		t.Fatal("--disable must reach the billing tool")
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
	bts := &billingToolServer{t: t}
	srv := httptest.NewServer(bts.handler())
	defer srv.Close()

	if err := runExtraUsage(t, srv.URL, "--enable", "--amount-eur", "7"); err == nil {
		t.Fatal("expected a local validation error for a non-€5-multiple amount")
	}
	if bts.gotRequest {
		t.Fatalf("a bad amount must fail before any tool call; server saw action %q", bts.lastAction)
	}
}

// TestExtraUsage_EnableAndDisableConflict verifies the mutually-exclusive guard.
func TestExtraUsage_EnableAndDisableConflict(t *testing.T) {
	bts := &billingToolServer{t: t}
	srv := httptest.NewServer(bts.handler())
	defer srv.Close()

	if err := runExtraUsage(t, srv.URL, "--enable", "--disable"); err == nil {
		t.Fatal("expected an error when both --enable and --disable are set")
	}
	if bts.gotRequest {
		t.Fatalf("the conflict must fail before any tool call; server saw action %q", bts.lastAction)
	}
}

// TestExtraUsage_ForbiddenSurfaces verifies the NON-isError {error,message}
// body the tool returns for a non-owner is surfaced as a clear CLI error
// (carrying the human message), not silently rendered as success.
func TestExtraUsage_ForbiddenSurfaces(t *testing.T) {
	bts := &billingToolServer{t: t, expectAct: "extra_usage", payload: `{"error":"forbidden","message":"Only the org owner can view Extra usage."}`}
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

// --- topup ---

// runTopup builds the billing command, runs `topup` with args, and returns the
// resulting error. It points the session at the mock server, mirroring
// runExtraUsage.
func runTopup(t *testing.T, srvURL string, args ...string) error {
	t.Helper()
	cfg := testSessionConfig(t, srvURL)
	cmd := newBillingCmd(cfg)
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs(append([]string{"topup"}, args...))
	return cmd.Execute()
}

// TestValidTopupEUR enforces the whole-euro [€20, €5,000] range (mirrors the
// worker's isValidTopupAmount), including sub-euro rejection.
func TestValidTopupEUR(t *testing.T) {
	cases := []struct {
		eur     float64
		wantErr bool
	}{
		{20, false},     // exactly the minimum
		{21, false},     // not bound to the €5 grid — any whole euro is fine
		{100, false},    // a typical amount
		{5000, false},   // exactly the maximum
		{19, true},      // below minimum
		{19.99, true},   // below minimum (and sub-euro)
		{0, true},       // below minimum
		{-20, true},     // negative
		{20.5, true},    // sub-euro precision
		{5000.01, true}, // above maximum (and sub-euro)
		{5001, true},    // above maximum
	}
	for _, c := range cases {
		err := validTopupEUR(c.eur)
		if (err != nil) != c.wantErr {
			t.Errorf("validTopupEUR(%g) err=%v, wantErr=%v", c.eur, err, c.wantErr)
		}
	}
}

// TestTopup_ValidPrintsURLAndCredits verifies a valid `--eur` drives a topup
// (action=topup) carrying amount_cents + an idempotency_key, and that the
// command succeeds against the canned {url,credits} payload.
func TestTopup_ValidPrintsURLAndCredits(t *testing.T) {
	bts := &billingToolServer{t: t, expectAct: "topup", payload: `{"url":"https://invoice.stripe.com/i/abc","invoice_id":"in_123","credits":8000}`}
	srv := httptest.NewServer(bts.handler())
	defer srv.Close()

	if err := runTopup(t, srv.URL, "--eur", "100"); err != nil {
		t.Fatalf("valid topup should not error: %v", err)
	}
	if !bts.gotRequest {
		t.Fatal("a valid topup must reach the billing tool")
	}
	if bts.lastAction != "topup" {
		t.Fatalf("topup must send action=topup, got %q", bts.lastAction)
	}
	// €100 → 10,000 cents. JSON numbers decode as float64.
	if got, _ := bts.lastArgs["amount_cents"].(float64); got != 10000 {
		t.Errorf("amount_cents = %v, want 10000", bts.lastArgs["amount_cents"])
	}
	// A money write must carry an idempotency key so a retry can't double-bill.
	if key, _ := bts.lastArgs["idempotency_key"].(string); key == "" {
		t.Error("topup must send a non-empty idempotency_key")
	}
}

// TestTopup_BelowMinimumFailsLocally verifies a sub-€20 amount is rejected
// BEFORE any tool call (the server is never reached), as both a client-side
// guard and to avoid a wasted round-trip to the server's 400.
func TestTopup_BelowMinimumFailsLocally(t *testing.T) {
	bts := &billingToolServer{t: t}
	srv := httptest.NewServer(bts.handler())
	defer srv.Close()

	if err := runTopup(t, srv.URL, "--eur", "10"); err == nil {
		t.Fatal("expected a local validation error for a sub-€20 amount")
	}
	if bts.gotRequest {
		t.Fatalf("a below-minimum amount must fail before any tool call; server saw action %q", bts.lastAction)
	}
}

// TestTopup_RequiresEUR verifies omitting --eur fails locally with a clear error,
// before any tool call.
func TestTopup_RequiresEUR(t *testing.T) {
	bts := &billingToolServer{t: t}
	srv := httptest.NewServer(bts.handler())
	defer srv.Close()

	if err := runTopup(t, srv.URL); err == nil {
		t.Fatal("expected an error when --eur is omitted")
	}
	if bts.gotRequest {
		t.Fatalf("a missing --eur must fail before any tool call; server saw action %q", bts.lastAction)
	}
}

// TestTopup_AmountInvalidSurfaces verifies the NON-isError {error,message} body
// the tool returns for a bad amount is surfaced as a clear CLI error, not
// silently rendered as success.
func TestTopup_AmountInvalidSurfaces(t *testing.T) {
	bts := &billingToolServer{t: t, expectAct: "topup", payload: `{"error":"amount_invalid","message":"Top-up must be between €20 and €5,000."}`}
	srv := httptest.NewServer(bts.handler())
	defer srv.Close()

	err := runTopup(t, srv.URL, "--eur", "20")
	if err == nil {
		t.Fatal("expected an amount_invalid error to surface")
	}
	if err.Error() != "Top-up must be between €20 and €5,000." {
		t.Fatalf("amount_invalid message not surfaced; got %q", err.Error())
	}
}

// TestRenderTopup formats the url + credits, handles the needs_action note, and
// rejects a payload with no usable URL (so the caller falls back to raw JSON
// rather than reporting success with no way to pay).
func TestRenderTopup(t *testing.T) {
	out, ok := renderTopup(json.RawMessage(`{"url":"https://invoice.stripe.com/i/abc","invoice_id":"in_1","credits":1600}`))
	if !ok {
		t.Fatal("renderTopup returned ok=false for a valid payload")
	}
	for _, want := range []string{"1600 credits", "https://invoice.stripe.com/i/abc", "Open to pay"} {
		if !strings.Contains(out, want) {
			t.Errorf("topup render missing %q\n%s", want, out)
		}
	}

	// needs_action (off-session SCA) surfaces a confirmation note.
	act, ok := renderTopup(json.RawMessage(`{"url":"https://invoice.stripe.com/i/xyz","credits":1600,"needs_action":true}`))
	if !ok {
		t.Fatal("renderTopup returned ok=false for a needs_action payload")
	}
	if !strings.Contains(act, "needs confirmation") {
		t.Errorf("needs_action render missing the confirmation note\n%s", act)
	}

	// No URL → ok=false so the caller can fall back to raw JSON.
	if _, ok := renderTopup(json.RawMessage(`{"credits":1600}`)); ok {
		t.Error("renderTopup should reject a payload with no url")
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
	// A valid-but-non-object body (e.g. a bare string) carries no error → nil.
	if err := billingError(json.RawMessage(`"just a string"`)); err != nil {
		t.Errorf("a non-object body should yield nil, got %v", err)
	}
	// Malformed JSON must surface as an error, NOT fall through as success — a
	// corrupt billing response can't be treated as "no error".
	if err := billingError(json.RawMessage(`{not json`)); err == nil {
		t.Error("malformed JSON must yield an error, got nil")
	}
	// An empty payload is not valid JSON either → an error.
	if err := billingError(json.RawMessage(``)); err == nil {
		t.Error("empty payload must yield an error, got nil")
	}
}
