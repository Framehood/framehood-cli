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
	var gotData bool
	if raw, err := sess.Client().Balance(cmd.Context()); err == nil {
		var b struct {
			Balance any    `json:"balance"`
			Role    string `json:"role"`
			Email   string `json:"email"`
		}
		if json.Unmarshal(raw, &b) == nil {
			gotData = true
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
			gotData = true
			id.plan = p.Plan
		}
	}
	return id, gotData
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
	var media mediaInputs
	cmd := &cobra.Command{
		Use:   "generate [prompt]",
		Short: "Generate an image, video or audio from a prompt (one-shot)",
		// Pipeline actions (lipsync, assemble, …) drive on input media, not a
		// prompt, so a prompt is required only when no input media is supplied.
		Args: cobra.ArbitraryArgs,
		Example: "  framehood generate \"a red fox in the snow\"\n" +
			"  framehood generate --type audio --voice Rachel \"welcome to Framehood\"\n" +
			"  framehood generate --type video \"a drone shot over a coastline\"\n" +
			"  framehood generate --type video --action lipsync --video-url … --audio-url …\n" +
			"  framehood generate --type video --action assemble --clips a.mp4,b.mp4 --audio-url vo.mp3",
		RunE: func(cmd *cobra.Command, args []string) error {
			prompt := strings.Join(args, " ")

			// Validate and assemble the tool args FIRST — before opening a session
			// (which may hit the network to refresh the token) or making any tool
			// call. A bad invocation (e.g. lipsync without --audio-url) fails fast
			// with a clear CLI error and never touches the network.
			tool, toolArgs, err := buildGenerateArgs(kind, action, prompt, out, tier, format, actorID, voice, media)
			if err != nil {
				return err
			}

			sess, err := NewSession(cfg)
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
	// Input-media flags for the pipeline actions (lipsync, assemble, edit, swap,
	// captions, upscale, mix_audio, …). They carry the URLs of existing assets so
	// a multi-step pipeline can be built — see mediaInputs / buildGenerateArgs.
	cmd.Flags().StringVar(&media.videoURL, "video-url", "", "Source video URL (edit, swap, lipsync, captions, upscale, mix_audio)")
	cmd.Flags().StringVar(&media.audioURL, "audio-url", "", "Audio track URL (lipsync, assemble)")
	cmd.Flags().StringArrayVar(&media.imageURLs, "image-url", nil, "Reference/source image URL (repeatable; image edit/upscale/animate, video swap/edit_ref/create)")
	cmd.Flags().StringSliceVar(&media.clips, "clips", nil, "Clip URLs to combine, comma-separated or repeated (video assemble)")
	cmd.Flags().StringSliceVar(&media.tracks, "tracks", nil, "Audio track URLs to layer/blend, comma-separated or repeated (video mix_audio, audio mix/concat)")
	return cmd
}

// mediaInputs holds the input-media URLs the pipeline actions consume. Each
// field maps to the matching worker tool arg (see buildGenerateArgs); an unset
// field is simply not forwarded so the worker keeps its own defaults.
type mediaInputs struct {
	videoURL  string
	audioURL  string
	imageURLs []string
	clips     []string
	tracks    []string
}

// normalized trims surrounding whitespace from every URL and drops blank
// entries (e.g. from `--clips a.mp4,,` or a stray `--image-url ""`), so the
// required-input checks in the builders can't be fooled by a present-but-empty
// value. The scalar fields collapse a whitespace-only value to "".
func (m mediaInputs) normalized() mediaInputs {
	return mediaInputs{
		videoURL:  strings.TrimSpace(m.videoURL),
		audioURL:  strings.TrimSpace(m.audioURL),
		imageURLs: nonEmptyTrimmed(m.imageURLs),
		clips:     nonEmptyTrimmed(m.clips),
		tracks:    nonEmptyTrimmed(m.tracks),
	}
}

// nonEmptyTrimmed trims each element and returns only the non-blank ones.
func nonEmptyTrimmed(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// buildGenerateArgs maps a generation type to an MCP tool name and argument map,
// folding in any input-media URLs (media) the pipeline actions consume. The arg
// names match the worker tool schemas (worker/src/tools/{image,audio,video}.ts).
func buildGenerateArgs(kind, action, prompt, out, tier, format, actorID, voice string, media mediaInputs) (string, map[string]any, error) {
	// Drop blank/whitespace-only URLs up front so required-input validation can't
	// be satisfied by an empty value.
	media = media.normalized()
	prompt = strings.TrimSpace(prompt)
	switch strings.ToLower(kind) {
	case "image":
		return buildImageArgs(action, prompt, out, tier, format, actorID, media)
	case "audio":
		return buildAudioArgs(action, prompt, out, voice, actorID, media)
	case "video":
		return buildVideoArgs(action, prompt, out, format, actorID, media)
	default:
		return "", nil, fmt.Errorf("unknown --type %q (want image, video or audio)", kind)
	}
}

// buildImageArgs maps the image tool's actions. create needs a prompt; edit
// needs an image + prompt; upscale/animate need an image. The worker takes a
// single source image as `image_url`, so the first --image-url is used.
func buildImageArgs(action, prompt, out, tier, format, actorID string, media mediaInputs) (string, map[string]any, error) {
	if action == "" {
		action = "create"
	}
	args := map[string]any{"action": action, "out": defaultOut(out, "image.jpg")}
	img := firstOrEmpty(media.imageURLs)
	switch action {
	case "create":
		if prompt == "" {
			return "", nil, fmt.Errorf("a prompt is required for image %s", action)
		}
	case "edit":
		if img == "" || prompt == "" {
			return "", nil, fmt.Errorf("--image-url and a prompt are required for image edit")
		}
		args["image_url"] = img
	case "upscale", "animate":
		// animate may instead derive its frame from --actor; require an image only
		// when no actor is given.
		if img == "" && !(action == "animate" && actorID != "") {
			return "", nil, fmt.Errorf("--image-url is required for image %s", action)
		}
		if img != "" {
			args["image_url"] = img
		}
	}
	if prompt != "" {
		args["prompt"] = prompt
	}
	if tier != "" {
		args["tier"] = tier
	}
	if format != "" {
		args["format"] = format
	}
	if actorID != "" {
		args["actor_id"] = actorID
	}
	return "image", args, nil
}

// buildAudioArgs maps the audio tool's actions. speak takes the prompt as
// `text`; sfx/music take it as `prompt`; mix/concat take --tracks.
func buildAudioArgs(action, prompt, out, voice, actorID string, media mediaInputs) (string, map[string]any, error) {
	if action == "" {
		action = "speak"
	}
	args := map[string]any{"action": action, "out": defaultOut(out, "audio.mp3")}
	switch action {
	case "speak":
		if prompt == "" {
			return "", nil, fmt.Errorf("text to speak is required for audio speak")
		}
		args["text"] = prompt
		if voice != "" {
			args["voice"] = voice
		}
	case "sfx", "music":
		if prompt == "" {
			return "", nil, fmt.Errorf("a prompt is required for audio %s", action)
		}
		args["prompt"] = prompt
	case "mix":
		if len(media.tracks) < 2 {
			return "", nil, fmt.Errorf("--tracks needs at least 2 audio URLs for audio mix")
		}
		args["tracks"] = toAnySlice(media.tracks)
	case "concat":
		if len(media.tracks) == 0 {
			return "", nil, fmt.Errorf("--tracks is required for audio concat")
		}
		args["tracks"] = toAnySlice(media.tracks)
	default:
		// Unknown action: forward the prompt as-is so the worker reports the error.
		if prompt != "" {
			args["prompt"] = prompt
		}
	}
	if actorID != "" {
		args["actor_id"] = actorID
	}
	return "audio", args, nil
}

// buildVideoArgs maps the video tool's actions, including the pipeline actions
// that drive on input media (lipsync, assemble, edit, swap, captions, upscale,
// mix_audio). Arg names mirror worker/src/tools/video.ts.
func buildVideoArgs(action, prompt, out, format, actorID string, media mediaInputs) (string, map[string]any, error) {
	if action == "" {
		// Plain text→video needs no actor (create); route to the actor scene
		// composite only when an actor is given.
		if actorID != "" {
			action = "scene"
		} else {
			action = "create"
		}
	}
	args := map[string]any{"action": action, "out": defaultOut(out, "video.mp4")}
	switch action {
	case "create":
		if prompt == "" {
			return "", nil, fmt.Errorf("a prompt is required for video create")
		}
		args["prompt"] = prompt
		if len(media.imageURLs) > 0 {
			args["reference_images"] = toAnySlice(media.imageURLs)
		}
	case "scene":
		if actorID == "" {
			return "", nil, fmt.Errorf("--actor is required for --type video --action scene")
		}
		if prompt == "" {
			return "", nil, fmt.Errorf("a scene prompt is required for video scene")
		}
		args["prompt"] = prompt
		args["scene_prompt"] = prompt
	case "edit":
		if media.videoURL == "" || prompt == "" {
			return "", nil, fmt.Errorf("--video-url and a prompt are required for video edit")
		}
		args["video_url"] = media.videoURL
		args["prompt"] = prompt
	case "edit_ref":
		if media.videoURL == "" || prompt == "" || len(media.imageURLs) == 0 {
			return "", nil, fmt.Errorf("--video-url, a prompt, and at least one --image-url are required for video edit_ref")
		}
		args["video_url"] = media.videoURL
		args["prompt"] = prompt
		args["reference_images"] = toAnySlice(media.imageURLs)
	case "swap":
		// swap needs the source video plus a reference image — either --image-url
		// or an --actor the worker resolves a reference from.
		if media.videoURL == "" {
			return "", nil, fmt.Errorf("--video-url is required for video swap")
		}
		if img := firstOrEmpty(media.imageURLs); img == "" && actorID == "" {
			return "", nil, fmt.Errorf("--image-url (or --actor) is required for video swap")
		} else if img != "" {
			args["image_url"] = img
		}
		args["video_url"] = media.videoURL
		if prompt != "" {
			args["prompt"] = prompt
		}
	case "lipsync":
		if media.videoURL == "" || media.audioURL == "" {
			return "", nil, fmt.Errorf("--video-url and --audio-url are required for video lipsync")
		}
		args["video_url"] = media.videoURL
		args["audio_url"] = media.audioURL
	case "captions", "upscale":
		if media.videoURL == "" {
			return "", nil, fmt.Errorf("--video-url is required for video %s", action)
		}
		args["video_url"] = media.videoURL
	case "assemble":
		if len(media.clips) == 0 {
			return "", nil, fmt.Errorf("--clips is required for video assemble (at least one clip URL)")
		}
		args["clips"] = toAnySlice(media.clips)
		if media.audioURL != "" {
			args["audio_url"] = media.audioURL
		}
		if prompt != "" {
			args["prompt"] = prompt
		}
	case "mix_audio":
		if media.videoURL == "" {
			return "", nil, fmt.Errorf("--video-url is required for video mix_audio")
		}
		if len(media.tracks) == 0 {
			return "", nil, fmt.Errorf("--tracks is required for video mix_audio (audio URLs to layer)")
		}
		args["video_url"] = media.videoURL
		args["tracks"] = toAnySlice(media.tracks)
	default:
		// Unknown action: forward the prompt so the worker surfaces the error.
		if prompt != "" {
			args["prompt"] = prompt
		}
	}
	if format != "" {
		args["format"] = format
	}
	if actorID != "" {
		args["actor_id"] = actorID
	}
	return "video", args, nil
}

// firstOrEmpty returns the first element of s, or "" when s is empty.
func firstOrEmpty(s []string) string {
	if len(s) == 0 {
		return ""
	}
	return s[0]
}

// toAnySlice converts a []string to []any so it serializes as a JSON array in
// the tool args map (the MCP tool schemas expect string arrays).
func toAnySlice(s []string) []any {
	out := make([]any, len(s))
	for i, v := range s {
		out[i] = v
	}
	return out
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
