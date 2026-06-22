// Package render turns raw MCP read/console tool payloads into human-readable
// text. It is a pure formatter shared by the one-shot CLI and the interactive
// studio's READ panel, so both surfaces show labeled fields and compact tables
// instead of raw JSON.
//
// Every formatter is keyed by tool+action and matched against the EXACT
// response shapes the worker returns (see worker/src/tools/*.ts and
// worker/src/handlers/*.ts). Any unknown or unhandled shape falls back to the
// caller's pretty-print, so no data is ever hidden or lost.
package render

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// Readable renders a tool's raw response for tool+action into human-readable
// text. It returns ok=false when the action has no dedicated formatter or the
// payload doesn't match the expected shape — callers then fall back to their
// own pretty-print (PrettyJSON is provided for that).
func Readable(tool, action string, raw json.RawMessage) (string, bool) {
	if len(raw) == 0 {
		return "", false
	}
	f, ok := formatters[tool+"."+action]
	if !ok {
		return "", false
	}
	return f(raw)
}

// PrettyJSON indents valid JSON; a bare JSON string is unquoted; anything else
// is returned trimmed. It never errors — the fallback for unknown shapes.
func PrettyJSON(raw json.RawMessage) string {
	s := strings.TrimSpace(string(raw))
	if s == "" {
		return "(empty)"
	}
	var str string
	if json.Unmarshal(raw, &str) == nil {
		return strings.TrimSpace(str)
	}
	var indented bytes.Buffer
	if err := json.Indent(&indented, raw, "", "  "); err == nil {
		return indented.String()
	}
	return s
}

// formatter renders one tool.action payload; ok=false means "shape didn't
// match, fall back".
type formatter func(raw json.RawMessage) (string, bool)

var formatters = map[string]formatter{
	"org.info":             fmtOrgInfo,
	"org.members":          fmtOrgMembers,
	"org.spend":            fmtOrgSpend,
	"org.trend":            fmtOrgTrend,
	"billing.balance":      fmtBillingBalance,
	"billing.plan":         fmtBillingPlan,
	"billing.plans":        fmtBillingPlans,
	"billing.transactions": fmtBillingTransactions,
	"files.list":           fmtFilesList,
	"library.list":         fmtLibraryList,
	"library.trashed":      fmtLibraryList,
	"project.list":         fmtProjectList,
	"project.current":      fmtProjectCurrent,
	"get_status.list":      fmtJobsList,
	"api_keys.list":        fmtAPIKeysList,
	"models.list":          fmtModelsList,
	"workflows.":           fmtWorkflowsList,
	"skill.":               fmtSkill,
}

// --- org ---

// org.info → {org_id, name, is_personal, your_role, member_count}
func fmtOrgInfo(raw json.RawMessage) (string, bool) {
	var v struct {
		Name        string `json:"name"`
		IsPersonal  bool   `json:"is_personal"`
		YourRole    string `json:"your_role"`
		MemberCount *int   `json:"member_count"`
	}
	if err := json.Unmarshal(raw, &v); err != nil || v.Name == "" {
		return "", false
	}
	var b lines
	b.kv("Organization", v.Name)
	if v.YourRole != "" {
		b.kv("Your role", v.YourRole)
	}
	if v.IsPersonal {
		b.kv("Type", "personal")
	} else {
		b.kv("Type", "shared")
	}
	if v.MemberCount != nil {
		b.kv("Members", plural(*v.MemberCount, "member"))
	}
	return b.String(), true
}

// org.members → {members:[{user_id, email, role, active, suspended, joined_at}]}
// (RPC public.org_members returns explicit `active`/`suspended` booleans; older
// shapes used `suspended_at` — both are honored.)
func fmtOrgMembers(raw json.RawMessage) (string, bool) {
	var v struct {
		Members []struct {
			Email       string `json:"email"`
			Role        string `json:"role"`
			Suspended   *bool  `json:"suspended"`
			SuspendedAt any    `json:"suspended_at"`
			Active      *bool  `json:"active"`
		} `json:"members"`
	}
	if err := json.Unmarshal(raw, &v); err != nil || v.Members == nil {
		return "", false
	}
	if len(v.Members) == 0 {
		return "No members.", true
	}
	rows := make([][]string, 0, len(v.Members))
	for _, m := range v.Members {
		status := "active"
		switch {
		case (m.Suspended != nil && *m.Suspended) || (m.Suspended == nil && m.SuspendedAt != nil):
			status = "suspended"
		case m.Active != nil && !*m.Active:
			status = "inactive"
		}
		rows = append(rows, []string{orDash(m.Email), orDash(m.Role), status})
	}
	return table([]string{"email", "role", "status"}, rows), true
}

