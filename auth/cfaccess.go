// Package auth verifies Cloudflare Access JWTs.
//
// Cloudflare Zero Trust (Access) intercepts requests at the edge and, after
// the user authenticates against a configured identity provider, forwards
// the request to the origin with two headers:
//
//	Cf-Access-Jwt-Assertion: <signed JWT>
//	Cf-Access-Authenticated-User-Email: <email of the signed-in user>
//
// The Verifier exposed here fetches Cloudflare's public keys from
// https://<team>.cloudflareaccess.com/cdn-cgi/access/certs and validates
// the JWT's signature, audience claim, and expiry. The JWKS is cached and
// refreshed automatically (default every hour).
//
// When both TeamDomain and AUD are empty (e.g. local dev), New returns a
// nil verifier and ValidEmail always returns ("", false) — callers should
// treat that as "CF Access not configured, fall back to legacy auth".
package auth

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/MicahParks/keyfunc/v3"
	"github.com/golang-jwt/jwt/v5"
)

// Verifier verifies Cloudflare Access JWTs. Nil-safe — a nil receiver
// always returns ("", false) from ValidEmail.
type Verifier struct {
	aud  string
	keys keyfunc.Keyfunc
}

// New builds a Verifier. Returns (nil, nil) when both teamDomain and aud
// are empty (no-op mode). Returns an error if the JWKS endpoint can't be
// reached at startup (defensive — we want startup to fail loud, not
// silently disable auth).
func New(ctx context.Context, teamDomain, aud string) (*Verifier, error) {
	if teamDomain == "" && aud == "" {
		return nil, nil
	}
	if teamDomain == "" || aud == "" {
		return nil, fmt.Errorf("CF Access requires both team domain and AUD (got domain=%q aud=%q)", teamDomain, aud)
	}
	jwksURL := fmt.Sprintf("https://%s/cdn-cgi/access/certs", strings.TrimSuffix(teamDomain, "/"))
	return newWithJWKSURL(ctx, jwksURL, aud)
}

// newWithJWKSURL is the test-injection seam: tests pass a localhost
// httptest URL; production callers use New() which derives the JWKS URL
// from the team domain.
func newWithJWKSURL(ctx context.Context, jwksURL, aud string) (*Verifier, error) {
	kf, err := keyfunc.NewDefaultCtx(ctx, []string{jwksURL})
	if err != nil {
		return nil, fmt.Errorf("fetch JWKS at %s: %w", jwksURL, err)
	}
	return &Verifier{aud: aud, keys: kf}, nil
}

// ValidEmail verifies the token and returns the authenticated email on
// success. A nil receiver returns ("", false) — used for "no auth
// configured" mode. An invalid/expired/missing token returns ("", false).
func (v *Verifier) ValidEmail(rawToken string) (string, bool) {
	if v == nil || rawToken == "" {
		return "", false
	}
	parsed, err := jwt.Parse(
		rawToken,
		v.keys.Keyfunc,
		jwt.WithAudience(v.aud),
		jwt.WithIssuedAt(),
		jwt.WithExpirationRequired(),
		jwt.WithLeeway(60*time.Second),
	)
	if err != nil || !parsed.Valid {
		return "", false
	}
	claims, ok := parsed.Claims.(jwt.MapClaims)
	if !ok {
		return "", false
	}
	email, _ := claims["email"].(string)
	if email == "" {
		return "", false
	}
	return email, true
}

// Enabled reports whether CF Access verification is active. Useful for
// branching the legacy X-Admin-Token path in middleware.
func (v *Verifier) Enabled() bool { return v != nil }
