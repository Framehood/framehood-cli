package render

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestReadable_KnownShapes renders each known tool.action payload (shaped like
// the real worker responses) and asserts the readable output contains the key
// facts — and crucially that it is NOT raw JSON (no braces).
func TestReadable_KnownShapes(t *testing.T) {
	cases := []struct {
		name         string
		tool, action string
		raw          string
		wantContains []string
	}{
		{
			name: "org.info", tool: "org", action: "info",
			raw:          `{"org_id":"o1","name":"Acme","is_personal":false,"your_role":"owner","member_count":3}`,
			wantContains: []string{"Acme", "owner", "shared", "3 members"},
		},
		{
			name: "org.members", tool: "org", action: "members",
			raw:          `{"members":[{"user_id":"u1","email":"a@x.io","role":"owner","active":true,"suspended":false},{"user_id":"u2","email":"b@x.io","role":"member","active":false,"suspended":true}]}`,
			wantContains: []string{"email", "role", "status", "a@x.io", "owner", "active", "b@x.io", "suspended"},
		},
		{
			name: "org.spend", tool: "org", action: "spend",
			raw:          `{"spend":[{"user_id":"u1","email":"a@x.io","spent":1200,"jobs":7}]}`,
			wantContains: []string{"member", "credits spent", "jobs", "a@x.io", "1200 credits", "7"},
		},
		{
			name: "org.trend", tool: "org", action: "trend",
			raw:          `{"trend":[{"day":"2026-06-20","spent":800}]}`,
			wantContains: []string{"day", "credits", "2026-06-20", "800 credits"},
		},
		{
			name: "billing.balance", tool: "billing", action: "balance",
			raw:          `{"balance":1640,"role":"owner","email":"a@x.io"}`,
			wantContains: []string{"1640 credits"},
		},
		{
			name: "billing.balance with plan", tool: "billing", action: "balance",
			raw:          `{"balance":1640,"plan":"pro","email":"a@x.io"}`,
			wantContains: []string{"1640 credits", "plan: pro"},
		},
		{
			name: "billing.plan", tool: "billing", action: "plan",
			raw:          `{"plan":"pro","status":"active","monthly_allowance":5000,"balance":1640,"role":"owner"}`,
			wantContains: []string{"Plan: pro", "Status: active", "Role: owner", "Balance: 1640 credits"},
		},
		{
			name: "billing.plans", tool: "billing", action: "plans",
			raw:          `{"plans":[{"tier":"starter","credits":1000,"price":9},{"tier":"pro","credits":5000,"price":29}]}`,
			wantContains: []string{"plan", "credits", "starter", "1000 credits", "pro", "5000 credits"},
		},
		{
			name: "files.list", tool: "files", action: "list",
			raw:          `{"files":[{"key":"clip.mp4","size":10485760,"uploaded":"2026-06-21T10:00:00Z"}],"truncated":false}`,
			wantContains: []string{"name", "size", "uploaded", "clip.mp4", "10.0MB", "2026-06-21"},
		},
		{
			name: "library.list", tool: "library", action: "list",
			raw:          `{"items":[{"type":"image","prompt":"a red fox","created_at":"2026-06-20T09:00:00Z"}],"total":42}`,
			wantContains: []string{"type", "prompt", "when", "image", "a red fox", "2026-06-20", "1 of 42 shown"},
		},
		{
			name: "library.trashed", tool: "library", action: "trashed",
			raw:          `{"items":[{"type":"video","prompt":"old clip","created_at":"2026-06-19T09:00:00Z"}],"total":1}`,
			wantContains: []string{"video", "old clip", "2026-06-19"},
		},
		{
			name: "project.list", tool: "project", action: "list",
			raw:          `{"projects":[{"name":"Campaign Q3","visibility":"shared"}]}`,
			wantContains: []string{"name", "visibility", "Campaign Q3", "shared"},
		},
		{
			name: "project.current", tool: "project", action: "current",
			raw:          `{"project":{"name":"Campaign Q3","visibility":"shared"}}`,
			wantContains: []string{"Active project: Campaign Q3", "Visibility: shared"},
		},
		{
			name: "billing.transactions", tool: "billing", action: "transactions",
			raw:          `{"transactions":[{"amount":1000,"transaction_type":"subscription_grant","model_display":"","description":"Monthly allowance","created_at":"2026-06-20T09:00:00Z"},{"amount":200,"transaction_type":"refund","model":"flux_schnell","description":"","created_at":"2026-06-19T09:00:00Z"}]}`,
			wantContains: []string{"when", "type", "credits", "description", "subscription_grant", "1000 credits", "Monthly allowance", "refund", "flux_schnell", "2026-06-20"},
		},
		{
			name: "get_status.list", tool: "get_status", action: "list",
			raw:          `{"jobs":[{"job_id":"j1","kind":"flux_schnell","status":"succeeded","created_at":"2026-06-20T09:00:00Z"}],"next_cursor":"c2"}`,
			wantContains: []string{"job id", "kind", "status", "when", "j1", "flux_schnell", "succeeded", "2026-06-20", "--cursor c2"},
		},
		{
			name: "api_keys.list", tool: "api_keys", action: "list",
			raw:          `{"api_keys":[{"api_key":"abcd1234…","name":"ci","created_at":"2026-06-20T09:00:00Z","last_used_at":null,"is_active":true},{"api_key":"efgh5678…","name":"old","created_at":"2026-06-10T09:00:00Z","last_used_at":"2026-06-12T09:00:00Z","is_active":false}]}`,
			wantContains: []string{"key", "name", "status", "created", "last used", "abcd1234…", "ci", "active", "efgh5678…", "revoked", "2026-06-12"},
		},
		{
			name: "models.list (wrapped object)", tool: "models", action: "list",
			raw:          `{"models":[{"name":"flux_schnell","category":"image"},{"name":"seedance_r2v","category":"video"}],"total":2}`,
			wantContains: []string{"model", "category", "flux_schnell", "image", "seedance_r2v", "video", "2 models"},
		},
		{
			// zvs://models (the MCP resource) returns a bare array with richer
			// fields; the catalog table renders the same name/category columns.
			name: "models.list (bare array, MCP resource)", tool: "models", action: "list",
			raw:          `[{"name":"flux_schnell","display_name":"FLUX.1 Schnell","category":"image_gen","cost_per_unit_usd":0},{"name":"seedance_r2v","display_name":"Seedance","category":"video_gen","cost_per_unit_usd":0.4}]`,
			wantContains: []string{"model", "category", "flux_schnell", "image_gen", "seedance_r2v", "video_gen", "2 models"},
		},
		{
			name: "workflows list", tool: "workflows", action: "",
			raw:          `[{"name":"video_production","description":"end-to-end video","skill_url":"/v1/workflows/video_production/skill"}]`,
			wantContains: []string{"workflow", "description", "video_production", "end-to-end video"},
		},
		{
			name: "skill (model)", tool: "skill", action: "",
			raw:          `{"name":"flux_schnell","type":"model","content":"# Flux Schnell\n\nFast text-to-image."}`,
			wantContains: []string{"flux_schnell", "Flux Schnell", "Fast text-to-image."},
		},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			out, ok := Readable(c.tool, c.action, json.RawMessage(c.raw))
			if !ok {
				t.Fatalf("Readable(%s.%s) returned ok=false", c.tool, c.action)
			}
			for _, want := range c.wantContains {
				if !strings.Contains(out, want) {
					t.Errorf("output missing %q\n--- got ---\n%s", want, out)
				}
			}
			// Readable output must not be raw JSON.
			if strings.HasPrefix(strings.TrimSpace(out), "{") || strings.HasPrefix(strings.TrimSpace(out), "[") {
				t.Errorf("output looks like raw JSON, not readable:\n%s", out)
			}
		})
	}
}

