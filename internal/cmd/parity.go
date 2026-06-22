package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

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

// --- billing (extended: transactions, change, preview, cancel) ---

// newBillingCmd groups the billing tool actions (MCP `billing`). It keeps the
// read views (balance/plan/plans/transactions) alongside the owner-only
// subscription changes (change/preview/cancel). The standalone `framehood
// balance` command stays for back-compat.
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
	)
	return cmd
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
	// Always restore the file to private, regardless of how the fetch goes.
	defer func() {
		if _, uerr := client.CallTool(cmd.Context(), "files", map[string]any{"action": "unpublish", "filename": key}); uerr != nil {
			fmt.Fprintf(os.Stderr, "warning: could not unpublish %q after download — it may still be public: %v\n", key, uerr)
		}
	}()
	if publicURL == "" {
		return fmt.Errorf("no public URL returned when publishing %q:\n%s", key, render.PrettyJSON(pubRaw))
	}
	if err := saveURLToFile(cmd.Context(), publicURL, out, token); err != nil {
		return err
	}
	fmt.Printf("✓ saved → %s\n", out)
	return nil
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
