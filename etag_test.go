package githubkit

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestETagRevalidationServesCachedBody(t *testing.T) {
	t.Parallel()

	clock := newStubClock(time.Now())

	var (
		mu       sync.Mutex
		etag     = `"abc123"`
		versions = 0
		body     = `{"id":"event-1","type":"PushEvent"}`
	)

	server := newRecordingServer(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		versions++
		current := versions
		mu.Unlock()

		w.Header().Set("ETag", etag)
		setRateHeaders(w, 5000, 4999, clock.futureReset(time.Hour))

		if r.Header.Get("If-None-Match") == etag {
			w.WriteHeader(http.StatusNotModified)

			return
		}

		if current > 1 {
			t.Errorf("server produced a fresh body after a valid ETag was cached")
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	})
	defer server.Close()

	kernel := newKernel(t, server.URL, clock,
		WithoutRateLimit(),
		WithETagCache(nil),
		withClock(clock),
	)

	first, err := kernelFetch(kernel, server.URL+"/users/octocat/events")
	if err != nil {
		t.Fatalf("first GET: %v", err)
	}

	if first.status != http.StatusOK {
		t.Fatalf("first response status = %d, want 200", first.status)
	}

	firstBody := first.body

	second, err := kernelFetch(kernel, server.URL+"/users/octocat/events")
	if err != nil {
		t.Fatalf("second GET: %v", err)
	}

	secondBody := second.body

	if firstBody != secondBody {
		t.Errorf("cached body differs: %q vs %q", firstBody, secondBody)
	}

	if second.status != http.StatusOK {
		t.Errorf("rebuilt response status = %d, want 200", second.status)
	}

	if second.header.Get(headerFromCache) != "1" {
		t.Error("rebuilt response must carry the from-cache marker")
	}

	if got := server.totalCalls(); got != 2 {
		t.Errorf("total server calls = %d, want 2 (one fresh, one revalidation)", got)
	}

	stats, ok := kernel.ETagStats()
	if !ok {
		t.Fatal("ETagStats must report availability")
	}

	if stats.Hits != 1 || stats.Stored != 1 {
		t.Errorf("stats = %+v, want 1 hit and 1 stored", stats)
	}
}

