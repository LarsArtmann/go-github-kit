package githubkit

import (
	"bytes"
	"io"
	"net/http"
	"sync"
)

// ETagOptions configures the client-side conditional GET cache.
//
// GitHub's REST API returns strong ETags on most GET responses. Replaying
// them as If-None-Match turns unchanged re-fetches into free 304s — one
// request spent, zero budget counted against the data rate limits — and
// the kernel serves the cached body to go-github as if it were a 200, so
// callers see no difference. ETags are treated as opaque strings by design:
// this is a client cache, not a validation framework (which is why
// server-side ETag middleware like go-etag is not a dependency here).
type ETagOptions struct {
	// MaxEntries bounds the cache. Zero means 256. When full, the oldest
	// entry is evicted (FIFO).
	MaxEntries int
}

// DefaultETagEntries is the cache size when ETagOptions.MaxEntries is zero.
const DefaultETagEntries = 256

// ETagStats reports conditional-cache activity.
type ETagStats struct {
	// Hits is the number of 304 responses served from cache.
	Hits int64
	// Stored is the number of 200 responses added to the cache.
	Stored int64
	// Entries is the current number of cached responses.
	Entries int
}

// headerFromCache marks responses reconstructed from the ETag cache so
// tests and diagnostics can distinguish them from network 200s.
const headerFromCache = "X-Github-Kit-From-Cache"

type etagEntry struct {
	etag   string
	status int
	header http.Header
	body   []byte
}

// ETagCache is an in-memory conditional GET store, safe for concurrent
// use. Keys include a fingerprint of the Authorization header, so rotating
// a token can never serve one credential's response to another.
type ETagCache struct {
	mu      sync.Mutex
	entries map[string]etagEntry
	order   []string
	max     int
	hits    int64
	stored  int64
}

// NewETagCache creates a cache honoring opts (zero fields defaulted).
func NewETagCache(opts ETagOptions) *ETagCache {
	capacity := opts.MaxEntries
	if capacity <= 0 {
		capacity = DefaultETagEntries
	}

	return &ETagCache{entries: make(map[string]etagEntry, capacity), max: capacity}
}

func (c *ETagCache) get(key string) (etagEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	entry, ok := c.entries[key]

	return entry, ok
}

func (c *ETagCache) set(key string, entry etagEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if _, exists := c.entries[key]; !exists {
		c.order = append(c.order, key)

		for len(c.order) > c.max {
			oldest := c.order[0]
			c.order = c.order[1:]
			delete(c.entries, oldest)
		}
	}

	c.entries[key] = entry
	c.stored++
}

func (c *ETagCache) countHit() {
	c.mu.Lock()
	c.hits++
	c.mu.Unlock()
}

// Stats returns current counters.
func (c *ETagCache) Stats() ETagStats {
	c.mu.Lock()
	defer c.mu.Unlock()

	return ETagStats{Hits: c.hits, Stored: c.stored, Entries: len(c.entries)}
}

// etagTransport implements conditional GETs against the cache: requests
// carry If-None-Match when a validator is known, 304 responses are
// rebuilt as 200s from the cached body, and fresh 200s with ETags are
// stored for next time. Non-GET methods pass through untouched.
type etagTransport struct {
	next  http.RoundTripper
	cache *ETagCache
}

func newETagTransport(next http.RoundTripper, cache *ETagCache) http.RoundTripper {
	return etagTransport{next: next, cache: cache}
}

func (t etagTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method != http.MethodGet {
		return t.next.RoundTrip(req)
	}

	key := cacheKey(req)

	entry, cached := t.cache.get(key)
	if cached {
		req.Header.Set("If-None-Match", entry.etag)
	}

	resp, err := t.next.RoundTrip(req)
	if err != nil || resp == nil {
		return resp, err
	}

	switch {
	case resp.StatusCode == http.StatusNotModified && cached:
		return t.rebuildFromCache(resp, entry), nil

	case resp.StatusCode == http.StatusOK:
		if etag := resp.Header.Get("ETag"); etag != "" {
			if t.store(resp, key, etag) != nil {
				// Storing must never break the caller's response.
				return resp, nil //nolint:nilerr // deliberate: store failure is a no-op
			}
		}
	}

	return resp, nil
}

// rebuildFromCache synthesizes the 200 the caller's SDK expects: cached
// body and content headers, merged with the 304's fresh rate-limit and
// retry-after headers so upstream layers still learn from the revalidation.
func (t etagTransport) rebuildFromCache(
	notModified *http.Response,
	entry etagEntry,
) *http.Response {
	t.cache.countHit()

	drainClose(notModified)

	header := entry.header.Clone()
	if header == nil {
		header = make(http.Header)
	}

	for _, name := range []string{
		headerRateLimitLimit, headerRateLimitRemaining, headerRateLimitReset,
		"X-RateLimit-Used", "X-RateLimit-Resource", "Retry-After", "Date",
	} {
		if values, ok := notModified.Header[name]; ok {
			header[name] = values
		} else if canonical := canonicalHeader(name); len(notModified.Header.Get(canonical)) > 0 {
			header[canonical] = notModified.Header.Values(canonical)
		}
	}

	header.Set(headerFromCache, "1")

	return &http.Response{
		Status:        "200 OK",
		StatusCode:    http.StatusOK,
		Proto:         notModified.Proto,
		ProtoMajor:    notModified.ProtoMajor,
		ProtoMinor:    notModified.ProtoMinor,
		Header:        header,
		Body:          io.NopCloser(bytes.NewReader(entry.body)),
		ContentLength: int64(len(entry.body)),
		Request:       notModified.Request,
	}
}

// store reads the response body into the cache and hands the caller a
// re-readable copy. Only responses the SDK could fully buffer are cached:
// a body that fails to read is left for the caller to surface.
func (t etagTransport) store(
	resp *http.Response,
	key, etag string,
) error {
	body, err := io.ReadAll(resp.Body)
	closeErr := resp.Body.Close()

	if err != nil {
		return err // caller treats store failure as a no-op
	}

	if closeErr != nil {
		return closeErr
	}

	header := resp.Header.Clone()
	if header == nil {
		header = make(http.Header)
	}

	header.Del(headerFromCache)

	t.cache.set(key, etagEntry{
		etag:   etag,
		status: resp.StatusCode,
		header: header,
		body:   body,
	})

	resp.Body = io.NopCloser(bytes.NewReader(body))
	resp.ContentLength = int64(len(body))

	return nil
}

// cacheKey identifies a cacheable response: method (always GET here), URL,
// and credential fingerprint.
func cacheKey(req *http.Request) string {
	return authKey(req.Header) + "|" + req.URL.String()
}
