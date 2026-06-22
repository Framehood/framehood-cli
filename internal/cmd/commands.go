package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Framehood/framehood-cli/internal/auth"
	"github.com/Framehood/framehood-cli/internal/config"
	"github.com/Framehood/framehood-cli/internal/mcp"
	"github.com/Framehood/framehood-cli/internal/render"
	"github.com/spf13/cobra"
)

// --- login ---

func newLoginCmd(cfg config.Config) *cobra.Command {
	return &cobra.Command{
		Use:   "login",
		Short: "Sign in via your browser",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), 5*time.Minute)
			defer cancel()
			fmt.Println("Opening your browser to sign in…")
			creds, err := auth.Login(ctx, cfg, nil)
			if err != nil {
				return err
			}
			if err := auth.Save(cfg.CredentialsPath(), creds); err != nil {
				return err
			}
			who := creds.Email
			if who == "" {
				who = "your account"
			}
			fmt.Printf("✓ Signed in as %s\n", who)
			return nil
		},
	}
}

// --- logout ---

func newLogoutCmd(cfg config.Config) *cobra.Command {
	return &cobra.Command{
		Use:   "logout",
		Short: "Remove stored credentials",
		RunE: func(_ *cobra.Command, _ []string) error {
			if err := auth.Clear(cfg.CredentialsPath()); err != nil {
				return err
			}
			fmt.Println("✓ Signed out")
			return nil
		},
	}
}

// --- whoami ---

func newWhoamiCmd(cfg config.Config) *cobra.Command {
	return &cobra.Command{
		Use:   "whoami",
		Short: "Show your account: email, org role, balance and plan",
		RunE: func(cmd *cobra.Command, _ []string) error {
			sess, err := NewSession(cfg)
			if err != nil {
				return err
			}
			id, ok := aggregateIdentity(cmd, sess)
			id.email = firstNonEmpty(sess.Email(), id.email)
			if !ok {
				return fmt.Errorf("couldn't reach your account — check your connection or run `framehood login`")
			}
			fmt.Print(id.render())
			return nil
		},
	}
}

// identity is the aggregated whoami view, drawn from billing(balance) +
// billing(plan).
type identity struct {
	email   string
	role    string
	balance string
	plan    string
}

// aggregateIdentity gathers the caller's email/role/balance from billing
// (balance) and the plan name from billing(plan). Each source is best-effort:
// a failing tool just leaves its fields blank rather than erroring out.
func aggregateIdentity(cmd *cobra.Command, sess *Session) (identity, bool) {
	var id identity
	var any bool
	if raw, err := sess.Client().Balance(cmd.Context()); err == nil {
		var b struct {
			Balance any    `json:"balance"`
			Role    string `json:"role"`
			Email   string `json:"email"`
		}
		if json.Unmarshal(raw, &b) == nil {
			any = true
			id.email, id.role = b.Email, b.Role
			if b.Balance != nil {
				id.balance = fmt.Sprintf("%v credits", b.Balance)
			}
		}
	}
	if raw, err := sess.Client().Plan(cmd.Context()); err == nil {
		var p struct {
			Plan string `json:"plan"`
		}
		if json.Unmarshal(raw, &p) == nil {
			any = true
			id.plan = p.Plan
		}
	}
	return id, any
}

