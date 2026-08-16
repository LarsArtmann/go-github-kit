package githubkit

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// RateLimitOptions configures the pre-flight gate. [New] starts from
// [DefaultRateLimitOptions], and [WithRateLimitOptions] overrides only the
// fields it sets to non-zero values, so partial overrides keep sane
// defaults for the rest.
type RateLimitOptions struct {
	// Enabled turns the gate on. Defaults to true; [WithoutRateLimit] is
	// the explicit opt-out.
	Enabled bool

	// MinRemaining is the budget floor: when at or below this many
	// remaining requests, the gate waits for the window to reset.
	// Defaults to 10.
	MinRemaining int

	// MaxWait bounds how long the gate may wait for a reset. A reset
	// further away fails fast with [ErrRateLimited] instead. Defaults to
	// 15 minutes.
	MaxWait time.Duration
}

// RetryOptions configures the retry layer. [New] starts from
// [DefaultRetryOptions]; [WithRetryOptions] overrides only non-zero
// fields.
type RetryOptions struct {
	// Enabled turns retry on. Defaults to true; [WithoutRetry] is the
	// explicit opt-out.
	Enabled bool

	// MaxRetries is the number of additional attempts after the first.
	// Defaults to 3.
	MaxRetries int

	// InitialBackoff is the delay before the first retry. Defaults to 1s.
	InitialBackoff time.Duration

	// MaxBackoff caps the exponential growth and any Retry-After honoring.
	// Defaults to 30s.
	MaxBackoff time.Duration
}

// probeCooldown bounds how often the gate will attempt a lazy /rate_limit
// probe while the cache stays empty (e.g. a host that strips the headers).
// Without it, every request under an unreachable probe endpoint would pay
// a failed round trip.
const probeCooldown = 30 * time.Second

// rateProber performs the lazy GET /rate_limit fetch behind a mutex with a
// cooldown, so a burst of requests with an empty cache produces one probe,
// not N.
type rateProber struct {
	next  http.RoundTripper
	cache *RateLimitCache
	token string
	clock clock

	mu          sync.Mutex
	lastAttempt time.Time
}

func newRateProber(next http.RoundTripper, cache *RateLimitCache, token string, timeSource clock) *rateProber {
	return &rateProber{next: next, cache: cache, token: token, clock: timeSource}
}

// probe fetches GET {base}/rate_limit through the feed-equipped stack, so
// the response's own X-RateLimit-* headers update the shared cache; the
// parsed snapshot is returned directly for callers that need it now.
func (p *rateProber) probe(ctx context.Context, base *url.URL) (RateLimitSnapshot, error) {
	if base == nil {
		return RateLimitSnapshot{}, fmt.Errorf("githubkit: no base URL to probe: %w", ErrAPIUnavailable)
	}

	p.mu.Lock()
	if !p.lastAttempt.IsZero() && p.clock.now().Sub(p.lastAttempt) < probeCooldown {
		p.mu.Unlock()

		if snapshot, known := p.cache.Get(); known {
			return snapshot, nil
		}

		return RateLimitSnapshot{}, fmt.Errorf(
			"githubkit: rate-limit probe on cooldown (last attempt %s ago): %w",
			p.clock.now().Sub(p.lastAttempt).Round(time.Second), ErrAPIUnavailable,
		)
	}

	p.lastAttempt = p.clock.now()
	p.mu.Unlock()

	target := *base
	target.Path = base.Path + "rate_limit"

	if base.Path == "" {
		target.Path = "rate_limit"
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return RateLimitSnapshot{}, fmt.Errorf("githubkit: build rate-limit probe: %w", err)
	}

	req.Header.Set("Accept", "application/vnd.github+json")

	if p.token != "" {
		req.Header.Set("Authorization", "Bearer "+p.token)
	}

	resp, err := p.next.RoundTrip(req)
	if err != nil {
		return RateLimitSnapshot{}, fmt.Errorf("githubkit: rate-limit probe: %w", err)
	}

	defer drainClose(resp)

	if resp.StatusCode >= http.StatusBadRequest {
		return RateLimitSnapshot{}, fmt.Errorf(
			"githubkit: rate-limit probe: status %d: %w", resp.StatusCode, ErrAPIUnavailable,
		)
	}

	snapshot, ok := p.cache.Get()
	if !ok {
		return RateLimitSnapshot{}, fmt.Errorf(
			"githubkit: rate-limit probe returned no X-RateLimit headers: %w", ErrAPIUnavailable,
		)
	}

	return snapshot, nil
}

// isRateLimitProbe recognizes probe requests so the gate never gates
// itself into recursion.
func isRateLimitProbe(req *http.Request) bool {
	return req.URL != nil && strings.HasSuffix(req.URL.Path, "/rate_limit")
}
