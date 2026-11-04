package appauth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	githubkit "github.com/LarsArtmann/go-github-kit"
	"github.com/google/go-github/v69/github"
)

// Sentinel validation errors for the appauth constructors; callers can
// match them with errors.Is instead of string comparison.
var (
	ErrAppIDRequired          = errors.New("appauth: app id is required")
	ErrInvalidPEM             = errors.New("appauth: private key is not valid PEM")
	ErrAppAuthRequired        = errors.New("appauth: app auth is required")
	ErrInstallationIDRequired = errors.New("appauth: installation id is required")
	ErrAppsServiceRequired    = errors.New("appauth: apps service is required")
	ErrEmptyToken             = errors.New("appauth: minted token is empty")
	ErrTokenSourceRequired    = errors.New("appauth: token source is required")
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
		return nil, ErrAppAuthRequired
	}

	if base == nil {
		base = http.DefaultTransport
	}

	return &AppAuthTransport{
		app:     app,
		base:    base,
		now:     time.Now,
		mu:      sync.Mutex{},
		jwt:     "",
		jwtGood: time.Time{},
	}, nil
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

	resp, err := t.base.RoundTrip(clone)
	if err != nil {
		return nil, fmt.Errorf("appauth: round trip as app: %w", err)
	}

	return resp, nil
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
		parsed, err := githubkit.ResolveBaseURL(baseURL)
		if err != nil {
			return nil, fmt.Errorf("appauth: resolve base URL %q: %w", baseURL, err)
		}

		client.BaseURL = parsed
	}

	return client, nil
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
		return nil, ErrInstallationIDRequired
	case apps == nil:
		return nil, ErrAppsServiceRequired
	}

	return &InstallationTokenSource{
		installationID: installationID,
		apps:           apps,
		now:            time.Now,
		mu:             sync.Mutex{},
		token:          "",
		good:           time.Time{},
	}, nil
}

// SetClock overrides the wall clock (tests).
func (s *InstallationTokenSource) SetClock(now func() time.Time) { s.now = now }

// Token returns a valid installation token, minting a fresh one when the
// cached token is missing or within the refresh window of its expiry. The
// context carries the minting request; pass the request's context so a
// canceled caller also cancels an in-flight mint.
func (s *InstallationTokenSource) Token(ctx context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.token != "" && s.now().Before(s.good) {
		return s.token, nil
	}

	minted, _, err := s.apps.CreateInstallationToken(ctx, s.installationID, nil)
	if err != nil {
		return "", fmt.Errorf("appauth: mint token for installation %d: %w", s.installationID, err)
	}

	token := minted.GetToken()
	if token == "" {
		return "", ErrEmptyToken
	}

	s.token = token

	s.good = minted.GetExpiresAt().Add(-tokenRefreshLead)
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
		return nil, ErrTokenSourceRequired
	}

	if base == nil {
		base = http.DefaultTransport
	}

	return &InstallationTokenTransport{src: src, base: base}, nil
}

// RoundTrip implements http.RoundTripper.
func (t *InstallationTokenTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	token, err := t.src.Token(req.Context())
	if err != nil {
		return nil, fmt.Errorf("appauth: installation token: %w", err)
	}

	clone := req.Clone(req.Context())
	clone.Header.Set("Authorization", "Bearer "+token)

	resp, err := t.base.RoundTrip(clone)
	if err != nil {
		return nil, fmt.Errorf("appauth: round trip as installation: %w", err)
	}

	return resp, nil
}
