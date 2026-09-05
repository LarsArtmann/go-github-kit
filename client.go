package githubkit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	gh "github.com/google/go-github/v69/github"
)

// errBaseURLNoHost rejects host-less base URLs before the first request.
var errBaseURLNoHost = errors.New("githubkit: base URL has no host")

// Kernel is a go-github client plus the kit's shared state. It embeds
// [*github.Client], so every native SDK method works directly on a Kernel,
// and code written against *github.Client compiles unchanged when handed
// k.Client.
//
// A Kernel is safe for concurrent use. Construct with [New].
type Kernel struct {
	*gh.Client

	rateCache *RateLimitCache
	etagCache *ETagCache
	prober    *rateProber
	rateLimit RateLimitOptions
	clock     clock
}

// New builds a Kernel: a native go-github client whose HTTP stack applies,
// outermost first, the rate-limit gate, header-fed budget tracking, retry
// with backoff, and (optionally) ETag conditional caching, over a tuned
// base transport.
//
// By default the gate is on (wait up to 15m when ≤10 requests remain,
// otherwise fail fast with [ErrRateLimited]) and retry is on (3 retries,
// 1s→30s exponential backoff on 429 and 5xx for idempotent requests).
// Authentication is opt-in via [WithPAT] or [WithAuthTokenFromEnv].
func New(opts ...Option) (*Kernel, error) {
	options := resolveDefaults(opts)

	token, err := resolveToken(options)
	if err != nil {
		return nil, err
	}

	baseURL, err := resolveBaseURL(options.BaseURL)
	if err != nil {
		return nil, err
	}

	cache := NewRateLimitCache()

	var etagCache *ETagCache

	base := baseTransport(options)

	// Probe stack (used for lazy /rate_limit fetches) deliberately excludes
	// the gate — a probing gate would recurse — and the ETag cache — a
	// cached probe answer would masquerade as fresh budget. It keeps feed
	// and retry so probes refresh shared state and survive transient 5xx.
	probeStack := newFeedTransport(base, cache)
	probeStack = newRetryTransport(probeStack, options.Retry, options.clock)
	prober := newRateProber(probeStack, cache, token, options.clock)

	stack := base

	if options.ETag != nil {
		etagCache = NewETagCache(*options.ETag)
		stack = etagCache.wrap(stack)
	}

	stack = newRetryTransport(stack, options.Retry, options.clock)
	stack = newFeedTransport(stack, cache)
	stack = newGateTransport(stack, prober, cache, options.RateLimit, baseURL, options.clock)

	httpClient := &http.Client{
		Transport: stack,
		Timeout:   resolveTimeout(options),
	}

	ghClient := gh.NewClient(httpClient)
	if token != "" {
		ghClient = ghClient.WithAuthToken(token)
	}

	if baseURL != nil {
		ghClient.BaseURL = baseURL
	}

	if options.UserAgent != "" {
		ghClient.UserAgent = options.UserAgent
	}

	return &Kernel{
		Client:    ghClient,
		rateCache: cache,
		etagCache: etagCache,
		prober:    prober,
		rateLimit: options.RateLimit,
		clock:     options.clock,
	}, nil
}

// RateLimitSnapshot returns the freshest known core budget, fed from the
// X-RateLimit-* headers of every response the Kernel has seen. The second
// return is false when nothing has been observed yet.
func (k *Kernel) RateLimitSnapshot() (RateLimitSnapshot, bool) {
	return k.rateCache.Get()
}

// RefreshRateLimit forces a GET /rate_limit round trip against the client's
// base URL and returns the resulting budget. The gate performs this probe
// automatically when it has no cached data; consumers may call it to warm
// the cache before a burst.
func (k *Kernel) RefreshRateLimit(ctx context.Context) (RateLimitSnapshot, error) {
	return k.prober.probe(ctx, k.BaseURL)
}

// ETagStats reports conditional-cache counters: hits (304 revalidations
// served from cache), misses (requests sent with a validator), and stored
// entries. The second return is false when [WithETagCache] was not used.
func (k *Kernel) ETagStats() (ETagStats, bool) {
	if k.etagCache == nil {
		return ETagStats{}, false
	}

	return k.etagCache.Stats(), true
}

