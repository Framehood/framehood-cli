package tui

import (
	"os"
	"testing"
)

func TestOutputFilename(t *testing.T) {
	cases := map[string]string{
		"https://cdn.framehood.ai/users/u/private/clip.mp4":    "clip.mp4",
		"https://cdn.framehood.ai/a/b/img.jpg?token=xyz&exp=1": "img.jpg",
		"https://x/y/voice.mp3#frag":                           "voice.mp3",
		"https://host/":                                        "framehood_output",
		"":                                                     "framehood_output",
	}
	for in, want := range cases {
		if got := outputFilename(in); got != want {
			t.Errorf("outputFilename(%q) = %q, want %q", in, got, want)
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

	f1, n1, err := createNonColliding("clip.mp4")
	if err != nil || n1 != "clip.mp4" {
		t.Fatalf("first = %q err=%v, want clip.mp4", n1, err)
	}
	f1.Close()
	// second save of the same name must NOT clobber → clip-1.mp4
	f2, n2, err := createNonColliding("clip.mp4")
	if err != nil || n2 != "clip-1.mp4" {
		t.Fatalf("second = %q err=%v, want clip-1.mp4", n2, err)
	}
	f2.Close()
	if _, err := os.Stat("clip.mp4"); err != nil {
		t.Fatal("original clip.mp4 should be untouched")
	}
}
