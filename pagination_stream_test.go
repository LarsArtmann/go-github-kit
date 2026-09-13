package githubkit_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	githubkit "github.com/LarsArtmann/go-github-kit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// pages yields n full pages of pageItems items, item values globally
// numbered 1..n*pageItems.
func pages(n, pageItems int) func(ctx context.Context, page int) ([]int, error) {
	return func(_ context.Context, page int) ([]int, error) {
		start := (page-1)*pageItems + 1
		out := make([]int, pageItems)

		for i := range out {
			out[i] = start + i
		}

		return out, nil
	}
}

func TestStreamPages_DeliversEveryPageInOrder(t *testing.T) {
	t.Parallel()

	fetch := pages(5, 3)

	// Later pages complete first: delivery must still be 1..5.
	inFlight := map[int]int{}
	var mu sync.Mutex

	fetchSlowestFirst := func(ctx context.Context, page int) ([]int, error) {
		mu.Lock()
		inFlight[page]++
		mu.Unlock()

		time.Sleep(time.Duration(6-page) * 5 * time.Millisecond)

		return fetch(ctx, page)
	}

	var delivered []int

	err := githubkit.StreamPages(t.Context(), githubkit.PaginationOptions{
		MaxPages:    5,
		PerPage:     3,
		Concurrency: 4,
	}, fetchSlowestFirst, func(_ context.Context, page int, items []int) error {
		delivered = append(delivered, page)

		assert.Len(t, items, 3)

		return nil
	})

	require.NoError(t, err)
	assert.Equal(t, []int{1, 2, 3, 4, 5}, delivered, "pages are delivered strictly in page order")
}

func TestStreamPages_CallbackErrorAbortsWalk(t *testing.T) {
	t.Parallel()

	callbackErr := errors.New("downstream sink failed")

	fetch := pages(4, 2)

	var delivered []int

	err := githubkit.StreamPages(t.Context(), githubkit.PaginationOptions{
		MaxPages: 4,
		PerPage:  2,
	}, fetch, func(_ context.Context, page int, _ []int) error {
		if page == 2 {
			return callbackErr
		}

		delivered = append(delivered, page)

		return nil
	})

	require.ErrorIs(t, err, callbackErr)
	assert.Contains(t, err.Error(), "page 2 callback")

	// Pages already dispatched may still be fetched (in-flight requests
	// are never yanked), but nothing is delivered after the callback
	// failed: delivery stops at the failure.
	assert.Equal(t, []int{1}, delivered, "no page is delivered after the callback failed")
}

func TestStreamPages_ShortPageEndsWalk(t *testing.T) {
	t.Parallel()

	fetch := func(ctx context.Context, page int) ([]int, error) {
		if page == 2 {
			return []int{3}, nil // short page: the collection ends here
		}

		return pages(3, 2)(ctx, page)
	}

	var delivered []int

	err := githubkit.StreamPages(t.Context(), githubkit.PaginationOptions{
		MaxPages: 10,
		PerPage:  2,
		// Sequential walk: page 3's dispatch decision happens before
		// page 2's shortness is known, so a concurrent walk could
		// legitimately dispatch (then skip at execution) page 3. With
		// one page in flight the skip is deterministic, which is what
		// this test pins.
		Concurrency: 1,
	}, fetch, func(_ context.Context, page int, items []int) error {
		delivered = append(delivered, page)
		assert.NotEmpty(t, items)

		return nil
	})

	require.NoError(t, err)
	assert.Equal(t, []int{1, 2}, delivered, "pages beyond the short page are never fetched or delivered")
}

func TestStreamPages_RespectsMaxPages(t *testing.T) {
	t.Parallel()

	var count int

	err := githubkit.StreamPages(t.Context(), githubkit.PaginationOptions{
		MaxPages: 3,
		PerPage:  2,
	}, pages(10, 2), func(_ context.Context, page int, _ []int) error {
		count = page

		return nil
	})

	require.NoError(t, err)
	assert.Equal(t, 3, count, "the walk stops at MaxPages even with more pages available")
}

func TestStreamPages_SingleShortPage(t *testing.T) {
	t.Parallel()

	fetch := func(ctx context.Context, page int) ([]int, error) {
		require.Equal(t, 1, page)

		return []int{1}, nil
	}

	var delivered [][]int

	err := githubkit.StreamPages(t.Context(), githubkit.PaginationOptions{
		MaxPages: 5,
		PerPage:  2,
	}, fetch, func(_ context.Context, _ int, items []int) error {
		delivered = append(delivered, items)

		return nil
	})

	require.NoError(t, err)
	require.Len(t, delivered, 1)
	assert.Equal(t, []int{1}, delivered[0])
}

// The streaming property that motivates the API: onPage runs while later
// pages are still being fetched, so callers flush downstream instead of
// accumulating. onPage(2) blocks until fetch(3) has landed; a batching
// implementation would deadlock into the deadline error instead.
func TestStreamPages_CallbackOverlapsLaterFetches(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	fetched := map[int]bool{}

	fetch := func(_ context.Context, page int) ([]int, error) {
		mu.Lock()
		fetched[page] = true
		mu.Unlock()

		return pages(3, 2)(t.Context(), page)
	}

	err := githubkit.StreamPages(t.Context(), githubkit.PaginationOptions{
		MaxPages:    3,
		PerPage:     2,
		Concurrency: 3,
	}, fetch, func(_ context.Context, page int, _ []int) error {
		if page != 2 {
			return nil
		}

		deadline := time.Now().Add(2 * time.Second)

		for {
			mu.Lock()
			f3 := fetched[3]
			mu.Unlock()

			if f3 {
				return nil
			}

			if time.Now().After(deadline) {
				return errors.New("page 3 was not fetched while page 2's callback ran: streaming is broken")
			}

			time.Sleep(time.Millisecond)
		}
	})

	require.NoError(t, err)
}

func TestStreamPages_RejectsInvalidMaxPages(t *testing.T) {
	t.Parallel()

	err := githubkit.StreamPages(t.Context(), githubkit.PaginationOptions{
		MaxPages: 0,
	}, pages(1, 1), func(_ context.Context, _ int, _ []int) error {
		return nil
	})

	require.ErrorIs(t, err, githubkit.ErrInvalidPagination)
}
