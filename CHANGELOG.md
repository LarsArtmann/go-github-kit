# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

Policy: only changes observable by consumers of the module get entries —
API, behavior, packaging, and CI-visible contracts. Doc-only edits
(comments, README/AGENTS/docs, plans, status reports) are not changelogged.

## [Unreleased]

### Changed

- Test helpers no longer import `encoding/json/v2`; the suite now builds
  and runs without `GOEXPERIMENT=jsonv2`. Also fixes the CI golangci-lint
  findings on a fresh checkout (`stdversion` on `json.MarshalWrite`,
  `importcomment` on the doc.go package comment).

## [0.1.0] - 2026-08-16

### Added

- `Kernel`: a native `*github.Client` (google/go-github v69) whose HTTP
  stack applies, outermost first, the rate-limit gate, header-fed budget
  tracking, retry with backoff, and (optionally) ETag conditional caching.
  Construct with `New`; authentication is opt-in via `WithPAT` or
  `WithAuthTokenFromEnv`.
- `RateLimitSnapshot` and `ParseRateLimitHeaders` parse GitHub's
  `X-RateLimit-*` family (both header spellings); malformed or negative
  values read as "no information", never as zero budget.
- Rate-limit gate: waits for the window to reset when the remaining budget
  is at or below `MinRemaining` (default 10), bounded by `MaxWait`
  (default 15m, beyond which it fails fast with `ErrRateLimited`). Unknown
  budgets are resolved by a lazy `GET /rate_limit` probe with a 30s
  cooldown; a failed probe never blocks traffic.
- Retry: `429` always retried; `5xx` retried only for idempotent methods —
  POST is never auto-retried. `Retry-After` honored, capped at `MaxBackoff`.
  Default: 3 retries, 1s→30s exponential backoff, request bodies replayed.
- Sentinel errors `ErrAuthRequired`, `ErrForbidden`, `ErrRateLimited`,
  `ErrNotFound`, `ErrAPIUnavailable`; `ClassifyError` wraps any failure in a
  `*StatusError` carrying both the sentinel (for `errors.Is`) and the
  original error (for `errors.AsType`/`errors.As`), so classification never
  destroys information. `403` is disambiguated into rate-limit vs.
  forbidden via the response headers.
- `FetchPages[T]`: concurrent pagination with page-1-first discovery, a
  bounded worker pool (default 3), early exit at the first short page, and
  page-ordered results.
- ETag conditional caching (opt-in via `WithETagCache`): GET-only
  `If-None-Match` revalidation, `304` responses synthesized as `200` with
  the cached body, and entries keyed by a credential fingerprint so two
  tokens never share cache entries.
- `WithBaseURL` for GitHub Enterprise Server, appending the trailing slash
  the native SDK needs and rejecting host-less URLs.
- Ginkgo/Gomega behavior suite covering authentication, pagination, error
  classification, budget waiting, retry behavior, and construction; fuzz
  target for `ParseRateLimitHeaders`; runnable godoc examples.

[Unreleased]: https://github.com/LarsArtmann/go-github-kit/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/LarsArtmann/go-github-kit/releases/tag/v0.1.0
