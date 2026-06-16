package cmd

import "testing"

func TestBuildGenerateArgs_Image(t *testing.T) {
	tool, args, err := buildGenerateArgs("image", "", "a fox", "", "fine", "square", "", "")
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
	tool, args, err := buildGenerateArgs("audio", "", "hello there", "greeting.mp3", "", "", "", "Rachel")
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
	tool, args, err := buildGenerateArgs("video", "", "a coastline", "", "", "", "act_123", "")
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
	if _, _, err := buildGenerateArgs("hologram", "", "x", "", "", "", "", ""); err == nil {
		t.Fatal("expected error for unknown type")
	}
}
