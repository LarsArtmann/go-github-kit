package githubkit

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
)

// PaginationOptions tunes [FetchPages].
type PaginationOptions struct {
	// MaxPages is the hard cap on pages fetched; it must be at least 1.
	// GitHub caps list endpoints at 1000 pages (300 items/page for some),
	// so callers bound by their domain, not by this default.
	MaxPages int

	// PerPage is the expected page size used to detect the final short
	// page. Zero means 100, GitHub's documented maximum.
	PerPage int

	// Concurrency is how many of pages 2..MaxPages may be in flight at
	// once. Zero means 3; 1 makes the walk sequential.
	Concurrency int

	// OnProgress, when set, is invoked after each page completes with the
	// page number, the page cap, and the cumulative item count so far.
	OnProgress func(page, totalPages, cumulative int)
}

// Defaults applied to zero-valued PaginationOptions fields.
const (
	defaultPerPage     = 100
	defaultConcurrency = 3
)

// ErrInvalidPagination is returned by [FetchPages] when MaxPages is not at
// least 1. An unbounded walk is never the right default: a misbehaving
// server that always returns full pages would make it infinite.
var ErrInvalidPagination = errors.New("githubkit: PaginationOptions.MaxPages must be at least 1")

// FetchPages walks a paginated GitHub list endpoint concurrently.
//
// Page 1 is fetched alone: it decides whether the walk is worthwhile and
// warms the rate-limit budget from its headers. Pages 2 through MaxPages
// then run through a bounded worker pool (PaginationOptions.Concurrency,
// default 3). The moment any page comes back short — fewer items than
// PerPage — every page beyond it is skipped or cancelled, because GitHub
// only returns short pages at the end of a collection. Results are
// returned in page order regardless of completion order.
//
// The fetch function receives the caller's context, cancelled when the
// walk ends early; a fetch that fails with context.Canceled after the
// short page was seen is treated as a successful skip, not an error.
//
// The per-page rate gate applies automatically when fetch goes through a
// Kernel, since each page is an ordinary request through the kernel stack.
func FetchPages[T any]( //nolint:cyclop,funlen // concurrency state machine: defaults, early stop, bounded dispatch
	ctx context.Context,
	opts PaginationOptions,
	fetch func(ctx context.Context, page int) ([]T, error),
) ([]T, error) {
	if opts.MaxPages < 1 {
		return nil, fmt.Errorf("githubkit: fetch pages: %w", ErrInvalidPagination)
	}

	if opts.PerPage <= 0 {
		opts.PerPage = defaultPerPage
	}

	if opts.Concurrency <= 0 {
		opts.Concurrency = defaultConcurrency
	}

	first, err := fetch(ctx, 1)
	if err != nil {
		return nil, fmt.Errorf("githubkit: fetch page 1: %w", err)
	}

	reportProgress(opts.OnProgress, 1, opts.MaxPages, len(first))

	if len(first) < opts.PerPage || opts.MaxPages == 1 {
		return first, nil
	}

	walkCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	var (
		shortPage atomic.Int64 // 0 = none seen; else the page number
		mu        sync.Mutex
		firstErr  error
		pages     = make([][]T, opts.MaxPages+1) // index 1..MaxPages; index 0 unused
		sem       = make(chan struct{}, opts.Concurrency)
		wg        sync.WaitGroup
	)

	pages[1] = first

	// fetchAndRecord is one page's work: skip beyond a seen short page,
	// fetch, record the result, and stop the walk on the first failure.
	fetchAndRecord := func(page int) {
		if skip := shouldSkip(walkCtx, shortPage.Load(), page); skip {
			return
		}

		items, fetchErr := fetch(walkCtx, page)
		if fetchErr != nil {
			// Cancellation after the short page is the designed early
			// exit, not a failure.
			if shortPage.Load() != 0 && errors.Is(fetchErr, context.Canceled) {
				return
			}

			mu.Lock()
			if firstErr == nil {
				firstErr = fmt.Errorf("githubkit: fetch page %d: %w", page, fetchErr)
			}
			mu.Unlock()

			cancel()

			return
		}

		if len(items) < opts.PerPage {
			// Nothing beyond a short page can exist: stop dispatching
			// (the loop checks shortPage), but never cancel in-flight
			// EARLIER pages — those are real data.
			shortPage.CompareAndSwap(0, int64(page))
		}

		mu.Lock()
		pages[page] = items

		cumulative := 0

		for i := 1; i <= page; i++ {
			cumulative += len(pages[i])
		}

		mu.Unlock()

		reportProgress(opts.OnProgress, page, opts.MaxPages, cumulative)
	}

	for page := 2; page <= opts.MaxPages; page++ {
		// A short page already seen means everything beyond it is absent.
		if short := shortPage.Load(); short != 0 && int64(page) > short {
			break
		}

		select {
		case sem <- struct{}{}:
		case <-walkCtx.Done():
			page = opts.MaxPages + 1 // stop dispatching new pages
		}

		if page > opts.MaxPages {
			break
		}

		wg.Add(1)

		go func(page int) {
			defer wg.Done()
			defer func() { <-sem }()

			fetchAndRecord(page)
		}(page)
	}

	wg.Wait()

	mu.Lock()
	defer mu.Unlock()

	if firstErr != nil {
		return nil, firstErr
	}

	return assemble[T](pages), nil
}

// shouldSkip reports whether a page beyond the observed short page (or
// after walk cancellation for any reason) must not be fetched.
func shouldSkip(ctx context.Context, short int64, page int) bool {
	if short != 0 && int64(page) > short {
		return true
	}

	return ctx.Err() != nil && short != 0
}

// assemble concatenates pages 1..n up to (and excluding) the first empty
// or absent one; GitHub returns no gaps inside a collection. Index 0 is
// unused by convention.
func assemble[T any](pages [][]T) []T {
	total := 0

	for _, page := range pages[1:] {
		if len(page) == 0 {
			break
		}

		total += len(page)
	}

	out := make([]T, 0, total)

	for _, page := range pages[1:] {
		if len(page) == 0 {
			break
		}

		out = append(out, page...)
	}

	return out
}

func reportProgress(onProgress func(page, totalPages, cumulative int), page, total, cumulative int) {
	if onProgress != nil {
		onProgress(page, total, cumulative)
	}
}
