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
- [ ] **T3** Observe the first nightly fuzz run (03:17 UTC); on green, flip
      the FEATURES row to FULLY_FUNCTIONAL. 5m — `.github/workflows/fuzz.yml`
- [ ] **T4** Observe the first monthly flake-lock PR; check the vendorHash
      guard fires correctly on a stale hash. 5m —
      `.github/workflows/flake-update.yml`
- [ ] **T5** After tagging v0.1.0, verify pkg.go.dev renders the module and
      smoke-test `go get` from a fresh temp module. 10m — `RELEASING.md`

## High

- [ ] **T6** Migrate Standup-Killer's GitHub integration onto the kit
      (rewrites the GitHub integration file, deletes the bare-PAT oauth2 path,
      adopts `FetchPages` for commits). Phase 2 of the extraction plan. 2h —
      `~/projects/Standup-Killer`
- [ ] **T7** Migrate Standup-Killer's OpenAI client to charm.land/fantasy
      (drops `sashabaranov/go-openai` and the AGENTS.md httpHeader workaround).
      Phase 2. 2h — `~/projects/Standup-Killer`
- [ ] **T8** Migrate github-local-sync onto the kit, keeping its
      `provider.Provider` shape and mapping its sentinels. Phase 3. 3h —
      `~/projects/github-local-sync`

## Medium

- [ ] **T9** Migrate standard-bug-tracking-schema plumbing onto the kit via
      adapters around `StatusError` (keep sbts's BBolt ETag layer — recorded
      recommendation). Phase 3. 3h — `~/projects/standard-bug-tracking-schema`
- [ ] **T10** Build the go-localsync GitHub-events `provider.Provider` over
      the kit. Phase 4. 4h — `~/projects/go-localsync`
- [ ] **T11** Mine nightly fuzz artifacts for corpus seeds once runs exist.
      ongoing — `.github/workflows/fuzz.yml`

## Parked (plan-level, tracked in the ecosystem plan — not this repo)

- Decide the go-localsync provider module layout (in-repo vs. optional
  module) before Phase 4 starts.
- Whether to tag v0.1.0 if GitHub Actions billing stays broken (seen on
  project-discovery-sdk); decision belongs to the extraction plan owner.
