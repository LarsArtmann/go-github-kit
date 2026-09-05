# TODO List

Short- and mid-term improvement tasks, ranked by impact. Completed items are
deleted from this file — their record lives in [CHANGELOG.md](CHANGELOG.md).
Long-term ideas live in [ROADMAP.md](ROADMAP.md).

Items carry stable IDs (`T1`, `T2`, …). Cite them as `TODO_LIST T3` in status
reports and annotations. New items take the next free number; IDs are never
renumbered, and deleting an item retires its ID for good.

## External (waiting on GitHub UI, schedules, or upstream)

- [x] **T1** Create the `LarsArtmann/go-github-kit` repository, push master,
      and watch the first CI run go green on all three matrix legs. 15m
- [ ] **T2** Install/enable the Renovate app (config validates; inert until
      the GitHub App is installed). 5m — `renovate.json`
- [ ] **T4** Observe a flake-lock PR open end-to-end (workflow now has the
      write permissions the bot was denied; the vendorHash guard is proven
      to fire on real drift — trigger `workflow_dispatch` or wait for the
      monthly cron). 5m — `.github/workflows/flake-update.yml`
- [ ] **T11** Mine nightly fuzz artifacts for corpus seeds once runs exist.
      ongoing — `.github/workflows/fuzz.yml`

## High

- [ ] **T12** Teach `ClassifyError` to recognize go-github's dedicated
      `*gh.RateLimitError` and `*gh.AbuseRateLimitError` types (map both to
      `ErrRateLimited`). Discovered during the consumer migrations: the
      kernel's gate only rejects budgets it already knows are empty, so the
      first teaching 403 with `X-RateLimit-Remaining: 0` surfaces as a raw
      SDK type — Standup-Killer and the go-localsync GitHub provider both
      carry identical local workarounds today. Ship with tests + a tag; the
      consumers can then drop their shims. 1h — `errors.go`

## Parked (plan-level, tracked in the ecosystem plan — not this repo)

- Follow-ups for the extracted `go-localsync/provider/github` module live in
  that repo's TODO_LIST (core release, parent pin bump, module tag, CI
  wiring, github-local-sync migration onto the shared provider).
