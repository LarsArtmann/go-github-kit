package githubkit_test

import (
	"net/http"
	"testing"
	"time"

	githubkit "github.com/LarsArtmann/go-github-kit"
)

func BenchmarkParseRateLimitHeadersCanonical(b *testing.B) {
	header := http.Header{}
	header.Set("X-RateLimit-Limit", "5000")
	header.Set("X-RateLimit-Remaining", "4999")
	header.Set("X-RateLimit-Reset", "1767225600")

	benchmarkParse(b, header)
}

func benchmarkParse(b *testing.B, header http.Header) {
	b.Helper()
	b.ReportAllocs()

	for b.Loop() {
		if snapshot, ok := githubkit.ParseRateLimitHeaders(header); !ok {
			b.Fatalf("parse failed: %+v", snapshot)
		}
	}
}

func BenchmarkRateLimitCacheUpdateGet(b *testing.B) {
	cache := githubkit.NewRateLimitCache()
	snapshot := githubkit.RateLimitSnapshot{Limit: 5000, Remaining: 4999, ResetAt: time.Unix(1767225600, 0)}

	b.ReportAllocs()

	for b.Loop() {
		cache.Update(snapshot)
		if _, ok := cache.Get(); !ok {
			b.Fatal("cache lost a fresh update")
		}
	}
}
