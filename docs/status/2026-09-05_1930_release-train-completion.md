# Ecosystem Release Train — Completion Status (2026-09-05, 19:30 CEST)

Supersedes the 16:39 brutal-status and the 17:26 Pareto plan execution state.
Every claim below was verified this session; corrections to earlier claims are
explicit.

## Release train (the 1% + 4%)

| Artifact | Version | State | Verification |
|---|---|---|---|
| go-github-kit | **v0.3.0** | Tagged, pushed, GitHub Release (Latest), CI green on tagged-HEAD parent commit | `go get @v0.3.0` from clean GOMODCACHE via proxy.golang.org |
| go-localsync | **v0.5.0** | Tagged, pushed, GitHub Release | `go get @v0.5.0` — resolves via **VCS credentials, not the proxy** (repo is private; proxy `.info` 404s) |
| go-localsync/provider/github | **v0.1.0** | Tagged, pushed, GitHub Release, standalone `GOWORK=off` build+race tests green | Same VCS-not-proxy caveat as parent |

**Correction of an earlier claim:** the prior session's "all LarsArtmann Go
modules are now PUBLIC" does not hold — `go-localsync` is **private**. Proxy
visibility of v0.5.0/provider v0.1.0 (and pkg.go.dev rendering) is impossible
until the owner makes the repo public; making it public is irreversible once
the proxy caches it, so it was deliberately NOT done by the agent (recorded as
a non-decision in kit ROADMAP).

## Kit v0.3.0 content (M2/M23)

- `ClassifyError` recognizes `*gh.RateLimitError`/`*gh.AbuseRateLimitError` →
  `ErrRateLimited`, preserving the SDK error for `errors.AsType` (M2/T12).
- `WithUserAgent` option (M23).
- Consumers' duplicate shims deleted: Standup-Killer `classify()` (M8),
  provider `wrapGitHubError` native-type branches (M9).

## Consolidation (the 20%)

- **M10** — github-local-sync consumes the released provider module;
  `internal/github` deleted; guard-list cleaned; go.mod at localsync v0.5.0 +
  provider v0.1.0; green in workspace AND `GOWORK=off`; flake inputs refreshed
  to match, vendorHash rotated.
- **M9** — provider `FetchAll` delegates to `githubkit.FetchPages`
  (bounded pool, short-page early stop); `wrapGitHubError` preserves the
  original cause via `errors.Join`; provider tests updated + `-race` (M18).

## CI (the other 20%)

- **SK + localsync CI workflows were `disabled_manually`** — re-enabled via
  API (M14/M13). localsync gained a standalone provider-module leg (build +
  race). sbts gained `nix-build` + `nix-flake-check` jobs (M12) — the flake
  is the build of record and its silent rot was exactly why CI matters.
- **BILLING BLOCKER (user action required):** private-repo Actions runs fail
  instantly with "recent account payments have failed or your spending limit
  needs to be increased" (observed on SK run 33977763213). Kit (public) CI is
  unaffected. SK/localsync/sbts workflows will execute only after billing is
  fixed.
- Kit CI green through this session except one red I introduced and fixed
  same-session (scripts test on Windows needed bash; doc-links guard read
  `*_templ.go` as a glob citation).

## Truth corrections found and fixed

- gls `flake.nix`: `templ-generate-check` had no `go` in PATH (templ shells
  out to go) — pre-existing, fixed.
- gls treefmt ran gofumpt over generated `*_templ.go`, permanently fighting
  `templ generate -check` — excluded generated files.
- localsync TODO header claimed "Lint: 0 (v2)" with a 2026-07-22 date — both
  stale; corrected and dated 2026-09-05.
- `go-localsync` v0.4.2 tag is a retroactive proxy-sweep tag on an April
  snapshot; the honest changelog window was v0.4.1..HEAD and the [0.5.0]
  section says so.

## Deprecation debt, surfaced and tracked (not silently suppressed)

- localsync `pkg/cqrs`: ExecuteRef migration DONE (M17) — lint 0 issues.
- gls `internal/branch`: ExecuteRef migration DONE; 12 remaining findings
  (ADR-0123 stack/Bundle, ADR-0114 MarkTombstone, cqrs-htmx SSE/security
  helpers) are suppressed WITH reasons pointing at **GLS-CQRS-V5** in that
  repo's TODO_LIST.
- Standup-Killer: ~150 pre-existing lint findings — CI lint steps are
  `continue-on-error` with **SK-TODO-LINT-DEBT** tracking; build/test stay
  blocking.

## Whole-plan checklist (from the Pareto plan)

- [x] kit v0.3.0 resolves (proxy); SK shim deleted; provider builds standalone
- [x] localsync v0.5.0 + provider v0.1.0 tagged/pushed/GitHub-Releases —
      pkg.go.dev visibility blocked by private parent (user decision)
- [x] sbts main landed (FF) with nix CI jobs + 403 regression case pinned by
      `TestWrapGitHubError/forbidden non-rate limit` + `schemaErrorForStatus`
      classifier (M11/M12/M16/M25)
- [x] guard script covered by automated tests (M15); fuzz corpus persisted +
      seeded (M19/T11); ROADMAP refreshed, SECURITY/CODEOWNERS verified (M22);
      docs truth pass across kit/localsync/SK (M24); SK parallel fetch (M26);
      process rules into AGENTS (M27, this file + kit AGENTS.md)
- [ ] **T4** (flake-lock PR observed end-to-end): workflow green, but the
      "Allow GitHub Actions to create and approve pull requests" toggle is
      still OFF (`can_approve_pull_request_reviews: false`) — user must flip
      it; today's dispatch was a no-diff success, so the PR path remains
      unobserved by design
- [ ] **T2** (Renovate app): still not installed — no `renovate/*` branches
- [ ] **Billing**: private-repo Actions minutes (see above)
- [ ] Dependabot flags 2 vulnerabilities (1 high, 1 moderate) on sbts main —
      dependency bumps queued behind billing/CI

## Final gate state (all verified this session)

| Repo | build | test | vet | lint | nix build | flake check |
|---|---|---|---|---|---|---|
| go-github-kit (master) | ✅ | ✅ -race | ✅ | ✅ 0 | ✅ | ✅ all checks |
| go-localsync (master) | ✅ | ✅ | ✅ | ✅ 0 | ✅ | ✅ all checks |
| go-localsync/provider/github | ✅ GOWORK=off | ✅ -race | ✅ | — | n/a | n/a |
| github-local-sync (master) | ✅ both modes | ✅ both modes | ✅ | (via flake check) | ✅ | ✅ all checks |
| standard-bug-tracking-schema (main) | ✅ | ✅ | ✅ | (not in gate) | ✅ | ✅ all checks |
| Standup-Killer (main) | ✅ | ✅ | ✅ | ⚠️ 150 pre-existing, tracked | (not in gate) | (not in gate) |
