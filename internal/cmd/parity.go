package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"strings"
	"time"

	"github.com/Framehood/framehood-cli/internal/config"
	"github.com/Framehood/framehood-cli/internal/mcp"
	"github.com/Framehood/framehood-cli/internal/render"
	"github.com/spf13/cobra"
)

// This file wires the one-shot commands that bring the CLI to parity with the
// MCP tool surface and the REST read endpoints. Each command is a thin wrapper
// over an MCP tool action (via callTool) or a REST GET (via getReadable) — it
// validates required args and renders readable output, never reimplementing the
// server-side logic.

// --- jobs (get_status: list | cancel) ---

// newJobsCmd — the generation-history feed and per-job cancel (MCP `get_status`).
func newJobsCmd(cfg config.Config) *cobra.Command {
	var kind, status string
	var limit int
	list := func(cmd *cobra.Command, _ []string) error {
		a := map[string]any{"action": "list"}
		if kind != "" {
			a["kind"] = kind
		}
		if status != "" {
			a["status"] = status
		}
		// Only forward --limit when the user explicitly set it, and enforce the
		// documented 1–100 range so an out-of-bounds value fails clearly instead
		// of being silently clamped (or rejected) by the server.
		if cmd.Flags().Changed("limit") {
			if err := checkLimit(limit, 1, 100); err != nil {
				return err
			}
			a["limit"] = limit
		}
		return callTool(cmd, cfg, "get_status", a)
	}
	cmd := &cobra.Command{
		Use:   "jobs",
		Short: "Your generation history; cancel a running job",
		Example: "  framehood jobs\n" +
			"  framehood jobs list --kind flux_schnell --status running,succeeded\n" +
			"  framehood jobs cancel <job-id>",
		RunE: list,
	}
	cmd.Flags().StringVar(&kind, "kind", "", "Filter by job kind (e.g. flux_schnell)")
	cmd.Flags().StringVar(&status, "status", "", "Filter by status, comma-separated (e.g. running,succeeded)")
	cmd.Flags().IntVarP(&limit, "limit", "n", 0, "Max rows (1–100)")

	listSub := &cobra.Command{
		Use:   "list",
		Short: "List recent jobs (the generation-history feed)",
		Args:  cobra.NoArgs,
		RunE:  list,
	}
	listSub.Flags().StringVar(&kind, "kind", "", "Filter by job kind (e.g. flux_schnell)")
	listSub.Flags().StringVar(&status, "status", "", "Filter by status, comma-separated (e.g. running,succeeded)")
	listSub.Flags().IntVarP(&limit, "limit", "n", 0, "Max rows (1–100)")

	cmd.AddCommand(
		listSub,
		&cobra.Command{Use: "cancel <job-id>", Short: "Cancel a non-terminal job (errors if it already finished)", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
			return callTool(cmd, cfg, "get_status", map[string]any{"action": "cancel", "job_id": args[0]})
		}},
	)
	return cmd
}

// --- billing (extended: transactions, change, preview, cancel, extra-usage) ---

// Extra-usage amount bounds, mirrored from the worker
// (worker/src/services/billing.ts: EXTRA_USAGE_MIN_CENTS / EXTRA_USAGE_STEP_CENTS).
// The per-top-up euro amount must be at least €5 and a whole multiple of €5.
// Validated locally in euros so a bad amount fails with a clear CLI error before
// any network call, instead of surfacing the server's "amount_invalid".
const (
	extraUsageMinEUR  = 5 // €5 minimum per top-up
	extraUsageStepEUR = 5 // €5 increment
)

