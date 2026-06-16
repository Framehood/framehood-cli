package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/Framehood/framehood-cli/internal/config"
)

// Endpoints captures the OAuth URLs the flow talks to.
type Endpoints struct {
	Authorize string
	Token     string
	Register  string
}

func endpointsFor(cfg config.Config) Endpoints {
	return Endpoints{
		Authorize: cfg.AuthorizeURL(),
		Token:     cfg.TokenURL(),
		Register:  cfg.RegisterURL(),
	}
}

// Login runs the full browser OAuth 2.1 + PKCE loopback flow and returns the
// resulting credentials. It:
//
//  1. Binds a loopback listener on an ephemeral port.
//  2. Dynamically registers a public client (DCR) with that exact redirect URI.
//  3. Opens the system browser to the authorize endpoint.
//  4. Waits for the redirect carrying the authorization code.
//  5. Exchanges the code (+ PKCE verifier) for tokens.
//
// openBrowser is injectable so tests can stub it; pass nil for the default.
func Login(ctx context.Context, cfg config.Config, openBrowser func(string) error) (Credentials, error) {
	if openBrowser == nil {
		openBrowser = OpenBrowser
	}
	ep := endpointsFor(cfg)

	// 1. Loopback listener on an ephemeral port.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return Credentials{}, fmt.Errorf("bind loopback: %w", err)
	}
	defer ln.Close()
	redirectURI := fmt.Sprintf("http://127.0.0.1:%d/callback", ln.Addr().(*net.TCPAddr).Port)

	// 2. Dynamic client registration for this exact redirect URI.
	clientID, err := registerClient(ctx, ep.Register, redirectURI)
	if err != nil {
		return Credentials{}, fmt.Errorf("register client: %w", err)
	}

	pk, err := newPKCE()
	if err != nil {
		return Credentials{}, err
	}
	state, err := randomState()
	if err != nil {
		return Credentials{}, err
	}

	// 3. Build the authorize URL and open it.
	authURL := buildAuthorizeURL(ep.Authorize, clientID, redirectURI, pk.Challenge, state)

	// 4. Serve the callback and wait for the code.
	codeCh := make(chan callbackResult, 1)
	srv := &http.Server{Handler: callbackHandler(state, codeCh)}
	go srv.Serve(ln) //nolint:errcheck // Serve returns on Shutdown
	defer srv.Shutdown(context.Background())

	if err := openBrowser(authURL); err != nil {
		// Non-fatal: the user can copy the URL manually.
		fmt.Printf("Open this URL in your browser to sign in:\n\n  %s\n\n", authURL)
	}

	var res callbackResult
	select {
	case res = <-codeCh:
	case <-ctx.Done():
		return Credentials{}, ctx.Err()
	case <-time.After(5 * time.Minute):
		return Credentials{}, fmt.Errorf("timed out waiting for browser login")
	}
	if res.err != "" {
		return Credentials{}, fmt.Errorf("authorization failed: %s", res.err)
	}

	// 5. Exchange the authorization code for tokens.
	creds, err := exchangeCode(ctx, ep.Token, clientID, redirectURI, res.code, pk.Verifier)
	if err != nil {
		return Credentials{}, err
	}
	creds.ClientID = clientID
	return creds, nil
}

// Refresh exchanges a refresh token for a fresh access token. Returns the
// updated credentials (preserving the refresh token if the server omits a new
// one).
func Refresh(ctx context.Context, cfg config.Config, c Credentials) (Credentials, error) {
	if c.RefreshToken == "" || c.ClientID == "" {
		return Credentials{}, ErrNotLoggedIn
	}
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {c.RefreshToken},
		"client_id":     {c.ClientID},
	}
	tok, err := postToken(ctx, cfg.TokenURL(), form)
	if err != nil {
		return Credentials{}, err
	}
	out := tokenToCreds(tok)
	if out.RefreshToken == "" {
		out.RefreshToken = c.RefreshToken
	}
	out.ClientID = c.ClientID
	if out.Email == "" {
		out.Email = c.Email
	}
	return out, nil
}

// --- internals ---

type callbackResult struct {
	code string
	err  string
}