// org.spend → {spend:[{user_id, email, spent, jobs}]}
// (RPC public.org_member_spend returns columns user_id,email,spent,jobs.)
func fmtOrgSpend(raw json.RawMessage) (string, bool) {
	var v struct {
		Spend []struct {
			Email string   `json:"email"`
			Spent *float64 `json:"spent"`
			Jobs  *int     `json:"jobs"`
		} `json:"spend"`
	}
	if err := json.Unmarshal(raw, &v); err != nil || v.Spend == nil {
		return "", false
	}
	if len(v.Spend) == 0 {
		return "No spend recorded.", true
	}
	rows := make([][]string, 0, len(v.Spend))
	for _, s := range v.Spend {
		jobs := "—"
		if s.Jobs != nil {
			jobs = fmt.Sprintf("%d", *s.Jobs)
		}
		rows = append(rows, []string{orDash(s.Email), credits(s.Spent), jobs})
	}
	return table([]string{"member", "credits spent", "jobs"}, rows), true
}

// org.trend → {trend:[{day, spent}]}
// (RPC public.org_spend_trend returns columns day,spent.)
func fmtOrgTrend(raw json.RawMessage) (string, bool) {
	var v struct {
		Trend []struct {
			Day   string   `json:"day"`
			Spent *float64 `json:"spent"`
		} `json:"trend"`
	}
	if err := json.Unmarshal(raw, &v); err != nil || v.Trend == nil {
		return "", false
	}
	if len(v.Trend) == 0 {
		return "No spend in this window.", true
	}
	rows := make([][]string, 0, len(v.Trend))
	for _, t := range v.Trend {
		rows = append(rows, []string{orDash(t.Day), credits(t.Spent)})
	}
	return table([]string{"day", "credits"}, rows), true
}

// --- billing ---

// billing.balance → {balance, role, email}
func fmtBillingBalance(raw json.RawMessage) (string, bool) {
	var v struct {
		Balance *float64 `json:"balance"`
		Plan    string   `json:"plan"`
		Email   string   `json:"email"`
	}
	if err := json.Unmarshal(raw, &v); err != nil || v.Balance == nil {
		return "", false
	}
	out := credits(v.Balance)
	if v.Plan != "" {
		out += "  ·  plan: " + v.Plan
	}
	return out, true
}

// billing.plan → {plan, status, monthly_allowance, balance, role, ...}
func fmtBillingPlan(raw json.RawMessage) (string, bool) {
	var v struct {
		Plan             string   `json:"plan"`
		Status           string   `json:"status"`
		MonthlyAllowance *float64 `json:"monthly_allowance"`
		Balance          *float64 `json:"balance"`
		Role             string   `json:"role"`
	}
	if err := json.Unmarshal(raw, &v); err != nil || v.Plan == "" {
		return "", false
	}
	var b lines
	b.kv("Plan", v.Plan)
	if v.Status != "" {
		b.kv("Status", v.Status)
	}
	if v.Role != "" {
		b.kv("Role", v.Role)
	}
	if v.MonthlyAllowance != nil && *v.MonthlyAllowance > 0 {
		b.kv("Monthly allowance", credits(v.MonthlyAllowance))
	}
	if v.Balance != nil {
		b.kv("Balance", credits(v.Balance))
	}
	return b.String(), true
}

// billing.plans → {plans:[{tier, credits, price, currency, ...}]}
func fmtBillingPlans(raw json.RawMessage) (string, bool) {
	var v struct {
		Plans []struct {
			Tier    string   `json:"tier"`
			Credits *float64 `json:"credits"`
			Price   *float64 `json:"price"`
		} `json:"plans"`
	}
	if err := json.Unmarshal(raw, &v); err != nil || v.Plans == nil {
		return "", false
	}
	if len(v.Plans) == 0 {
		return "No plans available.", true
	}
	rows := make([][]string, 0, len(v.Plans))
	for _, p := range v.Plans {
		rows = append(rows, []string{orDash(p.Tier), credits(p.Credits)})
	}
	return table([]string{"plan", "credits"}, rows), true
}

