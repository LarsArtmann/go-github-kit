package githubkit

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestGateWaitsUntilReset(t *testing.T) {
	t.Parallel()

	clock := newStubClock(time.Now())
	server := newRecordingServer(func(w http.ResponseWriter, r *http.Request) {
		if isRateLimitProbe(r) {
			writeJSON(w, map[string]any{}) // probe carries no headers: stays unknown

			return
		}

		setRateHeaders(w, 5000, 5, clock.futureReset(90*time.Minute))
		writeJSON(w, eventsPayload("event-1"))
	})

	kernel := newKernel(t, server.URL, clock,
		WithRateLimitOptions(RateLimitOptions{MinRemaining: 10, MaxWait: 2 * time.Hour}),
		withClock(clock),
	)
	defer server.Close()

	// Prime the cache with a tight budget.
	if _, _, err := kernel.Activity.ListEventsPerformedByUser(
		t.Context(),
		"octocat",
		false,
		nil,
	); err != nil {
		t.Fatalf("prime fetch: %v", err)
	}

	if _, _, err := kernel.Activity.ListEventsPerformedByUser(t.Context(), "octocat", false, nil); err != nil {
		t.Fatalf("gated fetch: %v", err)
	}

	slept := clock.slept()
	if len(slept) == 0 {
		t.Fatal("expected the gate to wait for the reset")
	}

	if slept[0] < time.Hour {
		t.Errorf("gate waited %v, expected ~90m", slept[0])
	}
}

func TestGateFailsFastBeyondMaxWait(t *testing.T) {
	t.Parallel()

	clock := newStubClock(time.Now())
	server := newRecordingServer(func(w http.ResponseWriter, r *http.Request) {
		if isRateLimitProbe(r) {
			writeJSON(w, map[string]any{})

			return
		}

		setRateHeaders(w, 5000, 5, clock.futureReset(8*time.Hour))
		writeJSON(w, eventsPayload("event-1"))
	})

	kernel := newKernel(t, server.URL, clock,
		WithRateLimitOptions(RateLimitOptions{MinRemaining: 10, MaxWait: 15 * time.Minute}),
		withClock(clock),
	)
	defer server.Close()

	if _, _, err := kernel.Activity.ListEventsPerformedByUser(t.Context(), "octocat", false, nil); err != nil {
		t.Fatalf("first fetch should succeed: %v", err)
	}

	_, _, err := kernel.Activity.ListEventsPerformedByUser(t.Context(), "octocat", false, nil)
	if err == nil {
		t.Fatal("expected gate rejection")
	}

	if !errors.Is(err, ErrRateLimited) {
		t.Fatalf("expected ErrRateLimited, got %v", err)
	}

	if len(clock.slept()) != 0 {
		t.Errorf("gate must not sleep beyond MaxWait, slept %v", clock.slept())
	}
}

func TestGateDisabledNeverBlocks(t *testing.T) {
	t.Parallel()

	clock := newStubClock(time.Now())
	server := newRecordingServer(func(w http.ResponseWriter, r *http.Request) {
		setRateHeaders(w, 5000, 1, clock.futureReset(24*time.Hour))
		writeJSON(w, eventsPayload("event-1"))
	})

	kernel := newKernel(t, server.URL, clock, WithoutRateLimit(), withClock(clock))
	defer server.Close()

	// Remaining stays at 1: go-github's own pre-flight only refuses at
	// exactly zero, and this test asserts the kit gate stays out of the way.
	for range 3 {
		if _, _, err := kernel.Activity.ListEventsPerformedByUser(
			t.Context(),
			"octocat",
			false,
			nil,
		); err != nil {
			t.Fatalf("ungated fetch: %v", err)
		}
	}

	if sleeps := clock.slept(); len(sleeps) > 0 {
		t.Errorf("disabled gate slept %v", sleeps)
	}
}

func TestGateContextCancelDuringWait(t *testing.T) {
	t.Parallel()

	clock := newStubClock(time.Now())
	server := newRecordingServer(func(w http.ResponseWriter, r *http.Request) {
		if isRateLimitProbe(r) {
			writeJSON(w, map[string]any{})

			return
		}

		setRateHeaders(w, 5000, 1, clock.futureReset(10*time.Hour))
		writeJSON(w, eventsPayload("event-1"))
	})

	kernel := newKernel(t, server.URL, clock,
		WithRateLimitOptions(RateLimitOptions{MinRemaining: 10, MaxWait: 20 * time.Hour}),
		withClock(clock),
	)
	defer server.Close()

	if _, _, err := kernel.Activity.ListEventsPerformedByUser(
		t.Context(),
		"octocat",
		false,
		nil,
	); err != nil {
		t.Fatalf("prime: %v", err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, _, err := kernel.Activity.ListEventsPerformedByUser(ctx, "octocat", false, nil)
	if err == nil {
		t.Fatal("expected cancellation to surface")
	}
}

func TestRetryOnServerErrorWithBackoff(t *testing.T) {
	t.Parallel()

	clock := newStubClock(time.Now())

	var attempts atomic.Int64

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if attempts.Add(1) < 3 {
			w.WriteHeader(http.StatusBadGateway)

			return
		}

		setRateHeaders(w, 5000, 4999, clock.futureReset(time.Hour))
		writeJSON(w, eventsPayload("event-1"))
	}))
	defer server.Close()

	kernel := newKernel(t, server.URL, clock, WithoutRateLimit(),
		WithRetryOptions(RetryOptions{InitialBackoff: time.Second, MaxRetries: 3}),
		withClock(clock),
	)

	if _, _, err := kernel.Activity.ListEventsPerformedByUser(t.Context(), "octocat", false, nil); err != nil {
		t.Fatalf("expected success after retries: %v", err)
	}

	if got := attempts.Load(); got != 3 {
		t.Errorf("attempts = %d, want 3", got)
	}

	slept := clock.slept()
	if len(slept) < 2 {
		t.Fatalf("expected backoff sleeps, got %v", slept)
	}

	if slept[0] != time.Second {
		t.Errorf("first backoff = %v, want 1s", slept[0])
	}

	if slept[1] != 2*time.Second {
		t.Errorf("second backoff = %v, want 2s", slept[1])
	}
}

