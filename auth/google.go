// Direct Google OAuth — no Cloudflare Zero Trust required.
//
// Flow:
//
//   1. User hits /admin/login (or any /admin/* without a session).
//   2. We set a short-lived signed state cookie and 302 to Google.
//   3. Google authenticates, redirects back to /auth/google/callback?code=…&state=…
//   4. We verify the state cookie matches, exchange the code, call Google's
//      userinfo endpoint, confirm the email matches AllowedEmail.
//   5. We set a signed session cookie ("samqna_admin") with the email and
//      an expiry. All admin requests check that cookie.
//
// The cookie is HMAC-SHA256 signed with SessionKey — flipping a bit
// invalidates it. Email is the only state we carry.
package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

const (
	sessionCookieName = "samqna_admin"
	stateCookieName   = "samqna_oauth_state"
	stateMaxAge       = 10 * 60 // seconds
	defaultSessionDur = 24 * time.Hour
)

type GoogleAuth struct {
	cfg          *oauth2.Config
	allowedEmail string // single email allowed; case-insensitive
	sessionKey   []byte // HMAC key for cookie signing
	sessionDur   time.Duration
}

// NewGoogleAuth returns nil when not configured (clientID, secret,
// redirectURL, or allowedEmail empty) — callers should treat nil as
// "Google OAuth disabled, fall back to other auth paths".
//
// Returns an error if sessionSecret is too short (must be ≥32 bytes for
// HMAC-SHA256).
func NewGoogleAuth(clientID, clientSecret, redirectURL, allowedEmail, sessionSecret string) (*GoogleAuth, error) {
	if clientID == "" || clientSecret == "" || redirectURL == "" || allowedEmail == "" {
		return nil, nil
	}
	if len(sessionSecret) < 32 {
		return nil, fmt.Errorf("SESSION_SECRET must be at least 32 chars (got %d) — generate one with: openssl rand -hex 32", len(sessionSecret))
	}
	return &GoogleAuth{
		cfg: &oauth2.Config{
			ClientID:     clientID,
			ClientSecret: clientSecret,
			RedirectURL:  redirectURL,
			Scopes:       []string{"openid", "email", "profile"},
			Endpoint:     google.Endpoint,
		},
		allowedEmail: strings.ToLower(strings.TrimSpace(allowedEmail)),
		sessionKey:   []byte(sessionSecret),
		sessionDur:   defaultSessionDur,
	}, nil
}

// Enabled reports whether Google OAuth is active. Nil-safe.
func (g *GoogleAuth) Enabled() bool { return g != nil }

// BeginLogin sets the state cookie and returns the Google auth URL the
// caller should redirect to.
func (g *GoogleAuth) BeginLogin(w http.ResponseWriter, secure bool) string {
	state := randState()
	http.SetCookie(w, &http.Cookie{
		Name:     stateCookieName,
		Value:    state,
		Path:     "/auth/google/callback",
		MaxAge:   stateMaxAge,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
	return g.cfg.AuthCodeURL(state, oauth2.AccessTypeOnline, oauth2.SetAuthURLParam("prompt", "select_account"))
}

// CompleteLogin runs steps 3–4 of the flow. On success it sets the
// session cookie and returns the authenticated email.
func (g *GoogleAuth) CompleteLogin(ctx context.Context, w http.ResponseWriter, r *http.Request, secure bool) (string, error) {
	stateCookie, err := r.Cookie(stateCookieName)
	if err != nil {
		return "", errors.New("missing state cookie")
	}
	got := r.URL.Query().Get("state")
	if got == "" || got != stateCookie.Value {
		return "", errors.New("state mismatch")
	}
	// Wipe the state cookie regardless of outcome.
	http.SetCookie(w, &http.Cookie{
		Name: stateCookieName, Value: "", Path: "/auth/google/callback",
		MaxAge: -1, HttpOnly: true, Secure: secure,
	})

	code := r.URL.Query().Get("code")
	if code == "" {
		return "", errors.New("missing code")
	}

	tok, err := g.cfg.Exchange(ctx, code)
	if err != nil {
		return "", fmt.Errorf("token exchange: %w", err)
	}

	client := g.cfg.Client(ctx, tok)
	resp, err := client.Get("https://www.googleapis.com/oauth2/v3/userinfo")
	if err != nil {
		return "", fmt.Errorf("userinfo: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", fmt.Errorf("userinfo status %d: %s", resp.StatusCode, string(body))
	}
	var info struct {
		Email         string `json:"email"`
		EmailVerified bool   `json:"email_verified"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return "", fmt.Errorf("decode userinfo: %w", err)
	}
	if !info.EmailVerified {
		return "", errors.New("email not verified by Google")
	}
	if strings.ToLower(info.Email) != g.allowedEmail {
		return "", fmt.Errorf("email %q is not on the allowlist", info.Email)
	}

	g.issueSession(w, info.Email, secure)
	return info.Email, nil
}

// ValidEmail reads + verifies the session cookie. Empty string + false on
// any failure (missing, malformed, bad signature, expired, wrong email).
func (g *GoogleAuth) ValidEmail(r *http.Request) (string, bool) {
	if g == nil {
		return "", false
	}
	c, err := r.Cookie(sessionCookieName)
	if err != nil || c.Value == "" {
		return "", false
	}
	parts := strings.SplitN(c.Value, ".", 2)
	if len(parts) != 2 {
		return "", false
	}
	mac := hmac.New(sha256.New, g.sessionKey)
	_, _ = mac.Write([]byte(parts[0]))
	expected := hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(parts[1]), []byte(expected)) {
		return "", false
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return "", false
	}
	var s sessionPayload
	if json.Unmarshal(raw, &s) != nil {
		return "", false
	}
	if time.Now().Unix() > s.Exp {
		return "", false
	}
	if strings.ToLower(s.Email) != g.allowedEmail {
		// Allowlist changed since session was issued — invalidate.
		return "", false
	}
	return s.Email, true
}

// Logout clears the session cookie. Browsers will drop it.
func (g *GoogleAuth) Logout(w http.ResponseWriter, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookieName, Value: "", Path: "/",
		MaxAge: -1, HttpOnly: true, Secure: secure,
	})
}

// ---- internals --------------------------------------------------------

type sessionPayload struct {
	Email string `json:"email"`
	Exp   int64  `json:"exp"`
}

func (g *GoogleAuth) issueSession(w http.ResponseWriter, email string, secure bool) {
	s := sessionPayload{
		Email: email,
		Exp:   time.Now().Add(g.sessionDur).Unix(),
	}
	payload, _ := json.Marshal(s)
	b64 := base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, g.sessionKey)
	_, _ = mac.Write([]byte(b64))
	sig := hex.EncodeToString(mac.Sum(nil))
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    b64 + "." + sig,
		Path:     "/",
		Expires:  time.Unix(s.Exp, 0),
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
}

func randState() string {
	buf := make([]byte, 24)
	_, _ = rand.Read(buf)
	return base64.RawURLEncoding.EncodeToString(buf)
}
