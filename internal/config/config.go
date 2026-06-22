// Package config holds runtime configuration for the Framehood CLI: service
// endpoints and the on-disk location of stored credentials.
//
// All endpoints default to the production Framehood deployment but can be
// overridden via environment variables, which is handy for local development
// against a `wrangler dev` worker.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Config resolves the endpoints and paths the CLI uses.
type Config struct {
	// MCPBase is the origin of the MCP server. It hosts both the OAuth
	// endpoints (/authorize, /token, /register, /.well-known/...) and the
	// JSON-RPC endpoint (/mcp). The OAuth issuer equals this origin.
	MCPBase string

	// ConfigDir is the directory holding credentials and CLI state
	// (default: ~/.framehood).
	ConfigDir string
}

// Load builds a Config from environment overrides with production defaults.
func Load() (Config, error) {
	c := Config{
		MCPBase: envOr("FRAMEHOOD_MCP_BASE", "https://mcp.framehood.ai"),
	}

	dir := os.Getenv("FRAMEHOOD_CONFIG_DIR")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return Config{}, err
		}
		dir = filepath.Join(home, ".framehood")
	}
	c.ConfigDir = dir
	return c, nil
}

// CredentialsPath is the file storing the OAuth token set.
func (c Config) CredentialsPath() string {
	return filepath.Join(c.ConfigDir, "credentials.json")
}

// HistoryPath is the file storing the local studio generation history
// (type/prompt/url/timestamp only — no tokens). It lives alongside the
// credentials in the config dir.
func (c Config) HistoryPath() string {
	return filepath.Join(c.ConfigDir, "history.json")
}

// SettingsPath is the file storing user preferences (output dir, …). It is a
// small JSON, separate from credentials.json, written with 0600 perms.
func (c Config) SettingsPath() string {
	return filepath.Join(c.ConfigDir, "config.json")
}

// settings is the on-disk preferences envelope. It holds no secrets.
type settings struct {
	OutputDir string `json:"output_dir,omitempty"`
}

// loadSettings reads the preferences file. A missing or corrupt file yields
// zero-value settings (never an error) so a bad file can't block the CLI.
func (c Config) loadSettings() settings {
	b, err := os.ReadFile(c.SettingsPath())
	if err != nil {
		return settings{}
	}
	var s settings
	if err := json.Unmarshal(b, &s); err != nil {
		return settings{}
	}
	return s
}

// saveSettings writes the preferences file atomically (temp + rename) with
// 0600 perms, creating the config dir if needed.
func (c Config) saveSettings(s settings) error {
	dir := c.ConfigDir
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".config-*.tmp")
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
	if err := os.Rename(tmpName, c.SettingsPath()); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}

// OutputDir returns the configured output directory for saved results, or ""
// when unset (callers then fall back to the current working directory). The
// stored value is returned as-is (already absolute when set via SetOutputDir).
func (c Config) OutputDir() string {
	return c.loadSettings().OutputDir
}

// SetOutputDir validates path (expanding a leading ~), creates the directory if
// it doesn't exist, rejects a path that exists but isn't a directory, and
// persists the resulting absolute path. Passing "" clears the setting (reverts
// to the current working directory). Returns the stored absolute path ("" when
// cleared).
func (c Config) SetOutputDir(path string) (string, error) {
	if path == "" {
		s := c.loadSettings()
		s.OutputDir = ""
		if err := c.saveSettings(s); err != nil {
			return "", err
		}
		return "", nil
	}
	abs, err := EnsureDir(path)
	if err != nil {
		return "", err
	}
	s := c.loadSettings()
	s.OutputDir = abs
	if err := c.saveSettings(s); err != nil {
		return "", err
	}
	return abs, nil
}

// ExpandHome expands a leading "~" or "~/" to the user's home directory.
// Other paths are returned unchanged. "~user" forms are not expanded.
func ExpandHome(path string) (string, error) {
	if path == "~" || path == "~/" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return home, nil
	}
	if len(path) >= 2 && path[0] == '~' && (path[1] == '/' || path[1] == os.PathSeparator) {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, path[2:]), nil
	}
	return path, nil
}

// EnsureDir resolves path to an absolute directory: it expands a leading ~,
// makes it absolute, creates it (0700) if missing, and rejects a path that
// exists but is not a directory. Returns the absolute path.
func EnsureDir(path string) (string, error) {
	expanded, err := ExpandHome(path)
	if err != nil {
		return "", err
	}
	abs, err := filepath.Abs(expanded)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(abs)
	switch {
	case err == nil:
		if !info.IsDir() {
			return "", fmt.Errorf("%q exists but is not a directory", abs)
		}
	case errors.Is(err, os.ErrNotExist):
		if err := os.MkdirAll(abs, 0o700); err != nil {
			return "", err
		}
	default:
		return "", err
	}
	return abs, nil
}

// MCPEndpoint is the JSON-RPC endpoint URL.
func (c Config) MCPEndpoint() string { return c.MCPBase + "/mcp" }

// AuthorizeURL, TokenURL, RegisterURL are the OAuth 2.1 endpoints.
func (c Config) AuthorizeURL() string { return c.MCPBase + "/authorize" }
func (c Config) TokenURL() string     { return c.MCPBase + "/token" }
func (c Config) RegisterURL() string  { return c.MCPBase + "/register" }

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
