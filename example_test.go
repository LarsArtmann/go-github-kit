package githubkit_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"time"

	githubkit "github.com/LarsArtmann/go-github-kit"
	gh "github.com/google/go-github/v69/github"
)

// rateHeaderMiddleware stamps healthy budget headers on every response so
// the kernel's gate never blocks the example flow, and answers the lazy
// /rate_limit probe.
func rateHeaderMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-RateLimit-Limit", "5000")
		w.Header().Set("X-RateLimit-Remaining", "4999")
		w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10))

		if r.URL.Path == "/rate_limit" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// Construct a kernel from the environment. WithAuthTokenFromEnv tries each
// variable in order and fails with ErrAuthRequired when none is set, reporting
// every name it tried.
func ExampleNew() {
	server := httptest.NewServer(rateHeaderMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"login": "octocat", "id": 583231}`))
	})))
	defer server.Close()

	// Production code resolves the token from the environment:
	// githubkit.WithAuthTokenFromEnv(). The example uses a fake PAT.
	kernel, err := githubkit.New(
		githubkit.WithPAT("ghp_example"),
		githubkit.WithBaseURL(server.URL),
	)
	if err != nil {
		fmt.Println("construction failed:", err)
		return
	}

	user, _, err := kernel.Users.Get(context.Background(), "")
	if err != nil {
		fmt.Println("request failed:", err)
		return
	}

	fmt.Println("hello,", user.GetLogin())

	// Output: hello, octocat
}

// FetchPages walks a paginated endpoint concurrently: page 1 alone, then pages
// 2..N through a bounded worker pool, stopping at the first short page.
func ExampleFetchPages() {
	mux := http.NewServeMux()
	mux.HandleFunc("/events", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch r.URL.Query().Get("page") {
		case "", "1":
			_, _ = w.Write([]byte(`[{"id": "e1"}, {"id": "e2"}]`))
		case "2":
			_, _ = w.Write([]byte(`[{"id": "e3"}, {"id": "e4"}]`))
		default:
			_, _ = w.Write([]byte(`[{"id": "e5"}]`))
		}
	})

	server := httptest.NewServer(rateHeaderMiddleware(mux))
	defer server.Close()

	kernel, err := githubkit.New(
		githubkit.WithPAT("ghp_example"),
		githubkit.WithBaseURL(server.URL),
	)
	if err != nil {
		fmt.Println("construction failed:", err)
		return
	}

	events, err := githubkit.FetchPages(context.Background(),
		githubkit.PaginationOptions{MaxPages: 10, PerPage: 2, Concurrency: 1},
		func(ctx context.Context, page int) ([]*gh.Event, error) {
			events, _, err := kernel.Activity.ListEvents(ctx, &gh.ListOptions{Page: page, PerPage: 2})
			return events, err
		})
	if err != nil {
		fmt.Println("walk failed:", err)
		return
	}

	fmt.Println("fetched", len(events), "events")

	// Output: fetched 5 events
}

// ClassifyError maps any call failure to a StatusError wrapping both a kit
// sentinel (for errors.Is) and the original error (for errors.AsType), so
// classification never destroys information.
func ExampleClassifyError() {
	server := httptest.NewServer(rateHeaderMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message": "Not Found"}`))
	})))
	defer server.Close()

	kernel, err := githubkit.New(
		githubkit.WithPAT("ghp_example"),
		githubkit.WithBaseURL(server.URL),
	)
	if err != nil {
		fmt.Println("construction failed:", err)
		return
	}

	_, _, err = kernel.Repositories.Get(context.Background(), "LarsArtmann", "does-not-exist")

	classified := githubkit.ClassifyError(err)

	if statusErr, ok := errors.AsType[*githubkit.StatusError](classified); ok {
		if errors.Is(statusErr, githubkit.ErrNotFound) {
			fmt.Println("no such repository")
		}
	}

	if ghErr, ok := errors.AsType[*gh.ErrorResponse](err); ok {
		fmt.Println("server said:", ghErr.Message)
	}

	// Output:
	// no such repository
	// server said: Not Found
}

// The environment fallback fails with a typed, actionable error.
func ExampleWithAuthTokenFromEnv() {
	_ = os.Unsetenv("GITHUB_TOKEN")
	_ = os.Unsetenv("GH_TOKEN")

	_, err := githubkit.New(githubkit.WithAuthTokenFromEnv())
	fmt.Println(errors.Is(err, githubkit.ErrAuthRequired))

	// Output: true
}
