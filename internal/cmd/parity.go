package cmd

import (
	"fmt"

	"github.com/Framehood/framehood-cli/internal/config"
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
		if limit > 0 {
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
			if limit > 0 {
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
		Short: "Get a usable URL for a file (write it to disk with -o)",
		Args:  cobra.ExactArgs(1),
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
// it or, when -o is given, fetches the bytes to disk (reusing the existing
// safe-download helper that validates the path and streams the body).
func runFileDownload(cmd *cobra.Command, cfg config.Config, key, out string) error {
	sess, err := NewSession(cfg)
	if err != nil {
		return err
	}
	raw, err := sess.Client().CallTool(cmd.Context(), "files", map[string]any{"action": "download", "filename": key})
	if err != nil {
		return err
	}
	url := downloadURLFrom(raw)
	if url == "" {
		// Unknown shape — surface what the tool returned rather than guessing.
		fmt.Println(render.PrettyJSON(raw))
		return nil
	}
	if out == "" {
		fmt.Println(url)
		return nil
	}
	// A private file's download_url needs the caller's bearer token; pass it so
	// the fetch is authenticated. Public URLs ignore the header harmlessly.
	if err := saveURLToFile(cmd.Context(), url, out, sess.Access()); err != nil {
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

// --- read endpoints: models | skill | workflows (REST GET /v1/...) ---

// newModelsCmd — the model catalog (GET /v1/models, or one model's full schema).
func newModelsCmd(cfg config.Config) *cobra.Command {
	return &cobra.Command{
		Use:   "models [kind]",
		Short: "List available models, or show one model's schema",
		Args:  cobra.MaximumNArgs(1),
		Example: "  framehood models\n" +
			"  framehood models flux_schnell",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 {
				return getReadable(cmd, cfg, "/v1/models/"+pathSeg(args[0]), "", "")
			}
			return getReadable(cmd, cfg, "/v1/models", "models", "list")
		},
	}
}

// newSkillCmd — a model's skill / prompt guide (GET /v1/models/{kind}/skill).
func newSkillCmd(cfg config.Config) *cobra.Command {
	return &cobra.Command{
		Use:   "skill <kind>",
		Short: "Show a model's skill (parameters, tips, prompt guide)",
		Args:  cobra.ExactArgs(1),
		Example: "  framehood skill flux_schnell\n" +
			"  framehood skill elevenlabs_tts_v3",
		RunE: func(cmd *cobra.Command, args []string) error {
			return getReadable(cmd, cfg, "/v1/models/"+pathSeg(args[0])+"/skill", "skill", "")
		},
	}
}

// newWorkflowsCmd — the multi-step pipeline catalog (GET /v1/workflows), or one
// workflow's skill.
func newWorkflowsCmd(cfg config.Config) *cobra.Command {
	return &cobra.Command{
		Use:   "workflows [name]",
		Short: "List multi-step workflows, or show one workflow's skill",
		Args:  cobra.MaximumNArgs(1),
		Example: "  framehood workflows\n" +
			"  framehood workflows video_production",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 {
				return getReadable(cmd, cfg, "/v1/workflows/"+pathSeg(args[0])+"/skill", "skill", "")
			}
			return getReadable(cmd, cfg, "/v1/workflows", "workflows", "")
		},
	}
}
