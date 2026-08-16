package githubkit

import (
	"strconv"
	"sync"
	"time"
)

// RateLimitSnapshot is the core request budget for one window, as GitHub
// reports it in X-RateLimit-* response headers.
type RateLimitSnapshot struct {
	// Limit is the total requests allowed per window.
	Limit int
	// Remaining is the requests left in the current window.
	Remaining int
	// ResetAt is when the window resets.
	ResetAt time.Time
}

// Header names GitHub sets on (nearly) every API response. The kit reads
// these instead of calling GET /rate_limit, which would itself spend
// budget: every response updates the cache for free.
const (
	headerRateLimitLimit     = "X-Ratelimit-Limit"
	headerRateLimitRemaining = "X-Ratelimit-Remaining"
	headerRateLimitReset     = "X-Ratelimit-Reset"
)

// ParseRateLimitHeaders extracts the core budget from response headers.
// GitHub spells the header family "X-Ratelimit-*" (lowercase "l"), and
// both spellings are accepted because proxies in the wild have been seen
// normalizing the casing. ok is false when Limit or Remaining is absent or
// unparseable — callers treat that as "no information", never as zero
// budget, because acting on a misread header would stall healthy traffic.
func ParseRateLimitHeaders(header rateLimitHeaderSource) (RateLimitSnapshot, bool) {
	limit, okLimit := parseHeaderInt(header, headerRateLimitLimit)
	remaining, okRemaining := parseHeaderInt(header, headerRateLimitRemaining)

	if !okLimit || !okRemaining {
		return RateLimitSnapshot{}, false
	}

	snapshot := RateLimitSnapshot{Limit: limit, Remaining: remaining}

	if reset, okReset := parseHeaderInt(header, headerRateLimitReset); okReset {
		snapshot.ResetAt = time.Unix(int64(reset), 0).UTC()
	}

	return snapshot, true
}

// rateLimitHeaderSource is the slice of http.Header the parser needs; an
// interface keeps the function testable without building responses.
type rateLimitHeaderSource interface {
	Get(key string) string
}

func parseHeaderInt(header rateLimitHeaderSource, key string) (int, bool) {
	raw := header.Get(key)
	if raw == "" {
		raw = header.Get(canonicalHeader(key))
	}

	if raw == "" {
		return 0, false
	}

	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 {
		return 0, false
	}

	return value, true
}

func canonicalHeader(key string) string {
	switch key {
	case headerRateLimitLimit:
		return "X-RateLimit-Limit"
	case headerRateLimitRemaining:
		return "X-RateLimit-Remaining"
	case headerRateLimitReset:
		return "X-RateLimit-Reset"
	default:
		return key
	}
}

// RateLimitCache stores the last observed core budget. It is safe for
// concurrent use and shared by every layer of one Kernel.
//
// The zero value is not usable; construct via [NewRateLimitCache].
type RateLimitCache struct {
	mu       sync.Mutex
	snapshot RateLimitSnapshot
	known    bool
}

// NewRateLimitCache creates an empty cache.
func NewRateLimitCache() *RateLimitCache {
	return &RateLimitCache{}
}

// Update stores the authoritative budget from an API response. Snapshots
// with a zero Limit are ignored: GitHub sends them only on responses that
// do not participate in the core budget (e.g. redirects), and overwriting
// good data with them would make the gate fly blind.
func (c *RateLimitCache) Update(snapshot RateLimitSnapshot) {
	if c == nil || snapshot.Limit == 0 {
		return
	}

	c.mu.Lock()
	c.snapshot = snapshot
	c.known = true
	c.mu.Unlock()
}

// Decrement subtracts n from the remaining count after dispatching
// requests, keeping the estimate conservative between authoritative
// responses. It can only shrink the value to zero.
func (c *RateLimitCache) Decrement(n int) {
	if c == nil || n <= 0 {
		return
	}

	c.mu.Lock()
	if c.known {
		c.snapshot.Remaining -= n
		if c.snapshot.Remaining < 0 {
			c.snapshot.Remaining = 0
		}
	}
	c.mu.Unlock()
}

// Get returns the cached budget and whether one exists.
func (c *RateLimitCache) Get() (RateLimitSnapshot, bool) {
	if c == nil {
		return RateLimitSnapshot{}, false
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	return c.snapshot, c.known
}
