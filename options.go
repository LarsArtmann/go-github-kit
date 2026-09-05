package githubkit

import (
	"net/http"
	"time"
)

// Default token environment variables consulted by [WithAuthTokenFromEnv]
// when called without arguments. GITHUB_TOKEN wins because it is the name
// GitHub's own documentation uses; GH_TOKEN is the gh CLI convention.
const (
	DefaultTokenEnvGITHUB = "GITHUB_TOKEN" //nolint:gosec // env var name, not a credential
	DefaultTokenEnvGH     = "GH_TOKEN"     //nolint:gosec // env var name, not a credential
)

// Numeric defaults behind the exported option vars, named so the values
// read as policy rather than magic.
const (
	defaultMinRemaining   = 10
	defaultMaxWait        = 15 * time.Minute
	defaultMaxRetries     = 3
	defaultInitialBackoff = 1 * time.Second
	defaultMaxBackoff     = 30 * time.Second
)

// DefaultRateLimitOptions gates when ten or fewer core requests remain in
// the window, waiting for the reset for at most fifteen minutes. Beyond
// that, calls fail fast with [ErrRateLimited] instead of blocking a caller
// for an unbounded time.
var DefaultRateLimitOptions = RateLimitOptions{ //nolint:gochecknoglobals // intentional shared default
	Enabled:      true,
	MinRemaining: defaultMinRemaining,
	MaxWait:      defaultMaxWait,
}

// DefaultRetryOptions retries up to three times with exponential backoff
// growing from one second to at most thirty seconds. These bounds match
// GitHub's guidance for secondary rate limits and transient 5xx storms.
var DefaultRetryOptions = RetryOptions{ //nolint:gochecknoglobals // intentional shared default
	Enabled:        true,
	MaxRetries:     defaultMaxRetries,
	InitialBackoff: defaultInitialBackoff,
	MaxBackoff:     defaultMaxBackoff,
}

// DefaultRequestTimeout bounds a single HTTP round trip. The kernel's own
// waits (rate-limit reset, backoff) are governed by the caller's context,
// not this timeout.
const DefaultRequestTimeout = 30 * time.Second

// Options is the fully resolved configuration for [New]. The zero value is
// not used directly; [New] applies defaults before running the given
// [Option] functions.
type Options struct {
	// Token authenticates every request as a GitHub Personal Access Token.
	// Empty means unauthenticated (60 req/h budget).
	Token string

	// TokenEnvVars lists environment variables to consult when Token is
	// empty; the first variable that is set wins. Nil disables env lookup.
	TokenEnvVars []string

	// BaseURL overrides the GitHub API base URL (GitHub Enterprise Server,
	// httptest servers). A missing trailing slash is appended automatically,
	// matching the native SDK's WithEnterpriseURLs behavior.
	BaseURL string

	// HTTPClient, when set, contributes its Transport as the innermost
	// layer and its Timeout as the per-request timeout; the kernel wraps
	// rather than replaces it.
	HTTPClient *http.Client

	// UserAgent overrides the client identity sent with every request.
	// Empty means the go-github default.
	UserAgent string

	// RequestTimeout bounds one round trip. Zero means [DefaultRequestTimeout].
	RequestTimeout time.Duration

	// RateLimit configures the pre-flight gate. Zero-value fields take
	// [DefaultRateLimitOptions] values.
	RateLimit RateLimitOptions

	// Retry configures the retry layer. Zero-value fields take
	// [DefaultRetryOptions] values.
	Retry RetryOptions

	// ETag, when non-nil, enables the conditional GET cache with the given
	// settings.
	ETag *ETagOptions

	// clock is injectable for tests; nil means wall clock. See [withClock].
	clock clock
}

// Option configures [New].
type Option func(*Options)

// WithPAT authenticates with an explicit Personal Access Token. For
// environment-based resolution see [WithAuthTokenFromEnv].
func WithPAT(token string) Option {
	return func(o *Options) { o.Token = token }
}