// validExtraUsageEUR checks that amountEur is a €5-multiple of at least €5. It
// mirrors the worker's isValidExtraUsageAmount (in cents) so the CLI rejects a
// bad amount locally with a clear message. A non-multiple or below-minimum value
// returns an error naming the rule.
func validExtraUsageEUR(amountEur float64) error {
	// Work in cents to avoid float-modulo surprises, the same boundary the worker
	// converts at. Reject sub-cent precision too (e.g. €5.001).
	cents := amountEur * 100
	rounded := math.Round(cents)
	if math.Abs(cents-rounded) > 1e-6 {
		return fmt.Errorf("--amount-eur must be a whole-euro amount (got €%g)", amountEur)
	}
	c := int(rounded)
	if c < extraUsageMinEUR*100 || c%(extraUsageStepEUR*100) != 0 {
		return fmt.Errorf("--amount-eur must be at least €%d and a multiple of €%d (got €%g)", extraUsageMinEUR, extraUsageStepEUR, amountEur)
	}
	return nil
}

// newBillingCmd groups the billing tool actions (MCP `billing`). It keeps the
// read views (balance/plan/plans/transactions) alongside the owner-only
// subscription changes (change/preview/cancel) and the Extra-usage config
// (extra-usage). The standalone `framehood balance` command stays for
// back-compat.
func newBillingCmd(cfg config.Config) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "billing",
		Short: "Credits, plan and subscription for your organization",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return callTool(cmd, cfg, "billing", map[string]any{"action": "plan"})
		},
	}
	var reactivate bool
	cancel := &cobra.Command{
		Use:   "cancel",
		Short: "Cancel the subscription at period end (owner only); --reactivate resumes it",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			a := map[string]any{"action": "cancel"}
			if reactivate {
				a["reactivate"] = true
			}
			return callTool(cmd, cfg, "billing", a)
		},
	}
	cancel.Flags().BoolVar(&reactivate, "reactivate", false, "Resume a subscription set to cancel at period end")

	var limit int
	transactions := &cobra.Command{
		Use:   "transactions",
		Short: "Recent credit ledger (newest first)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			a := map[string]any{"action": "transactions"}
			if cmd.Flags().Changed("limit") {
				if err := checkLimit(limit, 1, 50); err != nil {
					return err
				}
				a["limit"] = limit
			}
			return callTool(cmd, cfg, "billing", a)
		},
	}
	transactions.Flags().IntVarP(&limit, "limit", "n", 0, "Max ledger rows (1–50)")

	extraUsage := newExtraUsageCmd(cfg)

	cmd.AddCommand(
		&cobra.Command{Use: "balance", Short: "Show your credit balance", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
			return callTool(cmd, cfg, "billing", map[string]any{"action": "balance"})
		}},
		&cobra.Command{Use: "plan", Short: "Show your current plan", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
			return callTool(cmd, cfg, "billing", map[string]any{"action": "plan"})
		}},
		&cobra.Command{Use: "plans", Short: "List the available packages", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
			return callTool(cmd, cfg, "billing", map[string]any{"action": "plans"})
		}},
		transactions,
		&cobra.Command{Use: "preview <package>", Short: "Prorated cost of switching to another package (owner only)", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
			return callTool(cmd, cfg, "billing", map[string]any{"action": "preview", "step": args[0]})
		}},
		&cobra.Command{Use: "change <package>", Short: "Switch the subscription to another package, prorated (owner only)", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
			return callTool(cmd, cfg, "billing", map[string]any{"action": "change", "step": args[0]})
		}},
		cancel,
		extraUsage,
	)
	return cmd
}

