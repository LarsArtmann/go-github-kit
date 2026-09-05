# go-github-kit

A thin operational kernel over `google/go-github/v69`: authentication,
rate-limit gate, retry with backoff, sentinel errors, ETag conditional
caching, concurrent pagination. Consumers keep native SDK types.

## Session context

- Extracted from the 2026-08-15 stream plan (Stream G) in the projects
  workspace; the seed code was github-local-sync's internal/github package.
- Consumer migrations (plan phases 2–4) are COMPLETE as of 2026-09-05:
  Standup-Killer (kit v0.2.0 + FetchPages), github-local-sync (kept its
  provider.Provider shape), standard-bug-tracking-schema (v66→v69 + kit at
  the AuthFactory seam, StatusError adapters, BBolt ETag kept), and the new
  `go-localsync/provider/github` optional nested module (unreleased; release
  follow-ups live in that repo's TODO_LIST).
- T12 shipped in kit v0.3.0 (2026-09-05): `ClassifyError` recognizes
  `*gh.RateLimitError`/`*gh.AbuseRateLimitError` → `ErrRateLimited`; the
  consumers' local shims are deleted (SK, provider). v0.3.0 also added
  `WithUserAgent`.
- Release state 2026-09-05: kit v0.3.0, go-localsync v0.5.0, and
  go-localsync/provider/github v0.1.0 (submodule tag: `provider/github/v0.1.0`)
  are tagged and pushed. The sbts `m5-adapters` branch is landed on `main`.
- **go-localsync is PUBLIC** (owner flipped it 2026-09-05): v0.5.0 and
  provider/github v0.1.0 resolve from proxy.golang.org (clean-cache
  `GOPROXY=https://proxy.golang.org GONOPROXY=none go list -m` + sumdb
  proof; tag hashes match the proxy's Origin) and both render on pkg.go.dev.
  Every LarsArtmann repo is public now — consumers need no GOPRIVATE, SSH,
  or PAT auth; github-local-sync dropped its git+ssh inputs +
  mkPreparedSource deps map the same day. Caveat: pkg.go.dev shows
  "License: UNKNOWN" (no detectable LICENSE file) and therefore hides
  provider/github's docs — adding a LICENSE is an owner call.
- **Private-repo GitHub Actions are billing-blocked** (SK CI run failed in
  3s: "recent account payments have failed or your spending limit needs to
  be increased"). Kit and go-localsync are public — their CI runs normally
  (localsync's first-ever real runs started 2026-09-05 after the flip and
  are being debugged there). SK/sbts workflows are enabled and correct;
  they execute once billing is fixed. sbts CI runs only on
  `main`/`develop` — now exercised, since `m5-adapters` landed.
- **The auto-git daemon races agents**: heuristic auto-commits can capture
  HALF-EDITED states (a go.mod bump once landed without the matching code).
  Before trusting any commit — local, daemon, or remote — verify the tree
  actually builds: baseline gates are build+test+lint, not a clean `git status`.
- **treefmt vs templ**: formatters must exclude templ-generated files (the `*_templ` suffix); gofumpt
  rewriting templ's generated output makes `templ generate -check`
  permanently red (fixed in the gls flake via treefmt excludes).
- Convention template: `go-crush-data` (CI matrix, golangci config,
  RELEASING/SECURITY/CODEOWNERS, renovate, nightly fuzz, doc-link gate,
  TODO_LIST stable IDs, tag-after-final-commit).

## Release gate checklist (per-repo, from the 2026-09-05 train)

1. Baseline BEFORE editing: build + tests + lint + `nix build`/`flake check`.
2. Verify under `set -o pipefail`; never trust a piped exit code.
3. go.mod: no `replace`, no pseudo-versions in the tagged module.
4. Commit release prep with a real message (daemon may sweep first — check
   `git log` before assuming your commit is missing).
5. Tag only after origin CI is green on the exact HEAD (`gh run list`).
6. Annotated tag; push; proxy/VCS verify from a clean GOMODCACHE temp module.

## Technical facts (non-obvious, hard to rediscover)

- **All kernel behavior lives in `http.RoundTripper`s** — never in wrapper
  methods. `Kernel` embeds `*gh.Client`; consumers compile unchanged. This
  is the wrap contract (CONTRIBUTING.md); breaking it is a design regression.
- **go-github's native pre-flight check refuses at exactly Remaining==0**
  using its own wall clock (github.go `checkRateLimitBeforeDo`); the kit
  cannot inject time there. Tests use Remaining≥1 to isolate the kit gate;
  the kit's gate (floor 10) normally acts first anyway.
- **The probe stack deliberately excludes the gate** (recursion) **and the
  ETag layer** (a cached probe answer would masquerade as fresh budget).
- **Retry safety rule**: 429 always retried (GitHub rejects pre-processing);
  5xx only for idempotent methods; POST never.
- **`errors.AsType` returns `(T, bool)`** — single-value use does not
  compile. `StatusError.Unwrap() []error` returns sentinel + original so
  `errors.Is` and `errors.As` both work.
- **Rate headers**: GitHub's real spelling is `X-Ratelimit-*`; the kit also
  accepts `X-RateLimit-*` (proxies normalize). Malformed/negative parse as
  "no information", never zero budget.
- **GitHub reports resets as Unix seconds** — sub-second precision is
  truncated away. Tests that assert real waits must account for up to 1s of
  truncation (see `kernel_test.go` floor-wait spec).
- GOMODCACHE on this machine is `/mnt/buildcache/go-mod` (not the default).

## Commands

```bash
go test -race -count=1 ./...                       # full suite
nix run .#lint                                     # golangci-lint
nix run .#test                                     # suite via nix
nix flake check                                    # build + format + vendorHash
scripts/check-doc-links.sh                         # markdown/citation guard
```

## Gotchas

- Nix ignores untracked files: `git add` new `.go` files before
  `nix flake check`, or expect misleading "undefined:" errors.
- `go.mod`/`go.sum` changes require re-deriving `flake.nix` `vendorHash`
  (`nix build .#default` → copy `got:` hash → build again).
- The lint config excludes several strictness linters for test files
  (fixture servers and table cases make them noise); the Ginkgo bootstrap is
  excluded from paralleltest (RunSpecs owns parallelism).
