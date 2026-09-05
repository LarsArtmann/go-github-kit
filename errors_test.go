package githubkit

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	gh "github.com/google/go-github/v69/github"
)

func TestClassifyErrorNil(t *testing.T) {
	t.Parallel()

	if err := ClassifyError(nil); err != nil {
		t.Fatalf("nil must classify to nil, got %v", err)
	}
}

func TestClassifyErrorStatusMapping(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		status      int
		rateHeaders map[string]string
		want        error
	}{
		{name: "401", status: http.StatusUnauthorized, want: ErrAuthRequired},
		{name: "404", status: http.StatusNotFound, want: ErrNotFound},
		{name: "429", status: http.StatusTooManyRequests, want: ErrRateLimited},
		{
			name:        "403 with exhausted budget",
			status:      http.StatusForbidden,
			rateHeaders: map[string]string{"X-RateLimit-Remaining": "0"},
			want:        ErrRateLimited,
		},
		{
			name:   "403 permissions denial",
			status: http.StatusForbidden,
			rateHeaders: map[string]string{
				"X-RateLimit-Remaining": "4000",
			},
			want: ErrForbidden,
		},
		{name: "500", status: http.StatusInternalServerError, want: ErrAPIUnavailable},
		{name: "503", status: http.StatusServiceUnavailable, want: ErrAPIUnavailable},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			classified := ClassifyError(newGhErrorResponse(tt.status, tt.rateHeaders))

			if !errors.Is(classified, tt.want) {
				t.Fatalf("errors.Is(%v, %v) = false", classified, tt.want)
			}

			statusErr, ok := errors.AsType[*StatusError](classified)
			if !ok {
				t.Fatalf("expected *StatusError, got %T", classified)
			}

			if statusErr.Status != tt.status {
				t.Errorf("Status = %d, want %d", statusErr.Status, tt.status)
			}

			// The underlying go-github error must survive classification.
			if _, ok := errors.AsType[*gh.ErrorResponse](classified); !ok {
				t.Errorf("classified error lost *gh.ErrorResponse: %v", classified)
			}
		})
	}
}

func TestClassifyErrorUnclassifiedPassthrough(t *testing.T) {
	t.Parallel()

	original := newGhErrorResponse(http.StatusBadRequest, nil)
	if classified := ClassifyError(original); classified != original { //nolint:errorlint // identity is the assertion
		t.Fatalf("400 must pass through unchanged, got %v", classified)
	}
}

func TestClassifyErrorAlreadyClassified(t *testing.T) {
	t.Parallel()

	once := ClassifyError(newGhErrorResponse(http.StatusNotFound, nil))
	if again := ClassifyError(once); again != once { //nolint:errorlint // identity is the assertion
		t.Fatalf("double classification must be identity, got %v", again)
	}
}

func TestClassifyErrorWrapped(t *testing.T) {
	t.Parallel()

	inner := ClassifyError(newGhErrorResponse(http.StatusNotFound, nil))
	wrapped := fmt.Errorf("fetching events: %w", inner)

	if !errors.Is(wrapped, ErrNotFound) {
		t.Fatalf("errors.Is through fmt.Errorf chain = false")
	}
}

func TestClassifyErrorGateRejection(t *testing.T) {
	t.Parallel()

	gateErr := &StatusError{
		Sentinel: ErrRateLimited,
		Method:   http.MethodGet,
		URL:      "https://api.github.com/users/octocat/events",
		err: resetTooFarError{
			wait:    2 * time.Hour,
			maxWait: 15 * time.Minute,
			resetAt: time.Now().Add(2 * time.Hour).UTC(),
		},
	}

	if !errors.Is(gateErr, ErrRateLimited) {
		t.Fatal("gate rejection must match ErrRateLimited")
	}

	if ClassifyError(gateErr) != error(gateErr) { //nolint:errorlint // identity is the assertion
		t.Fatal("gate rejection must classify to itself")
	}
}

