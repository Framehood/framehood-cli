package auth

import (
	"crypto/sha256"
	"encoding/base64"
	"testing"
)

func TestNewPKCE_ChallengeIsS256OfVerifier(t *testing.T) {
	pk, err := newPKCE()
	if err != nil {
		t.Fatalf("newPKCE: %v", err)
	}
	if pk.Verifier == "" || pk.Challenge == "" {
		t.Fatal("empty verifier or challenge")
	}
	sum := sha256.Sum256([]byte(pk.Verifier))
	want := base64.RawURLEncoding.EncodeToString(sum[:])
	if pk.Challenge != want {
		t.Fatalf("challenge mismatch:\n got %s\nwant %s", pk.Challenge, want)
	}
}

func TestNewPKCE_Unique(t *testing.T) {
	a, _ := newPKCE()
	b, _ := newPKCE()
	if a.Verifier == b.Verifier {
		t.Fatal("verifiers should be unique")
	}
}

func TestBuildAuthorizeURL_HasPKCEParams(t *testing.T) {
	u := buildAuthorizeURL("https://mcp.example/authorize", "cid", "http://127.0.0.1:9/callback", "chal", "st")
	for _, sub := range []string{
		"response_type=code",
		"client_id=cid",
		"code_challenge=chal",
		"code_challenge_method=S256",
		"state=st",
		"scope=mcp%3Atools",
	} {
		if !contains(u, sub) {
			t.Errorf("authorize URL missing %q: %s", sub, u)
		}
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