// newExtraUsageCmd builds `framehood billing extra-usage`. With no flags it reads
// the current Extra-usage config (MCP billing action=extra_usage, owner-only).
// With one or more flags it updates the config (action=set_extra_usage,
// owner-only): --enable/--disable, --amount-eur, --trigger, --cap-eur. Only the
// flags the user actually set are forwarded, so unchanged fields keep their
// server-side values. Extra usage is premium overflow billing (the per-euro
// credit rate is lower than a package); the read view surfaces that rate note
// prominently.
func newExtraUsageCmd(cfg config.Config) *cobra.Command {
	var enable, disable bool
	var amountEUR, capEUR float64
	var trigger int
	cmd := &cobra.Command{
		Use:   "extra-usage",
		Short: "View or configure Extra usage — premium overflow top-ups (owner only)",
		Long: "View the org's Extra-usage config, or change it with flags.\n\n" +
			"Extra usage automatically tops up credits at a premium overflow rate when\n" +
			"the balance runs low, charging the card on your subscription. With no flags\n" +
			"this reads the current config. With any flag it updates the config (owner\n" +
			"only): enable/disable, the euro amount charged per top-up, the balance that\n" +
			"triggers a top-up, and the per-cycle euro cap.",
		Example: "  framehood billing extra-usage\n" +
			"  framehood billing extra-usage --enable --amount-eur 5 --trigger 200\n" +
			"  framehood billing extra-usage --cap-eur 50\n" +
			"  framehood billing extra-usage --disable",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if enable && disable {
				return fmt.Errorf("set either --enable or --disable, not both")
			}
			changedAny := enable || disable ||
				cmd.Flags().Changed("amount-eur") ||
				cmd.Flags().Changed("trigger") ||
				cmd.Flags().Changed("cap-eur")
			// No config flag set → read the current config.
			if !changedAny {
				return runBillingExtraUsageRead(cmd, cfg)
			}

			a := map[string]any{"action": "set_extra_usage"}
			if enable {
				a["enabled"] = true
			} else if disable {
				a["enabled"] = false
			}
			if cmd.Flags().Changed("amount-eur") {
				// Validate the €5-multiple rule locally before the call.
				if err := validExtraUsageEUR(amountEUR); err != nil {
					return err
				}
				a["amount_eur"] = amountEUR
			}
			if cmd.Flags().Changed("trigger") {
				if trigger < 0 {
					return fmt.Errorf("--trigger must be 0 or more (got %d)", trigger)
				}
				a["trigger_below"] = trigger
			}
			if cmd.Flags().Changed("cap-eur") {
				if capEUR < 0 {
					return fmt.Errorf("--cap-eur must be €0 or more (got €%g)", capEUR)
				}
				// The MCP tool takes the cap in euros (extra_usage_cap_eur) and converts
				// to extra_usage_cap_cents server-side.
				a["extra_usage_cap_eur"] = capEUR
			}
			return runBillingSetExtraUsage(cmd, cfg, a)
		},
	}
	cmd.Flags().BoolVar(&enable, "enable", false, "Turn Extra usage on")
	cmd.Flags().BoolVar(&disable, "disable", false, "Turn Extra usage off")
	cmd.Flags().Float64Var(&amountEUR, "amount-eur", 0, "Euros charged per top-up (≥ €5, in €5 steps)")
	cmd.Flags().IntVar(&trigger, "trigger", 0, "Top up when the balance drops below this many credits")
	cmd.Flags().Float64Var(&capEUR, "cap-eur", 0, "Max euros of Extra usage allowed per billing cycle")
	return cmd
}

// billingError extracts a structured {error, message} from a billing tool
// payload and returns it as a clean Go error (preferring the human message),
// or nil when the payload carries no error. The billing tool returns forbidden
// / card_required / amount_invalid / cap_invalid / cap_reached as a NON-isError
// JSON body {error, message} (not an MCP error), so CallTool hands them back as
// a successful payload that we must inspect here to surface them clearly.
func billingError(raw json.RawMessage) error {
	var v struct {
		Error   string `json:"error"`
		Message string `json:"message"`
	}
	if jsonUnmarshal(raw, &v) != nil || v.Error == "" {
		return nil
	}
	if v.Message != "" {
		return fmt.Errorf("%s", v.Message)
	}
	return fmt.Errorf("%s", strings.ReplaceAll(v.Error, "_", " "))
}

// runBillingExtraUsageRead reads the org's Extra-usage config via the MCP billing
// tool (action=extra_usage, owner-only) and prints it as a labeled block. A
// forbidden (non-owner) response surfaces as a clear error.
func runBillingExtraUsageRead(cmd *cobra.Command, cfg config.Config) error {
	sess, err := NewSession(cfg)
	if err != nil {
		return err
	}
	raw, err := sess.Client().CallTool(cmd.Context(), "billing", map[string]any{"action": "extra_usage"})
	if err != nil {
		return err
	}
	if err := billingError(raw); err != nil {
		return err
	}
	if out, ok := renderExtraUsage(raw); ok {
		fmt.Println(out)
		return nil
	}
	fmt.Println(render.PrettyJSON(raw))
	return nil
}

