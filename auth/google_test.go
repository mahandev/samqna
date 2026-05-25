package auth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const testSecret = "12345678901234567890123456789012abcd" // 36 chars, fine for HMAC

func newAuth(t *testing.T) *GoogleAuth {
	t.Helper()
	g, err := NewGoogleAuth("cid", "csecret", "https://example.com/cb", "owner@example.com", testSecret)
	require.NoError(t, err)
	require.NotNil(t, g)
	return g
}

func TestGoogleAuth_New_RequiresAll(t *testing.T) {
	g, err := NewGoogleAuth("", "x", "x", "x", testSecret)
	require.NoError(t, err)
	require.Nil(t, g)

	g, err = NewGoogleAuth("cid", "csecret", "redir", "email", "too-short")
	require.Error(t, err)
	require.Nil(t, g)
}

func TestGoogleAuth_Nil_Safe(t *testing.T) {
	var g *GoogleAuth
	require.False(t, g.Enabled())
	r := httptest.NewRequest("GET", "/", nil)
	_, ok := g.ValidEmail(r)
	require.False(t, ok)
}

func TestSessionCookie_RoundTrip(t *testing.T) {
	g := newAuth(t)
	w := httptest.NewRecorder()
	g.issueSession(w, "owner@example.com", false)

	cookies := w.Result().Cookies()
	require.Len(t, cookies, 1)
	require.Equal(t, sessionCookieName, cookies[0].Name)
	require.True(t, cookies[0].HttpOnly)

	r := httptest.NewRequest("GET", "/", nil)
	r.AddCookie(cookies[0])
	email, ok := g.ValidEmail(r)
	require.True(t, ok)
	require.Equal(t, "owner@example.com", email)
}

func TestSessionCookie_TamperedSignatureRejected(t *testing.T) {
	g := newAuth(t)
	w := httptest.NewRecorder()
	g.issueSession(w, "owner@example.com", false)
	c := w.Result().Cookies()[0]

	// Flip a bit in the signature half.
	parts := strings.SplitN(c.Value, ".", 2)
	require.Len(t, parts, 2)
	parts[1] = strings.Repeat("a", len(parts[1]))
	c.Value = parts[0] + "." + parts[1]

	r := httptest.NewRequest("GET", "/", nil)
	r.AddCookie(c)
	_, ok := g.ValidEmail(r)
	require.False(t, ok)
}

func TestSessionCookie_TamperedPayloadRejected(t *testing.T) {
	g := newAuth(t)
	// Build a cookie with the right signing key but a different email in the payload.
	w := httptest.NewRecorder()
	g.issueSession(w, "owner@example.com", false)
	c := w.Result().Cookies()[0]
	parts := strings.SplitN(c.Value, ".", 2)
	// Use a totally bogus base64 payload (won't decode) — signature still mismatches.
	c.Value = "not-base64." + parts[1]
	r := httptest.NewRequest("GET", "/", nil)
	r.AddCookie(c)
	_, ok := g.ValidEmail(r)
	require.False(t, ok)
}

func TestSessionCookie_WrongAllowedEmail(t *testing.T) {
	g, _ := NewGoogleAuth("cid", "csecret", "redir", "owner@example.com", testSecret)
	w := httptest.NewRecorder()
	g.issueSession(w, "different@example.com", false)
	c := w.Result().Cookies()[0]
	r := httptest.NewRequest("GET", "/", nil)
	r.AddCookie(c)
	_, ok := g.ValidEmail(r)
	require.False(t, ok, "session emails not on the allowlist must be rejected")
}

func TestBeginLogin_SetsStateCookieAndReturnsGoogleURL(t *testing.T) {
	g := newAuth(t)
	w := httptest.NewRecorder()
	url := g.BeginLogin(w, true)
	require.True(t, strings.HasPrefix(url, "https://accounts.google.com/"), "got %q", url)
	require.Contains(t, url, "client_id=cid")
	require.Contains(t, url, "state=")

	cookies := w.Result().Cookies()
	require.Len(t, cookies, 1)
	require.Equal(t, stateCookieName, cookies[0].Name)
	require.True(t, cookies[0].Secure)
	require.True(t, cookies[0].HttpOnly)
}

func TestCompleteLogin_StateMismatchRejected(t *testing.T) {
	g := newAuth(t)
	r := httptest.NewRequest("GET", "/auth/google/callback?code=x&state=evil", nil)
	r.AddCookie(&http.Cookie{Name: stateCookieName, Value: "good"})
	w := httptest.NewRecorder()
	_, err := g.CompleteLogin(r.Context(), w, r, false)
	require.Error(t, err)
	require.Contains(t, err.Error(), "state")
}

func TestCompleteLogin_MissingStateCookieRejected(t *testing.T) {
	g := newAuth(t)
	r := httptest.NewRequest("GET", "/auth/google/callback?code=x&state=anything", nil)
	w := httptest.NewRecorder()
	_, err := g.CompleteLogin(r.Context(), w, r, false)
	require.Error(t, err)
	require.Contains(t, err.Error(), "state")
}

func TestLogout_ClearsCookie(t *testing.T) {
	g := newAuth(t)
	w := httptest.NewRecorder()
	g.Logout(w, false)
	cookies := w.Result().Cookies()
	require.Len(t, cookies, 1)
	require.Equal(t, sessionCookieName, cookies[0].Name)
	require.Equal(t, "", cookies[0].Value)
	require.Less(t, cookies[0].MaxAge, 0)
}
