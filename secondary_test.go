package githubkit

import (
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newPacingKernel builds a kernel against a fresh recording server with
// the given pacing and the stub clock.
func newPacingKernel(t *testing.T, clock *stubClock, pacing time.Duration) (*Kernel, *recordingServer) {
	t.Helper()

	server := newRecordingServer(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	kernel := newKernel(t, server.URL, clock,
		WithoutRateLimit(),
		WithoutRetry(),
		WithSecondaryPacing(pacing),
	)

	return kernel, server
}

func TestSecondaryPacing_SpacesConcurrentRequests(t *testing.T) {
	t.Parallel()

	clock := newStubClock(time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC))
	kernel, server := newPacingKernel(t, clock, 15*time.Millisecond)

	const requests = 5

	var wg sync.WaitGroup
	for range requests {
		wg.Go(func() {
			_, _, err := kernel.Users.Get(t.Context(), "")
			require.NoError(t, err)
		})
	}
	wg.Wait()

	// With the stub clock, waits are recorded rather than slept. The
	// pacing budget shows up as waits summing to at least
	// (requests-1) × interval: the first request goes immediately, every
	// further request waits out its slot.
	clock.mu.Lock()
	var total time.Duration
	for _, d := range clock.sleeps {
		total += d
	}
	clock.mu.Unlock()

	assert.GreaterOrEqual(t, total, 4*15*time.Millisecond,
		"concurrent requests wait out their pacing slots")
	assert.Equal(t, requests, server.totalCalls(), "every request reached the server")
}

func TestSecondaryPacing_DisabledByDefault(t *testing.T) {
	t.Parallel()

	clock := newStubClock(time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC))
	kernel, server := newPacingKernel(t, clock, 0)

	var wg sync.WaitGroup
	for range 5 {
		wg.Go(func() {
			_, _, err := kernel.Users.Get(t.Context(), "")
			require.NoError(t, err)
		})
	}
	wg.Wait()

	clock.mu.Lock()
	sleeps := len(clock.sleeps)
	clock.mu.Unlock()

	assert.Zero(t, sleeps, "no pacing configured, so no waits")
	assert.Equal(t, 5, server.totalCalls(), "requests reach the server unpaced")
}

func TestSecondaryPacing_NegativeIsDisabled(t *testing.T) {
	t.Parallel()

	clock := newStubClock(time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC))
	kernel, _ := newPacingKernel(t, clock, -time.Second)

	_, _, err := kernel.Users.Get(t.Context(), "")
	require.NoError(t, err)

	clock.mu.Lock()
	sleeps := len(clock.sleeps)
	clock.mu.Unlock()

	assert.Zero(t, sleeps, "a negative interval disables pacing instead of failing")
}
