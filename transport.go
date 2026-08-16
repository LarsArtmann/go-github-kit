package githubkit

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// clock abstracts time for the kernel's waits so tests run in microseconds
// instead of backoff seconds. Production kernels use wallClock.
type clock interface {
	now() time.Time
	sleep(ctx context.Context, d time.Duration) error
}

type wallClock struct{}

func (wallClock) now() time.Time { return time.Now() }

func (wallClock) sleep(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return context.Cause(ctx)
	case <-timer.C:
		return nil
	}
}

// roundTripperFunc adapts a function to http.RoundTripper, letting each
// kernel layer be expressed as one small closure-friendly type.
type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

// feedTransport reads X-RateLimit-* headers off every response and updates
// the shared cache. Sitting outside retry means every attempt — not just
// the final one — refreshes the budget, so a 429's own headers immediately
// teach the gate how long to wait.
type feedTransport struct {
	next  http.RoundTripper
	cache *RateLimitCache
}

func newFeedTransport(next http.RoundTripper, cache *RateLimitCache) http.RoundTripper {
	return feedTransport{next: next, cache: cache}
}

func (t feedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.next.RoundTrip(req)
	if resp != nil {
		if snapshot, ok := ParseRateLimitHeaders(resp.Header); ok {
			t.cache.Update(snapshot)
		}
	}

	return resp, err
}

// gateTransport blocks or rejects requests that would exceed the core
// budget, consulting the shared cache and lazily probing GET /rate_limit
// when the cache is empty. It is the outermost layer: one decision before
// any bytes hit the wire.
type gateTransport struct {
	next   http.RoundTripper
	prober *rateProber
	cache  *RateLimitCache
	opts   RateLimitOptions
	base   *url.URL
	clock  clock
}

func newGateTransport(
	next http.RoundTripper,
	prober *rateProber,
	cache *RateLimitCache,
	opts RateLimitOptions,
	base *url.URL,
	timeSource clock,
) http.RoundTripper {
	return gateTransport{
		next:   next,
		prober: prober,
		cache:  cache,
		opts:   opts,
		base:   base,
		clock:  timeSource,
	}
}

func (t gateTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if !t.opts.Enabled || isRateLimitProbe(req) {
		return t.next.RoundTrip(req)
	}

	if err := t.awaitBudget(req); err != nil {
		return nil, err
	}

	// Pessimistically spend the request now; the next response's headers
	// overwrite with the authoritative count. Without this, a burst of
	// concurrent requests would all see the same stale "remaining".
	t.cache.Decrement(1)

	return t.next.RoundTrip(req)
}

func (t gateTransport) awaitBudget(req *http.Request) error {
	snapshot, known := t.cache.Get()
	if !known {
		probed, err := t.prober.probe(req.Context(), t.base)
		if err != nil {
			// A failed probe must not block traffic: header feeding
			// corrects the picture on the first real response.
			return nil //nolint:nilerr // deliberate: probes are advisory
		}

		snapshot = probed
	}

	if snapshot.Remaining > t.opts.MinRemaining {
		return nil
	}

	wait := snapshot.ResetAt.Sub(t.clock.now())
	if wait <= 0 {
		return nil
	}

	if wait > t.opts.MaxWait {
		return &StatusError{
			Sentinel: ErrRateLimited,
			Method:   req.Method,
			URL:      req.URL.String(),
			err:      resetTooFarError{wait: wait, maxWait: t.opts.MaxWait, resetAt: snapshot.ResetAt},
		}
	}

	return t.clock.sleep(req.Context(), wait)
}

// resetTooFarError explains a gate rejection: the budget is empty and the
// reset is further away than the configured maximum wait.
type resetTooFarError struct {
	wait    time.Duration
	maxWait time.Duration
	resetAt time.Time
}

func (e resetTooFarError) Error() string {
	return "reset in " + e.wait.Round(time.Second).String() +
		" at " + e.resetAt.UTC().Format(time.RFC3339) +
		", exceeding max wait " + e.maxWait.String()
}