func callbackHandler(wantState string, ch chan<- callbackResult) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if e := q.Get("error"); e != "" {
			ch <- callbackResult{err: e}
			writeClosePage(w, "Sign-in failed", "You can close this tab and return to the terminal.")
			return
		}
		if q.Get("state") != wantState {
			ch <- callbackResult{err: "state mismatch (possible CSRF)"}
			writeClosePage(w, "Sign-in failed", "State mismatch. Please try again.")
			return
		}
		code := q.Get("code")
		if code == "" {
			ch <- callbackResult{err: "no authorization code in callback"}
			writeClosePage(w, "Sign-in failed", "No code returned.")
			return
		}
		ch <- callbackResult{code: code}
		writeClosePage(w, "Signed in to Framehood", "You can close this tab and return to the terminal.")
	})
	return mux
}

func writeClosePage(w http.ResponseWriter, title, msg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<!doctype html><html><head><meta charset="utf-8"><title>%s</title>
<style>body{font-family:system-ui,sans-serif;background:#0a0a0a;color:#e8e8e8;display:flex;
align-items:center;justify-content:center;height:100vh;margin:0}.c{text-align:center}
h1{font-weight:600;font-size:1.25rem}p{color:#9a9a9a}</style></head>
<body><div class="c"><h1>%s</h1><p>%s</p></div></body></html>`, title, title, msg)
}

func buildAuthorizeURL(base, clientID, redirectURI, challenge, state string) string {
	q := url.Values{
		"response_type":         {"code"},
		"client_id":             {clientID},
		"redirect_uri":          {redirectURI},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
		"state":                 {state},
		"scope":                 {"mcp:tools"},
	}
	return base + "?" + q.Encode()
}

// registerClient performs OAuth 2.0 Dynamic Client Registration (RFC 7591) for
// a public native client using PKCE (token_endpoint_auth_method=none).
func registerClient(ctx context.Context, registerURL, redirectURI string) (string, error) {
	body, _ := json.Marshal(map[string]any{
		"client_name":                "Framehood CLI",
		"redirect_uris":              []string{redirectURI},
		"grant_types":                []string{"authorization_code", "refresh_token"},
		"response_types":             []string{"code"},
		"token_endpoint_auth_method": "none",
		"scope":                      "mcp:tools",
	})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, registerURL, strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var out struct {
		ClientID string `json:"client_id"`
		Error    string `json:"error"`
		Desc     string `json:"error_description"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	if out.ClientID == "" {
		return "", fmt.Errorf("registration returned no client_id (%s %s)", out.Error, out.Desc)
	}
	return out.ClientID, nil
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	Scope        string `json:"scope"`
	Error        string `json:"error"`
	Desc         string `json:"error_description"`
}

func exchangeCode(ctx context.Context, tokenURL, clientID, redirectURI, code, verifier string) (Credentials, error) {
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {redirectURI},
		"client_id":     {clientID},
		"code_verifier": {verifier},
	}
	tok, err := postToken(ctx, tokenURL, form)
	if err != nil {
		return Credentials{}, err
	}
	return tokenToCreds(tok), nil
}

func postToken(ctx context.Context, tokenURL string, form url.Values) (tokenResponse, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return tokenResponse{}, err
	}
	defer resp.Body.Close()
	var tok tokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tok); err != nil {
		return tokenResponse{}, err
	}
	if tok.Error != "" {
		return tokenResponse{}, fmt.Errorf("token endpoint: %s %s", tok.Error, tok.Desc)
	}
	if tok.AccessToken == "" {
		return tokenResponse{}, fmt.Errorf("token endpoint returned no access_token")
	}
	return tok, nil
}

func tokenToCreds(tok tokenResponse) Credentials {
	c := Credentials{
		AccessToken:  tok.AccessToken,
		RefreshToken: tok.RefreshToken,
		TokenType:    tok.TokenType,
	}
	if tok.ExpiresIn > 0 {
		c.Expiry = time.Now().Add(time.Duration(tok.ExpiresIn) * time.Second)
	}
	return c
}

// OpenBrowser opens url in the platform's default browser.
func OpenBrowser(target string) error {
	var cmd string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		cmd = "open"
		args = []string{target}
	case "windows":
		cmd = "rundll32"
		args = []string{"url.dll,FileProtocolHandler", target}
	default: // linux, bsd
		cmd = "xdg-open"
		args = []string{target}
	}
	return exec.Command(cmd, args...).Start()
}
