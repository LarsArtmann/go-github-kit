# Features

Honest inventory of what this library does, by status. Code is the source of
truth; every row cites its evidence. Update rows in place when status changes.

| Status | Feature | Evidence |
|---|---|---|
| FULLY_FUNCTIONAL | `Kernel` embeds `*gh.Client` with a gate→feed→retry→etag→base transport stack | `client.go:28`, `transport.go` |
| FULLY_FUNCTIONAL | Token resolution: explicit `WithPAT`, `WithAuthTokenFromEnv` (defaults `GITHUB_TOKEN`, `GH_TOKEN`), `ErrAuthRequired` naming tried variables | `client.go:156` |
| FULLY_FUNCTIONAL | `WithBaseURL`: host validation, trailing-slash normalization (native-SDK parity), GHES support | `client.go:195`, `kernel_test.go` construction specs |
| FULLY_FUNCTIONAL | `ParseRateLimitHeaders`: both header spellings, malformed/negative rejected as "no information" | `ratelimit.go:35`, `ratelimit_test.go` |
| FULLY_FUNCTIONAL | Rate-limit cache: authoritative updates, pessimistic decrements floored at 0, nil-safe | `ratelimit.go:108` |
| FULLY_FUNCTIONAL | Gate: wait-until-reset at budget floor, `MaxWait` fail-fast, lazy `/rate_limit` probe with 30s cooldown, probe failure never blocks | `transport.go:116`, `probe.go` |
| FULLY_FUNCTIONAL | Retry: 429 always, 5xx idempotent-only (POST never), Retry-After capped, body replay, drain+close | `transport.go:181` |
| FULLY_FUNCTIONAL | Sentinels + `StatusError` + `ClassifyError`; 403 disambiguated by rate headers; originals preserved via `Unwrap() []error` | `errors.go` |
| FULLY_FUNCTIONAL | `FetchPages[T]`: page-1-first, bounded concurrency (default 3), short-page early exit, page-ordered results, progress callback | `pagination.go:58`, `pagination_test.go` |
| FULLY_FUNCTIONAL | ETag cache: FIFO eviction (default 256), credential-fingerprinted keys, 304→synthesized 200 with `X-Github-Kit-From-Cache` marker | `etag.go:62`, `etag_test.go` |
| FULLY_FUNCTIONAL | `RateLimitSnapshot`/`RefreshRateLimit`/`ETagStats` accessors for live state | `client.go` |
| FULLY_FUNCTIONAL | Ginkgo/Gomega behavior suite (auth, pagination, not-found, budget wait, retry, no-retry, construction) | `kernel_test.go`, `githubkit_suite_test.go` |
| FULLY_FUNCTIONAL | Fuzz target for `ParseRateLimitHeaders` with seed corpus | `fuzz_test.go` |
| FULLY_FUNCTIONAL | Runnable godoc examples (New, FetchPages, ClassifyError) | `example_test.go` |
| FULLY_FUNCTIONAL | Committed benchmark baseline (header parsing, cache) + benchstat CI | `bench_test.go`, `docs/benchmarks/baseline-benchmarks.txt` |
| PARTIALLY_FUNCTIONAL | CI workflows written (3-leg matrix, `-count=2`, `go mod verify`, coverage ≥85%, golangci-lint, govulncheck, flake check, nightly fuzz, monthly flake-lock bump, bench trend, tag-driven release) — first run on origin pending | `.github/workflows/` |
