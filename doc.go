// Package githubkit is a thin, composable HTTP kernel on top of
// google/go-github/v69: authentication, rate-limit budgeting fed from
// X-RateLimit-* response headers, retry with exponential backoff, typed
// sentinel errors, conditional ETag caching, and concurrent pagination.
//
// The kit wraps rather than replaces go-github: [New] returns a native
// [*github.Client], so every SDK method and type keeps working. All kernel
// behavior lives in the client's http.RoundTripper stack, which means
// consumers get rate limiting, retry, and header-driven budget tracking for
// every call — including calls made with code that has never heard of this
// package.
//
// # Design in one paragraph
//
// Each concern is one small RoundTripper, composed outermost-in as:
// rate-limit gate → header feed → retry → ETag cache → tuned base transport.
// The gate consults a shared [RateLimitCache] that the feed layer keeps
// current from every response; when the cache is empty the gate lazily
// probes GET /rate_limit (which itself feeds the cache through its response
// headers). Retry only re-sends requests that are safe to repeat (429 always,
// since GitHub rejects before processing; 5xx only for idempotent methods).
// [ClassifyError] maps final errors onto package sentinels while preserving
// the underlying [*github.ErrorResponse] for errors.Is and errors.AsType.
//
// # Usage
//
//	client, err := githubkit.New(
//		githubkit.WithAuthTokenFromEnv("GITHUB_TOKEN", "GH_TOKEN"),
//	)
//	// client is a *github.Client: use it exactly as before.
//	events, _, err := client.Activity.ListEventsPerformedByUser(ctx, "octocat", false, nil)
package githubkit
