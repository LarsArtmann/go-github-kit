package githubkit_test

import (
	"net/http"
	"testing"

	githubkit "github.com/LarsArtmann/go-github-kit"
)

// FuzzParseRateLimitHeaders feeds arbitrary header values into the budget
// parser. Invariants: it never panics, and whenever it reports success the
// counts are non-negative (negative or malformed values must read as "no
// information", never as a poisoned budget the gate would act on).
func FuzzParseRateLimitHeaders(f *testing.F) {
	f.Add("5000", "4999", "1767225600") // healthy
	f.Add("5000", "0", "0")             // exhausted window
	f.Add("60", "59", "not-a-number")   // garbage reset is tolerated
	f.Add("-1", "-1", "-1")             // negatives rejected wholesale
	f.Add("", "", "")                   // absent
	f.Add("9999999999999999999", "1", "1767225600")
	f.Add("0x10", "1e3", "1.5")
	f.Add(" 5000 ", "+4999", "1767225600.9")

	f.Fuzz(func(t *testing.T, limit, remaining, reset string) {
		header := http.Header{}
		header.Set("X-RateLimit-Limit", limit)
		header.Set("X-RateLimit-Remaining", remaining)
		header.Set("X-RateLimit-Reset", reset)

		snapshot, ok := githubkit.ParseRateLimitHeaders(header)
		if !ok {
			return
		}

		if snapshot.Limit < 0 || snapshot.Remaining < 0 {
			t.Fatalf("accepted negative budget: %+v (limit=%q remaining=%q)", snapshot, limit, remaining)
		}
	})
}
