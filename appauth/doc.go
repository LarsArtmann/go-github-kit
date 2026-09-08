// Package appauth implements GitHub App authentication for go-github-kit:
// signing installation JWTs, minting short-lived installation tokens, and
// wiring them into HTTP transports and kit kernels.
//
// # The auth flow
//
// GitHub Apps authenticate in two steps:
//
//  1. The App signs a RS256 JWT with its private key (issued by GitHub's
//     App settings page). The JWT lives at most 10 minutes; we backdate
//     iat by 60s for clock skew and let each JWT expire after 10 minutes.
//  2. The JWT authorizes calls to the REST Apps API only. The only call
//     that matters here is "create an installation access token"
//     (POST /app/installations/{id}/access_tokens), which returns a
//     Bearer token valid for ~1 hour and scoped to one installation.
//
// AppAuth produces step-1 JWTs. AppAuthTransport attaches them to
// requests against the Apps API (use NewAppsClient for a ready-made
// *github.Client whose AppsService speaks JWT auth). InstallationTokenSource
// performs step 2 and caches the minted token until one minute before
// expiry, so concurrent consumers share a token and refresh happens
// safely inside the validity window. InstallationTokenTransport attaches
// the minted token to every request against the installation-scoped API.
//
// # Wiring a kernel per installation
//
// A kit kernel needs one installation token per customer installation.
// Until kit grows a first-class option, compose it through the existing
// surface:
//
//	apps, _ := appauth.NewAppsClient(app, "")            // JWT auth, api.github.com
//	src, _ := appauth.NewInstallationTokenSource(instID, apps.Apps)
//	tr, _ := appauth.NewInstallationTokenTransport(src, nil)
//	kernel, _ := githubkit.New(
//	    githubkit.WithBaseURL(base),
//	    githubkit.WithHTTPClient(&http.Client{Transport: tr}),
//	)
//
// WithHTTPClient makes the installation transport the innermost kernel
// layer, so every kernel request carries the minted Bearer token and
// kernel error/etag/rate-limit layers sit on top as designed.
//
// # Operational constraints (verified against GitHub's docs)
//
//   - JWTs: RS256 only, iss = numeric App ID, exp at most 10 minutes out.
//   - Installation tokens expire after about 1 hour; we treat the
//     refresh lead as 1 minute and refuse tokens with suspicious
//     lifetimes (> 24h) because they indicate a mis-parsed response.
//   - Minting a token counts against the Apps API rate limit (low,
//     per-App), not the installation's core limit — hence the caching.
//   - Secondary rate limits still apply to the installation token's
//     requests; pair this package with the kit rate-limit layer.
//
// # Known gaps
//
//   - Token minting is not single-flight across goroutines (the cache is
//     mutex-guarded, but N concurrent callers at expiry may mint N
//     tokens). The last token wins; correctness is unaffected, mint
//     budget is slightly wasted. A single-flight wrapper is a planned
//     improvement.
//   - NewAppsClient hardcodes the standard API root when baseURL is
//     empty; GitHub Enterprise Server users must pass their root
//     explicitly (with trailing slash).
package appauth
