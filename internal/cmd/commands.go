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
		Short: "Show the signed-in account and credit balance",
		RunE: func(cmd *cobra.Command, _ []string) error {
			sess, err := NewSession(cfg)
			if err != nil {
				return err
			}
			bal, balErr := sess.Client().Balance(cmd.Context())
			who := sess.Email()
			if who == "" && balErr == nil {
				// The login flow doesn't always capture the email; billing(balance)
				// returns it, so fall back to that.
				var b struct {
					Email string `json:"email"`
				}
				if json.Unmarshal(bal, &b) == nil && b.Email != "" {
					who = b.Email
				}
			}
			if who == "" {
				who = "(unknown email)"
			}
			fmt.Printf("Signed in as %s\n", who)
			if balErr == nil {
				fmt.Printf("Balance: %s\n", prettyInline(bal))
			}
			return nil
		},
	}
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
			fmt.Println(prettyInline(bal))
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
			action = "scene"
		}
		args["action"] = action
		args["scene_prompt"] = prompt
		args["prompt"] = prompt
		args["out"] = defaultOut(out, "video.mp4")
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
