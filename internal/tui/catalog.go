package tui

import "strings"

// actionKind classifies what selecting an action does, so the studio can hint
// the right next step and (later) render the right output.
type actionKind int

const (
	kindGenerate actionKind = iota // submits a job, produces media
	kindRead                       // returns information
	kindManage                     // mutates account / files / actors
)

// actionSpec is one selectable action in the NAV pane. promptField is the single
// text field the studio can submit this action with TODAY (from the prompt box);
// "" means the action needs the per-parameter form (a later step) before it can
// run. needs lists the required inputs, shown as a hint until the form lands.
type actionSpec struct {
	tool        string
	action      string
	summary     string
	kind        actionKind
	promptField string   // "prompt" | "text" | "" (needs a form)
	outName     string   // default output filename for generate actions
	needs       []string // required inputs (for the not-yet-wired hint)
}

// runnable reports whether the studio can submit this action from the prompt box
// alone (Step 4). Form-driven actions become runnable in a later step.
func (a actionSpec) runnable() bool { return a.promptField != "" }

type toolGroup struct {
	tool    string
	summary string
	actions []actionSpec
}

// catalog is the hand-derived map of the worker's MCP tool surface
// (worker/src/tools/*.ts). It drives the NAV list; promptField is set only where
// a single text input is enough to submit today.
var catalog = []toolGroup{
	{"image", "Images", []actionSpec{
		{"image", "create", "text → image", kindGenerate, "prompt", "image.jpg", []string{"prompt"}},
		{"image", "edit", "modify an image", kindGenerate, "", "image.jpg", []string{"image_url", "prompt"}},
		{"image", "upscale", "higher resolution", kindGenerate, "", "image.jpg", []string{"image_url"}},
		{"image", "animate", "image → video", kindGenerate, "", "video.mp4", []string{"image_url"}},
		{"image", "actor_sheet", "multi-angle actor sheet", kindGenerate, "", "sheet.jpg", []string{"actor_id"}},
	}},
	{"video", "Video", []actionSpec{
		{"video", "create", "text → video", kindGenerate, "prompt", "video.mp4", []string{"prompt"}},
		{"video", "edit", "edit a video", kindGenerate, "", "video.mp4", []string{"video_url", "prompt"}},
		{"video", "edit_ref", "edit with reference images", kindGenerate, "", "video.mp4", []string{"video_url", "prompt", "reference_images"}},
		{"video", "swap", "swap a person / object", kindGenerate, "", "video.mp4", []string{"video_url"}},
		{"video", "lipsync", "sync lips to audio", kindGenerate, "", "video.mp4", []string{"video_url", "audio_url"}},
		{"video", "captions", "burn in captions", kindGenerate, "", "video.mp4", []string{"video_url"}},
		{"video", "upscale", "higher resolution", kindGenerate, "", "video.mp4", []string{"video_url"}},
		{"video", "assemble", "assemble clips", kindGenerate, "", "video.mp4", []string{"clips"}},
		{"video", "mix_audio", "mix in audio tracks", kindGenerate, "", "video.mp4", []string{"video_url", "tracks"}},
		{"video", "scene", "actor scene (image→animate→speak→mix)", kindGenerate, "", "video.mp4", []string{"actor_id", "scene_prompt"}},
	}},
	{"audio", "Audio", []actionSpec{
		{"audio", "speak", "text → speech", kindGenerate, "text", "audio.mp3", []string{"text"}},
		{"audio", "sfx", "text → sound effect", kindGenerate, "prompt", "sfx.mp3", []string{"prompt"}},
		{"audio", "music", "text → music", kindGenerate, "prompt", "music.mp3", []string{"prompt"}},
		{"audio", "mix", "blend audio tracks", kindGenerate, "", "mix.mp3", []string{"tracks"}},
		{"audio", "concat", "join audio in sequence", kindGenerate, "", "concat.mp3", []string{"tracks"}},
	}},
	{"actor", "Actors", []actionSpec{
		{"actor", "list", "list your actors", kindRead, "", "", nil},
		{"actor", "get", "get one actor", kindRead, "", "", []string{"actor_id"}},
		{"actor", "create", "create a persistent actor", kindManage, "", "", []string{"name", "images_data_url"}},
		{"actor", "update", "update an actor", kindManage, "", "", []string{"actor_id"}},
		{"actor", "delete", "delete an actor", kindManage, "", "", []string{"actor_id"}},
		{"actor", "batch", "batch-generate with an actor", kindGenerate, "", "", []string{"actor_id", "prompts"}},
	}},
	{"qa", "Quality checks", []actionSpec{
		{"qa", "full", "full video QA", kindGenerate, "", "", []string{"video"}},
		{"qa", "person", "compare two faces", kindGenerate, "", "", []string{"image1", "image2"}},
		{"qa", "voice", "voice consistency", kindGenerate, "", "", []string{"audio"}},
		{"qa", "scene", "scene vs plan", kindGenerate, "", "", []string{"video", "plan"}},
		{"qa", "transcript", "transcribe a video", kindGenerate, "", "", []string{"video"}},
		{"qa", "image", "check an image vs a brief", kindGenerate, "", "", []string{"image_url", "description"}},
	}},
	{"files", "Files", []actionSpec{
		{"files", "list", "list your files", kindRead, "", "", nil},
		{"files", "upload", "upload from a URL", kindManage, "", "", []string{"url", "filename"}},
		{"files", "delete", "delete a file", kindManage, "", "", []string{"filename"}},
		{"files", "publish", "make a file public", kindManage, "", "", []string{"filename"}},
	}},
	{"billing", "Credits & plan", []actionSpec{
		{"billing", "balance", "credit balance", kindRead, "", "", nil},
		{"billing", "plans", "available plans", kindRead, "", "", nil},
		{"billing", "plan", "current plan", kindRead, "", "", nil},
		{"billing", "manage", "manage subscription", kindRead, "", "", nil},
		{"billing", "subscribe", "subscribe to a plan", kindManage, "", "", []string{"step"}},
		{"billing", "request_upgrade", "ask the owner to upgrade", kindManage, "", "", nil},
	}},
	{"org", "Organization", []actionSpec{
		{"org", "info", "organization info", kindRead, "", "", nil},
		{"org", "members", "list members", kindRead, "", "", nil},
		{"org", "spend", "spend report", kindRead, "", "", nil},
		{"org", "invite", "invite a member", kindManage, "", "", []string{"email"}},
		{"org", "remove", "remove a member", kindManage, "", "", []string{"email"}},
	}},
	{"get_status", "Job status", []actionSpec{
		{"get_status", "check", "check a job by id", kindRead, "", "", []string{"job_id"}},
	}},
}

