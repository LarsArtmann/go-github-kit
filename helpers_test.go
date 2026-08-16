package githubkit_test

import (
	"encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"time"

	githubkit "github.com/LarsArtmann/go-github-kit"
)

// bddToken is a fake PAT; it never leaves the test process.
const bddToken = "ghp_bdd_token_67890"

// fakeGitHub is an httptest server standing in for api.github.com. It counts
// requests, answers the lazy /rate_limit probe, requires a bearer token, and
// stamps healthy rate-limit headers on every response unless a spec overrides
// the budget.
type fakeGitHub struct {
	*httptest.Server

	mu       sync.Mutex
	requests map[string]int // "path|page" -> count
	budget   func() (limit, remaining int, resetAt time.Time)
}

// startFakeGitHub launches the server; stop it with DeferCleanup(fake.Close).
// handle receives every non-probe request after auth checking.
func startFakeGitHub(handle http.HandlerFunc) *fakeGitHub {
	fake := &fakeGitHub{
		requests: map[string]int{},
		budget:   func() (int, int, time.Time) { return 5000, 4999, time.Now().Add(time.Hour) },
	}

	fake.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fake.mu.Lock()
		fake.requests[r.URL.Path+"|"+r.URL.Query().Get("page")]++
		limit, remaining, resetAt := fake.budget()
		fake.mu.Unlock()

		if r.URL.Path == "/rate_limit" {
			writeRateHeaders(w, limit, remaining, resetAt)
			w.WriteHeader(http.StatusOK)
			return
		}

		if got := r.Header.Get("Authorization"); got != "Bearer "+bddToken {
			writeJSONError(w, http.StatusUnauthorized, "Requires authentication")
			return
		}

		writeRateHeaders(w, limit, remaining, resetAt)
		handle(w, r)
	}))

	return fake
}

// setBudget overrides the rate headers every response carries.
func (f *fakeGitHub) setBudget(limit, remaining int, resetAt time.Time) {
	f.mu.Lock()
	f.budget = func() (int, int, time.Time) { return limit, remaining, resetAt }
	f.mu.Unlock()
}

// count reports how many requests hit the path (any page).
func (f *fakeGitHub) count(path string) int {
	f.mu.Lock()
	defer f.mu.Unlock()

	total := 0
	for key, count := range f.requests {
		if len(key) > len(path) && key[:len(path)] == path && key[len(path)] == '|' {
			total += count
		}
	}

	return total
}

// pageSeen reports whether the given page parameter was ever requested.
func (f *fakeGitHub) pageSeen(path string, page int) bool {
	f.mu.Lock()
	defer f.mu.Unlock()

	_, ok := f.requests[path+"|"+strconv.Itoa(page)]
	return ok
}

// writeRateHeaders simulates GitHub's X-RateLimit-* family.
func writeRateHeaders(w http.ResponseWriter, limit, remaining int, resetAt time.Time) {
	header := w.Header()
	header.Set("X-RateLimit-Limit", strconv.Itoa(limit))
	header.Set("X-RateLimit-Remaining", strconv.Itoa(remaining))
	header.Set("X-RateLimit-Reset", strconv.FormatInt(resetAt.Unix(), 10))
}

// writeJSONError replies the way GitHub does.
func writeJSONError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(`{"message":` + strconv.Quote(message) + `}`))
}

// eventsPage renders a minimal GitHub events list body.
func eventsPage(ids ...string) []map[string]any {
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

// writeUser renders the authenticated-user payload.
func writeUser(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.MarshalWrite(w, map[string]any{
		"login": "octocat",
		"id":    583231,
	})
}

// writeEvents renders an events list payload.
func writeEvents(w http.ResponseWriter, events []map[string]any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.MarshalWrite(w, events)
}

// newBDDKernel builds a Kernel against a test server the way a consumer
// would: PAT plus base URL.
func newBDDKernel(serverURL string, opts ...githubkit.Option) (*githubkit.Kernel, error) {
	all := append([]githubkit.Option{
		githubkit.WithPAT(bddToken),
		githubkit.WithBaseURL(serverURL),
	}, opts...)

	return githubkit.New(all...)
}
