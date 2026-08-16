package githubkit

import (
	"net/http"

	etagclient "github.com/larsartmann/go-etag/client"
)

// ETagOptions configures the client-side conditional GET cache.
//
// GitHub's REST API returns strong ETags on most GET responses. Replaying
// them as If-None-Match turns unchanged re-fetches into free 304s — one
// request spent, zero budget counted against the data rate limits — and
// the kernel serves the cached body to go-github as if it were a 200, so
// callers see no difference. The generic mechanism lives in
// github.com/larsartmann/go-etag/client; this type carries only GitHub
// policy on top of it.
type ETagOptions struct {
	// MaxEntries bounds the cache. Zero means 256. When full, the oldest
	// entry is evicted (FIFO).
	MaxEntries int

	// MaxBodyBytes is the largest response body cached. Zero means 8 MiB,
	// sized for GitHub's larger list payloads; oversized responses pass
	// through uncached with their bodies intact.
	MaxBodyBytes int
}

// DefaultETagEntries is the cache size when ETagOptions.MaxEntries is zero.
const DefaultETagEntries = 256

// defaultETagMaxBodyBytes is the largest cached body when
// ETagOptions.MaxBodyBytes is zero.
const defaultETagMaxBodyBytes = 8 << 20

const (
	headerRateLimitUsed     = "X-RateLimit-Used"
	headerRateLimitResource = "X-RateLimit-Resource"
)

// ETagStats reports conditional-cache activity. It aliases the counters of
// the underlying etagclient transport: Hits (304s served from cache),
// Stored (200s added to the cache), and Entries (currently cached
// responses).
type ETagStats = etagclient.Stats

// headerFromCache marks responses reconstructed from the ETag cache so
// tests and diagnostics can distinguish them from network 200s.
const headerFromCache = "X-Github-Kit-From-Cache"

// ETagCache is the kernel's conditional GET cache: a policy wrapper over
// etagclient.Transport with credential-scoped keys, rate-limit header
// preservation, and the kit's from-cache marker.
type ETagCache struct {
	opts      ETagOptions
	transport *etagclient.Transport
}

// NewETagCache creates a cache honoring opts (zero fields defaulted). Call
// wrap to place its transport into a RoundTripper stack; Stats reports
// counters once wrapped.
func NewETagCache(opts ETagOptions) *ETagCache {
	return &ETagCache{opts: opts, transport: nil}
}

// wrap wraps next with the conditional GET transport and retains it for
// Stats. The transport keys entries by credential fingerprint and URL, so
// rotating a token can never serve one credential's response to another.
func (c *ETagCache) wrap(next http.RoundTripper) http.RoundTripper {
	c.transport = newETagTransport(next, c.opts)

	return c.transport
}

// Stats returns current counters, zero before the first wrap.
func (c *ETagCache) Stats() ETagStats {
	if c.transport == nil {
		return ETagStats{}
	}

	return c.transport.Stats()
}

// newETagTransport builds the etagclient transport carrying GitHub policy:
// auth-scoped cache keys, rate-limit and retry-after headers preserved from
// 304s, an explicit body-size bound, and the branded from-cache marker.
func newETagTransport(next http.RoundTripper, opts ETagOptions) *etagclient.Transport {
	maxBodyBytes := opts.MaxBodyBytes
	if maxBodyBytes <= 0 {
		maxBodyBytes = defaultETagMaxBodyBytes
	}

	return etagclient.NewTransport(next, etagclient.Options{
		KeyFunc: func(req *http.Request) string {
			return authKey(req.Header) + "|" + req.URL.String()
		},
		MaxEntries:   opts.MaxEntries,
		MaxBodyBytes: maxBodyBytes,
		PreserveOn304: []string{
			headerRateLimitLimit, headerRateLimitRemaining, headerRateLimitReset,
			headerRateLimitUsed, headerRateLimitResource, "Retry-After", "Date",
		},
		FromCacheHeader: headerFromCache,
	})
}
