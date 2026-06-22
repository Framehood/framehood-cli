package tui

import (
	"encoding/json"
	"strconv"
	"strings"
)

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
// immediate marks read-only, no-required-arg actions that the palette runs right
// away without asking the user for any additional input.
type actionSpec struct {
	tool        string
	action      string
	summary     string
	kind        actionKind
	promptField string   // "prompt" | "text" | "" (needs a form)
	outName     string   // default output filename for generate actions
	needs       []string // required inputs (for the not-yet-wired hint)
	immediate   bool     // true → palette executes without prompting (read-only actions)
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
		{"image", "create", "text → image", kindGenerate, "prompt", "image.jpg", []string{"prompt"}, false},
		{"image", "edit", "modify an image", kindGenerate, "", "image.jpg", []string{"image_url", "prompt"}, false},
		{"image", "upscale", "higher resolution", kindGenerate, "", "image.jpg", []string{"image_url"}, false},
		{"image", "animate", "image → video", kindGenerate, "", "video.mp4", []string{"image_url"}, false},
		{"image", "actor_sheet", "multi-angle actor sheet", kindGenerate, "", "sheet.jpg", []string{"actor_id"}, false},
	}},
	{"video", "Video", []actionSpec{
		{"video", "create", "text → video", kindGenerate, "prompt", "video.mp4", []string{"prompt"}, false},
		{"video", "edit", "edit a video", kindGenerate, "", "video.mp4", []string{"video_url", "prompt"}, false},
		{"video", "edit_ref", "edit with reference images", kindGenerate, "", "video.mp4", []string{"video_url", "prompt", "reference_images"}, false},
		{"video", "swap", "swap a person / object", kindGenerate, "", "video.mp4", []string{"video_url"}, false},
		{"video", "lipsync", "sync lips to audio", kindGenerate, "", "video.mp4", []string{"video_url", "audio_url"}, false},
		{"video", "captions", "burn in captions", kindGenerate, "", "video.mp4", []string{"video_url"}, false},
		{"video", "upscale", "higher resolution", kindGenerate, "", "video.mp4", []string{"video_url"}, false},
		{"video", "assemble", "assemble clips", kindGenerate, "", "video.mp4", []string{"clips"}, false},
		{"video", "mix_audio", "mix in audio tracks", kindGenerate, "", "video.mp4", []string{"video_url", "tracks"}, false},
		{"video", "scene", "actor scene (image→animate→speak→mix)", kindGenerate, "", "video.mp4", []string{"actor_id", "scene_prompt"}, false},
	}},
	{"audio", "Audio", []actionSpec{
		{"audio", "speak", "text → speech", kindGenerate, "text", "audio.mp3", []string{"text"}, false},
		{"audio", "sfx", "text → sound effect", kindGenerate, "prompt", "sfx.mp3", []string{"prompt"}, false},
		{"audio", "music", "text → music", kindGenerate, "prompt", "music.mp3", []string{"prompt"}, false},
		{"audio", "mix", "blend audio tracks", kindGenerate, "", "mix.mp3", []string{"tracks"}, false},
		{"audio", "concat", "join audio in sequence", kindGenerate, "", "concat.mp3", []string{"tracks"}, false},
	}},
	{"actor", "Actors", []actionSpec{
		{"actor", "list", "list your actors", kindRead, "", "", nil, false},
		{"actor", "get", "get one actor", kindRead, "", "", []string{"actor_id"}, false},
		{"actor", "create", "create a persistent actor", kindManage, "", "", []string{"name", "images_data_url"}, false},
		{"actor", "update", "update an actor", kindManage, "", "", []string{"actor_id"}, false},
		{"actor", "delete", "delete an actor", kindManage, "", "", []string{"actor_id"}, false},
		{"actor", "batch", "batch-generate with an actor", kindGenerate, "", "", []string{"actor_id", "prompts"}, false},
	}},
	{"qa", "Quality checks", []actionSpec{
		{"qa", "full", "full video QA", kindGenerate, "", "", []string{"video"}, false},
		{"qa", "person", "compare two faces", kindGenerate, "", "", []string{"image1", "image2"}, false},
		{"qa", "voice", "voice consistency", kindGenerate, "", "", []string{"audio"}, false},
		{"qa", "scene", "scene vs plan", kindGenerate, "", "", []string{"video", "plan"}, false},
		{"qa", "transcript", "transcribe a video", kindGenerate, "", "", []string{"video"}, false},
		{"qa", "image", "check an image vs a brief", kindGenerate, "", "", []string{"image_url", "description"}, false},
	}},
	{"files", "Files", []actionSpec{
		{"files", "list", "list your files", kindRead, "", "", nil, true}, // immediate: no args needed
		{"files", "upload", "upload from a URL", kindManage, "", "", []string{"url", "filename"}, false},
		{"files", "delete", "delete a file", kindManage, "", "", []string{"filename"}, false},
		{"files", "publish", "make a file public", kindManage, "", "", []string{"filename"}, false},
		{"files", "unpublish", "make a file private again", kindManage, "", "", []string{"filename"}, false},
		{"files", "download", "get a URL to fetch a file", kindRead, "", "", []string{"filename"}, false},
	}},
	{"billing", "Credits & plan", []actionSpec{
		{"billing", "balance", "credit balance", kindRead, "", "", nil, true},            // immediate
		{"billing", "plans", "available plans", kindRead, "", "", nil, true},             // immediate
		{"billing", "plan", "current plan", kindRead, "", "", nil, true},                 // immediate
		{"billing", "transactions", "recent credit ledger", kindRead, "", "", nil, true}, // immediate
		{"billing", "manage", "manage subscription", kindRead, "", "", nil, false},
		{"billing", "subscribe", "subscribe to a plan", kindManage, "", "", []string{"step"}, false},
		{"billing", "preview", "preview a plan change", kindManage, "", "", []string{"step"}, false},
		{"billing", "change", "switch to another package", kindManage, "", "", []string{"step"}, false},
		{"billing", "cancel", "cancel at period end (or reactivate)", kindManage, "", "", nil, false},
		{"billing", "request_upgrade", "ask the owner to upgrade", kindManage, "", "", nil, false},
	}},
	{"org", "Organization", []actionSpec{
		{"org", "info", "organization info", kindRead, "", "", nil, true}, // immediate
		{"org", "members", "list members", kindRead, "", "", nil, true},   // immediate
		{"org", "spend", "spend report", kindRead, "", "", nil, true},     // immediate
		{"org", "invite", "invite a member", kindManage, "", "", []string{"email"}, false},
		{"org", "accept_invite", "join an org with a token", kindManage, "", "", []string{"token"}, false},
		{"org", "remove", "remove a member", kindManage, "", "", []string{"email"}, false},
	}},
	{"library", "Library", []actionSpec{
		{"library", "list", "search your generated assets", kindRead, "", "", nil, false}, // form (query/type optional)
		{"library", "trashed", "list trashed assets", kindRead, "", "", nil, true},        // immediate
		{"library", "trash", "move an asset to trash", kindManage, "", "", []string{"id"}, false},
		{"library", "restore", "restore an asset from trash", kindManage, "", "", []string{"id"}, false},
	}},
	{"project", "Projects", []actionSpec{
		{"project", "list", "list your projects", kindRead, "", "", nil, true},         // immediate
		{"project", "current", "show the active project", kindRead, "", "", nil, true}, // immediate
		{"project", "create", "create a project", kindManage, "", "", []string{"name"}, false},
		{"project", "update", "rename / change a project", kindManage, "", "", []string{"id"}, false},
		{"project", "use", "set the active project", kindManage, "", "", nil, false},
		{"project", "assign", "put an asset in a project", kindManage, "", "", []string{"asset_id"}, false},
		{"project", "delete", "delete a project", kindManage, "", "", []string{"id"}, false},
	}},
	{"api_keys", "API keys", []actionSpec{
		{"api_keys", "list", "list your API keys", kindRead, "", "", nil, true}, // immediate
		{"api_keys", "create", "mint a new key (shown once)", kindManage, "", "", nil, false},
		{"api_keys", "delete", "revoke a key", kindManage, "", "", []string{"key"}, false},
	}},
	{"get_status", "Jobs", []actionSpec{
		{"get_status", "check", "check a job by id", kindRead, "", "", []string{"job_id"}, false},
		{"get_status", "list", "your generation history", kindRead, "", "", nil, true}, // immediate
		{"get_status", "cancel", "cancel a running job", kindManage, "", "", []string{"job_id"}, false},
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

// argsForImmediateAction builds the minimal args for a read-only immediate
// action (spec.immediate == true). These actions need no user-supplied fields.
func argsForImmediateAction(spec actionSpec) (string, map[string]any) {
	return spec.tool, map[string]any{"action": spec.action}
}

// paramKind controls how a form field is rendered and parsed.
type paramKind int

const (
	pText      paramKind = iota // free text
	pMedia                      // a single media URL (picker offers recent results)
	pMediaList                  // comma-separated media URLs → []string
	pTextList                   // comma-separated free-text values → []string (e.g. prompts)
	pNumber                     // an optional integer (omitted when empty / non-numeric)
	pJSON                       // a JSON object (a non-JSON value is wrapped as {"text": …})
	pBool                       // a boolean — true/yes/1/y/on → true, false/no/0/n/off → false
)

type paramSpec struct {
	name     string
	label    string
	kind     paramKind
	required bool
}

func (p paramSpec) isMedia() bool { return p.kind == pMedia || p.kind == pMediaList }

// isList reports whether the field collects a comma-separated list (media URLs
// or plain text) parsed into a []string.
func (p paramSpec) isList() bool { return p.kind == pMediaList || p.kind == pTextList }

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
	"qa.scene":        {req("video", "video to check", pMedia), req("plan", "scene plan", pJSON)},

	// Actor-driven generation + manipulation (schemas: worker/src/tools/*.ts).
	"image.actor_sheet": {req("actor_id", "actor id (act_…)", pText), opt("out_prefix", "output prefix", pText), opt("variations", "variations (1-9)", pNumber)},
	"video.edit_ref":    {req("video_url", "source video", pMedia), req("prompt", "edit instruction", pText), req("reference_images", "reference images (comma-sep urls, ≤4)", pMediaList)},
	"video.scene":       {req("actor_id", "actor id (act_…)", pText), req("scene_prompt", "scene description", pText), opt("speech_text", "spoken line (optional)", pText), opt("outfit_name", "outfit name (optional)", pText)},
	"actor.create":      {req("name", "actor name", pText), req("images_data_url", "reference photos ZIP url", pMedia), opt("voice_sample_url", "voice sample url (optional)", pMedia)},
	"actor.update":      {req("actor_id", "actor id (act_…)", pText), opt("voice_id", "ElevenLabs voice id (optional)", pText), opt("voice_sample_url", "voice sample url (optional)", pMedia)},
	"actor.delete":      {req("actor_id", "actor id to delete (act_…)", pText)},
	"actor.batch":       {req("actor_id", "actor id (act_…)", pText), req("prompts", "prompts (comma-separated)", pTextList), opt("kind", "kind: image | video", pText)},

	// Console: projects + library — parameterized actions get a small form.
	"project.create":  {req("name", "project name", pText), opt("visibility", "visibility: personal | shared", pText), opt("description", "description (optional)", pText)},
	"project.use":     {opt("id", "project id (empty = clear active)", pText)},
	"project.delete":  {req("id", "project id to delete", pText)},
	"project.assign":  {req("asset_id", "asset id", pText), opt("id", "project id (empty = unassign)", pText)},
	"library.list":    {opt("query", "search query (optional)", pText), opt("type", "type: image | video | audio", pText), opt("project", "project id (optional)", pText)},
	"library.trash":   {req("id", "asset id to trash", pText)},
	"library.restore": {req("id", "asset id to restore", pText)},

	// Parity service actions (billing / files / org / project / api_keys / jobs).
	"project.update":    {req("id", "project id", pText), opt("name", "new name (optional)", pText), opt("visibility", "visibility: personal | shared", pText), opt("description", "new description (optional)", pText)},
	"billing.preview":   {req("step", "package id (from billing plans)", pText)},
	"billing.change":    {req("step", "package id (from billing plans)", pText)},
	"billing.cancel":    {opt("reactivate", "reactivate? true to resume, blank to cancel at period end", pBool)},
	"files.unpublish":   {req("filename", "file key to make private", pText)},
	"files.download":    {req("filename", "file key to fetch", pText)},
	"org.accept_invite": {req("token", "invite token", pText)},
	"api_keys.create":   {opt("name", "label (optional)", pText)},
	"api_keys.delete":   {req("key", "key prefix or full value", pText)},
	"get_status.cancel": {req("job_id", "job id to cancel", pText)},
}

func (a actionSpec) form() []paramSpec { return actionForms[a.tool+"."+a.action] }
func (a actionSpec) hasForm() bool     { return len(actionForms[a.tool+"."+a.action]) > 0 }

// argsForForm builds the MCP tool name + arguments from collected form values.
// Each field is parsed per its kind: list fields → []string, number → int,
// json → object (a plain value is wrapped as {"text": …}); the rest stay
// strings. Empty fields are omitted entirely.
func argsForForm(spec actionSpec, vals map[string]string) (string, map[string]any) {
	args := map[string]any{"action": spec.action}
	for _, f := range spec.form() {
		v := strings.TrimSpace(vals[f.name])
		if v == "" {
			continue
		}
		switch f.kind {
		case pMediaList, pTextList:
			if list := splitList(v); len(list) > 0 {
				args[f.name] = list
			}
		case pNumber:
			if n, err := strconv.Atoi(v); err == nil {
				args[f.name] = n
			}
		case pJSON:
			args[f.name] = parseJSONField(v)
		case pBool:
			// The MCP tool expects a real boolean, not the string "true". Only
			// forward an unambiguous value; an unrecognized one is dropped so the
			// action falls back to its server-side default.
			if b, ok := parseBoolField(v); ok {
				args[f.name] = b
			}
		default:
			args[f.name] = v
		}
	}
	if spec.outName != "" {
		args["out"] = spec.outName
	}
	return spec.tool, args
}

// splitList parses a comma-separated value into a trimmed, non-empty []string.
func splitList(v string) []string {
	parts := strings.Split(v, ",")
	list := make([]string, 0, len(parts))
	for _, p := range parts {
		if s := strings.TrimSpace(p); s != "" {
			list = append(list, s)
		}
	}
	return list
}

// parseJSONField turns a form value into a JSON object for the worker. A valid
// JSON object/value is used as-is; anything else is wrapped as {"text": value}
// so a tool that requires an object (e.g. qa·scene's `plan`) always gets one.
func parseJSONField(v string) any {
	var obj map[string]any
	if json.Unmarshal([]byte(v), &obj) == nil {
		return obj
	}
	return map[string]any{"text": v}
}

// parseBoolField parses a form value into a boolean. ok=false for an
// unrecognized value so the caller can drop the field (server default applies).
func parseBoolField(v string) (val, ok bool) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "true", "yes", "y", "1", "on":
		return true, true
	case "false", "no", "n", "0", "off":
		return false, true
	}
	return false, false
}
