package cmd

import (
	"testing"

	"github.com/Framehood/framehood-cli/internal/config"
)

// noMedia is the empty input-media set for the prompt-only generation tests.
var noMedia = mediaInputs{}

// noAnim is the empty animate-timeline set (no --shot/--multi-prompt/--shot-type)
// for the generation tests that don't exercise the kling multi-shot path.
var noAnim = animateInputs{}

func TestBuildGenerateArgs_Image(t *testing.T) {
	tool, args, err := buildGenerateArgs("image", "", "a fox", "", "fine", "square", "", "", noMedia, noAnim)
	if err != nil {
		t.Fatal(err)
	}
	if tool != "image" {
		t.Fatalf("tool = %s", tool)
	}
	if args["action"] != "create" || args["prompt"] != "a fox" || args["out"] != "image.jpg" {
		t.Fatalf("unexpected args: %v", args)
	}
	if args["tier"] != "fine" || args["format"] != "square" {
		t.Fatalf("missing tier/format: %v", args)
	}
}

func TestBuildGenerateArgs_AudioSpeakUsesText(t *testing.T) {
	tool, args, err := buildGenerateArgs("audio", "", "hello there", "greeting.mp3", "", "", "", "Rachel", noMedia, noAnim)
	if err != nil {
		t.Fatal(err)
	}
	if tool != "audio" || args["action"] != "speak" {
		t.Fatalf("unexpected tool/action: %s %v", tool, args["action"])
	}
	if args["text"] != "hello there" {
		t.Fatalf("speak should map prompt→text: %v", args)
	}
	if args["out"] != "greeting.mp3" || args["voice"] != "Rachel" {
		t.Fatalf("out/voice not threaded: %v", args)
	}
}

func TestBuildGenerateArgs_VideoSceneAndActor(t *testing.T) {
	tool, args, err := buildGenerateArgs("video", "", "a coastline", "", "", "", "act_123", "", noMedia, noAnim)
	if err != nil {
		t.Fatal(err)
	}
	if tool != "video" || args["action"] != "scene" {
		t.Fatalf("unexpected: %s %v", tool, args)
	}
	if args["scene_prompt"] != "a coastline" {
		t.Fatalf("scene_prompt not set: %v", args)
	}
	if args["actor_id"] != "act_123" {
		t.Fatalf("actor_id not threaded: %v", args)
	}
}

func TestBuildGenerateArgs_UnknownType(t *testing.T) {
	if _, _, err := buildGenerateArgs("hologram", "", "x", "", "", "", "", "", noMedia, noAnim); err == nil {
		t.Fatal("expected error for unknown type")
	}
}

// --- pipeline (input-media) actions ---

// TestBuildGenerateArgs_VideoLipsync verifies lipsync wires --video-url and
// --audio-url to the worker's video_url/audio_url args (no prompt needed).
func TestBuildGenerateArgs_VideoLipsync(t *testing.T) {
	media := mediaInputs{videoURL: "https://cdn.framehood.ai/a.mp4", audioURL: "https://cdn.framehood.ai/v.mp3"}
	tool, args, err := buildGenerateArgs("video", "lipsync", "", "", "", "", "", "", media, noAnim)
	if err != nil {
		t.Fatal(err)
	}
	if tool != "video" || args["action"] != "lipsync" {
		t.Fatalf("unexpected tool/action: %s %v", tool, args["action"])
	}
	if args["video_url"] != media.videoURL || args["audio_url"] != media.audioURL {
		t.Fatalf("lipsync did not thread video_url/audio_url: %v", args)
	}
}

// TestBuildGenerateArgs_VideoLipsyncMissingInput requires both media URLs.
func TestBuildGenerateArgs_VideoLipsyncMissingInput(t *testing.T) {
	media := mediaInputs{videoURL: "https://cdn.framehood.ai/a.mp4"} // no audio
	if _, _, err := buildGenerateArgs("video", "lipsync", "", "", "", "", "", "", media, noAnim); err == nil {
		t.Fatal("expected error when --audio-url is missing for lipsync")
	}
}

// TestBuildGenerateArgs_VideoAssemble verifies assemble maps --clips to a JSON
// array of URLs and threads the optional --audio-url.
func TestBuildGenerateArgs_VideoAssemble(t *testing.T) {
	media := mediaInputs{clips: []string{"https://cdn.framehood.ai/1.mp4", "https://cdn.framehood.ai/2.mp4"}, audioURL: "https://cdn.framehood.ai/vo.mp3"}
	_, args, err := buildGenerateArgs("video", "assemble", "", "", "", "", "", "", media, noAnim)
	if err != nil {
		t.Fatal(err)
	}
	clips, ok := args["clips"].([]any)
	if !ok || len(clips) != 2 || clips[0] != media.clips[0] {
		t.Fatalf("clips not threaded as []any of URLs: %v", args["clips"])
	}
	if args["audio_url"] != media.audioURL {
		t.Fatalf("assemble did not thread audio_url: %v", args)
	}
}

