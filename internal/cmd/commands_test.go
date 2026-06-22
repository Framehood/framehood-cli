package cmd

import "testing"

// noMedia is the empty input-media set for the prompt-only generation tests.
var noMedia = mediaInputs{}

func TestBuildGenerateArgs_Image(t *testing.T) {
	tool, args, err := buildGenerateArgs("image", "", "a fox", "", "fine", "square", "", "", noMedia)
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
	tool, args, err := buildGenerateArgs("audio", "", "hello there", "greeting.mp3", "", "", "", "Rachel", noMedia)
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
	tool, args, err := buildGenerateArgs("video", "", "a coastline", "", "", "", "act_123", "", noMedia)
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
	if _, _, err := buildGenerateArgs("hologram", "", "x", "", "", "", "", "", noMedia); err == nil {
		t.Fatal("expected error for unknown type")
	}
}

// --- pipeline (input-media) actions ---

// TestBuildGenerateArgs_VideoLipsync verifies lipsync wires --video-url and
// --audio-url to the worker's video_url/audio_url args (no prompt needed).
func TestBuildGenerateArgs_VideoLipsync(t *testing.T) {
	media := mediaInputs{videoURL: "https://cdn.framehood.ai/a.mp4", audioURL: "https://cdn.framehood.ai/v.mp3"}
	tool, args, err := buildGenerateArgs("video", "lipsync", "", "", "", "", "", "", media)
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
	if _, _, err := buildGenerateArgs("video", "lipsync", "", "", "", "", "", "", media); err == nil {
		t.Fatal("expected error when --audio-url is missing for lipsync")
	}
}

// TestBuildGenerateArgs_VideoAssemble verifies assemble maps --clips to a JSON
// array of URLs and threads the optional --audio-url.
func TestBuildGenerateArgs_VideoAssemble(t *testing.T) {
	media := mediaInputs{clips: []string{"https://cdn.framehood.ai/1.mp4", "https://cdn.framehood.ai/2.mp4"}, audioURL: "https://cdn.framehood.ai/vo.mp3"}
	_, args, err := buildGenerateArgs("video", "assemble", "", "", "", "", "", "", media)
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
	if _, _, err := buildGenerateArgs("video", "assemble", "", "", "", "", "", "", noMedia); err == nil {
		t.Fatal("expected error when --clips is missing for assemble")
	}
}

// TestBuildGenerateArgs_VideoMixAudio verifies mix_audio threads video_url and
// the --tracks array.
func TestBuildGenerateArgs_VideoMixAudio(t *testing.T) {
	media := mediaInputs{videoURL: "https://cdn.framehood.ai/clip.mp4", tracks: []string{"https://cdn.framehood.ai/vo.mp3"}}
	_, args, err := buildGenerateArgs("video", "mix_audio", "", "", "", "", "", "", media)
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
	_, args, err := buildGenerateArgs("image", "edit", "make it night", "", "", "", "", "", media)
	if err != nil {
		t.Fatal(err)
	}
	if args["image_url"] != media.imageURLs[0] || args["prompt"] != "make it night" {
		t.Fatalf("image edit did not thread image_url/prompt: %v", args)
	}
	if _, _, err := buildGenerateArgs("image", "edit", "make it night", "", "", "", "", "", noMedia); err == nil {
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
	_, args, err := buildGenerateArgs("video", "edit_ref", "restyle as @Image1", "", "", "", "", "", media)
	if err != nil {
		t.Fatal(err)
	}
	refs, ok := args["reference_images"].([]any)
	if !ok || len(refs) != 2 {
		t.Fatalf("reference_images not threaded as []any: %v", args["reference_images"])
	}
}
