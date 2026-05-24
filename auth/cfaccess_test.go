package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"
)

// jwksServer is a tiny in-test HTTPS handler that serves a JWKS for one
// generated RSA key. Returns the server, the private key (for signing
// test tokens), and the kid.
func jwksServer(t *testing.T) (*httptest.Server, *rsa.PrivateKey, string) {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	n := base64.RawURLEncoding.EncodeToString(priv.N.Bytes())
	e := base64.RawURLEncoding.EncodeToString(big.NewInt(int64(priv.E)).Bytes())
	kid := "test-key-1"
	mux := http.NewServeMux()
	mux.HandleFunc("/cdn-cgi/access/certs", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"keys": []map[string]any{
				{"kty": "RSA", "alg": "RS256", "use": "sig", "kid": kid, "n": n, "e": e},
			},
		})
	})
	srv := httptest.NewServer(mux)
	return srv, priv, kid
}

func sign(t *testing.T, priv *rsa.PrivateKey, kid string, claims jwt.MapClaims) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tok.Header["kid"] = kid
	s, err := tok.SignedString(priv)
	require.NoError(t, err)
	return s
}

func TestNew_NoConfig_ReturnsNil(t *testing.T) {
	v, err := New(context.Background(), "", "")
	require.NoError(t, err)
	require.Nil(t, v)
	// Nil-safe usage:
	email, ok := v.ValidEmail("anything")
	require.False(t, ok)
	require.Empty(t, email)
	require.False(t, v.Enabled())
}

func TestNew_PartialConfig_Errors(t *testing.T) {
	_, err := New(context.Background(), "samqna.cloudflareaccess.com", "")
	require.Error(t, err)
	_, err = New(context.Background(), "", "aud-tag")
	require.Error(t, err)
}

func TestVerifier_HappyPath(t *testing.T) {
	srv, priv, kid := jwksServer(t)
	defer srv.Close()

	// Strip scheme — New() prepends https://, but we need to point at our
	// test server. Easier: temporarily build a Verifier directly.
	// (Production callers use New() with a real team domain.)
	v := buildTestVerifier(t, srv.URL, "test-aud")
	token := sign(t, priv, kid, jwt.MapClaims{
		"aud":   "test-aud",
		"email": "dev@example.com",
		"iat":   time.Now().Unix(),
		"exp":   time.Now().Add(10 * time.Minute).Unix(),
	})

	email, ok := v.ValidEmail(token)
	require.True(t, ok)
	require.Equal(t, "dev@example.com", email)
}

func TestVerifier_WrongAudience(t *testing.T) {
	srv, priv, kid := jwksServer(t)
	defer srv.Close()
	v := buildTestVerifier(t, srv.URL, "expected-aud")
	token := sign(t, priv, kid, jwt.MapClaims{
		"aud":   "wrong-aud",
		"email": "dev@example.com",
		"iat":   time.Now().Unix(),
		"exp":   time.Now().Add(10 * time.Minute).Unix(),
	})
	_, ok := v.ValidEmail(token)
	require.False(t, ok)
}

func TestVerifier_Expired(t *testing.T) {
	srv, priv, kid := jwksServer(t)
	defer srv.Close()
	v := buildTestVerifier(t, srv.URL, "aud")
	token := sign(t, priv, kid, jwt.MapClaims{
		"aud":   "aud",
		"email": "dev@example.com",
		"iat":   time.Now().Add(-2 * time.Hour).Unix(),
		"exp":   time.Now().Add(-1 * time.Hour).Unix(),
	})
	_, ok := v.ValidEmail(token)
	require.False(t, ok)
}

func TestVerifier_GarbageToken(t *testing.T) {
	srv, _, _ := jwksServer(t)
	defer srv.Close()
	v := buildTestVerifier(t, srv.URL, "aud")
	_, ok := v.ValidEmail("not-a-jwt")
	require.False(t, ok)
	_, ok = v.ValidEmail("")
	require.False(t, ok)
}

func buildTestVerifier(t *testing.T, baseURL, aud string) *Verifier {
	t.Helper()
	v, err := newWithJWKSURL(context.Background(), baseURL+"/cdn-cgi/access/certs", aud)
	require.NoError(t, err)
	require.NotNil(t, v)
	return v
}
