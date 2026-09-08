package appauth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"net/url"
	"sync"
	"time"

	"github.com/google/go-github/v69/github"
)

// AppAuthTransport authenticates requests to the Apps API with the app's
// JWT. It signs at most once per JWT lifetime and reuses the token for
// every request until shortly before expiry.
type AppAuthTransport struct {
	app  *AppAuth
	base http.RoundTripper

	mu      sync.Mutex
	jwt     string
	jwtGood time.Time
	now     func() time.Time
}

// NewAppAuthTransport wraps base (nil means http.DefaultTransport).
func NewAppAuthTransport(app *AppAuth, base http.RoundTripper) (*AppAuthTransport, error) {
	if app == nil {
		return nil, errors.New("appauth: app auth is required")
	}
	if base == nil {
		base = http.DefaultTransport
	}
	return &AppAuthTransport{app: app, base: base, now: time.Now}, nil
}

// SetClock overrides the wall clock (tests).
func (t *AppAuthTransport) SetClock(now func() time.Time) { t.now = now }

// RoundTrip implements http.RoundTripper.
func (t *AppAuthTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	token, err := t.currentJWT()
	if err != nil {
		return nil, fmt.Errorf("appauth: sign app jwt: %w", err)
	}

	clone := req.Clone(req.Context())
	clone.Header.Set("Authorization", "Bearer "+token)
	return t.base.RoundTrip(clone)
}

func (t *AppAuthTransport) currentJWT() (string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	now := t.now()
	if t.jwt != "" && now.Before(t.jwtGood) {
		return t.jwt, nil
	}

	signed, err := t.app.Sign(now)
	if err != nil {
		return "", err
	}
	t.jwt = signed
	t.jwtGood = jwtValidUntil(now)
	return t.jwt, nil
}

// NewAppsClient builds a go-github client whose requests carry the app
// JWT: enough to list installations and mint installation tokens.
func NewAppsClient(app *AppAuth, baseURL string) (*github.Client, error) {
	transport, err := NewAppAuthTransport(app, nil)
	if err != nil {
		return nil, err
	}
	client := github.NewClient(&http.Client{Transport: transport})
	if baseURL != "" {
		parsed, err := normalizeBaseURL(baseURL)
		if err != nil {
			return nil, err
		}
		client.BaseURL = parsed
	}
	return client, nil
}

// normalizeBaseURL appends the trailing slash go-github requires.
func normalizeBaseURL(rawURL string) (*url.URL, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("appauth: parse base url: %w", err)
	}
	if !parsed.IsAbs() || parsed.Host == "" {
		return nil, fmt.Errorf("appauth: base url %q must be absolute", rawURL)
	}
	if !strings.HasSuffix(parsed.Path, "/") {
		parsed.Path += "/"
	}
	return parsed, nil
}

// InstallationTokenSource mints and caches one installation token. Tokens
// refresh tokenRefreshLead before expiry; minting is the only GitHub
// contact (the Apps API, authenticated by the app JWT).
type InstallationTokenSource struct {
	installationID int64
	apps           *github.AppsService

	mu    sync.Mutex
	token string
	good  time.Time
	now   func() time.Time
}

// NewInstallationTokenSource wires a token source for one installation.
func NewInstallationTokenSource(installationID int64, apps *github.AppsService) (*InstallationTokenSource, error) {
	switch {
	case installationID <= 0:
		return nil, errors.New("appauth: installation id is required")
	case apps == nil:
		return nil, errors.New("appauth: apps service is required")
	}
	return &InstallationTokenSource{
		installationID: installationID,
		apps:           apps,
		now:            time.Now,
	}, nil
}

// SetClock overrides the wall clock (tests).
func (s *InstallationTokenSource) SetClock(now func() time.Time) { s.now = now }

// Token returns a valid installation token, minting a fresh one when the
// cached token is missing or within the refresh window of its expiry.
func (s *InstallationTokenSource) Token() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.token != "" && s.now().Before(s.good) {
		return s.token, nil
	}

	minted, _, err := s.apps.CreateInstallationToken(context.Background(), s.installationID, nil)
	if err != nil {
		return "", fmt.Errorf("appauth: mint token for installation %d: %w", s.installationID, err)
	}
	token := minted.GetToken()
	if token == "" {
		return "", errors.New("appauth: minted token is empty")
	}

	s.token = token
	s.good = minted.GetExpiresAt().Time.Add(-tokenRefreshLead)
	if !s.now().Before(s.good) {
		// GitHub handed us a suspiciously short-lived token: use it, but
		// do not trust it past now (the next call re-mints).
		s.good = s.now()
	}
	return s.token, nil
}

// InstallationTokenTransport authenticates repository requests with the
// installation token from src, minting lazily on first use.
type InstallationTokenTransport struct {
	src  *InstallationTokenSource
	base http.RoundTripper
}

// NewInstallationTokenTransport wraps base (nil means http.DefaultTransport).
func NewInstallationTokenTransport(src *InstallationTokenSource, base http.RoundTripper) (http.RoundTripper, error) {
	if src == nil {
		return nil, errors.New("appauth: token source is required")
	}
	if base == nil {
		base = http.DefaultTransport
	}
	return &InstallationTokenTransport{src: src, base: base}, nil
}

// RoundTrip implements http.RoundTripper.
func (t *InstallationTokenTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	token, err := t.src.Token()
	if err != nil {
		return nil, fmt.Errorf("appauth: installation token: %w", err)
	}

	clone := req.Clone(req.Context())
	clone.Header.Set("Authorization", "Bearer "+token)
	return t.base.RoundTrip(clone)
}
