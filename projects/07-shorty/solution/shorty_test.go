package solution

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// newTestStore opens a Store backed by a fresh temp-file SQLite database.
func newTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := OpenStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return store
}

// postShorten sends a shorten request and returns the response plus decoded
// body (raw JSON object).
func postShorten(t *testing.T, ts *httptest.Server, rawURL string) (*http.Response, map[string]string) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"url": rawURL})
	resp, err := http.Post(ts.URL+"/api/shorten", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST /api/shorten: %v", err)
	}
	defer resp.Body.Close()
	var out map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return resp, out
}

// noRedirectClient returns a client that surfaces 3xx responses instead of
// following them.
func noRedirectClient() *http.Client {
	return &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// ---------------------------------------------------------------------------
// tests
// ---------------------------------------------------------------------------

// 1. Round-trip: shorten a URL, then resolve it through the redirect route.
func TestRoundTripShortenAndRedirect(t *testing.T) {
	store := newTestStore(t)
	sv := NewServer(store, "http://localhost:8080")
	ts := httptest.NewServer(sv.Handler())
	t.Cleanup(ts.Close)

	const target = "https://example.com/path?q=1"
	resp, body := postShorten(t, ts, target)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST status = %d, want 201 (body %v)", resp.StatusCode, body)
	}
	code := body["code"]
	if len(code) != 6 {
		t.Fatalf("code = %q, want 6 chars", code)
	}
	if body["short_url"] != "http://localhost:8080/"+code {
		t.Fatalf("short_url = %q, want base + / + code %q", body["short_url"], code)
	}

	client := noRedirectClient()
	redirect, err := client.Get(ts.URL + "/" + code)
	if err != nil {
		t.Fatalf("GET /%s: %v", code, err)
	}
	defer redirect.Body.Close()
	if redirect.StatusCode != http.StatusFound {
		t.Fatalf("GET /%s status = %d, want 302", code, redirect.StatusCode)
	}
	if loc := redirect.Header.Get("Location"); loc != target {
		t.Fatalf("Location = %q, want %q", loc, target)
	}

	n, err := store.Count()
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if n != 1 {
		t.Fatalf("Count = %d, want 1", n)
	}
}

// 2. Duplicate explicit codes conflict with ErrCodeTaken; original mapping
// stays intact.
func TestSaveCodeConflict(t *testing.T) {
	store := newTestStore(t)

	if err := store.SaveCode("dup1", "https://first.example"); err != nil {
		t.Fatalf("first SaveCode: %v", err)
	}
	err := store.SaveCode("dup1", "https://second.example")
	if !errors.Is(err, ErrCodeTaken) {
		t.Fatalf("second SaveCode err = %v, want ErrCodeTaken", err)
	}

	got, err := store.Resolve("dup1")
	if err != nil {
		t.Fatalf("Resolve after conflict: %v", err)
	}
	if got != "https://first.example" {
		t.Fatalf("Resolve = %q, want original target preserved", got)
	}
}

// 3. Validation table: malformed or non-http(s) URLs are rejected with 400
// and a JSON error body.
func TestShortenValidationRejectsBadURLs(t *testing.T) {
	store := newTestStore(t)
	ts := httptest.NewServer(NewServer(store, "http://localhost:8080").Handler())
	t.Cleanup(ts.Close)

	badURLs := []string{
		"",
		"example.com/no-scheme",
		"ftp://example.com/file",
		"javascript:alert(1)",
		"https://",
		"://missing-scheme",
	}
	for _, raw := range badURLs {
		resp, body := postShorten(t, ts, raw)
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("POST url=%q status = %d, want 400 (body %v)", raw, resp.StatusCode, body)
		}
		if _, ok := body["error"]; !ok {
			t.Errorf("POST url=%q body %v missing \"error\" key", raw, body)
		}
	}

	n, err := store.Count()
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if n != 0 {
		t.Fatalf("Count = %d after rejected posts, want 0", n)
	}
}