// TestBuildGenerateArgs_VideoAssembleMissingClips requires at least one clip.
func TestBuildGenerateArgs_VideoAssembleMissingClips(t *testing.T) {
	if _, _, err := buildGenerateArgs("video", "assemble", "", "", "", "", "", "", noMedia, noAnim); err == nil {
		t.Fatal("expected error when --clips is missing for assemble")
	}
}

// TestBuildGenerateArgs_VideoMixAudio verifies mix_audio threads video_url and
// the --tracks array.
func TestBuildGenerateArgs_VideoMixAudio(t *testing.T) {
	media := mediaInputs{videoURL: "https://cdn.framehood.ai/clip.mp4", tracks: []string{"https://cdn.framehood.ai/vo.mp3"}}
	_, args, err := buildGenerateArgs("video", "mix_audio", "", "", "", "", "", "", media, noAnim)
	if err != nil {
		t.Fatal(err)
	}
	if args["video_url"] != media.videoURL {
		t.Fatalf("mix_audio did not thread video_url: %v", args)
	}
	tracks, ok := args["tracks"].([]any)
	if !ok || len(tracks) != 1 {
		t.Fatalf("tracks not threaded as []any: %v", args["tracks"])
	}
}

// TestBuildGenerateArgs_ImageEdit verifies image edit maps the first --image-url
// to image_url and requires both image and prompt.
func TestBuildGenerateArgs_ImageEdit(t *testing.T) {
	media := mediaInputs{imageURLs: []string{"https://cdn.framehood.ai/src.jpg"}}
	_, args, err := buildGenerateArgs("image", "edit", "make it night", "", "", "", "", "", media, noAnim)
	if err != nil {
		t.Fatal(err)
	}
	if args["image_url"] != media.imageURLs[0] || args["prompt"] != "make it night" {
		t.Fatalf("image edit did not thread image_url/prompt: %v", args)
	}
	if _, _, err := buildGenerateArgs("image", "edit", "make it night", "", "", "", "", "", noMedia, noAnim); err == nil {
		t.Fatal("expected error when --image-url is missing for image edit")
	}
}

// TestBuildGenerateArgs_VideoEditRefArray verifies edit_ref maps multiple
// --image-url values to the reference_images array.
func TestBuildGenerateArgs_VideoEditRefArray(t *testing.T) {
	media := mediaInputs{
		videoURL:  "https://cdn.framehood.ai/in.mp4",
		imageURLs: []string{"https://cdn.framehood.ai/r1.jpg", "https://cdn.framehood.ai/r2.jpg"},
	}
	_, args, err := buildGenerateArgs("video", "edit_ref", "restyle as @Image1", "", "", "", "", "", media, noAnim)
	if err != nil {
		t.Fatal(err)
	}
	refs, ok := args["reference_images"].([]any)
	if !ok || len(refs) != 2 {
		t.Fatalf("reference_images not threaded as []any: %v", args["reference_images"])
	}
}

// TestBuildGenerateArgs_RejectsBlankInputs verifies a present-but-empty input is
// rejected exactly like a missing one — a whitespace-only --video-url and an
// all-blank --clips must not satisfy the required-input checks.
func TestBuildGenerateArgs_RejectsBlankInputs(t *testing.T) {
	// lipsync with a blank video URL → still "missing", must error.
	blank := mediaInputs{videoURL: "   ", audioURL: "https://cdn.framehood.ai/v.mp3"}
	if _, _, err := buildGenerateArgs("video", "lipsync", "", "", "", "", "", "", blank, noAnim); err == nil {
		t.Fatal("expected error for a whitespace-only --video-url")
	}
	// assemble with only blank/empty clip entries → no usable clips, must error.
	blankClips := mediaInputs{clips: []string{"", "   "}}
	if _, _, err := buildGenerateArgs("video", "assemble", "", "", "", "", "", "", blankClips, noAnim); err == nil {
		t.Fatal("expected error when every --clips entry is blank")
	}
}