// TestReadable_EmptyCollections renders friendly placeholders for empty lists.
func TestReadable_EmptyCollections(t *testing.T) {
	cases := []struct {
		tool, action, raw, want string
	}{
		{"org", "members", `{"members":[]}`, "No members."},
		{"org", "spend", `{"spend":[]}`, "No spend recorded."},
		{"files", "list", `{"files":[]}`, "No files."},
		{"library", "list", `{"items":[],"total":0}`, "No assets."},
		{"project", "list", `{"projects":[]}`, "No projects."},
		{"project", "current", `{"project":null}`, "No active project."},
		{"billing", "transactions", `{"transactions":[]}`, "No transactions."},
		{"get_status", "list", `{"jobs":[]}`, "No jobs."},
		{"api_keys", "list", `{"api_keys":[]}`, "No API keys."},
		{"models", "list", `{"models":[],"total":0}`, "No models."},
		{"models", "list", `[]`, "No models."}, // bare-array (MCP resource) empty case
		{"workflows", "", `[]`, "No workflows."},
	}
	for _, c := range cases {
		out, ok := Readable(c.tool, c.action, json.RawMessage(c.raw))
		if !ok || strings.TrimSpace(out) != c.want {
			t.Errorf("%s.%s empty: got (%q, %v), want %q", c.tool, c.action, out, ok, c.want)
		}
	}
}

// TestReadable_UnknownFallsBack: an unknown tool/action, or a known action with
// a mismatched payload, returns ok=false so the caller pretty-prints.
func TestReadable_UnknownFallsBack(t *testing.T) {
	// Unknown action.
	if _, ok := Readable("image", "create", json.RawMessage(`{"x":1}`)); ok {
		t.Error("unknown action should return ok=false")
	}
	// Known action, wrong shape (no expected keys).
	if _, ok := Readable("org", "info", json.RawMessage(`{"totally":"different"}`)); ok {
		t.Error("known action with mismatched shape should return ok=false")
	}
	// Bare string payload for a known action.
	if _, ok := Readable("billing", "balance", json.RawMessage(`"hi"`)); ok {
		t.Error("a bare string should not satisfy billing.balance")
	}
	// Empty raw.
	if _, ok := Readable("org", "info", json.RawMessage(``)); ok {
		t.Error("empty raw should return ok=false")
	}
}

func TestPrettyJSON(t *testing.T) {
	// Object → indented.
	got := PrettyJSON(json.RawMessage(`{"a":1}`))
	if !strings.Contains(got, "\"a\": 1") {
		t.Errorf("PrettyJSON object = %q, want indented", got)
	}
	// Bare string → unquoted.
	if got := PrettyJSON(json.RawMessage(`"hello"`)); got != "hello" {
		t.Errorf("PrettyJSON bare string = %q, want hello", got)
	}
	// Empty → placeholder.
	if got := PrettyJSON(json.RawMessage(``)); got != "(empty)" {
		t.Errorf("PrettyJSON empty = %q, want (empty)", got)
	}
}

// TestNum_WholeNumbers verifies credits render without trailing ".0".
func TestNum_WholeNumbers(t *testing.T) {
	if got := num(1640); got != "1640" {
		t.Errorf("num(1640) = %q, want 1640", got)
	}
	if got := num(340.5); got != "340.5" {
		t.Errorf("num(340.5) = %q, want 340.5", got)
	}
}
