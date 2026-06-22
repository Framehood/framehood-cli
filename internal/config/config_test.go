package config

import (
	"os"
	"path/filepath"
	"testing"
)

// newTestConfig returns a Config rooted at a temp config dir.
func newTestConfig(t *testing.T) Config {
	t.Helper()
	return Config{MCPBase: "https://mcp.framehood.ai", ConfigDir: t.TempDir()}
}

func TestSetGetOutputDir_RoundTrip(t *testing.T) {
	cfg := newTestConfig(t)

	// Unset by default → "".
	if got := cfg.OutputDir(); got != "" {
		t.Errorf("default OutputDir = %q, want empty", got)
	}

	target := filepath.Join(t.TempDir(), "results")
	abs, err := cfg.SetOutputDir(target)
	if err != nil {
		t.Fatalf("SetOutputDir: %v", err)
	}
	if !filepath.IsAbs(abs) {
		t.Errorf("returned path %q is not absolute", abs)
	}
	// The directory must now exist.
	if info, err := os.Stat(abs); err != nil || !info.IsDir() {
		t.Fatalf("SetOutputDir should create the dir: stat err=%v", err)
	}
	// And it must round-trip through the persisted settings file.
	if got := cfg.OutputDir(); got != abs {
		t.Errorf("OutputDir after set = %q, want %q", got, abs)
	}
	// The settings file is 0600.
	if info, err := os.Stat(cfg.SettingsPath()); err != nil {
		t.Fatalf("stat settings: %v", err)
	} else if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("settings perms = %o, want 0600", perm)
	}
}

func TestSetOutputDir_Clear(t *testing.T) {
	cfg := newTestConfig(t)
	if _, err := cfg.SetOutputDir(filepath.Join(t.TempDir(), "x")); err != nil {
		t.Fatalf("set: %v", err)
	}
	if cfg.OutputDir() == "" {
		t.Fatal("precondition: output dir should be set")
	}
	abs, err := cfg.SetOutputDir("")
	if err != nil || abs != "" {
		t.Fatalf("clear = (%q,%v), want ('',nil)", abs, err)
	}
	if got := cfg.OutputDir(); got != "" {
		t.Errorf("after clear OutputDir = %q, want empty", got)
	}
}

func TestSetOutputDir_RejectsNonDirectory(t *testing.T) {
	cfg := newTestConfig(t)
	// A regular file, not a directory.
	file := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := cfg.SetOutputDir(file); err == nil {
		t.Error("SetOutputDir should reject a path that is a file, not a directory")
	}
	// The setting must remain unset after the rejected attempt.
	if got := cfg.OutputDir(); got != "" {
		t.Errorf("rejected set should not persist: OutputDir = %q", got)
	}
}

func TestEnsureDir_CreatesAndIsAbsolute(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, "a", "b", "c") // nested, missing
	abs, err := EnsureDir(target)
	if err != nil {
		t.Fatalf("EnsureDir: %v", err)
	}
	if !filepath.IsAbs(abs) {
		t.Errorf("EnsureDir returned non-absolute path %q", abs)
	}
	if info, err := os.Stat(abs); err != nil || !info.IsDir() {
		t.Errorf("EnsureDir should create nested dirs: err=%v", err)
	}
}

func TestExpandHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home dir: %v", err)
	}
	cases := map[string]string{
		"~":            home,
		"~/":           home,
		"~/Downloads":  filepath.Join(home, "Downloads"),
		"/abs/path":    "/abs/path",
		"relative/dir": "relative/dir",
		"~user/x":      "~user/x", // ~user form is NOT expanded
	}
	for in, want := range cases {
		got, err := ExpandHome(in)
		if err != nil {
			t.Errorf("ExpandHome(%q): %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("ExpandHome(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSetOutputDir_ExpandsHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home dir: %v", err)
	}
	cfg := newTestConfig(t)
	// Use a unique subdir under home so the test creates/owns it.
	sub := ".framehood-test-outdir-" + filepath.Base(t.TempDir())
	abs, err := cfg.SetOutputDir("~/" + sub)
	if err != nil {
		t.Fatalf("SetOutputDir(~): %v", err)
	}
	defer os.RemoveAll(filepath.Join(home, sub))
	want := filepath.Join(home, sub)
	if abs != want {
		t.Errorf("~ expansion = %q, want %q", abs, want)
	}
}

func TestLoadSettings_CorruptOrMissingIsEmpty(t *testing.T) {
	cfg := newTestConfig(t)
	// Missing → empty.
	if got := cfg.OutputDir(); got != "" {
		t.Errorf("missing settings → OutputDir %q, want empty", got)
	}
	// Corrupt → empty, no panic.
	if err := os.WriteFile(cfg.SettingsPath(), []byte("{bad json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := cfg.OutputDir(); got != "" {
		t.Errorf("corrupt settings → OutputDir %q, want empty", got)
	}
}
