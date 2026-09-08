package githubkit_test

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	githubkit "github.com/LarsArtmann/go-github-kit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// startRecordingServer records when each request reaches the server.
type startRecordingServer struct {
	server *httptest.Server

	mu     sync.Mutex
	starts []time.Time
}

func newStartRecordingServer(t *testing.T) *startRecordingServer {
	t.Helper()

	rec := &startRecordingServer{}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		rec.mu.Lock()
		rec.starts = append(rec.starts, time.Now())
		rec.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	})
	rec.server = httptest.NewServer(mux)
	t.Cleanup(rec.server.Close)

	return rec
}

func (s *startRecordingServer) requestStarts() []time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()

	return append([]time.Time(nil), s.starts...)
}

func TestSecondaryPacing_SpacesConcurrentRequests(t *testing.T) {
	t.Parallel()

	rec := newStartRecordingServer(t)
	kernel := newKernel(t, rec.server.URL, nil,
		githubkit.WithoutRateLimit(),
		githubkit.WithoutRetry(),
		githubkit.WithSecondaryPacing(15*time.Millisecond),
	)

	const requests = 5

	var wg sync.WaitGroup
	for range requests {
		wg.Go(func() {
			resp, err := kernel.Client.Users.Get(t.Context(), "")
			require.NoError(t, err)
			_ = resp.Body.Close()
		})
	}
	wg.Wait()

	starts := rec.requestStarts()
	require.Len(t, starts, requests)

	for i := 1; i < len(starts); i++ {
		gap := starts[i].Sub(starts[i-1])
		assert.GreaterOrEqual(t, gap, 13*time.Millisecond,
			"request starts are spaced by at least the pacing interval")
	}
}

func TestSecondaryPacing_DisabledByDefault(t *testing.T) {
	t.Parallel()

	rec := newStartRecordingServer(t)
	kernel := newKernel(t, rec.server.URL, nil,
		githubkit.WithoutRateLimit(),
		githubkit.WithoutRetry(),
	)

	var wg sync.WaitGroup
	for range 5 {
		wg.Go(func() {
			resp, err := kernel.Client.Users.Get(t.Context(), "")
			require.NoError(t, err)
			_ = resp.Body.Close()
		})
	}
	wg.Wait()

	starts := rec.requestStarts()
	require.Len(t, starts, 5)

	var maxGap time.Duration
	for i := 1; i < len(starts); i++ {
		if gap := starts[i].Sub(starts[i-1]); gap > maxGap {
			maxGap = gap
		}
	}
	assert.Less(t, maxGap, 15*time.Millisecond,
		"without pacing, concurrent requests are not spaced")
}

func TestSecondaryPacing_NegativeIsDisabled(t *testing.T) {
	t.Parallel()

	rec := newStartRecordingServer(t)
	kernel := newKernel(t, rec.server.URL, nil,
		githubkit.WithoutRateLimit(),
		githubkit.WithoutRetry(),
		githubkit.WithSecondaryPacing(-time.Second),
	)

	resp, err := kernel.Client.Users.Get(t.Context(), "")
	require.NoError(t, err)
	_ = resp.Body.Close()

	assert.Len(t, rec.requestStarts(), 1, "a negative interval disables pacing instead of failing")
}
