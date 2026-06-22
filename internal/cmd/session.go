// Package cmd wires the Framehood CLI commands together: it owns the cobra
// command tree, the authenticated session, and the bridge to the MCP client.
package cmd

import (
	"context"
	"fmt"

	"github.com/Framehood/framehood-cli/internal/auth"
	"github.com/Framehood/framehood-cli/internal/config"
	"github.com/Framehood/framehood-cli/internal/mcp"
)

// Session is an authenticated CLI session. It implements mcp.Tokens so the MCP
// client can transparently refresh the access token on a 401.
type Session struct {
	cfg   config.Config
	creds auth.Credentials
}

// NewSession loads stored credentials and proactively refreshes them if the
// access token is expired. Returns auth.ErrNotLoggedIn if there are none.
func NewSession(cfg config.Config) (*Session, error) {
	creds, err := auth.Load(cfg.CredentialsPath())
	if err != nil {
		return nil, err
	}
	s := &Session{cfg: cfg, creds: creds}
	if creds.Expired() && creds.RefreshToken != "" {
		if _, rerr := s.Refresh(context.Background()); rerr != nil {
			// Stale refresh token — surface as not-logged-in.
			return nil, auth.ErrNotLoggedIn
		}
	}
	return s, nil
}

// Access returns the current access token.
func (s *Session) Access() string { return s.creds.AccessToken }

// Email returns the signed-in user's email, if known.
func (s *Session) Email() string { return s.creds.Email }

// Refresh obtains a new access token and persists it.
func (s *Session) Refresh(ctx context.Context) (string, error) {
	updated, err := auth.Refresh(ctx, s.cfg, s.creds)
	if err != nil {
		return "", err
	}
	if err := auth.Save(s.cfg.CredentialsPath(), updated); err != nil {
		return "", fmt.Errorf("save refreshed credentials: %w", err)
	}
	s.creds = updated
	return s.creds.AccessToken, nil
}

// Client returns an MCP client bound to this session.
func (s *Session) Client() *mcp.Client {
	return mcp.New(s.cfg.MCPEndpoint(), s)
}

// studioAuth bridges the interactive studio's `/login` and `/logout` palette
// commands to the same auth flow the `login`/`logout` cobra commands use. It
// implements tui.Authenticator.
type studioAuth struct{ cfg config.Config }

// Login runs the browser OAuth flow, persists the credentials, and returns a
// fresh MCP client + email — mirroring newLoginCmd.
func (a studioAuth) Login(ctx context.Context) (*mcp.Client, string, error) {
	creds, err := auth.Login(ctx, a.cfg, nil)
	if err != nil {
		return nil, "", err
	}
	if err := auth.Save(a.cfg.CredentialsPath(), creds); err != nil {
		return nil, "", err
	}
	// Build the session straight from the just-obtained creds rather than
	// re-loading from disk: a transient load failure must not report
	// "login failed" when the credentials were in fact saved successfully.
	s := &Session{cfg: a.cfg, creds: creds}
	return s.Client(), s.Email(), nil
}

// Logout clears the stored credentials — the same path as newLogoutCmd.
func (a studioAuth) Logout() error {
	return auth.Clear(a.cfg.CredentialsPath())
}