// TestBuildGenerateArgs_TrimsInputs verifies surrounding whitespace is stripped
// from URLs before they reach the tool args.
func TestBuildGenerateArgs_TrimsInputs(t *testing.T) {
	media := mediaInputs{
		videoURL: "  https://cdn.framehood.ai/in.mp4  ",
		audioURL: "\thttps://cdn.framehood.ai/v.mp3\n",
	}
	_, args, err := buildGenerateArgs("video", "lipsync", "", "", "", "", "", "", media, noAnim)
	if err != nil {
		t.Fatal(err)
	}
	if args["video_url"] != "https://cdn.framehood.ai/in.mp4" || args["audio_url"] != "https://cdn.framehood.ai/v.mp3" {
		t.Fatalf("inputs were not trimmed: %v", args)
	}
}

// --- kling multi_prompt / shot_type (image animate) ---

// animateMedia is a source image for the animate-path tests (image_url present so
// the actor branch isn't taken).
var animateMedia = mediaInputs{imageURLs: []string{"https://cdn.framehood.ai/frame.jpg"}}

// TestBuildImageArgs_MultiPrompt verifies --shot/--multi-prompt builds the
// multi_prompt array of {prompt,duration} (durations serialized as strings) and
// threads --shot-type, with no top-level prompt.
func TestBuildImageArgs_MultiPrompt(t *testing.T) {
	anim := animateInputs{shots: []string{"wide establishing shot@3s", "push in on the face@4"}, shotType: "customize"}
	_, args, err := buildGenerateArgs("image", "animate", "", "", "", "", "", "", animateMedia, anim)
	if err != nil {
		t.Fatal(err)
	}
	if _, hasPrompt := args["prompt"]; hasPrompt {
		t.Fatalf("multi_prompt must not also set a top-level prompt: %v", args)
	}
	shots, ok := args["multi_prompt"].([]any)
	if !ok || len(shots) != 2 {
		t.Fatalf("multi_prompt not a 2-element array: %v", args["multi_prompt"])
	}
	s0 := shots[0].(map[string]any)
	if s0["prompt"] != "wide establishing shot" || s0["duration"] != "3" {
		t.Fatalf("shot 0 wrong (duration must be a string): %v", s0)
	}
	s1 := shots[1].(map[string]any)
	if s1["prompt"] != "push in on the face" || s1["duration"] != "4" {
		t.Fatalf("shot 1 wrong: %v", s1)
	}
	if args["shot_type"] != "customize" {
		t.Fatalf("shot_type not threaded: %v", args)
	}
}

// TestBuildImageArgs_MultiPromptDefaultDuration verifies a shot with no duration
// suffix omits `duration` (the worker applies its 5s default) and still counts
// toward the total-duration cap.
func TestBuildImageArgs_MultiPromptDefaultDuration(t *testing.T) {
	anim := animateInputs{shots: []string{"a calm dolly shot"}}
	_, args, err := buildGenerateArgs("image", "animate", "", "", "", "", "", "", animateMedia, anim)
	if err != nil {
		t.Fatal(err)
	}
	shots := args["multi_prompt"].([]any)
	s0 := shots[0].(map[string]any)
	if s0["prompt"] != "a calm dolly shot" {
		t.Fatalf("prompt not preserved: %v", s0)
	}
	if _, has := s0["duration"]; has {
		t.Fatalf("duration should be omitted when not given: %v", s0)
	}
}

// TestBuildImageArgs_MultiPromptKeepsAtInPrompt verifies a prompt containing '@'
// that is not followed by a number is preserved whole (split only on a numeric
// suffix).
func TestBuildImageArgs_MultiPromptKeepsAtInPrompt(t *testing.T) {
	anim := animateInputs{shots: []string{"email me at a@b later"}}
	_, args, err := buildGenerateArgs("image", "animate", "", "", "", "", "", "", animateMedia, anim)
	if err != nil {
		t.Fatal(err)
	}
	s0 := args["multi_prompt"].([]any)[0].(map[string]any)
	if s0["prompt"] != "email me at a@b later" {
		t.Fatalf("non-numeric '@' suffix should stay in the prompt: %v", s0)
	}
	if _, has := s0["duration"]; has {
		t.Fatalf("no duration expected: %v", s0)
	}
}

// TestBuildImageArgs_MultiPromptMutualExclusion rejects a prompt + --shot combo.
func TestBuildImageArgs_MultiPromptMutualExclusion(t *testing.T) {
	anim := animateInputs{shots: []string{"a shot@3"}}
	if _, _, err := buildGenerateArgs("image", "animate", "a single prompt", "", "", "", "", "", animateMedia, anim); err == nil {
		t.Fatal("expected error when both a prompt and --shot are set")
	}
}

