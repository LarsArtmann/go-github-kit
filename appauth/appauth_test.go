package appauth_test

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LarsArtmann/go-github-kit"
	"github.com/LarsArtmann/go-github-kit/appauth"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestKey generates an RSA key pair and returns the PEM (as GitHub's
// App settings page would hand it out) plus the public key for verification.
func newTestKey(t *testing.T) ([]byte, *rsa.PublicKey) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	pemBytes := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	})
	return pemBytes, &key.PublicKey
}

func TestNewAppAuth_RejectsGarbagePEM(t *testing.T) {
	t.Parallel()
	_, err := appauth.NewAppAuth(42, []byte("not pem"))
	assert.ErrorContains(t, err, "not valid PEM")
}

func TestNewAppAuth_RejectsBadAppID(t *testing.T) {
	t.Parallel()
	pemKey, _ := newTestKey(t)
	_, err := appauth.NewAppAuth(0, pemKey)
	assert.ErrorContains(t, err, "app id")
}

func TestSign_ProducesVerifiableRS256JWT(t *testing.T) {
	t.Parallel()

	pemKey, publicKey := newTestKey(t)
	app, err := appauth.NewAppAuth(12345, pemKey)
	require.NoError(t, err)

	now := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	signed, err := app.Sign(now)
	require.NoError(t, err)

	parts := strings.Split(signed, ".")
	require.Len(t, parts, 3, "header.payload.signature")

	var header struct {
		Alg string `json:"alg"`
		Typ string `json:"typ"`
	}
	headerBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(headerBytes, &header))
	assert.Equal(t, "RS256", header.Alg)
	assert.Equal(t, "JWT", header.Typ)

	var claims struct {
		Iat int64 `json:"iat"`
		Exp int64 `json:"exp"`
		Iss int64 `json:"iss"`
	}
	claimsBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(claimsBytes, &claims))
	assert.Equal(t, int64(12345), claims.Iss)
	assert.Equal(t, now.Add(-time.Minute).Unix(), claims.Iat, "iat is backdated for clock skew")
	assert.Equal(t, now.Add(10*time.Minute).Unix(), claims.Exp, "exp matches GitHub's 10-minute cap")

	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	require.NoError(t, err)
	assert.NoError(t, rsa.VerifyPKCS1v15(publicKey, crypto.SHA256, digest[:], signature),
		"the signature must verify against the app's public key")
}

// The Apps-API transport signs once and reuses the JWT until shortly
// before expiry.
func TestAppAuthTransport_SignsOncePerWindow(t *testing.T) {
	t.Parallel()

	pemKey, _ := newTestKey(t)
	app, err := appauth.NewAppAuth(7, pemKey)
	require.NoError(t, err)

	var mu sync.Mutex
	authHeaders := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		authHeaders = append(authHeaders, r.Header.Get("Authorization"))
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	transport, err := appauth.NewAppAuthTransport(app, nil)
	require.NoError(t, err)

	now := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	clock := now
	transport.SetClock(func() time.Time { return clock })

	for range 3 {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL, nil)
		require.NoError(t, err)
		resp, err := transport.RoundTrip(req)
		require.NoError(t, err)
		_ = resp.Body.Close()
	}

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, authHeaders, 3)
	assert.Equal(t, authHeaders[0], authHeaders[1], "JWT is reused within the window")
	assert.Equal(t, authHeaders[1], authHeaders[2])
	assert.True(t, strings.HasPrefix(authHeaders[0], "Bearer "))
}