func TestClassifyErrorNativeRateLimitTypes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		err    error
		status int
	}{
		{
			name:   "RateLimitError 403 exhausted",
			status: http.StatusForbidden,
			err:    newGhRateLimitError(http.StatusForbidden),
		},
		{
			name:   "AbuseRateLimitError 403 secondary limit",
			status: http.StatusForbidden,
			err:    newGhAbuseRateLimitError(http.StatusForbidden),
		},
		{
			name:   "AbuseRateLimitError 429 with Retry-After",
			status: http.StatusTooManyRequests,
			err:    newGhAbuseRateLimitError(http.StatusTooManyRequests),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			classified := ClassifyError(tt.err)

			if !errors.Is(classified, ErrRateLimited) {
				t.Fatalf("errors.Is(%v, ErrRateLimited) = false", classified)
			}

			statusErr, ok := errors.AsType[*StatusError](classified)
			if !ok {
				t.Fatalf("expected *StatusError, got %T", classified)
			}

			if statusErr.Status != tt.status {
				t.Errorf("Status = %d, want %d", statusErr.Status, tt.status)
			}

			if statusErr.Method != http.MethodGet || statusErr.URL == "" {
				t.Errorf("request context lost: method=%q url=%q", statusErr.Method, statusErr.URL)
			}

			// The dedicated SDK type must survive classification.
			if !errors.Is(classified, tt.err) {
				t.Errorf("classified error lost the original %T: %v", tt.err, classified)
			}
		})
	}
}

func TestClassifyErrorWrappedNativeRateLimit(t *testing.T) {
	t.Parallel()

	wrapped := fmt.Errorf("listing events: %w", newGhAbuseRateLimitError(http.StatusForbidden))

	classified := ClassifyError(wrapped)

	if !errors.Is(classified, ErrRateLimited) {
		t.Fatalf("errors.Is(%v, ErrRateLimited) = false through fmt.Errorf", classified)
	}

	if _, ok := errors.AsType[*gh.AbuseRateLimitError](classified); !ok {
		t.Errorf("classified error lost *gh.AbuseRateLimitError: %v", classified)
	}
}

func TestStatusErrorMessage(t *testing.T) {
	t.Parallel()

	err := ClassifyError(newGhErrorResponse(http.StatusNotFound, nil))
	message := err.Error()

	for _, want := range []string{"GET", "404", "resource not found"} {
		if !strings.Contains(message, want) {
			t.Errorf("message %q missing %q", message, want)
		}
	}
}

func newGhErrorResponse(status int, rateHeaders map[string]string) *gh.ErrorResponse {
	return &gh.ErrorResponse{
		//nolint:bodyclose // fixture response, never handed to an http.Client
		Response: newGhTestResponse(status, rateHeaders),
		Message:  http.StatusText(status),
	}
}

func newGhRateLimitError(status int) *gh.RateLimitError {
	return &gh.RateLimitError{
		Rate: gh.Rate{Reset: gh.Timestamp{Time: time.Now().Add(time.Hour).UTC()}},
		//nolint:bodyclose // fixture response, never handed to an http.Client
		Response: newGhTestResponse(status, nil),
		Message:  "API rate limit exceeded",
	}
}

func newGhAbuseRateLimitError(status int) *gh.AbuseRateLimitError {
	retryAfter := time.Minute

	return &gh.AbuseRateLimitError{
		//nolint:bodyclose // fixture response, never handed to an http.Client
		Response:   newGhTestResponse(status, nil),
		Message:    "You have triggered an abuse detection mechanism",
		RetryAfter: &retryAfter,
	}
}

func newGhTestResponse(status int, rateHeaders map[string]string) *http.Response {
	req, _ := http.NewRequestWithContext(
		context.Background(),
		http.MethodGet,
		"https://api.github.com/users/octocat/events",
		nil,
	)

	header := http.Header{}
	for key, value := range rateHeaders {
		header.Set(key, value)
	}

	return &http.Response{
		StatusCode: status,
		Status:     fmt.Sprintf("%d %s", status, http.StatusText(status)),
		Header:     header,
		Body:       http.NoBody,
		Request:    req,
	}
}
