package githubkit

import (
	"net/http"
	"sync"
	"time"
)

// pacingTransport spaces the START of consecutive requests at least
// interval apart, giving callers a proactive budget against GitHub's
// secondary rate limits (abuse detection on rapid-fire listing), which
// are undocumented, per-endpoint, and answered with 403 + Retry-After.
// The retry layer already waits out Retry-After reactively; pacing keeps
// the request pattern under the detection threshold in the first place.
//
// Slots are reserved under a mutex and waited out outside it: N
// concurrent requests reserve N consecutive slots (spacing stays exact)
// without serializing their waits. Every request through the kernel is
// paced — probes and retry attempts included, since both spend the same
// shared budget.
type pacingTransport struct {
	next     http.RoundTripper
	interval time.Duration
	clk      clock

	mu       sync.Mutex
	reserved time.Time // start slot already granted; the next grant follows interval later
}

func newPacingTransport(next http.RoundTripper, interval time.Duration, clk clock) http.RoundTripper {
	return &pacingTransport{next: next, interval: interval, clk: clk}
}

func (t *pacingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if t.interval <= 0 {
		return t.next.RoundTrip(req)
	}

	t.mu.Lock()
	now := t.clk.now()

	start := t.reserved
	if start.Before(now) {
		start = now
	}

	t.reserved = start.Add(t.interval)
	t.mu.Unlock()

	if wait := start.Sub(now); wait > 0 {
		if err := t.clk.sleep(req.Context(), wait); err != nil {
			// The slot stays spent — pacing is conservative by design.
			return nil, err
		}
	}

	return t.next.RoundTrip(req)
}