// runBillingSetExtraUsage applies an Extra-usage config change via the MCP
// billing tool (action=set_extra_usage, owner-only) and prints the resulting
// config. forbidden / card_required / amount_invalid / cap_invalid surface as
// clear errors.
func runBillingSetExtraUsage(cmd *cobra.Command, cfg config.Config, args map[string]any) error {
	sess, err := NewSession(cfg)
	if err != nil {
		return err
	}
	raw, err := sess.Client().CallTool(cmd.Context(), "billing", args)
	if err != nil {
		return err
	}
	if err := billingError(raw); err != nil {
		return err
	}
	fmt.Println("✓ Extra usage updated")
	if out, ok := renderExtraUsage(raw); ok {
		fmt.Println(out)
		return nil
	}
	fmt.Println(render.PrettyJSON(raw))
	return nil
}

// renderExtraUsage formats an Extra-usage config payload as a labeled block. It
// accepts both the read shape ({extra_usage:{…}}) and the write shape ({ok,
// extra_usage:{…}, rate_note}), so the same renderer serves extra_usage and
// set_extra_usage. Euro amounts are shown alongside their ≈credit value at the
// payload's credits_per_eur; the per-cycle cap shows spent-of-cap; and the
// premium-rate note is surfaced prominently.
func renderExtraUsage(raw json.RawMessage) (string, bool) {
	type cfg struct {
		Enabled         *bool    `json:"enabled"`
		TriggerBelow    *int     `json:"trigger_below"`
		RefillAmtCents  *int     `json:"refill_amount_cents"`
		CapCents        *int     `json:"extra_usage_cap_cents"`
		CreditsPerEUR   *float64 `json:"credits_per_eur"`
		SpentCycleCents *int     `json:"spent_this_cycle_cents"`
		HasCard         *bool    `json:"has_card"`
		RateNote        string   `json:"rate_note"`
	}
	// The payload wraps the config under "extra_usage". rate_note may sit on the
	// wrapper (write shape) or inside the config (read shape) — capture both.
	var v struct {
		ExtraUsage *cfg   `json:"extra_usage"`
		RateNote   string `json:"rate_note"`
	}
	if jsonUnmarshal(raw, &v) != nil || v.ExtraUsage == nil {
		return "", false
	}
	c := *v.ExtraUsage
	// A config payload always carries at least the enabled flag.
	if c.Enabled == nil {
		return "", false
	}
	var b strings.Builder
	state := "off"
	if *c.Enabled {
		state = "on"
	}
	fmt.Fprintf(&b, "Extra usage: %s\n", state)
	if c.RefillAmtCents != nil && *c.RefillAmtCents > 0 {
		line := fmt.Sprintf("Amount per top-up: €%s", trimEUR(float64(*c.RefillAmtCents)/100))
		if cr := extraUsageCredits(c.RefillAmtCents, c.CreditsPerEUR); cr != "" {
			line += " (≈" + cr + ")"
		}
		fmt.Fprintln(&b, line)
	}
	if c.TriggerBelow != nil {
		fmt.Fprintf(&b, "Trigger below: %d credits\n", *c.TriggerBelow)
	}
	if c.CapCents != nil {
		cap := fmt.Sprintf("Per-cycle cap: €%s", trimEUR(float64(*c.CapCents)/100))
		if c.SpentCycleCents != nil {
			cap += fmt.Sprintf(" (€%s used this cycle)", trimEUR(float64(*c.SpentCycleCents)/100))
		}
		fmt.Fprintln(&b, cap)
	} else if c.SpentCycleCents != nil {
		fmt.Fprintf(&b, "Spent this cycle: €%s\n", trimEUR(float64(*c.SpentCycleCents)/100))
	}
	if c.HasCard != nil {
		card := "no card on file — add one with `framehood billing manage`"
		if *c.HasCard {
			card = "card on file"
		}
		fmt.Fprintf(&b, "Payment method: %s\n", card)
	}
	// Surface the premium-rate note prominently (last, on its own line). Prefer
	// the wrapper's rate_note, then the config's.
	note := firstNonEmpty(v.RateNote, c.RateNote)
	if note != "" {
		fmt.Fprintf(&b, "\nNote: %s", note)
	}
	return strings.TrimRight(b.String(), "\n"), true
}

