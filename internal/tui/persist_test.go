package tui

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestHistory_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "history.json")

	now := time.Now().Truncate(time.Second)
	in := []persistedEntry{
		{Time: now.Add(-2 * time.Minute), Kind: "image·create", Prompt: "a fox", URL: "https://cdn.framehood.ai/1.jpg"},
		{Time: now.Add(-1 * time.Minute), Kind: "video·create", Prompt: "a wave", Failed: true},
		{Time: now, Kind: "audio·speak", Prompt: "hello", URL: "https://cdn.framehood.ai/3.mp3"},
	}
	if err := saveHistory(path, in); err != nil {
		t.Fatalf("saveHistory: %v", err)
	}

	got := loadHistory(path)
	if len(got) != len(in) {
		t.Fatalf("loaded %d entries, want %d", len(got), len(in))
	}
	for i := range in {
		if got[i].Kind != in[i].Kind || got[i].Prompt != in[i].Prompt ||
			got[i].URL != in[i].URL || got[i].Failed != in[i].Failed {
			t.Errorf("entry %d = %+v, want %+v", i, got[i], in[i])
		}
		if !got[i].Time.Equal(in[i].Time) {
			t.Errorf("entry %d time = %v, want %v", i, got[i].Time, in[i].Time)
		}
	}
}

func TestHistory_FilePermsAndAtomicReplace(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "history.json")

	if err := saveHistory(path, []persistedEntry{{Kind: "image·create", Prompt: "x"}}); err != nil {
		t.Fatalf("first save: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("history file perms = %o, want 0600", perm)
	}

	// Overwrite (rename-replace) must succeed and leave a single file.
	if err := saveHistory(path, []persistedEntry{{Kind: "video·create", Prompt: "y"}}); err != nil {
		t.Fatalf("second save: %v", err)
	}
	got := loadHistory(path)
	if len(got) != 1 || got[0].Kind != "video·create" {
		t.Errorf("after overwrite: %+v, want the second entry only", got)
	}
	// No leftover temp files.
	ents, _ := os.ReadDir(dir)
	for _, e := range ents {
		if filepath.Ext(e.Name()) == ".tmp" {
			t.Errorf("leftover temp file: %s", e.Name())
		}
	}
}

func TestHistory_CapOnSaveAndLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "history.json")

	// Save more than the cap; only the most-recent maxPersistedHistory persist.
	in := make([]persistedEntry, maxPersistedHistory+37)
	for i := range in {
		in[i] = persistedEntry{Kind: "image·create", Prompt: itoa(i)}
	}
	if err := saveHistory(path, in); err != nil {
		t.Fatalf("save: %v", err)
	}
	got := loadHistory(path)
	if len(got) != maxPersistedHistory {
		t.Fatalf("loaded %d, want cap %d", len(got), maxPersistedHistory)
	}
	// The newest (last) entry must be preserved; the oldest dropped.
	if got[len(got)-1].Prompt != itoa(len(in)-1) {
		t.Errorf("newest persisted = %q, want %q", got[len(got)-1].Prompt, itoa(len(in)-1))
	}
	if got[0].Prompt != itoa(len(in)-maxPersistedHistory) {
		t.Errorf("oldest persisted = %q, want %q", got[0].Prompt, itoa(len(in)-maxPersistedHistory))
	}
}

func TestHistory_MissingFileLoadsEmpty(t *testing.T) {
	got := loadHistory(filepath.Join(t.TempDir(), "does-not-exist.json"))
	if len(got) != 0 {
		t.Errorf("missing file should load empty, got %d entries", len(got))
	}
}

func TestHistory_CorruptFileLoadsEmpty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "history.json")
	if err := os.WriteFile(path, []byte("{not valid json"), 0o600); err != nil {
		t.Fatal(err)
	}
	got := loadHistory(path) // must NOT panic
	if len(got) != 0 {
		t.Errorf("corrupt file should load empty, got %d entries", len(got))
	}
}

func TestHistory_EmptyPathIsNoop(t *testing.T) {
	if got := loadHistory(""); got != nil {
		t.Errorf("loadHistory(\"\") = %v, want nil", got)
	}
	if err := saveHistory("", []persistedEntry{{Kind: "x"}}); err != nil {
		t.Errorf("saveHistory(\"\") should be a no-op, got %v", err)
	}
}

// TestHistory_RunLoadsAtStartup proves loaded entries surface in the model's
// history (via the same loadHistory path Run uses), newest-last in `history`.
func TestHistory_StartupLoadPopulatesModel(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "history.json")
	in := []persistedEntry{
		{Kind: "image·create", Prompt: "older"},
		{Kind: "video·create", Prompt: "newer", URL: "https://cdn.framehood.ai/v.mp4"},
	}
	if err := saveHistory(path, in); err != nil {
		t.Fatalf("save: %v", err)
	}

	// Mirror what Run() does to seed the model.
	m := newTestModel()
	m.history = nil
	m.histPath = path
	for _, e := range loadHistory(path) {
		m.history = append(m.history, fromPersisted(e))
	}
	m.rebuildHistory(true)

	if len(m.history) != 2 {
		t.Fatalf("model history = %d entries, want 2", len(m.history))
	}
	// Newest-first display: the first row is "newer".
	if it, ok := m.selectedItem(); !ok || it.prompt != "newer" {
		t.Errorf("newest selected = %+v ok=%v, want 'newer'", it, ok)
	}
}
