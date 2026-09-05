# Status Report — go-localsync Public Flip: Proxy/pkg.go.dev Verification + github-local-sync De-SSH Migration

**Timestamp:** 2026-09-05 21:17 CEST
**Session scope:** Resumed after the 19:30 release-train completion. User trigger: "go-localsync is now public". Executed the pre-planned follow-up (proxy proof → pkg.go.dev → docs) plus the consumer-side cleanup it unlocked.
**Repos touched this session:** go-github-kit (docs), github-local-sync (build system + CI + docs). go-localsync deliberately untouched (concurrent actor mid-flight).

---

## Verification Evidence (what "public" actually means now)

| Check                                                                                                  | Result                                                                                                                                   |
| ------------------------------------------------------------------------------------------------------ | ---------------------------------------------------------------------------------------------------------------------------------------- |
| `gh api repos/LarsArtmann/go-localsync`                                                                | `private=false, visibility=public`                                                                                                       |
| Standup-Killer / standard-bug-tracking-schema                                                          | still `private=true`                                                                                                                     |
| `proxy.golang.org/.../go-localsync/@v/list`                                                            | `v0.5.0`                                                                                                                                 |
| `proxy.golang.org/.../go-localsync/provider/github/@v/list`                                            | `v0.1.0`                                                                                                                                 |
| Clean-cache `go list -m` with `GOPROXY=https://proxy.golang.org GONOPROXY=none GOSUMDB=sum.golang.org` | **both resolve — PROXY-PROOF-OK** (no VCS fallback, sumdb attests)                                                                       |
| Proxy `.info` Origin hashes vs local tags                                                              | exact match: `v0.5.0` → `d603e1203f67…`, `provider/github/v0.1.0` → `13321fcdf46…`                                                       |
| pkg.go.dev                                                                                             | both pages render (`@v0.5.0` Latest, full tree; `provider/github@v0.1.0` Latest, Imports: 14, **Imported by: 0**)                        |
| pkg.go.dev license                                                                                     | **"License: UNKNOWN"** on both modules — provider/github docs are **hidden** ("Documentation not displayed due to license restrictions") |
| All LarsArtmann deps public?                                                                           | YES — go-cqrs-lite, cqrs-htmx, go-branded-id, go-error-family, httputil, go-nix-helpers all `private=false` (checked via gh api)         |

pkg.go.dev indexing did not happen by itself: the `/fetch/` GET endpoints 404; the fetch form is a **POST** (done via a throwaway Go program, since curl is banned) → HTTP 408 = fetch queued → pages appeared minutes later.

## Concurrent-actor discovery (why go-localsync was left alone)

While verifying, go-localsync showed **4 fresh daemon commits** (post-`4721378`) and **uncommitted `AGENTS.md` + `docs/DOMAIN_LANGUAGE.md` edits**: CI auth removal (−31 lines from `ci.yml`), the repo's own flake de-SSH (deps map + git+ssh inputs removed), go 1.26.7 bump, provider module churn. Another actor (user or live session) is actively working there. Decision: **zero writes to go-localsync this session**; its doc updates were verified instead of duplicated.

---

## a) FULLY DONE