// billing.transactions → {transactions:[{amount, transaction_type, model_display,
// model, units, unit_credits, description, created_at}]}. Credits only — the
// worker never selects cost_usd / unit_cost_usd, so the markup stays hidden.
func fmtBillingTransactions(raw json.RawMessage) (string, bool) {
	var v struct {
		Transactions []struct {
			Amount          *float64 `json:"amount"`
			TransactionType string   `json:"transaction_type"`
			Model           string   `json:"model"`
			ModelDisplay    string   `json:"model_display"`
			Description     string   `json:"description"`
			CreatedAt       string   `json:"created_at"`
		} `json:"transactions"`
	}
	if err := json.Unmarshal(raw, &v); err != nil || v.Transactions == nil {
		return "", false
	}
	if len(v.Transactions) == 0 {
		return "No transactions.", true
	}
	rows := make([][]string, 0, len(v.Transactions))
	for _, t := range v.Transactions {
		desc := t.Description
		if desc == "" {
			desc = t.ModelDisplay
		}
		if desc == "" {
			desc = t.Model
		}
		rows = append(rows, []string{shortTime(t.CreatedAt), orDash(t.TransactionType), credits(t.Amount), truncate(desc, 40)})
	}
	return table([]string{"when", "type", "credits", "description"}, rows), true
}

// --- files ---

// files.list → {files:[{key, size, uploaded}], truncated}
func fmtFilesList(raw json.RawMessage) (string, bool) {
	var v struct {
		Files []struct {
			Key      string   `json:"key"`
			Size     *float64 `json:"size"`
			Uploaded string   `json:"uploaded"`
		} `json:"files"`
		Truncated bool `json:"truncated"`
	}
	if err := json.Unmarshal(raw, &v); err != nil || v.Files == nil {
		return "", false
	}
	if len(v.Files) == 0 {
		return "No files.", true
	}
	rows := make([][]string, 0, len(v.Files))
	for _, f := range v.Files {
		rows = append(rows, []string{orDash(f.Key), humanSize(f.Size), shortTime(f.Uploaded)})
	}
	out := table([]string{"name", "size", "uploaded"}, rows)
	if v.Truncated {
		out += "\n…more (truncated)"
	}
	return out, true
}

// --- library ---

// library.list / library.trashed → {items:[{type, prompt, created_at, ...}], total}
func fmtLibraryList(raw json.RawMessage) (string, bool) {
	var v struct {
		Items []struct {
			Type      string `json:"type"`
			Name      string `json:"name"`
			Prompt    string `json:"prompt"`
			CreatedAt string `json:"created_at"`
		} `json:"items"`
		Total *int `json:"total"`
	}
	if err := json.Unmarshal(raw, &v); err != nil || v.Items == nil {
		return "", false
	}
	if len(v.Items) == 0 {
		return "No assets.", true
	}
	rows := make([][]string, 0, len(v.Items))
	for _, it := range v.Items {
		desc := it.Prompt
		if desc == "" {
			desc = it.Name
		}
		rows = append(rows, []string{orDash(it.Type), truncate(desc, 48), shortTime(it.CreatedAt)})
	}
	out := table([]string{"type", "prompt", "when"}, rows)
	if v.Total != nil && *v.Total > len(v.Items) {
		out += fmt.Sprintf("\n%d of %d shown", len(v.Items), *v.Total)
	}
	return out, true
}

// --- project ---

// project.list → {projects:[{name, visibility, ...}]}
func fmtProjectList(raw json.RawMessage) (string, bool) {
	var v struct {
		Projects []projectShape `json:"projects"`
	}
	if err := json.Unmarshal(raw, &v); err != nil || v.Projects == nil {
		return "", false
	}
	if len(v.Projects) == 0 {
		return "No projects.", true
	}
	rows := make([][]string, 0, len(v.Projects))
	for _, p := range v.Projects {
		rows = append(rows, []string{orDash(p.Name), orDash(p.Visibility)})
	}
	return table([]string{"name", "visibility"}, rows), true
}

