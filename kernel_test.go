package githubkit_test

import (
	"context"
	"errors"
	"net/http"
	"os"
	"strconv"
	"time"

	githubkit "github.com/LarsArtmann/go-github-kit"
	gh "github.com/google/go-github/v69/github"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Kernel", func() {
	var (
		ctx  context.Context
		fake *fakeGitHub
	)

	BeforeEach(func() {
		ctx = context.Background()
		fake = startFakeGitHub(func(w http.ResponseWriter, r *http.Request) { writeUser(w, r) })
		DeferCleanup(fake.Close)
	})

	Describe("authenticating requests", func() {
		It("sends the PAT as a bearer token on every request", func() {
			kernel, err := newBDDKernel(fake.URL)
			Expect(err).NotTo(HaveOccurred())

			_, _, err = kernel.Users.Get(ctx, "")
			Expect(err).NotTo(HaveOccurred())
		})

		It("rejects a missing token with a 401 surfaced as ErrAuthRequired", func() {
			kernel, err := githubkit.New(githubkit.WithBaseURL(fake.URL))
			Expect(err).NotTo(HaveOccurred())

			_, _, err = kernel.Users.Get(ctx, "")
			Expect(err).To(HaveOccurred())
			Expect(errors.Is(githubkit.ClassifyError(err), githubkit.ErrAuthRequired)).To(BeTrue())
		})
	})

	Describe("fetching the authenticated user", func() {
		It("returns the account the token belongs to", func() {
			kernel, err := newBDDKernel(fake.URL)
			Expect(err).NotTo(HaveOccurred())

			user, _, err := kernel.Users.Get(ctx, "")
			Expect(err).NotTo(HaveOccurred())
			Expect(user.GetLogin()).To(Equal("octocat"))
		})
	})

	Describe("walking paginated collections", func() {
		BeforeEach(func() {
			fake = startFakeGitHub(func(w http.ResponseWriter, r *http.Request) {
				switch pageOf(r) {
				case 1:
					writeEvents(w, eventsPage("e1", "e2"))
				case 2:
					writeEvents(w, eventsPage("e3", "e4"))
				case 3:
					writeEvents(w, eventsPage("e5"))
				default:
					writeEvents(w, nil)
				}
			})
			DeferCleanup(fake.Close)
		})

		It("returns every item in page order", func() {
			kernel, err := newBDDKernel(fake.URL)
			Expect(err).NotTo(HaveOccurred())

			events, err := githubkit.FetchPages(ctx,
				githubkit.PaginationOptions{MaxPages: 5, PerPage: 2},
				func(ctx context.Context, page int) ([]*gh.Event, error) {
					events, _, err := kernel.Activity.ListEvents(ctx, &gh.ListOptions{Page: page, PerPage: 2})
					return events, err
				})
			Expect(err).NotTo(HaveOccurred())

			ids := make([]string, 0, len(events))
			for _, event := range events {
				ids = append(ids, event.GetID())
			}
			Expect(ids).To(Equal([]string{"e1", "e2", "e3", "e4", "e5"}))
		})

		It("stops the walk at the short final page and never requests later pages", func() {
			kernel, err := newBDDKernel(fake.URL)
			Expect(err).NotTo(HaveOccurred())

			_, err = githubkit.FetchPages(ctx,
				githubkit.PaginationOptions{MaxPages: 5, PerPage: 2, Concurrency: 1},
				func(ctx context.Context, page int) ([]*gh.Event, error) {
					events, _, err := kernel.Activity.ListEvents(ctx, &gh.ListOptions{Page: page, PerPage: 2})
					return events, err
				})
			Expect(err).NotTo(HaveOccurred())
			Expect(fake.pageSeen("/events", 4)).To(BeFalse())
			Expect(fake.pageSeen("/events", 5)).To(BeFalse())
		})
	})

	Describe("when the requested user does not exist", func() {
		BeforeEach(func() {
			fake = startFakeGitHub(func(w http.ResponseWriter, r *http.Request) {
				writeJSONError(w, http.StatusNotFound, "Not Found")
			})
			DeferCleanup(fake.Close)
		})

		It("fails with ErrNotFound while preserving the native API error", func() {
			kernel, err := newBDDKernel(fake.URL)
			Expect(err).NotTo(HaveOccurred())

			_, _, err = kernel.Users.Get(ctx, "ghost")
			Expect(err).To(HaveOccurred())

			classified := githubkit.ClassifyError(err)
			Expect(errors.Is(classified, githubkit.ErrNotFound)).To(BeTrue())

			var ghErr *gh.ErrorResponse
			Expect(errors.As(err, &ghErr)).To(BeTrue())
			Expect(ghErr.Message).To(Equal("Not Found"))
		})
	})

	Describe("the shared rate budget", func() {
		It("exposes the live budget fed from response headers", func() {
			kernel, err := newBDDKernel(fake.URL)
			Expect(err).NotTo(HaveOccurred())

			_, _, err = kernel.Users.Get(ctx, "")
			Expect(err).NotTo(HaveOccurred())

			snapshot, ok := kernel.RateLimitSnapshot()
			Expect(ok).To(BeTrue())
			Expect(snapshot.Limit).To(Equal(5000))
			Expect(snapshot.Remaining).To(BeNumerically("<=", 4999))
		})

		Context("when the remaining budget sits at the configured floor", func() {
			BeforeEach(func() {
				// GitHub reports resets as Unix seconds, so truncation eats up
				// to a full second: 2.1s guarantees a >= 1.1s real wait at any
				// sub-second phase, with margin for CI load between the
				// budget being set and the gate reading it.
				fake.setBudget(5000, 5, time.Now().Add(2100*time.Millisecond))
			})

			It("waits for the window to reset instead of failing", func() {
				kernel, err := newBDDKernel(fake.URL)
				Expect(err).NotTo(HaveOccurred())

				start := time.Now()
				user, _, err := kernel.Users.Get(ctx, "")
				Expect(err).NotTo(HaveOccurred())
				Expect(user.GetLogin()).To(Equal("octocat"))
				Expect(time.Since(start)).To(BeNumerically(">=", 100*time.Millisecond))
			})
		})
	})

	Describe("retrying transient failures", func() {
		Context("when the API returns a server error and then succeeds", func() {
			BeforeEach(func() {
				fake = startFakeGitHub(func(w http.ResponseWriter, r *http.Request) {
					if fake.count("/user") == 1 {
						writeJSONError(w, http.StatusBadGateway, "upstream timeout")
						return
					}
					writeUser(w, r)
				})
				DeferCleanup(fake.Close)
			})

			It("retries the request and succeeds", func() {
				kernel, err := newBDDKernel(fake.URL, githubkit.WithRetryOptions(githubkit.RetryOptions{
					InitialBackoff: time.Millisecond,
					MaxBackoff:     5 * time.Millisecond,
				}))
				Expect(err).NotTo(HaveOccurred())

				user, _, err := kernel.Users.Get(ctx, "")
				Expect(err).NotTo(HaveOccurred())
				Expect(user.GetLogin()).To(Equal("octocat"))
				Expect(fake.count("/user")).To(Equal(2))
			})
		})

		Context("when the API rejects the request outright", func() {
			BeforeEach(func() {
				fake = startFakeGitHub(func(w http.ResponseWriter, r *http.Request) {
					writeJSONError(w, http.StatusNotFound, "Not Found")
				})
				DeferCleanup(fake.Close)
			})

			It("fails immediately without a second attempt", func() {
				kernel, err := newBDDKernel(fake.URL, githubkit.WithRetryOptions(githubkit.RetryOptions{
					InitialBackoff: time.Millisecond,
					MaxBackoff:     5 * time.Millisecond,
				}))
				Expect(err).NotTo(HaveOccurred())

				_, _, err = kernel.Users.Get(ctx, "ghost")
				Expect(err).To(HaveOccurred())
				Expect(fake.count("/users/ghost")).To(Equal(1))
			})
		})
	})

	Describe("construction", func() {
		It("rejects a base URL without a host", func() {
			_, err := githubkit.New(githubkit.WithPAT(bddToken), githubkit.WithBaseURL("http://:9999/no-host"))
			Expect(err).To(HaveOccurred())
		})

		It("appends the trailing slash the native SDK needs", func() {
			kernel, err := githubkit.New(
				githubkit.WithPAT(bddToken),
				githubkit.WithBaseURL("https://ghe.example.com/api/v3"),
			)
			Expect(err).NotTo(HaveOccurred())
			Expect(kernel.Client.BaseURL.Path).To(Equal("/api/v3/"))
		})

		Context("when no token can be resolved", func() {
			BeforeEach(func() {
				for _, name := range []string{"GITHUB_TOKEN", "GH_TOKEN"} {
					prior, had := os.LookupEnv(name)
					Expect(os.Unsetenv(name)).To(Succeed())
					if had {
						DeferCleanup(os.Setenv, name, prior)
					} else {
						DeferCleanup(os.Unsetenv, name)
					}
				}
			})

			It("fails with ErrAuthRequired naming the variables it tried", func() {
				_, err := githubkit.New(githubkit.WithAuthTokenFromEnv())
				Expect(err).To(HaveOccurred())
				Expect(errors.Is(err, githubkit.ErrAuthRequired)).To(BeTrue())
				Expect(err.Error()).To(ContainSubstring("GITHUB_TOKEN"))
				Expect(err.Error()).To(ContainSubstring("GH_TOKEN"))
			})
		})
	})
})

// pageOf returns the requested page number, 1 when absent.
func pageOf(r *http.Request) int {
	raw := r.URL.Query().Get("page")
	if raw == "" {
		return 1
	}

	page, err := strconv.Atoi(raw)
	if err != nil || page < 1 {
		return 1
	}

	return page
}