// extraUsageCredits renders the ≈credit value of an amount-in-cents at a given
// credits-per-euro rate, e.g. (500c, 80/€) → "400 credits". Returns "" when
// either input is missing so the caller can omit the parenthetical.
func extraUsageCredits(amountCents *int, creditsPerEUR *float64) string {
	if amountCents == nil || creditsPerEUR == nil || *creditsPerEUR <= 0 {
		return ""
	}
	credits := float64(*amountCents) / 100 * *creditsPerEUR
	return fmt.Sprintf("%s credits", trimEUR(credits))
}

// trimEUR renders a euro (or credit) amount without a trailing ".00" for whole
// values (e.g. 20.00 → "20", 4.50 → "4.50").
func trimEUR(v float64) string {
	if v == math.Trunc(v) {
		return fmt.Sprintf("%.0f", v)
	}
	return fmt.Sprintf("%.2f", v)
}

// --- files (extended: list/upload/delete/publish/unpublish/download) ---

// newFilesCmd — manage R2 storage (MCP `files`). The positional <key> is the
// file name the tool expects as `filename`.
func newFilesCmd(cfg config.Config) *cobra.Command {
	var prefix string
	cmd := &cobra.Command{
		Use:   "files",
		Short: "Manage your storage: list, upload, delete, publish, download",
		Example: "  framehood files\n" +
			"  framehood files upload https://example.com/clip.mp4 clip.mp4\n" +
			"  framehood files publish clip.mp4\n" +
			"  framehood files download clip.mp4 -o ./clip.mp4",
		RunE: func(cmd *cobra.Command, _ []string) error {
			a := map[string]any{"action": "list"}
			if prefix != "" {
				a["prefix"] = prefix
			}
			return callTool(cmd, cfg, "files", a)
		},
	}
	cmd.Flags().StringVar(&prefix, "prefix", "", "Filter the listing by key prefix")

	listSub := &cobra.Command{Use: "list", Short: "List your files", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		a := map[string]any{"action": "list"}
		if prefix != "" {
			a["prefix"] = prefix
		}
		return callTool(cmd, cfg, "files", a)
	}}
	listSub.Flags().StringVar(&prefix, "prefix", "", "Filter the listing by key prefix")

	var out string
	download := &cobra.Command{
		Use:   "download <key>",
		Short: "Get a usable URL for a file, or write it to disk with -o",
		Long: "Get a usable URL for a file (write it to disk with -o).\n\n" +
			"A private file is fetched by transiently publishing it, downloading the\n" +
			"public URL, then unpublishing it again — so the file ends up exactly as\n" +
			"it started. Public files are fetched directly.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runFileDownload(cmd, cfg, args[0], out)
		},
	}
	download.Flags().StringVarP(&out, "out", "o", "", "Write the file to this path instead of just printing the URL")

	cmd.AddCommand(
		listSub,
		&cobra.Command{Use: "upload <url> <key>", Short: "Upload a file from a URL", Args: cobra.ExactArgs(2), RunE: func(cmd *cobra.Command, args []string) error {
			return callTool(cmd, cfg, "files", map[string]any{"action": "upload", "url": args[0], "filename": args[1]})
		}},
		&cobra.Command{Use: "delete <key>", Short: "Delete a file", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
			return callTool(cmd, cfg, "files", map[string]any{"action": "delete", "filename": args[0]})
		}},
		&cobra.Command{Use: "publish <key>", Short: "Make a file public", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
			return callTool(cmd, cfg, "files", map[string]any{"action": "publish", "filename": args[0]})
		}},
		&cobra.Command{Use: "unpublish <key>", Short: "Make a published file private again", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
			return callTool(cmd, cfg, "files", map[string]any{"action": "unpublish", "filename": args[0]})
		}},
		download,
	)
	return cmd
}