// project.current → {project: {...}|null}
func fmtProjectCurrent(raw json.RawMessage) (string, bool) {
	var v struct {
		Project *projectShape `json:"project"`
	}
	// Distinguish "no project key" (unknown shape) from "project: null" (no
	// active project). Decode into a map first to check the key exists.
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(raw, &probe); err != nil {
		return "", false
	}
	if _, hasKey := probe["project"]; !hasKey {
		return "", false
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		return "", false
	}
	if v.Project == nil {
		return "No active project.", true
	}
	var b lines
	b.kv("Active project", orDash(v.Project.Name))
	if v.Project.Visibility != "" {
		b.kv("Visibility", v.Project.Visibility)
	}
	return b.String(), true
}

type projectShape struct {
	Name       string `json:"name"`
	Visibility string `json:"visibility"`
}

// --- jobs (get_status.list) ---

// get_status.list → {jobs:[{job_id, kind, status, created_at}], next_cursor}.
// The generation-history feed. Cost is intentionally omitted (credits-only
// surface elsewhere; the feed shows what ran, not what it cost).
func fmtJobsList(raw json.RawMessage) (string, bool) {
	var v struct {
		Jobs []struct {
			JobID     string `json:"job_id"`
			Kind      string `json:"kind"`
			Status    string `json:"status"`
			CreatedAt string `json:"created_at"`
		} `json:"jobs"`
		NextCursor string `json:"next_cursor"`
	}
	if err := json.Unmarshal(raw, &v); err != nil || v.Jobs == nil {
		return "", false
	}
	if len(v.Jobs) == 0 {
		return "No jobs.", true
	}
	rows := make([][]string, 0, len(v.Jobs))
	for _, j := range v.Jobs {
		rows = append(rows, []string{orDash(j.JobID), orDash(j.Kind), orDash(j.Status), shortTime(j.CreatedAt)})
	}
	out := table([]string{"job id", "kind", "status", "when"}, rows)
	if v.NextCursor != "" {
		out += "\n…more — list again with --cursor " + v.NextCursor
	}
	return out, true
}

// --- api_keys ---

// api_keys.list → {api_keys:[{api_key (prefix…), name, created_at, last_used_at,
// is_active}]}. The displayed api_key is a non-secret prefix hint, never the
// full secret (that is shown once at create time).
func fmtAPIKeysList(raw json.RawMessage) (string, bool) {
	var v struct {
		APIKeys []struct {
			APIKey     string `json:"api_key"`
			Name       string `json:"name"`
			CreatedAt  string `json:"created_at"`
			LastUsedAt string `json:"last_used_at"`
			IsActive   *bool  `json:"is_active"`
		} `json:"api_keys"`
	}
	if err := json.Unmarshal(raw, &v); err != nil || v.APIKeys == nil {
		return "", false
	}
	if len(v.APIKeys) == 0 {
		return "No API keys.", true
	}
	rows := make([][]string, 0, len(v.APIKeys))
	for _, k := range v.APIKeys {
		status := "active"
		if k.IsActive != nil && !*k.IsActive {
			status = "revoked"
		}
		rows = append(rows, []string{orDash(k.APIKey), orDash(k.Name), status, shortTime(k.CreatedAt), shortTime(k.LastUsedAt)})
	}
	return table([]string{"key", "name", "status", "created", "last used"}, rows), true
}

// --- models / workflows / skill (zvs:// catalog reads over /mcp) ---

// modelRow is one catalog entry. The MCP models resource (zvs://models) returns
// a bare array of these; the legacy REST shape wrapped them as {models:[…]}.
type modelRow struct {
	Name     string `json:"name"`
	Category string `json:"category"`
}

