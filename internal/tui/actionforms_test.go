package tui

import (
	"reflect"
	"testing"
)

// TestNewlyFormedActions_OpenFormWithRequiredFields verifies that each action
// that used to fail-safe ("not yet available") now opens a real form whose
// required fields match the worker's required args.
func TestNewlyFormedActions_OpenFormWithRequiredFields(t *testing.T) {
	cases := []struct {
		tool, action string
		wantRequired []string // required field names, in order
	}{
		{"image", "actor_sheet", []string{"actor_id"}},
		{"video", "edit_ref", []string{"video_url", "prompt", "reference_images"}},
		{"video", "scene", []string{"actor_id", "scene_prompt"}},
		{"actor", "create", []string{"name", "images_data_url"}},
		{"actor", "update", []string{"actor_id"}},
		{"actor", "delete", []string{"actor_id"}},
		{"actor", "batch", []string{"actor_id", "prompts"}},
		{"qa", "scene", []string{"video", "plan"}},
	}
	for _, c := range cases {
		c := c
		t.Run(c.tool+"."+c.action, func(t *testing.T) {
			spec := findAction(t, c.tool, c.action)
			if !spec.hasForm() {
				t.Fatalf("%s·%s should now have a form", c.tool, c.action)
			}
			// startForm must open the form (no "not yet available" fallback).
			m := newTestModel().startForm(spec)
			if len(m.formFields) == 0 {
				t.Fatalf("%s·%s: startForm opened no form (notice=%q)", c.tool, c.action, m.notice)
			}
			var gotReq []string
			for _, f := range m.formFields {
				if f.required {
					gotReq = append(gotReq, f.name)
				}
			}
			if !reflect.DeepEqual(gotReq, c.wantRequired) {
				t.Errorf("%s·%s required fields = %v, want %v", c.tool, c.action, gotReq, c.wantRequired)
			}
		})
	}
}

// TestNewlyFormedActions_BuildCorrectArgs fills each form and asserts the exact
// tool + args the worker expects (key names are load-bearing — they're sent
// verbatim).
func TestNewlyFormedActions_BuildCorrectArgs(t *testing.T) {
	cases := []struct {
		tool, action string
		vals         map[string]string
		wantTool     string
		wantArgs     map[string]any
	}{
		{
			tool: "image", action: "actor_sheet",
			vals:     map[string]string{"actor_id": "act_123", "out_prefix": "sheets/me", "variations": "4"},
			wantTool: "image",
			wantArgs: map[string]any{"action": "actor_sheet", "actor_id": "act_123", "out_prefix": "sheets/me", "variations": 4, "out": "sheet.jpg"},
		},
		{
			tool: "video", action: "edit_ref",
			vals:     map[string]string{"video_url": "https://x/v.mp4", "prompt": "make it night", "reference_images": "https://x/a.jpg, https://x/b.jpg"},
			wantTool: "video",
			wantArgs: map[string]any{"action": "edit_ref", "video_url": "https://x/v.mp4", "prompt": "make it night", "reference_images": []string{"https://x/a.jpg", "https://x/b.jpg"}, "out": "video.mp4"},
		},
		{
			tool: "video", action: "scene",
			vals:     map[string]string{"actor_id": "act_9", "scene_prompt": "walking on a beach", "speech_text": "hello world"},
			wantTool: "video",
			wantArgs: map[string]any{"action": "scene", "actor_id": "act_9", "scene_prompt": "walking on a beach", "speech_text": "hello world", "out": "video.mp4"},
		},
		{
			tool: "actor", action: "create",
			vals:     map[string]string{"name": "Nova", "images_data_url": "https://x/refs.zip"},
			wantTool: "actor",
			wantArgs: map[string]any{"action": "create", "name": "Nova", "images_data_url": "https://x/refs.zip"},
		},
		{
			tool: "actor", action: "update",
			vals:     map[string]string{"actor_id": "act_7", "voice_id": "vid_42"},
			wantTool: "actor",
			wantArgs: map[string]any{"action": "update", "actor_id": "act_7", "voice_id": "vid_42"},
		},
		{
			tool: "actor", action: "delete",
			vals:     map[string]string{"actor_id": "act_7"},
			wantTool: "actor",
			wantArgs: map[string]any{"action": "delete", "actor_id": "act_7"},
		},
		{
			tool: "actor", action: "batch",
			vals:     map[string]string{"actor_id": "act_7", "prompts": "a, b, c", "kind": "video"},
			wantTool: "actor",
			wantArgs: map[string]any{"action": "batch", "actor_id": "act_7", "prompts": []string{"a", "b", "c"}, "kind": "video"},
		},
		{
			tool: "qa", action: "scene",
			vals:     map[string]string{"video": "https://x/v.mp4", "plan": `{"shots":3}`},
			wantTool: "qa",
			wantArgs: map[string]any{"action": "scene", "video": "https://x/v.mp4", "plan": map[string]any{"shots": float64(3)}},
		},
	}
	for _, c := range cases {
		c := c
		t.Run(c.tool+"."+c.action, func(t *testing.T) {
			tool, args := argsForForm(findAction(t, c.tool, c.action), c.vals)
			if tool != c.wantTool {
				t.Errorf("tool = %q, want %q", tool, c.wantTool)
			}
			if !reflect.DeepEqual(args, c.wantArgs) {
				t.Errorf("args = %#v\n want %#v", args, c.wantArgs)
			}
		})
	}
}