// runFileDownload resolves a download URL via files(download) and either prints
// it or, when -o is given, fetches the bytes to disk.
//
// Auth note: the CLI's stored credential is the worker OAuthProvider's opaque
// access token, valid only on the /mcp endpoint. The download action returns a
// /files/public/... URL for a published file (no auth — fetchable directly) but
// an auth-gated /files/private/... URL for a private one. That private route is
// authenticated by the worker's authenticate() (Supabase JWT or API key) and
// rejects the OAuth token with a 401 — so the CLI cannot fetch a private file's
// bytes with the token it holds. To make any listed file downloadable in the
// same session as `files list`, a private file is transiently published (which
// yields a no-auth public URL), fetched, then unpublished to restore its prior
// state. Both publish and unpublish run through the same /mcp `files` tool the
// listing uses, so they carry the valid token.
func runFileDownload(cmd *cobra.Command, cfg config.Config, key, out string) error {
	sess, err := NewSession(cfg)
	if err != nil {
		return err
	}
	client := sess.Client()
	raw, err := client.CallTool(cmd.Context(), "files", map[string]any{"action": "download", "filename": key})
	if err != nil {
		return err
	}
	url := downloadURLFrom(raw)
	if url == "" {
		// No usable URL. With -o the user expects a file written, so failing to
		// extract one is a hard error (non-zero exit) — never report success
		// without writing. Without -o, surface what the tool returned.
		if out != "" {
			return fmt.Errorf("no download URL in the response for %q:\n%s", key, render.PrettyJSON(raw))
		}
		fmt.Println(render.PrettyJSON(raw))
		return nil
	}

	// A private file's download_url points at the auth-gated /files/{key} route,
	// which the OAuth token can't authenticate. Resolving a public URL needs a
	// transient publish, which mutates state — so only do it when actually
	// writing bytes (-o). Without -o, print the URL plus the tool's note so the
	// user still gets the (authenticated) path to fetch themselves.
	if !isPublicDownload(raw) {
		if out == "" {
			fmt.Println(render.PrettyJSON(raw))
			return nil
		}
		return downloadPrivateViaPublish(cmd, client, sess.Access(), key, out)
	}

	if out == "" {
		fmt.Println(url)
		return nil
	}
	if err := saveURLToFile(cmd.Context(), url, out, sess.Access()); err != nil {
		return err
	}
	fmt.Printf("✓ saved → %s\n", out)
	return nil
}

// downloadPrivateViaPublish makes a private file fetchable by the CLI's OAuth
// token: it publishes the file (moving it to the no-auth public tier and
// returning a public_url), fetches that URL, then unpublishes to move it back to
// private — leaving the storage in its original state. The unpublish runs even
// when the fetch fails so a failed download never leaves the file publicly
// exposed.
func downloadPrivateViaPublish(cmd *cobra.Command, client *mcp.Client, token, key, out string) error {
	pubRaw, err := client.CallTool(cmd.Context(), "files", map[string]any{"action": "publish", "filename": key})
	if err != nil {
		return fmt.Errorf("could not publish %q to download it: %w", key, err)
	}
	publicURL := downloadURLFrom(pubRaw)
	// Once the file is published, it MUST be restored to private no matter how the
	// rest of the function exits — so register the restore in a defer before doing
	// anything that can fail or block.
	defer restorePrivate(cmd.Context(), client, key)
	if publicURL == "" {
		return fmt.Errorf("no public URL returned when publishing %q:\n%s", key, render.PrettyJSON(pubRaw))
	}
	if err := saveURLToFile(cmd.Context(), publicURL, out, token); err != nil {
		return err
	}
	fmt.Printf("✓ saved → %s\n", out)
	return nil
}