func TestNoRetryOnClientError(t *testing.T) {
	t.Parallel()

	clock := newStubClock(time.Now())

	var attempts atomic.Int64

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer server.Close()

	kernel := newKernel(t, server.URL, clock, WithoutRateLimit(), withClock(clock))

	_, _, err := kernel.Activity.ListEventsPerformedByUser(t.Context(), "octocat", false, nil)
	if err == nil {
		t.Fatal("expected 400 error")
	}

	if got := attempts.Load(); got != 1 {
		t.Errorf("400 must not be retried, attempts = %d", got)
	}
}

func TestNoRetryPostOnServerError(t *testing.T) {
	t.Parallel()

	clock := newStubClock(time.Now())

	var attempts atomic.Int64

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	kernel := newKernel(t, server.URL, clock, WithoutRateLimit(), withClock(clock))

	request, err := http.NewRequestWithContext(
		t.Context(),
		http.MethodPost,
		server.URL+"/repos/octocat/Hello-World/issues",
		bytes.NewBufferString(`{"title":"x"}`),
	)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}

	resp, err := kernel.Client.Client().Do(request)
	if err != nil {
		t.Fatalf("round trip: %v", err)
	}

	defer func() { _ = resp.Body.Close() }()

	if got := attempts.Load(); got != 1 {
		t.Errorf("POST 5xx must not be retried, attempts = %d", got)
	}
}

func TestRetryHonorsRetryAfterHeader(t *testing.T) {
	t.Parallel()

	clock := newStubClock(time.Now())

	var attempts atomic.Int64

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if attempts.Add(1) < 2 {
			w.Header().Set("Retry-After", "7")
			w.WriteHeader(http.StatusTooManyRequests)

			return
		}

		writeJSON(w, eventsPayload("event-1"))
	}))
	defer server.Close()

	kernel := newKernel(t, server.URL, clock, WithoutRateLimit(),
		WithRetryOptions(RetryOptions{InitialBackoff: time.Second, MaxRetries: 2}),
		withClock(clock),
	)

	if _, _, err := kernel.Activity.ListEventsPerformedByUser(t.Context(), "octocat", false, nil); err != nil {
		t.Fatalf("expected success: %v", err)
	}

	slept := clock.slept()
	if len(slept) == 0 || slept[0] != 7*time.Second {
		t.Errorf("Retry-After must override backoff, slept %v", slept)
	}
}

func TestRetryCapsBackoffAtMax(t *testing.T) {
	t.Parallel()

	clock := newStubClock(time.Now())

	var attempts atomic.Int64

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts.Add(1)
		w.Header().Set("Retry-After", "3600")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()

	kernel := newKernel(t, server.URL, clock, WithoutRateLimit(),
		WithRetryOptions(RetryOptions{
			InitialBackoff: time.Second,
			MaxRetries:     2,
			MaxBackoff:     5 * time.Second,
		}),
		withClock(clock),
	)

	_, _, err := kernel.Activity.ListEventsPerformedByUser(t.Context(), "octocat", false, nil)
	if err == nil {
		t.Fatal("expected exhaustion error")
	}

	for _, slept := range clock.slept() {
		if slept > 5*time.Second {
			t.Errorf("sleep %v exceeds MaxBackoff", slept)
		}
	}
}

func TestRetryReplaysRequestBody(t *testing.T) {
	t.Parallel()

	clock := newStubClock(time.Now())

	var (
		mu     sync.Mutex
		bodies []string
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)

		mu.Lock()
		bodies = append(bodies, string(body))
		count := len(bodies)
		mu.Unlock()

		if count < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)

			return
		}

		writeJSON(w, eventsPayload("event-1"))
	}))
	defer server.Close()

	// PUT is idempotent, so 5xx retries are allowed.
	request, err := http.NewRequestWithContext(
		t.Context(),
		http.MethodPut,
		server.URL+"/repos/octocat/Hello-World/topics",
		bytes.NewBufferString(`{"names":["go"]}`),
	)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}

	kernel := newKernel(t, server.URL, clock, WithoutRateLimit(), withClock(clock))

	resp, err := kernel.Client.Client().Do(request)
	if err != nil {
		t.Fatalf("round trip: %v", err)
	}

	defer func() { _ = resp.Body.Close() }()

	mu.Lock()
	defer mu.Unlock()

	if len(bodies) != 3 {
		t.Fatalf("attempts = %d, want 3", len(bodies))
	}

	for i, body := range bodies {
		if body != `{"names":["go"]}` {
			t.Errorf("attempt %d body = %q, want replayed body", i, body)
		}
	}
}

func errorIs(err, target error) bool {
	return errors.Is(err, target)
}
