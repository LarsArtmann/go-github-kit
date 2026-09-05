# Roadmap

Long-term direction and raw ideas. Items graduate to [TODO_LIST.md](TODO_LIST.md)
when they become actionable and bounded. This file records _why_, not _when_.

## Direction

This library is the shared operational kernel for every LarsArtmann project
that talks to the GitHub API. The roadmap optimizes for: staying thin over
google/go-github (wrap, never replace), correct behavior under adversarial
API conditions (rate limits, 5xx, auth failures), and boring infrastructure
that fails loudly before shipping. Consumers migrate onto it in the order:
Standup-Killer, github-local-sync, standard-bug-tracking-schema, and a
GitHub-events provider for go-localsync.

## Raw ideas (not yet actionable)

- Secondary rate-limit awareness: GitHub's `Retry-After` on abuse-detection
  403s is already honored by retry, but the gate does not yet model the
  separate search/commerce budgets. Waits for real consumer pain.
- GraphQL support: the kernel is REST-shaped (header-fed budgets). GraphQL
  cost accounting would be a separate, opt-in layer. No consumer needs it.
- Webhook/ETag polling helper: `FetchPages` + ETag cache covers most read
  polling; a typed "changed since" helper could build on both. Waits for
  the go-localsync provider to reveal the real shape.

## Recorded non-decisions (anti-drift)

These were consciously evaluated and rejected. Do not re-litigate without
new information.

- **No client-wrapper API.** The kit embeds `*gh.Client` instead of
  defining its own method surface; consumers keep native types and mocks.
  Decided 2026-08-15 (extraction plan).
- **No POST retry.** A failed POST may have taken effect; silently
  re-sending could double-create resources. `429` is the exception GitHub
  documents as safe (rejected pre-processing). Decided 2026-08-16.
- **No probe through the gate or ETag layer.** A probing gate would
  recurse; a cached probe answer would masquerade as fresh budget. The
  probe stack is deliberately feed+retry only. Decided 2026-08-16.