// The installation token source mints lazily, caches, and re-mints after
// the refresh window.
func TestInstallationTokenSource_MintCacheRefresh(t *testing.T) {
	t.Parallel()

	pemKey, _ := newTestKey(t)
	app, err := appauth.NewAppAuth(7, pemKey)
	require.NoError(t, err)

	var mu sync.Mutex
	mintCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		mintCount++
		count := mintCount
		mu.Unlock()

		assert.Equal(t, http.MethodPost, r.Method)
		assert.Contains(t, r.URL.Path, "/app/installations/99/access_tokens")
		assert.True(t, strings.HasPrefix(r.Header.Get("Authorization"), "Bearer "),
			"mint requests carry the app JWT")

		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w,
			`{"token":"install-token-%d","expires_at":"%s"}`,
			count, time.Now().Add(time.Hour).Format(time.RFC3339))
	}))
	defer server.Close()

	appsClient, err := appauth.NewAppsClient(app, server.URL)
	require.NoError(t, err)

	source, err := appauth.NewInstallationTokenSource(99, appsClient.Apps)
	require.NoError(t, err)

	now := time.Now()
	clock := now
	source.SetClock(func() time.Time { return clock })

	first, err := source.Token()
	require.NoError(t, err)
	assert.Equal(t, "install-token-1", first)

	second, err := source.Token()
	require.NoError(t, err)
	assert.Equal(t, "install-token-1", second, "cached within the window")

	mu.Lock()
	assert.Equal(t, 1, mintCount, "no extra mint inside the window")
	mu.Unlock()

	clock = now.Add(2 * time.Hour)
	third, err := source.Token()
	require.NoError(t, err)
	assert.Equal(t, "install-token-2", third, "re-minted after expiry")
}

// B22.7: a kit kernel built over the installation transport reaches the
// fake GitHub with the MINTED token on every request.
func TestKernelPerInstallation_WiresInstallationToken(t *testing.T) {
	t.Parallel()

	pemKey, _ := newTestKey(t)
	app, err := appauth.NewAppAuth(7, pemKey)
	require.NoError(t, err)

	var mu sync.Mutex
	seenTokens := []string{}
	mintCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case strings.HasSuffix(r.URL.Path, "/access_tokens"):
			mintCount++
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w,
				`{"token":"kernel-token-%d","expires_at":"%s"}`,
				mintCount, time.Now().Add(time.Hour).Format(time.RFC3339))
		case r.URL.Path == "/repos/acme/widgets":
			seenTokens = append(seenTokens, r.Header.Get("Authorization"))
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":1,"full_name":"acme/widgets","private":false}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	appsClient, err := appauth.NewAppsClient(app, server.URL)
	require.NoError(t, err)
	source, err := appauth.NewInstallationTokenSource(42, appsClient.Apps)
	require.NoError(t, err)

	inner, err := appauth.NewInstallationTokenTransport(source, nil)
	require.NoError(t, err)

	kernel, err := githubkit.New(
		githubkit.WithBaseURL(server.URL),
		githubkit.WithHTTPClient(&http.Client{Transport: inner}),
	)
	require.NoError(t, err)

	repo, _, err := kernel.Client.Repositories.Get(context.Background(), "acme", "widgets")
	require.NoError(t, err)
	assert.Equal(t, "acme/widgets", repo.GetFullName())

	repo, _, err = kernel.Client.Repositories.Get(context.Background(), "acme", "widgets")
	require.NoError(t, err)
	assert.NotNil(t, repo)

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, seenTokens, 2)
	assert.Equal(t, "Bearer kernel-token-1", seenTokens[0])
	assert.Equal(t, "Bearer kernel-token-1", seenTokens[1], "kernel requests reuse the cached install token")
	assert.Equal(t, 1, mintCount, "exactly one mint for both kernel requests")
}

// Installation listing (B22.4) through the JWT-authenticated Apps client.
func TestAppsClient_ListsInstallations(t *testing.T) {
	t.Parallel()

	pemKey, _ := newTestKey(t)
	app, err := appauth.NewAppAuth(7, pemKey)
	require.NoError(t, err)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/app/installations", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"id":42,"account":{"login":"acme","type":"Organization"}}]`))
	}))
	defer server.Close()

	appsClient, err := appauth.NewAppsClient(app, server.URL)
	require.NoError(t, err)

	installations, _, err := appsClient.Apps.ListInstallations(context.Background(), nil)
	require.NoError(t, err)
	require.Len(t, installations, 1)
	assert.Equal(t, int64(42), installations[0].GetID())
	assert.Equal(t, "acme", installations[0].GetAccount().GetLogin())
}

// Unused import guard for the go-github types exercised via the kernel.