// 4. Middleware: Recoverer turns panics into 500 JSON plus a logged stack;
// RequestLogger writes method, path, and status per request.
func TestMiddlewareRecoverAndLog(t *testing.T) {
	var logBuf, reqBuf bytes.Buffer

	panicHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("boom")
	})
	wrapped := RequestLogger(&reqBuf)(Recoverer(&logBuf)(panicHandler))
	ts := httptest.NewServer(wrapped)
	t.Cleanup(ts.Close)

	client := noRedirectClient()
	resp, err := client.Get(ts.URL + "/explode")
	if err != nil {
		t.Fatalf("GET panic route: %v", err)
	}
	defer resp.Body.Close()

	var body map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode panic response: %v", err)
	}
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", resp.StatusCode)
	}
	if body["error"] == "" {
		t.Fatalf("body %v missing \"error\"", body)
	}

	recovered := logBuf.String()
	if !strings.Contains(recovered, "boom") || !strings.Contains(recovered, "goroutine") {
		t.Fatalf("recoverer log missing panic value/stack, got:\n%s", recovered)
	}

	line := reqBuf.String()
	if !strings.Contains(line, "GET") ||
		!strings.Contains(line, "/explode") ||
		!strings.Contains(line, "500") {
		t.Fatalf("request log %q missing method/path/status", line)
	}
}

// 5. Concurrency: 20 parallel POSTs all succeed with distinct codes under
// -race, and the store ends up holding exactly 20 rows.
func TestConcurrentShortensAllDistinct(t *testing.T) {
	store := newTestStore(t)
	ts := httptest.NewServer(NewServer(store, "http://localhost:8080").Handler())
	t.Cleanup(ts.Close)

	const total = 20
	codes := make([]string, total)
	var wg sync.WaitGroup
	var mu sync.Mutex
	failed := 0
	for i := 0; i < total; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			body, _ := json.Marshal(map[string]string{
				"url": fmt.Sprintf("https://example.com/conc/%d", i),
			})
			resp, err := http.Post(ts.URL+"/api/shorten", "application/json", bytes.NewReader(body))
			if err != nil {
				mu.Lock()
				failed++
				mu.Unlock()
				return
			}
			defer resp.Body.Close()
			var out map[string]string
			json.NewDecoder(resp.Body).Decode(&out)
			mu.Lock()
			defer mu.Unlock()
			if resp.StatusCode != http.StatusCreated || len(out["code"]) != 6 {
				failed++
				return
			}
			codes[i] = out["code"]
		}(i)
	}
	wg.Wait()
	if failed != 0 {
		t.Fatalf("%d concurrent POSTs failed", failed)
	}

	seen := make(map[string]bool, total)
	for i, code := range codes {
		if code == "" || seen[code] {
			t.Fatalf("code[%d]=%q empty or duplicated", i, code)
		}
		seen[code] = true
	}

	n, err := store.Count()
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if n != total {
		t.Fatalf("Count = %d, want %d", n, total)
	}
}

// 6. Persistence: close and reopen the same database file; mappings survive.
func TestPersistenceAcrossReopen(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "shorty.db")

	store, err := OpenStore(dbPath)
	if err != nil {
		t.Fatalf("open #1: %v", err)
	}
	if err := store.SaveCode("persist", "https://example.org/survives"); err != nil {
		t.Fatalf("SaveCode: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close #1: %v", err)
	}

	reopened, err := OpenStore(dbPath)
	if err != nil {
		t.Fatalf("open #2: %v", err)
	}
	t.Cleanup(func() { reopened.Close() })

	got, err := reopened.Resolve("persist")
	if err != nil {
		t.Fatalf("Resolve after reopen: %v", err)
	}
	if got != "https://example.org/survives" {
		t.Fatalf("Resolve = %q, want persisted target", got)
	}
	n, err := reopened.Count()
	if err != nil {
		t.Fatalf("Count after reopen: %v", err)
	}
	if n != 1 {
		t.Fatalf("Count = %d after reopen, want 1", n)
	}
}

// 7. Unknown codes resolve to ErrNotFound via errors.Is.
func TestResolveUnknownCodeErrNotFound(t *testing.T) {
	store := newTestStore(t)

	_, err := store.Resolve("missing")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Resolve unknown err = %v, want ErrNotFound", err)
	}
}