// WithAuthTokenFromEnv resolves the token from the first environment
// variable that is set and non-empty. Called without arguments it consults
// GITHUB_TOKEN, then GH_TOKEN. When no variable is set, [New] fails with an
// error wrapping [ErrAuthRequired] naming the variables it tried, so a
// missing token is a typed, actionable failure instead of 401s at request
// time.
func WithAuthTokenFromEnv(vars ...string) Option {
	return func(o *Options) {
		if len(vars) == 0 {
			vars = []string{DefaultTokenEnvGITHUB, DefaultTokenEnvGH}
		}

		o.TokenEnvVars = vars
	}
}

// WithBaseURL points the client at a different API root, e.g. a GitHub
// Enterprise Server ("https://github.example.com/api/v3") or an httptest
// server. A missing trailing slash is appended automatically, matching
// go-github's own WithEnterpriseURLs behavior.
func WithBaseURL(rawURL string) Option {
	return func(o *Options) { o.BaseURL = rawURL }
}

// WithHTTPClient supplies a custom base HTTP client. Its Transport becomes
// the innermost kernel layer, so custom proxies, mocks, or instruments keep
// working; its timeout is adopted unless [WithTimeout] overrides it.
func WithHTTPClient(client *http.Client) Option {
	return func(o *Options) { o.HTTPClient = client }
}

// WithUserAgent sets the client identity sent with every request, so
// consumers identify themselves to GitHub (and their traffic is visible
// in support cases) without mutating the kernel after construction.
func WithUserAgent(userAgent string) Option {
	return func(o *Options) { o.UserAgent = userAgent }
}

// WithTimeout bounds a single HTTP round trip. Zero-value options use
// [DefaultRequestTimeout]. Waits inside the kernel (rate-limit reset,
// backoff sleeps) are not bounded by this; they follow the call's context.
func WithTimeout(d time.Duration) Option {
	return func(o *Options) { o.RequestTimeout = d }
}

// WithRateLimitOptions overrides the rate-limit gate configuration.
// Zero-valued fields keep their defaults; the gate stays enabled.
func WithRateLimitOptions(opts RateLimitOptions) Option {
	return func(o *Options) {
		merged := DefaultRateLimitOptions
		if opts.MinRemaining > 0 {
			merged.MinRemaining = opts.MinRemaining
		}

		if opts.MaxWait > 0 {
			merged.MaxWait = opts.MaxWait
		}

		o.RateLimit = merged
	}
}

// WithoutRateLimit disables the pre-flight gate and lazy /rate_limit
// probes. Response headers still feed the cache, so
// [Kernel.RateLimitSnapshot] remains available for observability.
func WithoutRateLimit() Option {
	return func(o *Options) { o.RateLimit = RateLimitOptions{Enabled: false} }
}

// WithRetryOptions overrides retry behavior. Zero-valued fields keep
// their defaults; retry stays enabled.
func WithRetryOptions(opts RetryOptions) Option {
	return func(o *Options) {
		merged := DefaultRetryOptions
		if opts.MaxRetries > 0 {
			merged.MaxRetries = opts.MaxRetries
		}

		if opts.InitialBackoff > 0 {
			merged.InitialBackoff = opts.InitialBackoff
		}

		if opts.MaxBackoff > 0 {
			merged.MaxBackoff = opts.MaxBackoff
		}

		o.Retry = merged
	}
}

// WithoutRetry disables retries entirely: each call makes exactly one
// round trip.
func WithoutRetry() Option {
	return func(o *Options) { o.Retry = RetryOptions{Enabled: false} }
}

// WithETagCache enables the client-side conditional GET cache: response
// ETags are replayed as If-None-Match and 304 responses are served from
// the in-memory cache, transparently to go-github. opts may be nil for
// defaults.
func WithETagCache(opts *ETagOptions) Option {
	return func(o *Options) {
		if opts == nil {
			opts = &ETagOptions{}
		}

		o.ETag = opts
	}
}

// withClock injects time behavior for tests: a now function and a sleep
// function that must respect context cancellation.
func withClock(c clock) Option {
	return func(o *Options) { o.clock = c }
}
