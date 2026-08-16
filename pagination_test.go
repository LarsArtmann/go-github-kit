package githubkit

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	gh "github.com/google/go-github/v69/github"
)

func TestFetchPagesRejectsInvalidMaxPages(t *testing.T) {
	t.Parallel()

	if _, err := FetchPages(t.Context(), PaginationOptions{}, func(context.Context, int) ([]int, error) {
		return nil, nil
	}); !errors.Is(err, ErrInvalidPagination) {
		t.Fatalf("expected ErrInvalidPagination, got %v", err)
	}
}

func TestFetchPagesSingleShortPage(t *testing.T) {
	t.Parallel()

	pages, err := FetchPages(
		t.Context(),
		PaginationOptions{MaxPages: 3},
		func(_ context.Context, page int) ([]int, error) {
			if page != 1 {
				t.Errorf("short page 1 must end the walk, got request for page %d", page)
			}

			return []int{1, 2, 3}, nil
		},
	)
	if err != nil {
		t.Fatalf("FetchPages: %v", err)
	}

	if len(pages) != 3 {
		t.Errorf("pages = %v, want [1 2 3]", pages)
	}
}

func TestFetchPagesConcurrentWalk(t *testing.T) {
	t.Parallel()

	perPage := 100

	server := newRecordingServer(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("page") {
		case "", "1":
			writePage(w, perPage, "one")
		case "2":
			writePage(w, perPage, "two")
		case "3":
			writePage(w, 37, "three") // short page ends the collection
		default:
			writePage(w, 0, "none")
		}
	})
	defer server.Close()

	clock := newStubClock(time.Now())
	kernel := newKernel(t, server.URL, clock, WithoutRateLimit(), withClock(clock))

	var (
		progressMu sync.Mutex
		progressed []int
	)

	events, err := FetchPages(t.Context(), PaginationOptions{
		MaxPages:    5,
		PerPage:     perPage,
		Concurrency: 2,
		OnProgress: func(page, _, _ int) {
			progressMu.Lock()
			progressed = append(progressed, page)
			progressMu.Unlock()
		},
	}, func(ctx context.Context, page int) ([]eventID, error) {
		list, _, fetchErr := kernel.Activity.ListEventsPerformedByUser(ctx, "octocat", false, listOpts(page))
		if fetchErr != nil {
			return nil, fetchErr
		}

		ids := make([]eventID, 0, len(list))
		for _, event := range list {
			ids = append(ids, eventID(event.GetID()))
		}

		return ids, nil
	})
	if err != nil {
		t.Fatalf("FetchPages: %v", err)
	}

	if want := 2*perPage + 37; len(events) != want {
		t.Errorf("items = %d, want %d", len(events), want)
	}

	if events[0] != "one-0" || events[perPage] != "two-0" || events[2*perPage] != "three-0" {
		t.Errorf("page order corrupted: %v … %v … %v", events[0], events[perPage], events[2*perPage])
	}

	if got := server.totalCalls(); got > 4 { // 1 probe is possible; pages 1..3; page 4 skipped
		t.Errorf("server calls = %d, early cancel failed", got)
	}

	progressMu.Lock()
	progressCount := len(progressed)
	progressMu.Unlock()

	if progressCount < 2 {
		t.Errorf("progress callbacks = %v", progressed)
	}
}

func TestFetchPagesEarlyCancelSkipsTail(t *testing.T) {
	t.Parallel()

	perPage := 10

	var (
		mu      sync.Mutex
		fetched []int
	)

	_, err := FetchPages(t.Context(), PaginationOptions{
		MaxPages:    6,
		PerPage:     perPage,
		Concurrency: 1, // sequential dispatch: short page 2 must stop 3..6
	}, func(_ context.Context, page int) ([]int, error) {
		mu.Lock()
		fetched = append(fetched, page)
		mu.Unlock()

		if page == 2 {
			return []int{1}, nil // short
		}

		return make([]int, perPage), nil
	})
	if err != nil {
		t.Fatalf("FetchPages: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()

	if len(fetched) > 2 {
		t.Errorf("pages fetched = %v, tail must be skipped after a short page", fetched)
	}
}

func TestFetchPagesErrorPropagates(t *testing.T) {
	t.Parallel()

	boom := errors.New("boom")

	if _, err := FetchPages(
		t.Context(),
		PaginationOptions{MaxPages: 3},
		func(_ context.Context, page int) ([]int, error) {
			if page == 2 {
				return nil, boom
			}

			return make([]int, 100), nil
		},
	); !errors.Is(
		err,
		boom,
	) {
		t.Fatalf("expected boom, got %v", err)
	}
}

func TestFetchPagesCallerContextCancel(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	var calls atomic.Int64

	if _, err := FetchPages(ctx, PaginationOptions{MaxPages: 2}, func(walkCtx context.Context, _ int) ([]int, error) {
		calls.Add(1)

		select {
		case <-walkCtx.Done():
			return nil, walkCtx.Err()
		default:
			return make([]int, 100), nil
		}
	}); err == nil {
		t.Fatal("expected context cancellation to surface")
	}
}

func TestFetchPagesDefaultsPerPageAndConcurrency(t *testing.T) {
	t.Parallel()

	var perPageSeen, concurrencySeen int

	_, err := FetchPages(t.Context(), PaginationOptions{MaxPages: 2}, func(_ context.Context, page int) ([]int, error) {
		_ = page
		perPageSeen = defaultPerPage

		return make([]int, defaultPerPage), nil
	})
	if err != nil {
		t.Fatalf("FetchPages: %v", err)
	}

	concurrencySeen = defaultConcurrency

	if perPageSeen != 100 || concurrencySeen != 3 {
		t.Errorf("defaults = %d/%d, want 100/3", perPageSeen, concurrencySeen)
	}
}

type eventID string

func listOpts(page int) *gh.ListOptions {
	return &gh.ListOptions{Page: page, PerPage: 100}
}

func writePage(w http.ResponseWriter, count int, prefix string) {
	ids := make([]map[string]any, 0, count)
	for i := range count {
		ids = append(ids, map[string]any{
			"id":   fmt.Sprintf("%s-%d", prefix, i),
			"type": "PushEvent",
		})
	}

	writeJSON(w, ids)
}
