# Releasing

How this repository cuts a version. Read fully before tagging; module-proxy
poisoning from a botched tag is the one mistake that cannot be undone
cheaply.

## Preconditions

1. The full gate is green locally:

   ```bash
   go build ./... && go test -race ./... && nix run .#lint && nix flake check
   ```

2. `CHANGELOG.md` has a section for the new version under `[Unreleased]`
   content that is ready to ship (or move the `[Unreleased]` block under a
   new `## [x.y.z] - YYYY-MM-DD` heading).

3. The working tree is clean and `master` is pushed.

4. **CI is green on ALL matrix legs on origin** (ubuntu, windows, macos)
   for the commit you are about to tag. The local gate tests one platform
   only. Never tag while CI is still running — an immutable red tag stays
   red forever.

## Procedure

1. Create an **annotated** tag (lightweight tags are invisible to some
   tooling): `git tag -a vX.Y.Z -m "vX.Y.Z"`.

2. Push the tag: `git push origin vX.Y.Z`.

3. The `Release` workflow (`.github/workflows/release.yml`) fires on the tag,
   extracts the matching CHANGELOG section, and publishes the GitHub Release.

4. **Verify tag integrity** — local and remote must point at the same commit:

   ```bash
   git rev-parse vX.Y.Z^{commit}
   git ls-remote --tags origin vX.Y.Z
   ```

   If they differ, STOP: a moved tag may already be cached by
   proxy.golang.org. Never delete/re-push a published tag without checking
   whether the module proxy has crawled it; a retracted version (see below)
   is cheaper to reason about than a re-used one.

5. After the proxy crawls the tag (minutes), check
   <https://pkg.go.dev/github.com/LarsArtmann/go-github-kit> renders the new
   version, and smoke-test consumption:

   ```bash
   GOPROXY=proxy.golang.org go mod download github.com/LarsArtmann/go-github-kit@vX.Y.Z
   ```

## If a tag is wrong anyway

1. Prefer **retraction** over deletion: add `retract vX.Y.Z` to `go.mod`,
   release the retraction, then cut `vX.Y.Z+1`.
2. Deleting a tag that proxy.golang.org has already crawled does NOT
   un-crawl it; only a never-crawled tag (brand new, unusual) can be safely
   deleted and re-pushed.