func resolveDefaults(opts []Option) Options {
	options := Options{
		RequestTimeout: DefaultRequestTimeout,
		RateLimit:      DefaultRateLimitOptions,
		Retry:          DefaultRetryOptions,
		clock:          wallClock{},
	}

	for _, opt := range opts {
		if opt != nil {
			opt(&options)
		}
	}

	if options.clock == nil {
		options.clock = wallClock{}
	}

	return options
}

func resolveToken(options Options) (string, error) {
	if options.Token != "" {
		return options.Token, nil
	}

	if len(options.TokenEnvVars) == 0 {
		return "", nil
	}

	for _, name := range options.TokenEnvVars {
		if value := os.Getenv(name); value != "" {
			return value, nil
		}
	}

	return "", fmt.Errorf(
		"githubkit: none of the token environment variables is set (%s): %w",
		joinQuoted(options.TokenEnvVars),
		ErrAuthRequired,
	)
}

func joinQuoted(values []string) string {
	var out strings.Builder

	for i, value := range values {
		if i > 0 {
			out.WriteString(", ")
		}

		out.WriteString(strconv.Quote(value))
	}

	return out.String()
}

// resolveBaseURL parses and validates the configured API root, applying
// the trailing slash the native SDK's relative-URL resolution needs. An
// empty configuration yields the public api.github.com root.
func resolveBaseURL(raw string) (*url.URL, error) {
	if raw == "" {
		parsed, err := url.Parse("https://api.github.com/")
		if err != nil {
			return nil, fmt.Errorf("githubkit: parse default base URL: %w", err)
		}

		return parsed, nil
	}

	parsed, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("githubkit: parse base URL %q: %w", raw, err)
	}

	if parsed.Hostname() == "" {
		return nil, fmt.Errorf("%w (%q)", errBaseURLNoHost, raw)
	}

	if !strings.HasSuffix(parsed.Path, "/") {
		parsed.Path += "/"
	}

	return parsed, nil
}

// resolveTimeout prefers an explicit [WithTimeout]; otherwise a custom
// HTTPClient's own timeout is adopted; otherwise the default.
func resolveTimeout(options Options) time.Duration {
	if options.RequestTimeout != 0 && options.RequestTimeout != DefaultRequestTimeout {
		return options.RequestTimeout
	}

	if options.HTTPClient != nil && options.HTTPClient.Timeout > 0 {
		return options.HTTPClient.Timeout
	}

	return DefaultRequestTimeout
}

// Transport tuning for production GitHub traffic: many small JSON
// requests to one host.
const (
	defaultMaxIdleConns        = 100
	defaultMaxIdleConnsPerHost = 10
	defaultIdleConnTimeout     = 90 * time.Second
	defaultTLSHandshakeTimeout = 10 * time.Second
	defaultExpectContinueWait  = 1 * time.Second
)

// baseTransport returns the innermost layer: either the caller's transport
// or a tuned default modeled on production GitHub traffic (many small JSON
// requests to one host).
func baseTransport(options Options) http.RoundTripper {
	if options.HTTPClient != nil && options.HTTPClient.Transport != nil {
		return options.HTTPClient.Transport
	}

	return &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		MaxIdleConns:          defaultMaxIdleConns,
		MaxIdleConnsPerHost:   defaultMaxIdleConnsPerHost,
		IdleConnTimeout:       defaultIdleConnTimeout,
		TLSHandshakeTimeout:   defaultTLSHandshakeTimeout,
		ExpectContinueTimeout: defaultExpectContinueWait,
	}
}

// authKey fingerprints the Authorization header so the ETag cache never
// serves one credential's response to another. Empty auth is its own key.
func authKey(header http.Header) string {
	value := header.Get("Authorization")
	if value == "" {
		return ""
	}

	sum := sha256.Sum256([]byte(value))

	return hex.EncodeToString(sum[:8])
}
