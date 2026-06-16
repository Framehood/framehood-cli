// Package auth implements the OAuth 2.1 (PKCE, loopback redirect) browser
// login flow the CLI uses to obtain an MCP access token, plus persistent
// storage and silent refresh of that token.
package auth

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

// Credentials is the persisted OAuth token set.
type Credentials struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	TokenType    string    `json:"token_type,omitempty"`
	Expiry       time.Time `json:"expiry,omitzero"`
	// ClientID is the dynamically-registered (DCR) client used at login; it is
	// required to refresh the token later.
	ClientID string `json:"client_id,omitempty"`
	Email    string `json:"email,omitempty"`
}

// ErrNotLoggedIn is returned when no credentials are stored.
var ErrNotLoggedIn = errors.New("not logged in — run `framehood login`")

// Expired reports whether the access token is expired (with a 60s skew).
func (c Credentials) Expired() bool {
	if c.Expiry.IsZero() {
		return false // no expiry recorded — treat as long-lived
	}
	return time.Now().Add(60 * time.Second).After(c.Expiry)
}

// Load reads credentials from path. Returns ErrNotLoggedIn if absent.
func Load(path string) (Credentials, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Credentials{}, ErrNotLoggedIn
	}
	if err != nil {
		return Credentials{}, err
	}
	var c Credentials
	if err := json.Unmarshal(b, &c); err != nil {
		return Credentials{}, err
	}
	if c.AccessToken == "" {
		return Credentials{}, ErrNotLoggedIn
	}
	return c, nil
}

// Save writes credentials to path with 0600 permissions, creating the parent
// directory if needed.
func Save(path string, c Credentials) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o600)
}

// Clear removes the stored credentials (logout). Missing file is not an error.
func Clear(path string) error {
	err := os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