func TestETagCacheSeparatesCredentials(t *testing.T) {
	t.Parallel()

	server := newRecordingServer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"always-same"`)
		w.Header().Set("Content-Type", "text/plain")

		switch r.Header.Get("Authorization") {
		case "Bearer token-a":
			_, _ = w.Write([]byte("body-for-a"))
		default:
			_, _ = w.Write([]byte("body-for-b"))
		}
	})
	defer server.Close()

	kernelA, err := New(WithPAT("token-a"), WithBaseURL(server.URL+"/"), WithETagCache(nil))
	if err != nil {
		t.Fatalf("New A: %v", err)
	}

	kernelB, err := New(WithPAT("token-b"), WithBaseURL(server.URL+"/"), WithETagCache(nil))
	if err != nil {
		t.Fatalf("New B: %v", err)
	}

	bodyA1 := kernelGet(t, kernelA, server.URL+"/things")
	bodyB1 := kernelGet(t, kernelB, server.URL+"/things")
	bodyA2 := kernelGet(t, kernelA, server.URL+"/things")

	if bodyA1 != "body-for-a" || bodyA2 != "body-for-a" {
		t.Errorf("token-a bodies = %q, %q", bodyA1, bodyA2)
	}

	if bodyB1 != "body-for-b" {
		t.Errorf("token-b body = %q; cached cross-credential replay detected", bodyB1)
	}
}

func TestETagCacheEvictsOldest(t *testing.T) {
	t.Parallel()

	next := roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		etag := `"tag-` + req.URL.Path + `"`

		if req.Header.Get("If-None-Match") == etag {
			return stubRevalidation(req, etag), nil
		}

		return stubOKResponse(req, etag, "body of "+req.URL.Path), nil
	})

	cache := NewETagCache(ETagOptions{MaxEntries: 2})
	client := &http.Client{Transport: cache.wrap(next)}

	getBody := func(t *testing.T, path string) string {
		t.Helper()

		req, reqErr := http.NewRequestWithContext(t.Context(), http.MethodGet, stubBaseURL+path, nil)
		if reqErr != nil {
			t.Fatalf("build GET %s: %v", path, reqErr)
		}

		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}

		defer func() { _ = resp.Body.Close() }()

		data, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			t.Fatalf("read %s: %v", path, readErr)
		}

		return string(data)
	}

	for _, path := range []string{"/one", "/two", "/three"} {
		getBody(t, path)
	}

	if entries := cache.Stats().Entries; entries != 2 {
		t.Fatalf("entries = %d, want 2 after three stores with MaxEntries 2", entries)
	}

	// /one was evicted first: its revalidation carries no validator, so the
	// stub answers with a fresh 200 body instead of a 304 rebuild.
	if body := getBody(t, "/one"); body != "body of /one" {
		t.Errorf("evicted entry re-request = %q, want fresh body", body)
	}

	if hits := cache.Stats().Hits; hits != 0 {
		t.Errorf("hits = %d, want 0 (no entry was cached for revalidation)", hits)
	}
}

// stubBaseURL is the base URL stub responses report as their origin; the
// etagclient transport never dials it because next is a stub.
const stubBaseURL = "https://example.test"

// stubOKResponse builds a 200 carrying etag and body.
func stubOKResponse(req *http.Request, etag, body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Status:     "200 OK",
		Proto:      "HTTP/1.1",
		ProtoMajor: 1,
		ProtoMinor: 1,
		Header:     stubHeader("ETag", etag),
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    req,
	}
}

// stubRevalidation builds the 304 answering a known validator.
func stubRevalidation(req *http.Request, etag string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusNotModified,
		Status:     "304 Not Modified",
		Proto:      "HTTP/1.1",
		ProtoMajor: 1,
		ProtoMinor: 1,
		Header:     stubHeader("ETag", etag),
		Body:       http.NoBody,
		Request:    req,
	}
}

// stubHeader builds a canonicalized single-entry header map.
func stubHeader(name, value string) http.Header {
	header := http.Header{}
	header.Set(name, value)

	return header
}

func TestETagSkipsNonGET(t *testing.T) {
	t.Parallel()

	server := newRecordingServer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"should-not-cache"`)
		w.WriteHeader(http.StatusCreated)
	})
	defer server.Close()

	kernel, err := New(WithPAT(testToken), WithBaseURL(server.URL+"/"), WithETagCache(nil))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	request, requestErr := http.NewRequestWithContext(
		t.Context(),
		http.MethodPost,
		server.URL+"/repos/o/r/issues",
		nil,
	)
	if requestErr != nil {
		t.Fatalf("build request: %v", requestErr)
	}

	response, doErr := kernel.Client.Client().Do(request)
	if doErr != nil {
		t.Fatalf("POST: %v", doErr)
	}

	defer func() { _ = response.Body.Close() }()

	stats, _ := kernel.ETagStats()
	if stats.Stored != 0 {
		t.Errorf("POST must not populate the cache, stored = %d", stats.Stored)
	}
}

// kernelFetch performs a GET through the kernel's stack and returns the
// fully-read response with the body closed.
type fetched struct {
	status int
	header http.Header
	body   string
}

// kernelFetch performs a GET through the kernel's stack and returns the
// fully-read response with the body closed.
func kernelFetch(kernel *Kernel, url string) (fetched, error) {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	if err != nil {
		return fetched{}, err
	}

	resp, err := kernel.Client.Client().Do(req)
	if err != nil {
		return fetched{}, err
	}

	defer func() { _ = resp.Body.Close() }()

	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fetched{}, err
	}

	return fetched{status: resp.StatusCode, header: resp.Header, body: string(data)}, nil
}

func kernelGet(t *testing.T, kernel *Kernel, url string) string {
	t.Helper()

	result, err := kernelFetch(kernel, url)
	if err != nil {
		t.Fatalf("GET %s (status %d): %v", url, result.status, err)
	}

	return result.body
}