// models.list → either a bare array [{name, category}] (the zvs://models MCP
// resource) or {models:[{name, category}], total} (the legacy REST shape). Both
// are rendered as the same model catalog table.
func fmtModelsList(raw json.RawMessage) (string, bool) {
	var models []modelRow
	var total *int

	// Try the bare-array shape first (what the MCP resource returns).
	if err := json.Unmarshal(raw, &models); err != nil {
		// Fall back to the wrapped {models, total} object shape.
		var v struct {
			Models []modelRow `json:"models"`
			Total  *int       `json:"total"`
		}
		if err := json.Unmarshal(raw, &v); err != nil || v.Models == nil {
			return "", false
		}
		models, total = v.Models, v.Total
	}

	if len(models) == 0 {
		return "No models.", true
	}
	rows := make([][]string, 0, len(models))
	for _, m := range models {
		rows = append(rows, []string{orDash(m.Name), orDash(m.Category)})
	}
	out := table([]string{"model", "category"}, rows)
	if total != nil {
		out += fmt.Sprintf("\n%s", plural(*total, "model"))
	} else {
		out += fmt.Sprintf("\n%s", plural(len(models), "model"))
	}
	return out, true
}

// workflows → [{name, description, skill_url}] (a bare array). The pipeline catalog.
func fmtWorkflowsList(raw json.RawMessage) (string, bool) {
	var v []struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		return "", false
	}
	if len(v) == 0 {
		return "No workflows.", true
	}
	rows := make([][]string, 0, len(v))
	for _, w := range v {
		rows = append(rows, []string{orDash(w.Name), truncate(w.Description, 56)})
	}
	return table([]string{"workflow", "description"}, rows), true
}

// skill → {name, type, content} (model/workflow skill) or {model, prompt_guide}
// (the prompt-guide endpoint). Markdown/text content is printed as-is.
func fmtSkill(raw json.RawMessage) (string, bool) {
	var v struct {
		Name    string `json:"name"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(raw, &v); err == nil && v.Content != "" {
		head := ""
		if v.Name != "" {
			head = v.Name + "\n\n"
		}
		return head + strings.TrimSpace(v.Content), true
	}
	return "", false
}

// --- shared rendering helpers ---

// lines accumulates "Label: value" rows.
type lines struct{ rows []string }

func (l *lines) kv(k, v string) { l.rows = append(l.rows, k+": "+v) }
func (l *lines) String() string { return strings.Join(l.rows, "\n") }

// table renders headers + rows as a left-aligned, space-padded grid.
func table(headers []string, rows [][]string) string {
	widths := make([]int, len(headers))
	for i, h := range headers {
		widths[i] = len([]rune(h))
	}
	for _, r := range rows {
		for i := 0; i < len(headers) && i < len(r); i++ {
			if w := len([]rune(r[i])); w > widths[i] {
				widths[i] = w
			}
		}
	}
	var b strings.Builder
	writeRow := func(cells []string) {
		for i := 0; i < len(headers); i++ {
			cell := ""
			if i < len(cells) {
				cell = cells[i]
			}
			b.WriteString(cell)
			if i < len(headers)-1 {
				b.WriteString(strings.Repeat(" ", widths[i]-len([]rune(cell))+2))
			}
		}
		b.WriteByte('\n')
	}
	writeRow(headers)
	for _, r := range rows {
		writeRow(r)
	}
	return strings.TrimRight(b.String(), "\n")
}

func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "—"
	}
	return s
}

// credits renders a numeric credit value as "N credits" (integer when whole).
func credits(n *float64) string {
	if n == nil {
		return "—"
	}
	return fmt.Sprintf("%s credits", num(*n))
}

// num renders a float without a trailing ".0" for whole numbers.
func num(f float64) string {
	if !math.IsInf(f, 0) && !math.IsNaN(f) && f == math.Trunc(f) {
		return strconv.FormatFloat(f, 'f', 0, 64)
	}
	return strconv.FormatFloat(f, 'f', -1, 64)
}

func plural(n int, unit string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", unit)
	}
	return fmt.Sprintf("%d %ss", n, unit)
}

// humanSize renders a byte count as a compact human size.
func humanSize(n *float64) string {
	if n == nil {
		return "—"
	}
	b := *n
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%dB", int64(b))
	}
	div, exp := float64(unit), 0
	for b/div >= unit && exp < 4 {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f%cB", b/div, "KMGT"[exp])
}

// shortTime renders an ISO-8601 timestamp as "2006-01-02"; non-timestamps pass
// through trimmed (and "—" when empty).
func shortTime(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "—"
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05.999999Z07:00", "2006-01-02 15:04:05"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.Format("2006-01-02")
		}
	}
	// Already a plain date or unparseable — show the date prefix if present.
	if len(s) >= 10 {
		return s[:10]
	}
	return s
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}
