// Package config holds runtime configuration for the Framehood CLI: service
// endpoints and the on-disk location of stored credentials.
//
// All endpoints default to the production Framehood deployment but can be
// overridden via environment variables, which is handy for local development
// against a `wrangler dev` worker.
package config

import (
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
