# go-github-kit

A thin operational kernel over [google/go-github/v69](https://github.com/google/go-github):
authentication, rate-limit awareness, retry with backoff, sentinel errors, ETag
conditional caching, and concurrent pagination — while you keep using the native
SDK types everywhere.

[![CI](https://github.com/LarsArtmann/go-github-kit/actions/workflows/ci.yml/badge.svg)](https://github.com/LarsArtmann/go-github-kit/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/LarsArtmann/go-github-kit.svg)](https://pkg.go.dev/github.com/LarsArtmann/go-github-kit)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

## Why

Every project that talks to the GitHub API hand-rolls the same five concerns,
usually incompletely: a token resolved from some environment variable, rate
limits discovered the hard way (a 403 at 2 a.m.), retry loops that replay
non-idempotent POSTs, pagination loops that fetch serially, and error handling
that string-matches status codes. One real production incident started exactly
this way: a service authenticating with a bare PAT and no rate limiting at all.

This kit centralizes that plumbing once, as an HTTP transport stack *under* the
native SDK. It is deliberately **not** a client wrapper: `Kernel` embeds
`*github.Client`, so every native SDK method works directly, and code written
against `*github.Client` compiles unchanged.

## Quick start

```go
package main

import (
	"context"
	"fmt"

	"github.com/LarsArtmann/go-github-kit"
	gh "github.com/google/go-github/v69/github"
)

func main() {
	// Resolves GITHUB_TOKEN or GH_TOKEN; any other env names work too:
	// githubkit.WithAuthTokenFromEnv("MY_TOKEN"). Explicit tokens:
	// githubkit.WithPAT("ghp_...").
	kernel, err := githubkit.New(githubkit.WithAuthTokenFromEnv())
	if err != nil {
		panic(err) // githubkit.ErrAuthRequired when no token is found
	}

	ctx := context.Background()

	// Native SDK, unchanged — the kernel stack is underneath.
	user, _, err := kernel.Users.Get(ctx, "")
	if err != nil {
		panic(err)
	}
	fmt.Println("hello,", user.GetLogin())

	// Concurrent pagination with early-exit on the short final page.
	events, err := githubkit.FetchPages(ctx,
		githubkit.PaginationOptions{MaxPages: 10},
		func(ctx context.Context, page int) ([]*gh.Event, error) {
			events, _, err := kernel.Activity.ListEvents(ctx, &gh.ListOptions{
				Page:    page,
				PerPage: 100,
			})
			return events, err
		})
	if err != nil {
		panic(err)
	}
	fmt.Println("fetched", len(events), "events")

	// Live budget, fed from every response's rate-limit headers.
	if snapshot, ok := kernel.RateLimitSnapshot(); ok {
		fmt.Printf("rate budget: %d/%d, resets %s\n",
			snapshot.Remaining, snapshot.Limit, snapshot.ResetAt.Format("15:04:05"))
	}
}
```

## What the stack does

Every request flows through, outermost first:

```
gate ──▶ feed ──▶ retry ──▶ etag ──▶ base
```

| Layer | Behavior |
|---|---|
| **gate** | Pre-flight rate-limit check. Unknown budget → lazy `GET /rate_limit` probe (30s cooldown, probe failure never blocks). At or below `MinRemaining` (default 10) it sleeps until the reset; a reset further than `MaxWait` (default 15m) away fails fast with `ErrRateLimited`. |
| **feed** | Parses `X-RateLimit-*` headers from **every** response, including failed retries, into a shared `RateLimitCache`. |
| **retry** | `429` always retried (GitHub rejects before processing). `5xx` retried only for idempotent methods (GET/HEAD/OPTIONS/PUT/DELETE) — **POST is never auto-retried**. `Retry-After` honored, capped at `MaxBackoff`. Default: 3 retries, 1s→30s exponential backoff. Request bodies are replayed safely. |
| **etag** | Opt-in (`WithETagCache`). GET-only. Sends `If-None-Match`; a `304` becomes a synthesized `200` with the cached body and fresh rate headers, marked `X-Github-Kit-From-Cache: 1`. Entries are keyed by a credential fingerprint, so two tokens never share cache entries. |
| **base** | Tuned `http.Transport` (100 idle conns, 10 per host, 90s idle timeout). |

Each concern is individually disable-able: `WithoutRateLimit()`, `WithoutRetry()`.
`WithRateLimitOptions`/`WithRetryOptions` override only the fields you set —
partial configuration keeps sane defaults. `WithBaseURL` points the kernel at a
GitHub Enterprise Server root (a trailing slash is appended for you, matching
the SDK's own `WithEnterpriseURLs`).

## Errors: sentinels that preserve everything

`ClassifyError` maps any failure to a `*StatusError` that wraps **both** a kit
sentinel and the original error, so classification never destroys information:

```go
_, _, err := kernel.Repositories.Get(ctx, owner, repo)
err = githubkit.ClassifyError(err)

if statusErr, ok := errors.AsType[*githubkit.StatusError](err); ok {
	switch {
	case errors.Is(statusErr, githubkit.ErrNotFound):
		// 404
	case errors.Is(statusErr, githubkit.ErrRateLimited):
		// 429, or 403 with X-RateLimit-Remaining: 0 (GitHub conflates them)
	case errors.Is(statusErr, githubkit.ErrAuthRequired):
		// 401
	case errors.Is(statusErr, githubkit.ErrForbidden):
		// 403 without a rate-limit cause
	case errors.Is(statusErr, githubkit.ErrAPIUnavailable):
		// ≥500, network and URL errors
	}
}

// The original SDK error survives intact:
if ghErr, ok := errors.AsType[*gh.ErrorResponse](err); ok {
	fmt.Println("server said:", ghErr.Message)
}
```

Status `403` is disambiguated by the rate-limit headers: with
`X-RateLimit-Remaining: 0` it classifies as `ErrRateLimited`, otherwise
`ErrForbidden`.

## The native pre-flight check

go-github runs its own rate-limit check before each request and refuses with a
`*gh.RateLimitError` when its tracked budget hits exactly 0, using its own wall
clock — the kit cannot disable or time-control that check. In practice the kit's
gate (floor of 10 by default) acts first and waits instead of failing, so this
only surfaces when you opt out with `WithoutRateLimit()`. If you do, expect
native `*gh.RateLimitError` values from an exhausted budget rather than kit
sentinels.

## Design

- **Wrap, don't replace.** All kernel behavior lives in `http.RoundTripper`s;
  the SDK above stays stock. Your types, your call sites, your mocks —
  untouched.
- **Probes stay honest.** The lazy `/rate_limit` probe runs through its own
  feed+retry stack (no gate → no recursion; no ETag → no stale budget
  masquerading as fresh).
- **Errors as values.** Sentinels for `errors.Is`, `StatusError` for
  `errors.AsType`, originals preserved for everything else.
- **Bounded concurrency everywhere.** Pagination workers (default 3) and waits
  (`MaxWait`, `MaxBackoff`) are capped by default; long jobs degrade politely
  instead of hammering.
- **Clock injection.** The kernel's clock is injectable for tests (`stubClock`
  in this repo's suite), so waiting behavior is tested instantly and
  deterministically.

## Install

```
go get github.com/LarsArtmann/go-github-kit
```

Requires Go 1.26 or newer. Depends on
[google/go-github/v69](https://github.com/google/go-github) and nothing else.

## Development

```
nix develop       # dev shell with Go, golangci-lint, govulncheck
nix run .#lint    # golangci-lint (see .golangci.yml)
nix run .#test    # full suite, -race
nix flake check   # build + format checks
```

See also: [FEATURES.md](FEATURES.md) (honest feature inventory),
[ROADMAP.md](ROADMAP.md) (direction),
[CONTRIBUTING.md](CONTRIBUTING.md),
[RELEASING.md](RELEASING.md) (release procedure and tag integrity).

## License

[MIT](LICENSE)
