package githubkit

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"
)

// stubClock freezes time for the kernel: sleeps are recorded and the clock
// advances instantly, so gate and backoff waits take microseconds.
type stubClock struct {
	mu      sync.Mutex
	current time.Time
	sleeps  []time.Duration
	cancel  bool // when true, sleep reports the context as cancelled
}

func newStubClock(start time.Time) *stubClock {
	return &stubClock{current: start}
}

func (c *stubClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.current
}

func (c *stubClock) sleep(ctx context.Context, d time.Duration) error {
	c.mu.Lock()
	c.sleeps = append(c.sleeps, d)
	c.current = c.current.Add(d)
	cancelled := c.cancel
	c.mu.Unlock()

	if cancelled {
		return context.Canceled
	}

	select {
	case <-ctx.Done():
		return context.Cause(ctx)
	default:
		return nil
	}
}

func (c *stubClock) slept() []time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()

	return append([]time.Duration(nil), c.sleeps...)
}

// futureReset returns a reset timestamp the given duration after the
// clock's current time.
func (c *stubClock) futureReset(d time.Duration) time.Time {
	return c.now().Add(d)
}

// testTokens are fake PATs; they never leave the test process.
const testToken = "ghp_test_token_12345"

// newTestServer returns an httptest server whose handler also counts calls
// and can set rate-limit headers per response.
type recordingServer struct {
	*httptest.Server

	mu    sync.Mutex
	count map[string]int
}

func newRecordingServer(handler http.HandlerFunc) *recordingServer {
	server := &recordingServer{count: map[string]int{}}

	server.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		server.mu.Lock()
		server.count[r.URL.Path+"?page="+r.URL.Query().Get("page")]++
		server.mu.Unlock()

		handler(w, r)
	}))

	return server
}

func (s *recordingServer) calls(path string) int {
	s.mu.Lock()
	defer s.mu.Unlock()

	total := 0
	for key, count := range s.count {
		if path == "" || len(key) >= len(path) && key[:len(path)] == path {
			total += count
		}
	}

	return total
}

func (s *recordingServer) totalCalls() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	total := 0
	for _, count := range s.count {
		total += count
	}

	return total
}

// writeJSON marshals v as JSON and sets the content type.
func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		panic(err)
	}
}

// setRateHeaders simulates GitHub's X-RateLimit-* family. Both spellings
// appear in the wild; tests use the canonical one.
func setRateHeaders(w http.ResponseWriter, limit, remaining int, resetAt time.Time) {
	header := w.Header()
	header.Set("X-RateLimit-Limit", strconv.Itoa(limit))
	header.Set("X-RateLimit-Remaining", strconv.Itoa(remaining))
	header.Set("X-RateLimit-Reset", strconv.FormatInt(resetAt.Unix(), 10))
}

// newKernel builds a Kernel against the test server. Tests opt into the
// gate and retry explicitly; the stub clock is always installed last.
func newKernel(
	t *testing.T,
	serverURL string,
	clock *stubClock,
	opts ...Option,
) *Kernel {
	t.Helper()

	all := append([]Option{
		WithPAT(testToken),
		WithBaseURL(serverURL + "/"),
	}, opts...)

	if clock != nil {
		all = append(all, withClock(clock))
	}

	kernel, err := New(all...)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	return kernel
}

// eventsPayload builds a minimal GitHub events list body.
func eventsPayload(ids ...string) []map[string]any {
	events := make([]map[string]any, 0, len(ids))
	for _, id := range ids {
		events = append(events, map[string]any{
			"id":   id,
			"type": "PushEvent",
			"actor": map[string]any{
				"login":      "octocat",
				"avatar_url": "https://avatars.githubusercontent.com/u/1?v=4",
			},
			"repo": map[string]any{
				"name": "octocat/Hello-World",
				"url":  "https://api.github.com/repos/octocat/Hello-World",
			},
			"created_at": "2024-01-15T10:30:00Z",
		})
	}

	return events
}