// TestQaScenePlan_NonJSONWrapped checks the plan field wraps a plain string into
// an object (the worker requires plan to be an object).
func TestQaScenePlan_NonJSONWrapped(t *testing.T) {
	_, args := argsForForm(findAction(t, "qa", "scene"),
		map[string]string{"video": "https://x/v.mp4", "plan": "a calm street scene"})
	plan, ok := args["plan"].(map[string]any)
	if !ok {
		t.Fatalf("plan = %#v, want a map", args["plan"])
	}
	if plan["text"] != "a calm street scene" {
		t.Errorf("plan = %#v, want {text: …}", plan)
	}
}

// TestNewlyFormedActions_RequiredFieldValidationBlocksEmpty verifies the form's
// required-field validation still blocks a submit with empties.
func TestNewlyFormedActions_RequiredFieldValidationBlocksEmpty(t *testing.T) {
	// video·scene needs actor_id + scene_prompt; leave scene_prompt empty.
	m := newTestModel().startForm(findAction(t, "video", "scene"))
	m.formVals = map[string]string{"actor_id": "act_1"} // scene_prompt missing
	if miss := m.formMissing(); miss == "" {
		t.Error("missing scene_prompt should be flagged")
	}
	nm, cmd := m.submitForm()
	if nm.(model).phase == phaseWorking || cmd != nil {
		t.Error("submit must be blocked while a required field is empty")
	}

	// actor·batch needs a non-empty prompts list.
	mb := newTestModel().startForm(findAction(t, "actor", "batch"))
	mb.formVals = map[string]string{"actor_id": "act_1", "prompts": " , ,"}
	if mb.formMissing() == "" {
		t.Error("comma-only prompts must be flagged as missing")
	}
}

// TestReadFormActions_RouteThroughImmediate verifies that a kindRead form action
// (library·list) submits via the immediate (running) path — NOT the Job-decode
// path that would freeze on a data payload.
func TestReadFormActions_RouteThroughImmediate(t *testing.T) {
	m := newTestModel()
	m.action = findAction(t, "library", "list")
	m = m.startForm(m.action)
	m.formVals = map[string]string{"query": "red fox"}
	nm, cmd := m.submitForm()
	got := nm.(model)
	if got.phase != phaseWorking || got.status != "running" {
		t.Errorf("library·list should run (phaseWorking/running), got %v/%q", got.phase, got.status)
	}
	if got.jobID != "" {
		t.Errorf("a read action must not set a jobID, got %q", got.jobID)
	}
	if cmd == nil {
		t.Error("expected an immediate command")
	}
}

// TestProjectLibrary_InPaletteNotInRing confirms the console tools are reachable
// from the palette but never cycle in the Shift+Tab work ring.
func TestProjectLibrary_InPaletteNotInRing(t *testing.T) {
	for _, a := range workActions {
		if a.tool == "project" || a.tool == "library" {
			t.Errorf("%s·%s must not be in the work ring (palette-only)", a.tool, a.action)
		}
	}
	// They must still appear in the palette command list.
	want := map[string]bool{
		"library·list": false, "library·trashed": false, "library·trash": false, "library·restore": false,
		"project·list": false, "project·current": false, "project·create": false,
		"project·use": false, "project·assign": false, "project·delete": false,
	}
	for _, c := range allPaletteCmds {
		if _, ok := want[c.id]; ok {
			want[c.id] = true
		}
	}
	for id, found := range want {
		if !found {
			t.Errorf("%s missing from the palette command list", id)
		}
	}
}