// restorePrivate unpublishes key, moving a transiently-published file back to
// private. It deliberately runs on a context DETACHED from parent (via
// WithoutCancel): the restore must complete even if the parent context was
// cancelled (ctrl-c / timeout mid-download) — otherwise a cancelled download
// would leave the file PUBLIC. A short timeout still bounds the call so it can't
// hang forever. A failure is reported (never silently swallowed) but not
// returned, since this runs in a defer after the primary result is decided.
func restorePrivate(parent context.Context, client *mcp.Client, key string) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), 30*time.Second)
	defer cancel()
	if _, err := client.CallTool(ctx, "files", map[string]any{"action": "unpublish", "filename": key}); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not unpublish %q after download — it may still be public: %v\n", key, err)
	}
}

// --- keys (api_keys: list | create | delete) ---

// newKeysCmd — programmatic REST/CLI API keys (MCP `api_keys`). On create the
// one-time secret is printed with a "shown once" note.
func newKeysCmd(cfg config.Config) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "keys",
		Short: "Programmatic API keys for the REST API and CLI",
		Example: "  framehood keys\n" +
			"  framehood keys create --name ci\n" +
			"  framehood keys delete <prefix-or-key>",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return callTool(cmd, cfg, "api_keys", map[string]any{"action": "list"})
		},
	}
	var name string
	create := &cobra.Command{
		Use:   "create",
		Short: "Mint a new API key (the secret is shown once)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			a := map[string]any{"action": "create"}
			if name != "" {
				a["name"] = name
			}
			return runKeyCreate(cmd, cfg, a)
		},
	}
	create.Flags().StringVar(&name, "name", "", "Optional label for the key")
	cmd.AddCommand(
		&cobra.Command{Use: "list", Short: "List your keys (prefix + metadata)", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
			return callTool(cmd, cfg, "api_keys", map[string]any{"action": "list"})
		}},
		create,
		&cobra.Command{Use: "delete <prefix-or-key>", Short: "Revoke a key by its prefix or full value", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
			return callTool(cmd, cfg, "api_keys", map[string]any{"action": "delete", "key": args[0]})
		}},
	)
	return cmd
}

// runKeyCreate mints a key and prints the one-time secret prominently. The raw
// secret is never recoverable later, so this is the only chance to copy it.
func runKeyCreate(cmd *cobra.Command, cfg config.Config, args map[string]any) error {
	sess, err := NewSession(cfg)
	if err != nil {
		return err
	}
	raw, err := sess.Client().CallTool(cmd.Context(), "api_keys", args)
	if err != nil {
		return err
	}
	var v struct {
		APIKey  string `json:"api_key"`
		Name    string `json:"name"`
		Error   string `json:"error"`
		Message string `json:"message"`
	}
	if jsonUnmarshal(raw, &v) == nil && v.APIKey != "" {
		fmt.Printf("API key created%s\n", labelSuffix(v.Name))
		fmt.Printf("  %s\n", v.APIKey)
		fmt.Println("⚠ Shown once — store it now; it can't be retrieved later.")
		return nil
	}
	if v.Error != "" {
		msg := v.Message
		if msg == "" {
			msg = v.Error
		}
		return fmt.Errorf("%s", msg)
	}
	fmt.Println(render.PrettyJSON(raw))
	return nil
}

func labelSuffix(name string) string {
	if name == "" {
		return ""
	}
	return " (" + name + ")"
}

// --- catalog reads: models | skill | workflows (MCP zvs:// resources) ---
//
// These read the worker's zvs:// MCP resources over /mcp rather than issuing a
// raw GET against /v1/...: the CLI's stored credential is an OAuth-provider
// access token that only the OAuthProvider-wrapped /mcp endpoint accepts, so a
// direct /v1/... GET returns 401 even in a fully logged-in session. Reading the
// equivalent resource uses the same token + refresh-on-401 as every working
// command.