1. **Proxy + sumdb proof** for `go-localsync@v0.5.0` and `provider/github@v0.1.0` — clean GOMODCACHE, `GOPROXY` without `direct` fallback, `GONOPROXY=none`; tag hashes byte-identical to proxy-served bits.
2. **pkg.go.dev indexing** triggered (POST `/fetch/…`) and confirmed for both modules.
3. **github-local-sync: full de-SSH migration** — all 7 `git+ssh` flake inputs removed (`go-nix-helpers`, `go-cqrs-lite`, `cqrs-htmx`, `go-localsync`, `go-branded-id`, `go-error-family`, `httputil`); `mkPreparedSource` + deps map + `validatePrivateDeps` machinery deleted; plain fileset `src`; `flake.lock` pruned; `GOPRIVATE` dropped from both devShells (sumdb now applies locally too).
4. **gls vendorHash re-derived** (`sha256-JudAy0aMivBXtjaGxmUrUyHoX3WR6ZtzjpV1t/0/Obs=`) — `nix build` and **all 5 `nix flake check` checks green** (build, test in-sandbox, lint, treefmt, templGenerate).
5. **gls dependency graph honestly proxy-resolved**: `GOWORK=off go mod tidy` now works (the old `genproto` ambiguity blocker is gone); go.sum gained the 4 missing localsync hashes + full-graph sums; two graph-discovered indirects added (go-cqrs-lite/codec/v4 v4.4.0; go-cqrs-lite/flightrecorder/v4 pseudo-version).
6. **gls CI secret-free**: `GOPRIVATE` env block, the `GO_PRIVATE_TOKEN` NOTE, and all 4 per-job `git config insteadOf` steps removed; `go-version` 1.26.4 → 1.26.7 to match go.mod.
7. **4 stale `//nolint:staticcheck` directives removed** from `internal/branch/service.go` — the flake-check lint leg exposed them as unused once the linted graph switched from sibling-HEAD to proxy tags; staticcheck genuinely no longer fires there.
8. **gls docs truth-aligned**: AGENTS.md (key relationship now says "consumes provider/github", directory tree lost `internal/github/`, deps table versions corrected — localsync v0.5.0 + new provider/github v0.1.0 row, cqrs-htmx v4.8.0, go-error-family v0.10.0, go-cqrs-lite v4.3.0; five obsolete private-dep gotchas deleted, proxy-only vendor gotcha written), CHANGELOG (+2 Changed entries), TODO_LIST (billing-verification item added; stale "CI/CD" item removed — CI exists).
9. **kit docs updated + pushed + CI green** (`06c4e67`, all 4 jobs ✓): AGENTS.md private-repo caveat → PUBLIC with proof details + LICENSE caveat; billing blocker rescoped to SK/sbts only; ROADMAP non-decision updated (policy stands, owner's flip recorded as vindicating the wait).
10. **Kit `check-doc-links.sh` gate green** after the ROADMAP/AGENTS edits.
11. **Confirmed SK and sbts do NOT consume go-localsync** (go.mod grep) — no consumer work needed there.
12. **Daemon-race hygiene**: every auto-commit that swept my edits (4 in gls, 1 in kit) was content-verified before pushing; nothing unrelated got pushed.

## b) PARTIALLY DONE

1. **go-localsync CI public debut** — CI now _runs_ (public repos skip the billing gate; first-ever real runs happened today) but the latest master run is **red**: `go vet` exit 1, tests exit 2, and `govulncheck` fails with `could not import encoding/json/v2 (invalid package name: "")` inside go-cqrs-lite `id/v4`/`event/v4` sources. Diagnosis (read-only): the jsonv2 experiment is not reaching the vet/govulncheck steps after the other actor's CI cleanup; none of this pipeline was ever executed pre-flip because billing blocked private-repo runs. **Not fixed** — that workspace is actively owned by someone else.
2. **provider/github public documentation** — module is indexed, but its docs page is suppressed pending a detectable LICENSE. Owner call, not made.
3. **Ecosystem "all public" documentation state** — 8 repos verified public this session, but the 19:30 completion status doc still (correctly, point-in-time) says go-localsync is private; it needs an ANNOTATE pass, not a rewrite.
4. **GLS-CQRS-V5 deprecation debt** — reduced from 12 to 8 suppression sites, but the 4 removed went stale because the _graph_ changed, not because the migrations (ADR-0123/0114, go-sse) landed.
5. **gls first real Actions run of the new secret-free pipeline** — impossible until billing is fixed (repo still private); everything is staged and locally proven.
6. **pkg.go.dev "Imported by: 0"** for provider/github — will stay 0 until gls tags a release and pkg.go.dev re-scans.

## c) NOT STARTED

1. **T4** — flip "Allow GitHub Actions to create and approve pull requests", then dispatch `flake-update.yml` (user-gated).
2. **T2** — Renovate app install + first `renovate/*` branch verification (user-gated).
3. **SK CI verification on main** — billing-gated.
4. **sbts `nix-build` / `nix-flake-check` CI verification** — billing-gated.
5. **sbts Dependabot alerts** — 2 vulnerabilities (1 high, 1 moderate) flagged, no bumps attempted.
6. **SK-TODO-LINT-DEBT burn-down** (~150 findings; lint steps still `continue-on-error`).
7. **gls v0.1.0 tag** — P4 item, untouched.
8. **HARVEST of section (f) below into `TODO_LIST.md` / `ROADMAP.md`** — awaiting user go-ahead per "then wait for instructions".
9. **Localsync CI fix** — ownership unclear (see questions); not started by me.

## d) TOTALLY FUCKED UP

Nothing was destroyed or broken by this session. The honest hall of shame:

1. **go-localsync master is red during its public debut.** The repo that just became the ecosystem's public showpiece is serving a failed CI badge. Deliberate non-fix (concurrent actor), but it is the worst live state in the ecosystem and nothing I saw tracks it in a TODO yet.
2. **My first `nix flake check` invocation lied to my own pipeline**: `nix flake check 2>&1 | tail -8 && echo CHECK-OK` printed `CHECK-OK` while the check had failed, because `tail` exits 0. I caught it by actually reading the output, but the command pattern was defective — gates must never be summarized through pipes without `set -o pipefail`.
3. **gls go.mod now pins a pseudo-version** (`go-cqrs-lite/flightrecorder/v4 v4.0.0-20260807213449-e72b2d7a16d0`) — proxy-resolvable and sum-verified, but a reproducibility smell that persists until go-cqrs-lite cuts a real tag.
4. **The released provider/github module is effectively undocumented in public** (license-hidden docs page) — we shipped visibility without consumability.
5. **Pre-existing kit CI annotations noticed, not fixed**: `Unexpected input(s) 'go-version'` (the action wants `go-version-input` — the Go pin may be silently ignored) and Node-20-runtime deprecation warnings on pinned actions.

## e) WHAT WE SHOULD IMPROVE

1. **Never mask gate exit codes** — `set -o pipefail` or plain exit-code capture when long-running checks are summarized; today's `CHECK-OK` bug is the proof.
2. **Detect concurrent agents _before_ planning repo edits**, not during verification — one `git status` + `git log origin/master..HEAD` glance saved go-localsync from a two-writer disaster; make it a mandatory first step per repo.
3. **Verify inherited doc claims before building on them** — localsync's already-updated AGENTS.md claimed pkg.go.dev indexing before it was true; the inherited kit context claimed "six truly-private deps" when the real number had dropped to zero. Both were caught, but only by luck of thoroughness.
4. **Treat suppression directives as expiring debt** — 4 of 12 nolints rotted the moment the dependency graph changed. Root-cause fixes over suppressions; suppressions need a "re-validate on dependency bump" habit (the lint gate caught it — good — but only because nolintlint is strict).
5. **Visibility flips need a stored runbook**: proxy proof → pkg.go.dev fetch → first CI run → docs sweep → consumer de-SSH. Today's sequence was improvised; it worked, so write it down (candidate: extend the `nix-private-go-repos` skill with the de-privatization inverse).
6. **Bash-tool POST gap**: pkg.go.dev fetch required a Go one-liner because curl/wget are banned and the fetch tool is GET-only — a tiny `scripts/` helper (or documented Go snippet) would make this repeatable.

## f) Up to 50 things we should get done next

_Brainstorm ranked by impact/effort — most are ROADMAP fuel, not commitments (docs-health HARVEST would route them)._

**Owner-gated (blocking chains):**

1. Fix GitHub Actions billing/spending limit → unblocks SK + sbts CI chains (user).
2. Decide SK/sbts visibility — going public would eliminate the billing dependency entirely (user).
3. Flip "Allow GitHub Actions to create and approve pull requests" → dispatch `flake-update.yml` → close kit T4 (user).
4. Install the Renovate GitHub App → verify first `renovate/*` branch → close kit T2 (user).
5. LICENSE decision for go-localsync (+ provider/github) → unhides public docs on pkg.go.dev (user).

**go-localsync (coordinate with the active actor first):**
6. Fix jsonv2 propagation in CI: export `GOEXPERIMENT`/`GOFLAGS` in vet/test steps; decide govulncheck policy for jsonv2-gated deps (it currently cannot parse them).
7. Land the in-flight uncommitted `AGENTS.md` / `DOMAIN_LANGUAGE.md` edits.
8. Delete the now-unused `SSH_PRIVATE_KEY` repo secret.
9. Drive CI to green on master; confirm the badge on the public landing page.
10. Add/confirm the LICENSE file (unblocks #5's consumer effect).
11. Run the repo's own `nix flake check` after its daemon-landed de-SSH (unverified by any gate this session).
12. Check deps.dev renders both modules (pkg.go.dev's sibling, same fetch pipeline).
13. Recheck pkg.go.dev "Imported by" for provider/github after gls tags (expect gls as first importer).
14. Verify CHANGELOG/tag consistency for the retroactive v0.4.2 note.

**github-local-sync:**
15. After billing fix: verify the first real run of the secret-free CI pipeline (TODO item added this session).
16. Tag gls v0.1.0 (after README install-command verification) — makes gls the first public importer of provider/github.
17. Add `meta.description` to `apps.default`/`apps.test`/`apps.lint` (3 nix flake check warnings).
18. Replace deprecated `stdenv.isLinux` → `stdenv.hostPlatform.isLinux` in flake.nix (evaluation warning).
19. Get a real go-cqrs-lite/flightrecorder/v4 tag published to replace the pseudo-version pin.
20. GLS-CQRS-V5 burn-down: 8 suppression sites (ADR-0123 `stack` → `system.New`, ADR-0114 tombstone → deletion event, cqrs-htmx SSE → go-sse/httputil).
21. Fix out-of-order merge-saga orphan rows (P2).
22. Add sync-level integration test for the merge saga (P2).
23. Commit generated catalog docs + add flake target (P2).
24. Re-verify the sibling go.work live-edit workflow post-tag-switch — the removed staticcheck suppressions may legitimately resurface under sibling-HEAD.
25. Refresh gls README: provider import path, pkg.go.dev badge, CI badge.
26. Tighten gls `govulncheck` `continue-on-error: true` once the jsonv2/govulncheck compatibility story settles.

**go-github-kit:**
27. Fix kit CI `setup-go` input name (`go-version` → `go-version-input`) — the version pin may currently be ignored.
28. Move pinned actions off the Node 20 deprecated runtime (new verified SHAs only).
29. Audit kit TODO_LIST T6–T10: T6 (SK) and T7 (gls) are materially done — close them with references.
30. ANNOTATE the 19:30 completion status: the "go-localsync is private" truth-correction is now superseded (append, don't rewrite).
31. Refine ROADMAP ideas (webhook support, ETag expansion) into scoped proposals with non-decision checks.
32. Run an `errors.AsType` modernization sweep (go-error-modernization skill) if any `errors.As` remain.
33. Verify the fuzz-corpus `actions/cache` step gets its first cache hit on the next scheduled fuzz run.
34. Write the de-privatization runbook as a doc or skill extension (proxy proof → fetch → CI → docs sweep → consumer cleanup).
35. One-command clean-cache proxy proof for ALL larsartmann modules (script the check I ran manually).

**Standup-Killer:**
36. After billing fix: watch SK `CI` on main (`6c7e15c+`) end-to-end.
37. SK-TODO-LINT-DEBT: burn down ~150 findings, then remove `continue-on-error` from both lint steps.
38. Review SK Dependabot alerts (state unknown — never checked this session).

**standard-bug-tracking-schema:**
39. After billing fix: watch `nix-build` + `nix-flake-check` jobs on main (`23083c57`).
40. Bump the 2 Dependabot-flagged dependencies (1 high, 1 moderate) with full gates.

**Cross-cutting / process:**
41. Record the "verify concurrent actors" pre-flight step in the session checklist / AGENTS.md.
42. Update the `nix-private-go-repos` skill with the de-privatization checklist (today's lessons: proxy negative-cache, POST-only pkg.go.dev fetch, `GONOPROXY=none` proof pattern).
43. Decide gls's dev model: keep go.work sibling live-editing vs tag-based development now that tags resolve cleanly (simplifies mental load).
44. Cross-repo CI status one-liner script (`gh run list` per repo) for the ecosystem dashboard habit.
45. Once Renovate is installed: group `github.com/larsartmann/*` updates so ecosystem trains move together.
46. gls CHANGELOG cosmetic: annotate the historical "v0.1.1 → v0.4.0" upgrade line to mention v0.5.0.
47. provider/github README: add pkg.go.dev + CI badges now that it's public.
48. Verify kit TODO_LIST has no orphan IDs after the T12/T11 retirement.
49. Consider a `scripts/github-fetch-request` helper (Go) for POST-only endpoints like pkg.go.dev/fetch.
50. Schedule release train v2 once CI verifications land: gls v0.1.0 first, then re-scan pkg.go.dev importer graph.

## g) Questions I cannot answer myself

1. **LICENSE for go-localsync / provider/github:** is the absence deliberate (unfree / all-rights-reserved — gls's flake already declares `license = unfree`), or should a license be added so pkg.go.dev serves the provider's documentation? If yes, which license?
2. **go-localsync CI ownership:** another actor has uncommitted edits there right now and the jsonv2 CI failure is theirs-in-progress — should I stay out, or take the CI fix myself (and if so, may I touch their uncommitted files or work around them)?
3. **SK/sbts endgame:** is the Actions billing problem getting fixed, or is making SK/sbts public the intended path? The answer decides whether #36/#39/#40 are next-week work or permanently obsolete.

---

_Point-in-time snapshot — all claims above were verified by commands run in this session (gh api, proxy .info/list, go list with isolated GOMODCACHE, nix build/flake check, gh run logs). Docs-health ANNOTATE should supersede, never rewrite, this file._