// render produces the labeled whoami block, omitting fields we couldn't fetch.
func (id identity) render() string {
	email := id.email
	if email == "" {
		email = "(unknown email)"
	}
	out := "Email:   " + email + "\n"
	if id.role != "" {
		out += "Role:    " + id.role + "\n"
	}
	if id.balance != "" {
		out += "Balance: " + id.balance + "\n"
	}
	if id.plan != "" {
		out += "Plan:    " + id.plan + "\n"
	}
	return out
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// --- balance ---

func newBalanceCmd(cfg config.Config) *cobra.Command {
	return &cobra.Command{
		Use:   "balance",
		Short: "Show your credit balance",
		RunE: func(cmd *cobra.Command, _ []string) error {
			sess, err := NewSession(cfg)
			if err != nil {
				return err
			}
			bal, err := sess.Client().Balance(cmd.Context())
			if err != nil {
				return err
			}
			if out, ok := render.Readable("billing", "balance", bal); ok {
				fmt.Println(out)
			} else {
				fmt.Println(render.PrettyJSON(bal))
			}
			return nil
		},
	}
}

// --- generate ---

func newGenerateCmd(cfg config.Config) *cobra.Command {
	var kind, action, out, tier, format, actorID, voice string
	cmd := &cobra.Command{
		Use:   "generate [prompt]",
		Short: "Generate an image, video or audio from a prompt (one-shot)",
		Args:  cobra.MinimumNArgs(1),
		Example: "  framehood generate \"a red fox in the snow\"\n" +
			"  framehood generate --type audio --voice Rachel \"welcome to Framehood\"\n" +
			"  framehood generate --type video \"a drone shot over a coastline\"",
		RunE: func(cmd *cobra.Command, args []string) error {
			sess, err := NewSession(cfg)
			if err != nil {
				return err
			}
			prompt := strings.Join(args, " ")

			tool, toolArgs, err := buildGenerateArgs(kind, action, prompt, out, tier, format, actorID, voice)
			if err != nil {
				return err
			}

			client := sess.Client()
			start := time.Now()
			fmt.Fprintf(os.Stderr, "Generating %s…\n", kind)
			job, err := client.Generate(cmd.Context(), tool, toolArgs, func(j mcp.Job) {
				if j.ID != "" && !j.Terminal() {
					fmt.Fprintf(os.Stderr, "  job %s: %s\n", j.ID, j.Status)
				}
			})
			if err != nil {
				return err
			}
			if job.Status == "failed" {
				return fmt.Errorf("job failed: %s", strings.TrimSpace(string(job.Error)))
			}
			url := job.ResultURL()
			if url == "" {
				fmt.Println(prettyInline(mustJSON(job)))
				return nil
			}
			fmt.Printf("✓ Done in %s\n%s\n", time.Since(start).Round(time.Second), url)
			return nil
		},
	}
	cmd.Flags().StringVarP(&kind, "type", "t", "image", "What to generate: image | video | audio")
	cmd.Flags().StringVarP(&out, "out", "o", "", "Output filename (defaults by type)")
	cmd.Flags().StringVar(&action, "action", "", "Override the tool action (e.g. create, speak, scene)")
	cmd.Flags().StringVar(&tier, "tier", "", "Quality tier (image: draft|fine|photo)")
	cmd.Flags().StringVar(&format, "format", "", "Size preset (e.g. landscape_16_9, square)")
	cmd.Flags().StringVar(&actorID, "actor", "", "Actor id (act_…) to route through")
	cmd.Flags().StringVar(&voice, "voice", "", "Voice name for audio speak (e.g. Rachel)")
	return cmd
}

// buildGenerateArgs maps a generation type to an MCP tool name and argument map.
func buildGenerateArgs(kind, action, prompt, out, tier, format, actorID, voice string) (string, map[string]any, error) {
	args := map[string]any{}
	var tool string
	switch strings.ToLower(kind) {
	case "image":
		tool = "image"
		if action == "" {
			action = "create"
		}
		args["action"] = action
		args["prompt"] = prompt
		args["out"] = defaultOut(out, "image.jpg")
		if tier != "" {
			args["tier"] = tier
		}
		if format != "" {
			args["format"] = format
		}
	case "audio":
		tool = "audio"
		if action == "" {
			action = "speak"
		}
		args["action"] = action
		if action == "speak" {
			args["text"] = prompt
		} else {
			args["prompt"] = prompt
		}
		args["out"] = defaultOut(out, "audio.mp3")
		if voice != "" {
			args["voice"] = voice
		}
	case "video":
		tool = "video"
		if action == "" {
			// Plain text->video needs no actor (create); only route to the
			// actor scene composite when an actor is given.
			if actorID != "" {
				action = "scene"
			} else {
				action = "create"
			}
		}
		if action == "scene" && actorID == "" {
			return "", nil, fmt.Errorf("--actor is required for --type video --action scene")
		}
		args["action"] = action
		args["prompt"] = prompt
		if action == "scene" {
			args["scene_prompt"] = prompt
		}
		args["out"] = defaultOut(out, "video.mp4")
		if format != "" {
			args["format"] = format
		}
	default:
		return "", nil, fmt.Errorf("unknown --type %q (want image, video or audio)", kind)
	}
	if actorID != "" {
		args["actor_id"] = actorID
	}
	return tool, args, nil
}

func defaultOut(out, def string) string {
	if out != "" {
		return out
	}
	return def
}

// --- helpers ---

func prettyInline(raw json.RawMessage) string {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return string(raw)
	}
	b, _ := json.Marshal(v)
	return string(b)
}

func mustJSON(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}
