package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// maxPersistedHistory caps the on-disk generation history so the file stays
// bounded. Only the most-recent entries are kept.
const maxPersistedHistory = 500

// persistedEntry is one generation as stored in history.json. It holds only
// local studio metadata — a generation's type, the prompt/summary, the result
// URL (empty on failure), whether it failed, and when it completed. No tokens
// or credentials ever live here.
type persistedEntry struct {
	Time   time.Time `json:"time"`
	Kind   string    `json:"kind"`
	Prompt string    `json:"prompt"`
	URL    string    `json:"url,omitempty"`
	Failed bool      `json:"failed,omitempty"`
}

// historyFile is the on-disk envelope. A version field lets the schema evolve
// without misreading old files.
type historyFile struct {
	Version int              `json:"version"`
	Entries []persistedEntry `json:"entries"`
}

const historyFileVersion = 1

// loadHistory reads the persisted generation history from path, oldest-first.
// A missing or corrupt/unreadable file is treated as empty history — this is
// local convenience state, never a hard error that should block the studio.
func loadHistory(path string) []persistedEntry {
	if path == "" {
		return nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil // missing or unreadable → empty
	}
	var hf historyFile
	if err := json.Unmarshal(b, &hf); err != nil {
		return nil // corrupt → empty (don't crash the TUI)
	}
	if len(hf.Entries) > maxPersistedHistory {
		hf.Entries = hf.Entries[len(hf.Entries)-maxPersistedHistory:]
	}
	return hf.Entries
}

// saveHistory writes entries (oldest-first) to path atomically: it caps to the
// most-recent maxPersistedHistory, marshals into a sibling temp file with 0600
// perms, then renames over the target. A no-op when path is empty. Errors are
// returned but callers treat persistence as best-effort.
func saveHistory(path string, entries []persistedEntry) error {
	if path == "" {
		return nil
	}
	if len(entries) > maxPersistedHistory {
		entries = entries[len(entries)-maxPersistedHistory:]
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(historyFile{Version: historyFileVersion, Entries: entries}, "", "  ")
	if err != nil {
		return err
	}
	// Temp file in the same dir so the rename is atomic (same filesystem).
	tmp, err := os.CreateTemp(dir, ".history-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}
