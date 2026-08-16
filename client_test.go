package githubkit

import (
	"net/http"
	"testing"
	"time"
)

func TestNewDefaults(t *testing.T) {
	t.Parallel()

	kernel, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if kernel.Client == nil {
		t.Fatal("embedded *github.Client must be set")
	}

	if got := kernel.BaseURL.String(); got != "https://api.github.com/" {
		t.Errorf("BaseURL = %q, want api.github.com", got)
	}

	if kernel.rateLimit != DefaultRateLimitOptions {
		t.Errorf("rateLimit = %+v, want default", kernel.rateLimit)
	}
}

func TestNewWithPAT(t *testing.T) {
	t.Parallel()

	var seenAuth string

	server := newRecordingServer(func(w http.ResponseWriter, r *http.Request) {
		seenAuth = r.Header.Get("Authorization")
		writeJSON(w, eventsPayload("event-1"))
	})
	defer server.Close()

	kernel := newKernel(t, server.URL, nil)
	if _, _, err := kernel.Activity.ListEventsPerformedByUser(t.Context(), "octocat", false, nil); err != nil {
		t.Fatalf("fetch: %v", err)
	}

	if seenAuth != "Bearer "+testToken {
		t.Errorf("Authorization = %q", seenAuth)
	}
}

func TestNewAuthTokenFromEnv(t *testing.T) {
	// t.Setenv forbids t.Parallel.
	t.Setenv("KIT_TEST_TOKEN_A", "")
	t.Setenv("KIT_TEST_TOKEN_B", "tok-b")

	kernel, err := New(WithAuthTokenFromEnv("KIT_TEST_TOKEN_A", "KIT_TEST_TOKEN_B"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if kernel.Client == nil {
		t.Fatal("expected client")
	}

	// First SET variable wins: B is set, A is empty.
	if _, err := New(WithAuthTokenFromEnv("KIT_TEST_TOKEN_A")); err == nil {
		t.Fatal("expected error when the only candidate variable is unset")
	}
}

func TestNewAuthTokenFromEnvDefaultVars(t *testing.T) {
	// t.Setenv forbids t.Parallel.
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "gh-cli-token")

	kernel, err := New(WithAuthTokenFromEnv())
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if kernel.Client == nil {
		t.Fatal("expected client")
	}

	t.Setenv("GH_TOKEN", "")

	if _, err := New(WithAuthTokenFromEnv()); err == nil {
		t.Fatal("expected error when no default variable is set")
	}
}

func TestNewMissingTokenListsVariables(t *testing.T) {
	t.Parallel()

	_, err := New(WithAuthTokenFromEnv("ONE_MISSING", "TWO_MISSING"))
	if err == nil {
		t.Fatal("expected error")
	}

	if !errorIs(err, ErrAuthRequired) {
		t.Fatalf("expected ErrAuthRequired, got %v", err)
	}
}

func TestNewBaseURLValidation(t *testing.T) {
	t.Parallel()

	if _, err := New(WithBaseURL("://missing-scheme")); err == nil {
		t.Error("expected scheme-less URL to fail")
	}

	if _, err := New(WithBaseURL("http://:9999/no-host")); err == nil {
		t.Error("expected host-less URL to fail")
	}
}

func TestNewWithHTTPClientAdoptsTransportAndTimeout(t *testing.T) {
	t.Parallel()

	custom := &http.Client{
		Timeout: 7 * time.Second,
		Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
			return nil, http.ErrNotSupported // never called; construction only
		}),
	}

	kernel, err := New(WithHTTPClient(custom), WithBaseURL("https://api.github.com/"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if got := kernel.Client.Client().Timeout; got != 7*time.Second {
		t.Errorf("timeout = %v, want adopted 7s", got)
	}
}

func TestNewWithoutRateLimitKeepsSnapshotFeed(t *testing.T) {
	t.Parallel()

	clock := newStubClock(time.Now())
	server := newRecordingServer(func(w http.ResponseWriter, r *http.Request) {
		setRateHeaders(w, 5000, 4321, clock.futureReset(time.Hour))
		writeJSON(w, eventsPayload("event-1"))
	})
	defer server.Close()

	kernel := newKernel(t, server.URL, clock, WithoutRateLimit(), withClock(clock))

	if _, _, err := kernel.Activity.ListEventsPerformedByUser(t.Context(), "octocat", false, nil); err != nil {
		t.Fatalf("fetch: %v", err)
	}

	snapshot, known := kernel.RateLimitSnapshot()
	if !known || snapshot.Remaining != 4321 {
		t.Fatalf("snapshot = %+v known=%v; header feeding must work ungated", snapshot, known)
	}
}

func TestKernelRefreshRateLimit(t *testing.T) {
	t.Parallel()

	clock := newStubClock(time.Now())
	server := newRecordingServer(func(w http.ResponseWriter, r *http.Request) {
		setRateHeaders(w, 5000, 4711, clock.futureReset(time.Hour))
		writeJSON(w, map[string]any{"resources": map[string]any{"core": map[string]any{
			"limit": 5000, "remaining": 4711,
		}}})
	})
	defer server.Close()

	kernel := newKernel(t, server.URL, clock, WithoutRateLimit(), withClock(clock))

	snapshot, err := kernel.RefreshRateLimit(t.Context())
	if err != nil {
		t.Fatalf("RefreshRateLimit: %v", err)
	}

	if snapshot.Remaining != 4711 || snapshot.Limit != 5000 {
		t.Errorf("snapshot = %+v", snapshot)
	}

	if got := server.calls("/rate_limit"); got != 1 {
		t.Errorf("probe calls = %d, want 1", got)
	}

	if _, err = kernel.RefreshRateLimit(t.Context()); err != nil {
		t.Fatalf("second refresh (cooldown, cached) failed: %v", err)
	}

	if got := server.calls("/rate_limit"); got != 1 {
		t.Errorf("probe calls after cooldown hit = %d, want 1 (cache serves)", got)
	}
}
