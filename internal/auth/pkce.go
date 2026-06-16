package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
)

// pkce holds a PKCE verifier/challenge pair (RFC 7636, S256 method).
type pkce struct {
	Verifier  string
	Challenge string
}

// newPKCE generates a high-entropy code verifier and its S256 challenge.
func newPKCE() (pkce, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return pkce{}, err
	}
	verifier := base64.RawURLEncoding.EncodeToString(b)
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	return pkce{Verifier: verifier, Challenge: challenge}, nil
}

// randomState returns a URL-safe random string for the OAuth `state` param.
func randomState() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
