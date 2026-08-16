# Contributing

Thanks for your interest in contributing!

## How to Contribute

1. Fork the repository
2. Create a feature branch
3. Make your changes
4. Submit a pull request

## The wrap contract (read before touching the transport stack)

The kit's whole value proposition is that consumers keep using native
`*github.Client` types. Every behavioral layer must live in an
`http.RoundTripper`, never in a wrapper method surface. If a change would
make consumers stop receiving native types (`*gh.Repository`, `*gh.Response`,
`*gh.ErrorResponse`), it does not belong in this library. The error
contract is part of this: `ClassifyError` must preserve the original error
alongside the sentinel, and `errors.AsType[*gh.ErrorResponse]` must keep
working — `errors_test.go` pins both.

## Development

```bash
nix develop       # dev shell: Go, golangci-lint, govulncheck, actionlint
nix run .#lint    # golangci-lint (~90 linters, see .golangci.yml)
nix run .#test    # race test via nix
go test ./...     # full suite (needs GOEXPERIMENT=jsonv2; tests import encoding/json/v2)
nix flake check   # build + format checks
```

No Nix? `GOEXPERIMENT=jsonv2 go build ./... && GOEXPERIMENT=jsonv2 go test -race ./...`
is the whole gate (the test files import `encoding/json/v2`). CI additionally
runs the plain tests twice (`-count=2`, state-leak detection), the Ginkgo
suite once with `-ginkgo.randomize-all` (Ginkgo forbids `go test -count>1`),
a coverage gate (≥85%), golangci-lint, govulncheck, and `nix flake check`
(see `.github/workflows/`).

### go.sum ↔ vendorHash coupling

The Nix package vendors dependencies and pins their hash in `flake.nix`
(`vendorHash`). Whenever `go.mod`/`go.sum` change, that hash must be
re-derived: run `nix build .#default`, copy the `got:` sha256 from the error
into `flake.nix`, build again. `scripts/check-vendor-hash.sh` fails fast in
CI when the two drift apart. Also note the Nix source filter ignores
untracked files — a new `.go` file that was never `git add`ed produces
misleading "undefined:" build errors inside `nix flake check`.

### Benchmarks

`docs/benchmarks/baseline-benchmarks.txt` is the committed baseline;
the `Benchmark trend` workflow compares every push against it and posts the
diff to the job summary. To regenerate the baseline:

```bash
GOEXPERIMENT=jsonv2 go test -run '^$' -bench . -count 6 . \
  > docs/benchmarks/baseline-benchmarks.txt
```

To compare locally: `go tool benchstat old.txt new.txt`.

## Conventions

- Tests come in two shapes, both required to stay honest: table-driven unit
  tests (in-package, `*_test.go`) and a Ginkgo behavior suite
  (`githubkit_suite_test.go`, `kernel_test.go`, black-box package). New
  observable behavior gets a spec; new parsing edge cases get table rows.
- The kernel's clock is injectable (`withClock` in tests); waiting behavior
  is tested with the stub clock in microseconds, not real sleeps. The one
  exception (`kernel_test.go`'s reset-wait spec) documents why real time is
  observable there.
- Every behavior the library promises has a pinning test; if you add a
  promise, add the test in the same commit.
- Never commit binary testdata; fixtures are built per-test.

## Reporting Issues

Please use GitHub Issues to report bugs or request features.