// newModelsCmd — the model catalog (zvs://models), or one model's skill.
func newModelsCmd(cfg config.Config) *cobra.Command {
	return &cobra.Command{
		Use:   "models [kind]",
		Short: "List available models, or show one model's schema",
		Args:  cobra.MaximumNArgs(1),
		Example: "  framehood models\n" +
			"  framehood models flux_schnell",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 {
				return readReadable(cmd, cfg, "zvs://model/"+pathSeg(args[0]), "skill", "")
			}
			return readReadable(cmd, cfg, "zvs://models", "models", "list")
		},
	}
}

// newSkillCmd — a model's skill / prompt guide (zvs://model/{kind}).
func newSkillCmd(cfg config.Config) *cobra.Command {
	return &cobra.Command{
		Use:   "skill <kind>",
		Short: "Show a model's skill (parameters, tips, prompt guide)",
		Args:  cobra.ExactArgs(1),
		Example: "  framehood skill flux_schnell\n" +
			"  framehood skill elevenlabs_tts_v3",
		RunE: func(cmd *cobra.Command, args []string) error {
			return readReadable(cmd, cfg, "zvs://model/"+pathSeg(args[0]), "skill", "")
		},
	}
}

// knownWorkflows is the set of multi-step pipelines the worker exposes as
// zvs://workflow/{name} resources. The MCP surface has no list resource for
// them (only per-name skills), so the bare `workflows` command reads each one.
// The server owns the canonical set; this list mirrors the worker's
// zvs://workflow resource description.
var knownWorkflows = []string{"video_production", "character_creation", "qa_pipeline"}

// newWorkflowsCmd — the multi-step pipeline catalog, or one workflow's skill
// (zvs://workflow/{name}).
func newWorkflowsCmd(cfg config.Config) *cobra.Command {
	return &cobra.Command{
		Use:   "workflows [name]",
		Short: "List multi-step workflows, or show one workflow's skill",
		Args:  cobra.MaximumNArgs(1),
		Example: "  framehood workflows\n" +
			"  framehood workflows video_production",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 {
				return readReadable(cmd, cfg, "zvs://workflow/"+pathSeg(args[0]), "skill", "")
			}
			return runWorkflowsList(cmd, cfg)
		},
	}
}

// runWorkflowsList renders the workflow catalog by reading each known
// workflow's skill resource and pulling its name + summary line. It builds the
// {name, description} array shape that render's workflows formatter expects, so
// the bare `workflows` list looks identical to the previous REST-backed output.
func runWorkflowsList(cmd *cobra.Command, cfg config.Config) error {
	sess, err := NewSession(cfg)
	if err != nil {
		return err
	}
	client := sess.Client()
	type wf struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	out := make([]wf, 0, len(knownWorkflows))
	var lastErr error
	for _, name := range knownWorkflows {
		raw, err := client.ReadResource(cmd.Context(), "zvs://workflow/"+pathSeg(name))
		if err != nil {
			// A single missing/renamed workflow must not abort the whole listing —
			// remember the error and keep going so partial results still render.
			lastErr = err
			continue
		}
		out = append(out, wf{Name: name, Description: workflowSummary(raw)})
	}
	// If every read failed (expired auth, server down, network), surface the
	// error instead of printing an empty catalog that masquerades as success.
	if len(out) == 0 && lastErr != nil {
		return lastErr
	}
	encoded, err := json.Marshal(out)
	if err != nil {
		return err
	}
	if rendered, ok := render.Readable("workflows", "", encoded); ok {
		fmt.Println(rendered)
	} else {
		fmt.Println(render.PrettyJSON(encoded))
	}
	return nil
}

// workflowSummary extracts a one-line description from a workflow skill payload
// ({name, type, content}): the first non-heading, non-empty markdown line.
func workflowSummary(raw json.RawMessage) string {
	var v struct {
		Content string `json:"content"`
	}
	if jsonUnmarshal(raw, &v) != nil {
		return ""
	}
	for _, line := range strings.Split(v.Content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		return line
	}
	return ""
}
