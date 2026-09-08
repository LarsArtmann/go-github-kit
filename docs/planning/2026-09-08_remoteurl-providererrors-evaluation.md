# Evaluation: `remoteurl/` and `providererrors/` packages (plan V3 B-27)

**Date**: 2026-09-08
**Verdict**: remoteurl → NO new package; consolidate into one exported function (DONE). providererrors → NOT NOW (premature generalization).

## remoteurl/ — resolved by consolidation, not a package

The hypothesized package addressed one real problem: two base-URL
normalizers had already diverged inside this module —

- `resolveBaseURL` (root): empty → api.github.com, requires a host,
  appends the trailing slash;
- `normalizeBaseURL` (appauth): required an absolute URL explicitly,
  same trailing slash, no default.

Same job, different error contracts, different empty-string behavior —
a split brain forming in slow motion. The resolution is a single
exported `ResolveBaseURL(raw string) (*url.URL, error)` in the module
root that kernels, appauth, and external callers share; appauth's
private copy is deleted. The empty-string default (api.github.com)
matches the documented `NewAppsClient` contract. No new package is
warranted for one pure function — YAGNI.

## providererrors/ — deferred until a second provider exists

The idea: a provider-agnostic error taxonomy so S's backup pipeline
could treat GitLab/Gitea errors identically to GitHub's. The reality
checked against the code:

- The domain has exactly one provider. S sweeps GitHub; `classify()`
  (errors.go) already maps go-github's `RateLimitError`,
  `AbuseRateLimitError`, and `ErrorResponse` into the module's own
  `FetchErrorKind`/conflict/retryable taxonomy, and S's
  `ClassifyFetchError` consumes kinds, not go-github types.
- The seam that WOULD generalize is the internal classification kind,
  not a public package. When a second provider becomes a real roadmap
  item, the move is: promote the existing kind taxonomy into a small
  package, implement one classifier per provider, and keep call sites
  untouched. Doing it now would invent an abstraction against one data
  point and freeze the wrong seams.

Revisit when: a second forge enters the roadmap, or a third consumer
needs the classification kinds outside S.
