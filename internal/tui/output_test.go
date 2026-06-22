package tui

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOutputFilename(t *testing.T) {
	cases := map[string]string{
		"https://cdn.framehood.ai/users/u/private/clip.mp4":    "clip.mp4",
		"https://cdn.framehood.ai/a/b/img.jpg?token=xyz&exp=1": "img.jpg",
		"https://x/y/voice.mp3#frag":                           "voice.mp3",
		"https://host/":                                        "framehood_output",
		"":                                                     "framehood_output",
		// Server-controlled names that must never reach the cwd:
		"https://cdn.framehood.ai/results/.env":    "framehood_output", // dotfile
		"https://cdn.framehood.ai/.npmrc":          "framehood_output", // dotfile
		"https://cdn.framehood.ai/a/C:windows.exe": "framehood_output", // drive/ADS colon
		"https://cdn.framehood.ai/a%5cb.exe":       "framehood_output", // %5c → backslash
	}
	for in, want := range cases {
		if got := outputFilename(in); got != want {
			t.Errorf("outputFilename(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestResultHostAllowed(t *testing.T) {
	for _, h := range []string{"framehood.ai", "cdn.framehood.ai", "CDN.Framehood.AI", "cdn.framehood.ai:443"} {
		if !resultHostAllowed(h) {
			t.Errorf("resultHostAllowed(%q) = false, want true", h)
		}
	}
	for _, h := range []string{"evil.com", "framehood.ai.evil.com", "169.254.169.254", "notframehood.ai", ""} {
		if resultHostAllowed(h) {
			t.Errorf("resultHostAllowed(%q) = true, want false", h)
		}
	}
}

// TestHistorySelection verifies the table rows mirror history newest-first and
// that selectedItem() resolves the cursor to the right item.
func TestHistorySelection(t *testing.T) {
	m := model{hist: newHistoryTable(), width: 78}
	m.history = []historyItem{
		{kind: "image", prompt: "first", url: "https://x/1.jpg"},
		{kind: "video", prompt: "second", url: "https://x/2.mp4"},
	}
	m.rebuildHistory(true)

	// newest (second) is selected by default
	if it, ok := m.selectedItem(); !ok || it.prompt != "second" {
		t.Fatalf("default selection = %+v ok=%v, want 'second'", it, ok)
	}
	// cursor down → the older row
	m.hist.SetCursor(1)
	if it, ok := m.selectedItem(); !ok || it.prompt != "first" {
		t.Fatalf("row 1 = %+v ok=%v, want 'first'", it, ok)
	}
	if len(m.rows) != 2 {
		t.Fatalf("rows len = %d, want 2", len(m.rows))
	}
}

func TestCreateNonColliding(t *testing.T) {
	dir := t.TempDir()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir to temp: %v", err)
	}
	defer func() {
		if err := os.Chdir(cwd); err != nil {
			t.Errorf("restore cwd: %v", err)
		}
	}()

	// dir="" → current working directory (we chdir'd into the temp dir above).
	f1, n1, err := createNonColliding("", "clip.mp4")
	if err != nil || n1 != "clip.mp4" {
		t.Fatalf("first = %q err=%v, want clip.mp4", n1, err)
	}
	f1.Close()
	// second save of the same name must NOT clobber → clip-1.mp4
	f2, n2, err := createNonColliding("", "clip.mp4")
	if err != nil || n2 != "clip-1.mp4" {
		t.Fatalf("second = %q err=%v, want clip-1.mp4", n2, err)
	}
	f2.Close()
	if _, err := os.Stat("clip.mp4"); err != nil {
		t.Fatal("original clip.mp4 should be untouched")
	}
}

// TestCreateNonColliding_RootedInDir verifies the file lands inside the given
// directory (not the cwd) and collisions are still avoided there.
func TestCreateNonColliding_RootedInDir(t *testing.T) {
	dir := t.TempDir()

	f1, n1, err := createNonColliding(dir, "out.png")
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	f1.Close()
	if filepath.Dir(n1) != dir {
		t.Errorf("file landed in %q, want dir %q", filepath.Dir(n1), dir)
	}
	if filepath.Base(n1) != "out.png" {
		t.Errorf("basename = %q, want out.png", filepath.Base(n1))
	}

	f2, n2, err := createNonColliding(dir, "out.png")
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	f2.Close()
	if filepath.Base(n2) != "out-1.png" {
		t.Errorf("collision name = %q, want out-1.png", filepath.Base(n2))
	}
}
