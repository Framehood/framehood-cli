package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Framehood/framehood-cli/internal/auth"
	"github.com/Framehood/framehood-cli/internal/config"
	"github.com/Framehood/framehood-cli/internal/mcp"
	"github.com/Framehood/framehood-cli/internal/render"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
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
	var kind, action, out, tier, format, actorID, voice, shotType string
	var multiPrompt []string
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
			"  framehood generate --type image --action animate --image-url frame.jpg \"slow dolly in\"\n" +
			"  framehood generate --type image --action animate --image-url frame.jpg \\\n" +
			"      --shot \"wide establishing shot@3s\" --shot \"push in on the face@4s\" --shot-type customize\n" +
			"  framehood generate --type video --action lipsync --video-url … --audio-url …\n" +
			"  framehood generate --type video --action assemble --clips a.mp4,b.mp4 --audio-url vo.mp3",
		RunE: func(cmd *cobra.Command, args []string) error {
			prompt := strings.Join(args, " ")

			// Validate and assemble the tool args FIRST — before opening a session
			// (which may hit the network to refresh the token) or making any tool
			// call. A bad invocation (e.g. lipsync without --audio-url) fails fast
			// with a clear CLI error and never touches the network.
			tool, toolArgs, err := buildGenerateArgs(kind, action, prompt, out, tier, format, actorID, voice, media, animateInputs{shots: multiPrompt, shotType: shotType})
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
	cmd.Flags().StringVar(&format, "format", "", "Aspect/size: friendly names square|portrait|9:16|landscape|16:9|3:4|4:3|shorts|reels (case-insensitive) or a raw preset like landscape_16_9; works on the actor path too")
	cmd.Flags().StringVar(&actorID, "actor", "", "Actor id (act_…) to route through")
	cmd.Flags().StringVar(&voice, "voice", "", "Voice name for audio speak (e.g. Rachel)")
	// Kling multi-shot timeline for image→video (--action animate). Each --shot is
	// one segment "prompt@duration"; the duration suffix is optional (default 5s)
	// and a trailing 's' is allowed, e.g. "push in@4s". Repeat for more shots.
	// Caps mirror the worker: ≤6 shots, ≤15s total, each shot 1–15s. Mutually
	// exclusive with the positional prompt. --shot is an alias of --multi-prompt.
	cmd.Flags().StringArrayVar(&multiPrompt, "multi-prompt", nil, "animate: one shot \"prompt@duration\" (repeatable; also as --shot; ≤6 shots, ≤15s total, each 1–15s; default 5s; conflicts with the prompt arg)")
	cmd.Flags().StringVar(&shotType, "shot-type", "", "animate: how multi-prompt shots are stitched: customize (default) | intelligent")
	// --shot is a friendly alias of --multi-prompt: a normalize func folds both
	// spellings onto the single registered flag, so repeated --shot and
	// --multi-prompt accumulate into one slice in invocation order (binding two
	// StringArrayVars to one var instead would drop earlier values).
	cmd.Flags().SetNormalizeFunc(func(_ *pflag.FlagSet, name string) pflag.NormalizedName {
		if name == "shot" {
			name = "multi-prompt"
		}
		return pflag.NormalizedName(name)
	})
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

// animateInputs carries the image→video (animate) timeline flags. shots are the
// raw "prompt@duration" tokens from repeated --shot/--multi-prompt; shotType is
// the --shot-type value. Both feed the kling multi_prompt path in buildImageArgs.
type animateInputs struct {
	shots    []string
	shotType string
}

// buildGenerateArgs maps a generation type to an MCP tool name and argument map,
// folding in any input-media URLs (media) the pipeline actions consume. The arg
// names match the worker tool schemas (worker/src/tools/{image,audio,video}.ts).
func buildGenerateArgs(kind, action, prompt, out, tier, format, actorID, voice string, media mediaInputs, anim animateInputs) (string, map[string]any, error) {
	// Drop blank/whitespace-only URLs up front so required-input validation can't
	// be satisfied by an empty value.
	media = media.normalized()
	prompt = strings.TrimSpace(prompt)
	switch strings.ToLower(kind) {
	case "image":
		return buildImageArgs(action, prompt, out, tier, format, actorID, media, anim)
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
//
// animate also accepts a kling multi-shot timeline (anim.shots): when present it
// emits `multi_prompt` (and optional `shot_type`) instead of a single prompt —
// mirroring worker/src/tools/image.ts. --format is forwarded verbatim for every
// action (incl. the actor path); the worker normalizes friendly names via
// actorImageSize, so the CLI need not reject or map them.
func buildImageArgs(action, prompt, out, tier, format, actorID string, media mediaInputs, anim animateInputs) (string, map[string]any, error) {
	if action == "" {
		action = "create"
	}
	args := map[string]any{"action": action, "out": defaultOut(out, "image.jpg")}
	img := firstOrEmpty(media.imageURLs)

	// multi_prompt / shot_type are animate-only. Reject them up front on any
	// other action so a misplaced --shot fails clearly instead of being dropped.
	hasShots := len(nonEmptyTrimmed(anim.shots)) > 0
	if action != "animate" && (hasShots || strings.TrimSpace(anim.shotType) != "") {
		return "", nil, fmt.Errorf("--shot/--multi-prompt and --shot-type only apply to image --action animate")
	}

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

	// Kling multi-shot timeline (animate only). Validate the caps locally and emit
	// multi_prompt; it is mutually exclusive with the positional prompt.
	if action == "animate" && hasShots {
		if prompt != "" {
			return "", nil, fmt.Errorf("set either a prompt or --shot/--multi-prompt, not both")
		}
		shots, err := parseMultiPrompt(anim.shots)
		if err != nil {
			return "", nil, err
		}
		args["multi_prompt"] = shots
		if st := strings.TrimSpace(anim.shotType); st != "" {
			if st != "customize" && st != "intelligent" {
				return "", nil, fmt.Errorf("--shot-type must be customize or intelligent (got %q)", st)
			}
			args["shot_type"] = st
		}
	} else if prompt != "" {
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

// Kling multi_prompt timeline caps — mirror of worker/src/tools/image.ts.
const (
	multiPromptMaxShots        = 6
	multiPromptMaxTotalSeconds = 15
	multiPromptMinShotSeconds  = 1
	multiPromptMaxShotSeconds  = 15
	multiPromptDefaultSeconds  = 5
)

// parseMultiPrompt turns repeated --shot/--multi-prompt tokens of the form
// "prompt@duration" into the worker's multi_prompt array ([]map{prompt,duration})
// and validates Kling's caps (≤6 shots, ≤15s total, each shot 1–15s) up front so
// a bad timeline fails with a clear CLI error before any network call. The
// duration suffix is optional (default 5s) and may carry a trailing 's'. The
// split is on the LAST '@' and only taken as a duration when it parses as a
// number, so prompts containing '@' are preserved. Durations serialize as
// strings to match the model's enum, exactly like the worker.
func parseMultiPrompt(raw []string) ([]any, error) {
	tokens := nonEmptyTrimmed(raw)
	if len(tokens) == 0 {
		return nil, fmt.Errorf("--shot/--multi-prompt needs at least one shot")
	}
	if len(tokens) > multiPromptMaxShots {
		return nil, fmt.Errorf("--shot/--multi-prompt allows at most %d shots (got %d)", multiPromptMaxShots, len(tokens))
	}
	shots := make([]any, 0, len(tokens))
	total := 0
	for _, tok := range tokens {
		promptPart, durPart := splitShot(tok)
		promptPart = strings.TrimSpace(promptPart)
		if promptPart == "" {
			return nil, fmt.Errorf("each --shot needs a prompt (got %q)", tok)
		}
		shot := map[string]any{"prompt": promptPart}
		secs := multiPromptDefaultSeconds
		if durPart != "" {
			n, err := parseShotDuration(durPart)
			if err != nil {
				return nil, fmt.Errorf("invalid duration in shot %q: %w", tok, err)
			}
			if n < multiPromptMinShotSeconds || n > multiPromptMaxShotSeconds {
				return nil, fmt.Errorf("each shot duration must be %d–%ds (got %ds in %q)", multiPromptMinShotSeconds, multiPromptMaxShotSeconds, n, tok)
			}
			secs = n
			// Serialize as a string to match the model's duration enum (the worker
			// does the same via String(s.duration)).
			shot["duration"] = strconv.Itoa(n)
		}
		total += secs
		shots = append(shots, shot)
	}
	if total > multiPromptMaxTotalSeconds {
		return nil, fmt.Errorf("multi-prompt total duration must be ≤ %ds (got %ds across %d shots)", multiPromptMaxTotalSeconds, total, len(shots))
	}
	return shots, nil
}

// splitShot separates a "prompt@duration" token. It splits on the LAST '@' and
// only treats the suffix as a duration when it looks numeric (optionally with a
// trailing 's'); otherwise the whole token is the prompt — so a prompt that
// itself contains '@' (e.g. an email) is preserved as long as it doesn't end in
// "@<number>". Returns (prompt, durationSuffix); durationSuffix is "" when none.
func splitShot(tok string) (string, string) {
	i := strings.LastIndex(tok, "@")
	if i < 0 {
		return tok, ""
	}
	suffix := strings.TrimSpace(tok[i+1:])
	if suffix == "" || !looksLikeDuration(suffix) {
		return tok, ""
	}
	return tok[:i], suffix
}

// looksLikeDuration reports whether s is a bare number with an optional trailing
// 's' (e.g. "3", "3s", "3.0s") — the only forms splitShot treats as a duration.
func looksLikeDuration(s string) bool {
	s = strings.TrimSuffix(strings.ToLower(s), "s")
	if s == "" {
		return false
	}
	for _, r := range s {
		if (r < '0' || r > '9') && r != '.' {
			return false
		}
	}
	return true
}

// parseShotDuration parses a duration suffix ("3", "3s") into whole seconds. The
// upstream Kling enum is integer seconds, so a fractional value is rejected with
// a clear error rather than silently truncated.
func parseShotDuration(s string) (int, error) {
	s = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(s)), "s")
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("duration must be a whole number of seconds (e.g. 3 or 3s), got %q", s)
	}
	return n, nil
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
