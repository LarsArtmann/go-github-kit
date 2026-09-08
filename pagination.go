package githubkit

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
)

// PaginationOptions tunes [FetchPages] and [StreamPages].
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

// ErrInvalidPagination is returned by [FetchPages] and [StreamPages] when
// MaxPages is not at least 1. An unbounded walk is never the right
// default: a misbehaving server that always returns full pages would make
// it infinite.
var ErrInvalidPagination = errors.New("githubkit: PaginationOptions.MaxPages must be at least 1")

// FetchPages walks a paginated GitHub list endpoint concurrently and
// returns every item. For collections too large to hold in memory, use
// [StreamPages], which delivers page by page instead of accumulating.
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
func FetchPages[T any]( //nolint:cyclop // see walkPages
	ctx context.Context,
	opts PaginationOptions,
	fetch func(ctx context.Context, page int) ([]T, error),
) ([]T, error) {
	var all []T

	err := walkPages(ctx, opts, fetch, func(_ context.Context, _ int, items []T) error {
		all = append(all, items...)

		return nil
	})
	if err != nil {
		return nil, err
	}

	return all, nil
}

// StreamPages walks a paginated GitHub list endpoint like [FetchPages],
// but hands each completed page to onPage in page order instead of
// accumulating the whole collection. Peak memory is bounded by the walk
// window — at most Concurrency pages in flight plus one page awaiting
// delivery — rather than the size of the result. It is the right tool
// when pages are flushed downstream as they arrive: envelope stores,
// NDJSON streams, very large repositories.
//
// onPage runs sequentially, never concurrently. Returning an error from
// onPage aborts the walk; the returned error wraps the callback failure,
// and in-flight pages are cancelled through the walk context. A short
// page ends the walk early exactly as in FetchPages: pages beyond it are
// never fetched, and onPage is never called for them.
func StreamPages[T any]( //nolint:cyclop // see walkPages
	ctx context.Context,
	opts PaginationOptions,
	fetch func(ctx context.Context, page int) ([]T, error),
	onPage func(ctx context.Context, page int, items []T) error,
) error {
	return walkPages(ctx, opts, fetch, onPage)
}

// walkPages is the shared concurrency state machine behind FetchPages
// and StreamPages: defaults, lone page 1, bounded dispatch of pages
// 2..MaxPages, short-page early stop, and strictly in-order delivery to
// onPage.
func walkPages[T any]( //nolint:cyclop,funlen // concurrency state machine: defaults, early stop, bounded dispatch, ordered delivery
	ctx context.Context,
	opts PaginationOptions,
	fetch func(ctx context.Context, page int) ([]T, error),
	onPage func(ctx context.Context, page int, items []T) error,
) error {
	if opts.MaxPages < 1 {
		return fmt.Errorf("githubkit: fetch pages: %w", ErrInvalidPagination)
	}

	if opts.PerPage <= 0 {
		opts.PerPage = defaultPerPage
	}

	if opts.Concurrency <= 0 {
		opts.Concurrency = defaultConcurrency
	}

	first, err := fetch(ctx, 1)
	if err != nil {
		return fmt.Errorf("githubkit: fetch page 1: %w", err)
	}

	reportProgress(opts.OnProgress, 1, opts.MaxPages, len(first))

	if err := onPage(ctx, 1, first); err != nil {
		return fmt.Errorf("githubkit: page %d callback: %w", 1, err)
	}

	if len(first) < opts.PerPage || opts.MaxPages == 1 {
		return nil
	}

	walkCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	var (
		shortPage    atomic.Int64 // 0 = none seen; else the page number
		itemsFetched atomic.Int64 // cumulative items recorded, for progress
		aborted      atomic.Bool  // a fetch or onPage failed; stop delivering
		mu           sync.Mutex
		firstErr     error
		ready        = make([]bool, opts.MaxPages+1) // index 1..MaxPages; 0 unused
		pending      = make([][]T, opts.MaxPages+1)  // fetched items awaiting in-order delivery
		delivered    = 1                             // last page handed to onPage
		deliverMu    sync.Mutex
		sem          = make(chan struct{}, opts.Concurrency)
		wg           sync.WaitGroup
	)

	itemsFetched.Store(int64(len(first)))
	ready[1] = true
	pending[1] = first

	fail := func(err error) {
		mu.Lock()

		if firstErr == nil {
			firstErr = err
		}

		mu.Unlock()

		aborted.Store(true)
		cancel()
	}

	// drain hands every page that is ready but not yet delivered to
	// onPage, in page order, one delivery at a time. Pages completed
	// ahead of the delivery frontier wait in pending — at most
	// Concurrency of them, never the whole collection.
	drain := func() {
		deliverMu.Lock()
		defer deliverMu.Unlock()

		for delivered < opts.MaxPages && !aborted.Load() {
			next := delivered + 1

			mu.Lock()
			pageReady := ready[next]
			var items []T
			if pageReady {
				items = pending[next]
			}
			mu.Unlock()

			if !pageReady {
				return
			}

			if err := onPage(walkCtx, next, items); err != nil {
				fail(fmt.Errorf("githubkit: page %d callback: %w", next, err))

				return
			}

			mu.Lock()
			delivered = next
			pending[next] = nil // release the delivered page's buffer
			mu.Unlock()
		}
	}

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

			fail(fmt.Errorf("githubkit: fetch page %d: %w", page, fetchErr))

			return
		}

		if len(items) < opts.PerPage {
			// Nothing beyond a short page can exist: stop dispatching
			// (the loop checks shortPage), but never cancel in-flight
			// EARLIER pages — those are real data.
			shortPage.CompareAndSwap(0, int64(page))
		}

		total := itemsFetched.Add(int64(len(items)))

		mu.Lock()
		ready[page] = true
		pending[page] = items
		mu.Unlock()

		reportProgress(opts.OnProgress, page, opts.MaxPages, int(total))

		drain()
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

	drain() // deliver a tail page that completed after the last drain

	mu.Lock()
	defer mu.Unlock()

	if firstErr != nil {
		return firstErr
	}

	return nil
}

// shouldSkip reports whether a page beyond the observed short page (or
// after walk cancellation for any reason) must not be fetched.
func shouldSkip(ctx context.Context, short int64, page int) bool {
	if short != 0 && int64(page) > short {
		return true
	}

	return ctx.Err() != nil && short != 0
}

func reportProgress(onProgress func(page, totalPages, cumulative int), page, total, cumulative int) {
	if onProgress != nil {
		onProgress(page, total, cumulative)
	}
}
