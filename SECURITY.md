# Security Policy

## Reporting a Vulnerability

This library is an HTTP client kernel: it holds an auth token in memory,
sends it as a bearer header, and caches response metadata (including ETag
fingerprints derived from the token). The attack surface is credential
handling and header parsing.

To report a vulnerability, open a private GitHub Security Advisory
(GitHub > Security > Advisories > New draft advisory). Include a
minimal reproduction and the affected version. You will receive a
response within 72 hours.

## Scope

- **In scope**: any input that causes a panic or crash via the public API
  (`New`, any transport-layer behavior reachable through a `Kernel`,
  `ParseRateLimitHeaders`, `FetchPages`, `ClassifyError`), credential
  leakage into logs or cross-credential cache poisoning in the ETag layer.
- **Out of scope**: the google/go-github SDK itself (report upstream),
  behavior documented as known limitations (the native pre-flight rate
  check described in README's "The native pre-flight check" section).