// retryTransport re-sends failed requests with exponential backoff.
//
// Safety rule: 429 is retried for any method because GitHub rejects the
// request before processing it, while 5xx is retried only for idempotent
// methods (GET/HEAD/OPTIONS/PUT/DELETE) — a failed POST may have taken
// effect, and silently re-sending it could double-create a resource.
// A Retry-After header, when present, overrides the computed backoff.
type retryTransport struct {
	next  http.RoundTripper
	opts  RetryOptions
	clock clock
}

func newRetryTransport(next http.RoundTripper, opts RetryOptions, timeSource clock) http.RoundTripper {
	return retryTransport{next: next, opts: opts, clock: timeSource}
}

func (t retryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if !t.opts.Enabled {
		return t.next.RoundTrip(req)
	}

	body, hasBody, err := readBody(req)
	if err != nil {
		return nil, err
	}

	backoff := t.opts.InitialBackoff

	var (
		resp  *http.Response
		rtErr error
	)

	for attempt := 0; ; attempt++ {
		if hasBody {
			req.Body = replacer(body)
		}

		resp, rtErr = t.next.RoundTrip(req)

		if attempt >= t.opts.MaxRetries || !t.shouldRetry(req, resp, rtErr) {
			break
		}

		delay := backoff
		if after := retryAfter(resp); after > delay {
			delay = after
		}

		if delay > t.opts.MaxBackoff {
			delay = t.opts.MaxBackoff
		}

		drainClose(resp)

		if sleepErr := t.clock.sleep(req.Context(), delay); sleepErr != nil {
			return nil, sleepErr
		}

		backoff *= 2
		if backoff > t.opts.MaxBackoff {
			backoff = t.opts.MaxBackoff
		}
	}

	return resp, rtErr
}

func (t retryTransport) shouldRetry(req *http.Request, resp *http.Response, err error) bool {
	if err != nil {
		// Transport errors are retried only for idempotent methods: the
		// request may or may not have reached the server.
		return isIdempotent(req.Method)
	}

	if resp == nil {
		return false
	}

	if resp.StatusCode == http.StatusTooManyRequests {
		return true
	}

	return resp.StatusCode >= http.StatusInternalServerError && isIdempotent(req.Method)
}

func isIdempotent(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodPut, http.MethodDelete:
		return true
	default:
		return false
	}
}

// retryAfter parses a Retry-After delta-seconds header, clamped to the
// retry MaxBackoff by the caller. Absent or malformed values return 0.
func retryAfter(resp *http.Response) time.Duration {
	if resp == nil {
		return 0
	}

	raw := resp.Header.Get("Retry-After")
	if raw == "" {
		return 0
	}

	seconds, err := strconv.Atoi(raw)
	if err != nil || seconds < 0 {
		return 0
	}

	return time.Duration(seconds) * time.Second
}

// readBody buffers a request body so retries can replay it, returning the
// bytes, whether a body existed, and any read error.
func readBody(req *http.Request) ([]byte, bool, error) {
	if req.Body == nil || req.Body == http.NoBody {
		return nil, false, nil
	}

	body, err := io.ReadAll(req.Body)
	closeErr := req.Body.Close()

	if err != nil {
		return nil, false, err
	}

	if closeErr != nil {
		return nil, false, closeErr
	}

	return body, true, nil
}

func replacer(body []byte) io.ReadCloser {
	return io.NopCloser(bytes.NewReader(body))
}

// drainLimitBytes caps how much of an unwanted body is drained before
// closing: enough to keep the connection reusable, bounded so a huge body
// cannot stall the retry loop.
const drainLimitBytes = 4 << 10

// drainClose empties and closes a response body so the connection can be
// reused by the next attempt.
func drainClose(resp *http.Response) {
	if resp == nil || resp.Body == nil {
		return
	}

	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, drainLimitBytes))
	_ = resp.Body.Close()
}