// argsForAction builds the MCP tool name + arguments to submit a runnable action
// from the prompt box. Only valid when spec.runnable().
func argsForAction(spec actionSpec, prompt string) (string, map[string]any) {
	args := map[string]any{"action": spec.action, spec.promptField: prompt}
	if spec.outName != "" {
		args["out"] = spec.outName
	}
	return spec.tool, args
}

// paramKind controls how a form field is rendered and parsed.
type paramKind int

const (
	pText      paramKind = iota // free text
	pMedia                      // a single media URL (picker offers recent results)
	pMediaList                  // comma-separated media URLs → []string
)

type paramSpec struct {
	name     string
	label    string
	kind     paramKind
	required bool
}

func (p paramSpec) isMedia() bool { return p.kind == pMedia || p.kind == pMediaList }

// req/opt build a required/optional field (requiredness is schema, not label text).
func req(name, label string, k paramKind) paramSpec { return paramSpec{name, label, k, true} }
func opt(name, label string, k paramKind) paramSpec { return paramSpec{name, label, k, false} }

// actionForms maps "tool.action" to the fields the studio collects for the
// form-driven GENERATE actions, so they can run without the MCP prompt-only path.
// Reads/manage actions (billing, org, files, actor mgmt) come in a later step.
var actionForms = map[string][]paramSpec{
	"image.edit":      {req("image_url", "source image", pMedia), req("prompt", "edit instruction", pText)},
	"image.upscale":   {req("image_url", "source image", pMedia)},
	"image.animate":   {req("image_url", "source image", pMedia), opt("prompt", "motion", pText)},
	"video.edit":      {req("video_url", "source video", pMedia), req("prompt", "edit instruction", pText)},
	"video.swap":      {req("video_url", "source video", pMedia), req("image_url", "swap-in image", pMedia)},
	"video.lipsync":   {req("video_url", "face video", pMedia), req("audio_url", "voice audio", pMedia)},
	"video.captions":  {req("video_url", "source video", pMedia)},
	"video.upscale":   {req("video_url", "source video", pMedia)},
	"video.mix_audio": {req("video_url", "source video", pMedia), req("tracks", "audio tracks (comma-sep)", pMediaList)},
	"video.assemble":  {req("clips", "clips (comma-sep urls)", pMediaList)},
	"audio.mix":       {req("tracks", "tracks (comma-sep urls)", pMediaList)},
	"audio.concat":    {req("tracks", "tracks (comma-sep urls)", pMediaList)},
	"qa.full":         {req("video", "video to check", pMedia)},
	"qa.voice":        {req("audio", "audio to check", pMedia)},
	"qa.transcript":   {req("video", "video to transcribe", pMedia)},
	"qa.person":       {req("image1", "first face", pMedia), req("image2", "second face", pMedia)},
	"qa.image":        {req("image_url", "image to check", pMedia), req("description", "what it should show", pText)},
}

func (a actionSpec) form() []paramSpec { return actionForms[a.tool+"."+a.action] }
func (a actionSpec) hasForm() bool     { return len(actionForms[a.tool+"."+a.action]) > 0 }

// argsForForm builds the MCP tool name + arguments from collected form values.
// Media-list fields are split on commas into a []string.
func argsForForm(spec actionSpec, vals map[string]string) (string, map[string]any) {
	args := map[string]any{"action": spec.action}
	for _, f := range spec.form() {
		v := strings.TrimSpace(vals[f.name])
		if v == "" {
			continue
		}
		if f.kind == pMediaList {
			parts := strings.Split(v, ",")
			list := make([]string, 0, len(parts))
			for _, p := range parts {
				if s := strings.TrimSpace(p); s != "" {
					list = append(list, s)
				}
			}
			args[f.name] = list
		} else {
			args[f.name] = v
		}
	}
	if spec.outName != "" {
		args["out"] = spec.outName
	}
	return spec.tool, args
}