// TestBuildImageArgs_MultiPromptCaps exercises each documented cap.
func TestBuildImageArgs_MultiPromptCaps(t *testing.T) {
	cases := []struct {
		name  string
		shots []string
	}{
		{"too many shots", []string{"a@1", "b@1", "c@1", "d@1", "e@1", "f@1", "g@1"}},
		{"total over 15s", []string{"a@8", "b@8"}},
		{"shot over 15s", []string{"a@16"}},
		{"shot under 1s", []string{"a@0"}},
		{"fractional duration", []string{"a@2.5"}},
		{"empty prompt", []string{"@3"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			anim := animateInputs{shots: tc.shots}
			if _, _, err := buildGenerateArgs("image", "animate", "", "", "", "", "", "", animateMedia, anim); err == nil {
				t.Fatalf("expected error for %s", tc.name)
			}
		})
	}
}

// TestBuildImageArgs_MultiPromptBoundary accepts the max — 6 shots summing to 15s.
func TestBuildImageArgs_MultiPromptBoundary(t *testing.T) {
	anim := animateInputs{shots: []string{"a@3", "b@3", "c@3", "d@2", "e@2", "f@2"}}
	if _, _, err := buildGenerateArgs("image", "animate", "", "", "", "", "", "", animateMedia, anim); err != nil {
		t.Fatalf("6 shots totaling 15s should be accepted: %v", err)
	}
}

// TestBuildImageArgs_ShotTypeInvalid rejects an unknown --shot-type.
func TestBuildImageArgs_ShotTypeInvalid(t *testing.T) {
	anim := animateInputs{shots: []string{"a@3"}, shotType: "freeform"}
	if _, _, err := buildGenerateArgs("image", "animate", "", "", "", "", "", "", animateMedia, anim); err == nil {
		t.Fatal("expected error for an unknown --shot-type")
	}
}

// TestBuildImageArgs_MultiPromptOnlyOnAnimate rejects --shot on a non-animate
// action (e.g. create) so a misplaced flag fails clearly.
func TestBuildImageArgs_MultiPromptOnlyOnAnimate(t *testing.T) {
	anim := animateInputs{shots: []string{"a@3"}}
	if _, _, err := buildGenerateArgs("image", "create", "a fox", "", "", "", "", "", noMedia, anim); err == nil {
		t.Fatal("expected error when --shot is used with a non-animate action")
	}
	st := animateInputs{shotType: "customize"}
	if _, _, err := buildGenerateArgs("image", "create", "a fox", "", "", "", "", "", noMedia, st); err == nil {
		t.Fatal("expected error when --shot-type is used with a non-animate action")
	}
}

// TestBuildImageArgs_ActorForwardsFormat verifies the actor path forwards the
// friendly --format name verbatim (the worker normalizes it via actorImageSize),
// rather than dropping or rejecting it.
func TestBuildImageArgs_ActorForwardsFormat(t *testing.T) {
	// animate with an actor and no image_url (actor supplies the frame); a
	// friendly format must reach the worker untouched.
	_, args, err := buildGenerateArgs("image", "animate", "a wave", "", "", "9:16", "act_123", "", noMedia, noAnim)
	if err != nil {
		t.Fatal(err)
	}
	if args["actor_id"] != "act_123" {
		t.Fatalf("actor_id not threaded: %v", args)
	}
	if args["format"] != "9:16" {
		t.Fatalf("friendly --format dropped on the actor path: %v", args["format"])
	}
}

// TestGenerateCmd_ShotFlagsParse verifies the cobra flags --multi-prompt, --shot
// (its alias) and --shot-type are registered and that both spellings accumulate
// into one slice in invocation order (so an interleaved --shot is not dropped).
func TestGenerateCmd_ShotFlagsParse(t *testing.T) {
	cmd := newGenerateCmd(config.Config{})
	err := cmd.ParseFlags([]string{
		"--type", "image", "--action", "animate",
		"--shot", "wide@3s", "--multi-prompt", "push in@4s", "--shot", "pull out@2s",
		"--shot-type", "intelligent",
	})
	if err != nil {
		t.Fatal(err)
	}
	shots, err := cmd.Flags().GetStringArray("multi-prompt")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"wide@3s", "push in@4s", "pull out@2s"}
	if len(shots) != len(want) {
		t.Fatalf("--shot/--multi-prompt should share one slice in order: got %v, want %v", shots, want)
	}
	for i := range want {
		if shots[i] != want[i] {
			t.Fatalf("shot %d = %q, want %q (full: %v)", i, shots[i], want[i], shots)
		}
	}
	st, _ := cmd.Flags().GetString("shot-type")
	if st != "intelligent" {
		t.Fatalf("--shot-type not parsed: %q", st)
	}
}
