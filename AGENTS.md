# go-github-kit

A thin operational kernel over `google/go-github/v69`: authentication,
rate-limit gate, retry with backoff, sentinel errors, ETag conditional
caching, concurrent pagination. Consumers keep native SDK types.

## Session context

- Extracted from the 2026-08-15 stream plan (Stream G) in the projects
  workspace; the seed code was github-local-sync's internal/github package.
- Consumer migration order (phases 2–4 of the plan): Standup-Killer →
  github-local-sync → standard-bug-tracking-schema → a go-localsync
  GitHub-events provider. Track via `TODO_LIST.md` T6–T10.
- Convention template: `go-crush-data` (CI matrix, golangci config,
  RELEASING/SECURITY/CODEOWNERS, renovate, nightly fuzz, doc-link gate,
  TODO_LIST stable IDs, tag-after-final-commit).

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
- **Tests require `GOEXPERIMENT=jsonv2`** (test files import
  `encoding/json/v2`); production code builds without it. CI and
  `nix run .#test` set it; plain `go test` in a shell without it fails to
  compile the test package.
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
GOEXPERIMENT=jsonv2 go test -race -count=1 ./...   # full suite
nix run .#lint                                     # golangci-lint
nix run .#test                                     # suite via nix (sets GOEXPERIMENT)
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
