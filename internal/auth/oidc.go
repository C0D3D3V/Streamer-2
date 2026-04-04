package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"

	"github.com/c0d3d3v/streamer-2/internal/config"
	gooidc "github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

// oidcProvider is the initialized OIDC provider. It is set by InitOIDC and then
// used by the auth handlers.
var (
	oidcProvider *gooidc.Provider
	oauth2Config oauth2.Config
)

// InitOIDC discovers the OIDC endpoints from the issuer URL and sets up the
// OAuth2 config. Must be called after config is loaded and setup is complete.
func InitOIDC(ctx context.Context) error {
	cfg := config.Get()

	var err error
	oidcProvider, err = gooidc.NewProvider(ctx, cfg.OIDC.IssuerURL)
	if err != nil {
		return fmt.Errorf("discover OIDC provider at %s: %w", cfg.OIDC.IssuerURL, err)
	}

	oauth2Config = oauth2.Config{
		ClientID:     cfg.OIDC.ClientID,
		ClientSecret: cfg.OIDC.ClientSecret,
		RedirectURL:  cfg.OIDC.RedirectURL(cfg.Server.ExternalURL),
		Endpoint:     oidcProvider.Endpoint(),
		Scopes:       []string{"openid", "email", "profile"},
	}

	return nil
}

// AuthCodeURL generates the Authelia authorization URL using PKCE.
// It returns the URL, the state nonce, and the PKCE code verifier.
//
// PKCE (RFC 7636) prevents authorization code interception attacks:
//   - A random code_verifier is generated locally.
//   - Its SHA-256 hash (code_challenge) is sent to the auth server.
//   - On callback the verifier is sent and the server checks the hash.
func AuthCodeURL() (url, state, codeVerifier string, err error) {
	state, err = randomHex(16)
	if err != nil {
		return
	}

	codeVerifier, err = randomBase64(32)
	if err != nil {
		return
	}

	// S256 challenge: BASE64URL(SHA256(code_verifier))
	sum := sha256.Sum256([]byte(codeVerifier))
	codeChallenge := base64.RawURLEncoding.EncodeToString(sum[:])

	url = oauth2Config.AuthCodeURL(state,
		oauth2.SetAuthURLParam("code_challenge", codeChallenge),
		oauth2.SetAuthURLParam("code_challenge_method", "S256"),
	)
	return
}

// Exchange trades the authorization code for tokens and verifies the ID token.
// Returns the authenticated User on success.
func Exchange(ctx context.Context, code, codeVerifier string) (*User, error) {
	// Exchange code for tokens, sending the PKCE verifier so the server can
	// verify it matches the challenge we sent earlier.
	token, err := oauth2Config.Exchange(ctx, code,
		oauth2.SetAuthURLParam("code_verifier", codeVerifier),
	)
	if err != nil {
		return nil, fmt.Errorf("exchange code: %w", err)
	}

	// Extract the raw id_token from the token extras.
	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok {
		return nil, fmt.Errorf("no id_token in token response")
	}

	// Verify the ID token: signature, issuer, audience, and expiry.
	verifier := oidcProvider.Verifier(&gooidc.Config{ClientID: oauth2Config.ClientID})
	idToken, err := verifier.Verify(ctx, rawIDToken)
	if err != nil {
		return nil, fmt.Errorf("verify id_token: %w", err)
	}

	// Parse standard claims from the verified token.
	var claims struct {
		Subject string `json:"sub"`
		Email   string `json:"email"`
		Name    string `json:"name"`
	}
	if err := idToken.Claims(&claims); err != nil {
		return nil, fmt.Errorf("parse id_token claims: %w", err)
	}

	return &User{
		Subject: claims.Subject,
		Email:   claims.Email,
		Name:    claims.Name,
	}, nil
}

// randomHex returns a cryptographically random hex string of n bytes.
func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// randomBase64 returns a URL-safe base64 string of n random bytes.
func randomBase64(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
