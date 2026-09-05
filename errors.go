package githubkit

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"

	gh "github.com/google/go-github/v69/github"
)

// Sentinel errors classifying GitHub API failures. Names align with the
// providererrors vocabulary used across LarsArtmann provider projects so
// cross-project errors.Is checks read naturally. [ClassifyError] produces
// errors that match these via errors.Is while preserving the underlying
// go-github error for errors.AsType.
var (
	// ErrAuthRequired marks a missing or rejected credential (HTTP 401, or
	// no token could be resolved at construction time).
	ErrAuthRequired = errors.New("github: authentication required")

	// ErrForbidden marks a 403 that is a permissions denial, not a rate
	// limit. GitHub overloads 403 for both; the kit disambiguates using
	// the X-RateLimit-Remaining header.
	ErrForbidden = errors.New("github: forbidden")

	// ErrRateLimited marks an exhausted request budget: HTTP 429, a 403
	// with zero remaining requests, go-github's dedicated
	// *RateLimitError/*AbuseRateLimitError types, or a reset time too far
	// in the future to wait for (see RateLimitOptions.MaxWait).
	ErrRateLimited = errors.New("github: rate limit exceeded")

	// ErrNotFound marks a missing resource (HTTP 404).
	ErrNotFound = errors.New("github: resource not found")

	// ErrAPIUnavailable marks transport-level failures and exhausted 5xx
	// retries: the API could not be reached or kept failing.
	ErrAPIUnavailable = errors.New("github: API unavailable")
)

// StatusError classifies an error from a GitHub API call. It wraps both a
// kit sentinel (for errors.Is) and the original error (for errors.AsType
// and error-message detail), so classifying never destroys information.
type StatusError struct {
	// Sentinel is the matching kit sentinel, one of ErrAuthRequired,
	// ErrForbidden, ErrRateLimited, ErrNotFound, or ErrAPIUnavailable.
	Sentinel error
	// Status is the HTTP status code, or 0 for transport errors.
	Status int
	// Method and URL identify the request that failed.
	Method string
	URL    string

	err error
}

// Error implements the error interface with the request context first and
// the underlying message last, so log lines read like
// "github: GET https://api.github.com/users/x/events: 404 resource not found: not found, users/x".
func (e *StatusError) Error() string {
	prefix := "github:"
	if e.Method != "" || e.URL != "" {
		prefix = fmt.Sprintf("github: %s %s:", e.Method, e.URL)
	}

	if e.Status != 0 {
		prefix = fmt.Sprintf("%s %d:", prefix, e.Status)
	}

	return fmt.Sprintf("%s %s: %v", prefix, e.Sentinel, e.err)
}

// Unwrap exposes both the sentinel and the underlying error so errors.Is
// and errors.AsType (notably [*github.ErrorResponse]) keep working on
// classified errors.
func (e *StatusError) Unwrap() []error {
	return []error{e.Sentinel, e.err}
}

// ClassifyError maps an error returned by a go-github call to a
// [*StatusError] matching a kit sentinel. The original error is preserved:
// errors.AsType[*github.ErrorResponse], [*github.RateLimitError], and
// [*github.AbuseRateLimitError] as well as errors.Is against go-github's
// own sentinels still succeed on the result. nil classifies to nil.
//
// Errors that match no category (e.g. HTTP 400) are returned unchanged —
// forcing them under a wrong sentinel would be a lie.
func ClassifyError(err error) error {
	if err == nil {
		return nil
	}

	if _, ok := errors.AsType[*StatusError](err); ok {
		return err
	}

	// go-github reports exhausted budgets through dedicated types, both
	// from its pre-flight check and from CheckResponse. They are not
	// *ErrorResponse values, so they need their own branch — without it a
	// teaching 403 (remaining 0) or a secondary limit surfaces unclassified.
	if rlErr, ok := errors.AsType[*gh.RateLimitError](err); ok {
		return classifyRateLimit(rlErr.Response, rlErr)
	}

	if abuseErr, ok := errors.AsType[*gh.AbuseRateLimitError](err); ok {
		return classifyRateLimit(abuseErr.Response, abuseErr)
	}

	if ghErr, ok := errors.AsType[*gh.ErrorResponse](err); ok && ghErr.Response != nil {
		return classifyResponseError(ghErr)
	}

	if urlErr, ok := errors.AsType[*url.Error](err); ok {
		return &StatusError{
			Sentinel: ErrAPIUnavailable,
			Method:   urlErr.Op,
			URL:      urlErr.URL,
			err:      err,
		}
	}

	if isNetError(err) {
		return &StatusError{Sentinel: ErrAPIUnavailable, err: err}
	}

	return err
}

// classifyRateLimit wraps one of go-github's dedicated rate-limit error
// types (cause) as a StatusError with ErrRateLimited. resp carries the
// request context; both fields may be nil in adversarial cases, which
// yields a StatusError without HTTP detail rather than a panic.
func classifyRateLimit(resp *http.Response, cause error) *StatusError {
	statusErr := &StatusError{Sentinel: ErrRateLimited, err: cause}

	if resp == nil {
		return statusErr
	}

	statusErr.Status = resp.StatusCode

	if resp.Request != nil {
		statusErr.Method = resp.Request.Method
		statusErr.URL = resp.Request.URL.String()
	}

	return statusErr
}

func classifyResponseError(ghErr *gh.ErrorResponse) error {
	resp := ghErr.Response

	statusErr := &StatusError{
		Status: resp.StatusCode,
		err:    ghErr,
	}

	if resp.Request != nil {
		statusErr.Method = resp.Request.Method
		statusErr.URL = resp.Request.URL.String()
	}

	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		statusErr.Sentinel = ErrAuthRequired
	case resp.StatusCode == http.StatusNotFound:
		statusErr.Sentinel = ErrNotFound
	case resp.StatusCode == http.StatusTooManyRequests:
		statusErr.Sentinel = ErrRateLimited
	case resp.StatusCode == http.StatusForbidden:
		// GitHub uses 403 for both rate limiting and permissions. The
		// remaining-requests header is the documented disambiguator.
		if remaining, ok := parseHeaderInt(resp.Header, headerRateLimitRemaining); ok && remaining == 0 {
			statusErr.Sentinel = ErrRateLimited
		} else {
			statusErr.Sentinel = ErrForbidden
		}
	case resp.StatusCode >= http.StatusInternalServerError:
		statusErr.Sentinel = ErrAPIUnavailable
	default:
		return ghErr
	}

	return statusErr
}

// isNetError reports whether the error chain contains a net.Error or a
// *net.OpError, which is what "the API is unreachable" means in practice.
func isNetError(err error) bool {
	_, isNet := errors.AsType[net.Error](err)
	_, isOp := errors.AsType[*net.OpError](err)

	return isNet || isOp
}
