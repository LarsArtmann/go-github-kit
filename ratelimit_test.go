package githubkit

import (
	"net/http"
	"strconv"
	"testing"
	"time"
)

func TestParseRateLimitHeaders(t *testing.T) {
	t.Parallel()

	reset := time.Now().Add(time.Hour).UTC().Truncate(time.Second)

	tests := []struct {
		name      string
		limit     string
		remaining string
		reset     string
		want      RateLimitSnapshot
		wantOK    bool
	}{
		{
			name:      "all present",
			limit:     "5000",
			remaining: "4999",
			reset:     itoa(reset.Unix()),
			want:      RateLimitSnapshot{Limit: 5000, Remaining: 4999, ResetAt: reset},
			wantOK:    true,
		},
		{
			name:      "missing limit",
			remaining: "10",
			wantOK:    false,
		},
		{
			name:   "missing remaining",
			limit:  "10",
			wantOK: false,
		},
		{
			name:      "malformed limit",
			limit:     "five-thousand",
			remaining: "10",
			wantOK:    false,
		},
		{
			name:      "negative remaining",
			limit:     "5000",
			remaining: "-3",
			wantOK:    false,
		},
		{
			name:      "garbage reset tolerated",
			limit:     "5000",
			remaining: "10",
			reset:     "next-tuesday",
			want:      RateLimitSnapshot{Limit: 5000, Remaining: 10},
			wantOK:    true,
		},
		{
			name:      "float reset tolerated",
			limit:     "5000",
			remaining: "10",
			reset:     "17.5",
			want:      RateLimitSnapshot{Limit: 5000, Remaining: 10},
			wantOK:    true,
		},
		{
			name:      "overflow limit",
			limit:     "99999999999999999999",
			remaining: "10",
			wantOK:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			header := http.Header{}
			setNonEmpty(header, "X-Ratelimit-Limit", tt.limit)
			setNonEmpty(header, "X-Ratelimit-Remaining", tt.remaining)
			setNonEmpty(header, "X-Ratelimit-Reset", tt.reset)

			got, ok := ParseRateLimitHeaders(header)

			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}

			if !tt.wantOK {
				return
			}

			if got.Limit != tt.want.Limit || got.Remaining != tt.want.Remaining {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}

			if !tt.want.ResetAt.IsZero() && !got.ResetAt.Equal(tt.want.ResetAt) {
				t.Errorf("ResetAt = %v, want %v", got.ResetAt, tt.want.ResetAt)
			}
		})
	}
}

func TestParseRateLimitHeadersCanonicalCasing(t *testing.T) {
	t.Parallel()

	reset := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	header := http.Header{}
	header.Set("X-RateLimit-Limit", "5000")
	header.Set("X-RateLimit-Remaining", "42")
	header.Set("X-RateLimit-Reset", itoa(reset.Unix()))

	got, ok := ParseRateLimitHeaders(header)
	if !ok {
		t.Fatal("expected ok with canonical casing")
	}

	if got.Limit != 5000 || got.Remaining != 42 || !got.ResetAt.Equal(reset) {
		t.Errorf("got %+v", got)
	}
}

func TestRateLimitCacheUpdateGetDecrement(t *testing.T) {
	t.Parallel()

	cache := NewRateLimitCache()

	if _, known := cache.Get(); known {
		t.Fatal("empty cache should be unknown")
	}

	cache.Update(RateLimitSnapshot{Limit: 0, Remaining: 5})
	if _, known := cache.Get(); known {
		t.Fatal("zero-limit snapshot must be ignored")
	}

	cache.Update(RateLimitSnapshot{Limit: 100, Remaining: 10, ResetAt: time.Now().Add(time.Hour)})

	snapshot, known := cache.Get()
	if !known || snapshot.Limit != 100 || snapshot.Remaining != 10 {
		t.Fatalf("got %+v known=%v", snapshot, known)
	}

	cache.Decrement(3)

	snapshot, _ = cache.Get()
	if snapshot.Remaining != 7 {
		t.Errorf("Remaining after decrement = %d, want 7", snapshot.Remaining)
	}

	cache.Decrement(100)

	snapshot, _ = cache.Get()
	if snapshot.Remaining != 0 {
		t.Errorf("Remaining floors at zero, got %d", snapshot.Remaining)
	}

	cache.Decrement(-5) // no-op, must not panic or resurrect budget

	snapshot, _ = cache.Get()
	if snapshot.Remaining != 0 {
		t.Errorf("negative decrement is a no-op, got %d", snapshot.Remaining)
	}
}

func TestRateLimitCacheUpdateOverwritesDecay(t *testing.T) {
	t.Parallel()

	cache := NewRateLimitCache()
	cache.Decrement(10)
	if _, known := cache.Get(); known {
		t.Fatal("decrement on unknown cache must not fabricate data")
	}

	cache.Update(RateLimitSnapshot{Limit: 50, Remaining: 50, ResetAt: time.Now().Add(time.Hour)})
	cache.Decrement(49)
	cache.Update(RateLimitSnapshot{Limit: 50, Remaining: 50, ResetAt: time.Now().Add(time.Hour)})

	snapshot, _ := cache.Get()
	if snapshot.Remaining != 50 {
		t.Errorf("authoritative update must overwrite decay, got %d", snapshot.Remaining)
	}
}

func setNonEmpty(header http.Header, key, value string) {
	if value != "" {
		header.Set(key, value)
	}
}

func itoa(v int64) string {
	return strconv.FormatInt(v, 10)
}
