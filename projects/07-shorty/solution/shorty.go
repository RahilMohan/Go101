// Package shorty provides a URL shortener REST API, teaching Go 1.22+
// http.ServeMux method+path patterns ("POST /api/shorten", "GET /{code}"),
// middleware chaining with the func(http.Handler) http.Handler idiom,
// database/sql idioms against SQLite (placeholders, connection pooling),
// and httptest-based HTTP testing.
//
// Storage uses modernc.org/sqlite: a pure-Go (cgo-free) SQLite driver,
// trading raw speed for zero toolchain friction — no C compiler needed
// on any platform.
package solution

import (
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"runtime/debug"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// ErrCodeTaken is returned by SaveCode when the code already exists.
var ErrCodeTaken = errors.New("shorty: code already taken")

// ErrNotFound is returned when Resolve cannot find the code. It wraps
// sql.ErrNoRows so callers can use errors.Is.
var ErrNotFound = errors.New("shorty: not found")

const (
	codeLen     = 6
	insertTries = 5
	maxBodyLen  = 1 << 20
	base62      = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
)

const schema = `
CREATE TABLE IF NOT EXISTS urls (
	code TEXT PRIMARY KEY,
	url  TEXT NOT NULL
);`

// Store is the SQLite-backed URL store.
type Store struct {
	db *sql.DB
}

// OpenStore opens (creating if necessary) the SQLite database at dbPath,
// enables WAL journaling, and runs schema migration
// (CREATE TABLE IF NOT EXISTS).
func OpenStore(dbPath string) (*Store, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("shorty: open %s: %w", dbPath, err)
	}
	db.SetMaxOpenConns(1) // modernc.org/sqlite serializes writes; one conn avoids SQLITE_BUSY churn
	for _, pragma := range []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA busy_timeout=5000",
	} {
		if _, err := db.Exec(pragma); err != nil {
			db.Close()
			return nil, fmt.Errorf("shorty: %s: %w", pragma, err)
		}
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("shorty: migrate: %w", err)
	}
	return &Store{db: db}, nil
}

// Close releases the underlying database connections.
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// Save inserts code->url if code is unused; returns ErrCodeTaken if
// duplicate. It delegates to SaveCode.
func (s *Store) Save(code, url string) error {
	return s.SaveCode(code, url)
}

// SaveCode inserts an explicit code->url mapping; returns ErrCodeTaken if
// code already exists. Used by tests to exercise conflicts deterministically
// and internally by Save. The query uses placeholders (?), never string
// interpolation, so values cannot inject SQL.
func (s *Store) SaveCode(code, url string) error {
	_, err := s.db.Exec(`INSERT INTO urls (code, url) VALUES (?, ?)`, code, url)
	if err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed") {
		return fmt.Errorf("shorty: save %q: %w", code, ErrCodeTaken)
	}
	if err != nil {
		return fmt.Errorf("shorty: save %q: %w", code, err)
	}
	return nil
}

// Resolve returns the target URL for code, or ErrNotFound when unknown.
func (s *Store) Resolve(code string) (string, error) {
	var target string
	err := s.db.QueryRow(`SELECT url FROM urls WHERE code = ?`, code).Scan(&target)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("shorty: resolve %q: %w", code, ErrNotFound)
	}
	if err != nil {
		return "", fmt.Errorf("shorty: resolve %q: %w", code, err)
	}
	return target, nil
}

// Count returns how many URLs are stored.
func (s *Store) Count() (int64, error) {
	var n int64
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM urls`).Scan(&n); err != nil {
		return 0, fmt.Errorf("shorty: count: %w", err)
	}
	return n, nil
}

// Server wires routes and middleware around a Store.
type Server struct {
	store *Store
	base  string
}

// NewServer returns a Server serving store, using baseURL (e.g.
// "http://localhost:8080") to build Location-style short_url responses.
func NewServer(store *Store, baseURL string) *Server {
	return &Server{store: store, base: strings.TrimSuffix(baseURL, "/")}
}

// Handler returns the fully-wired http.Handler: RequestLogger ->
// Recoverer -> ServeMux with Go 1.22 method+path patterns.
func (sv *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/shorten", sv.handleShorten)
	mux.HandleFunc("GET /api/stats", sv.handleStats)
	mux.HandleFunc("GET /{code}", sv.handleRedirect)
	mux.HandleFunc("/", writeError(http.StatusNotFound, "not found"))
	return RequestLogger(log.Writer())(Recoverer(log.Writer())(mux))
}

func (sv *Server) handleShorten(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyLen)
	var req struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(http.StatusBadRequest, "invalid JSON body")(w, r)
		return
	}
	u, err := url.Parse(req.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		writeError(http.StatusBadRequest, "url must be an absolute http(s) URL")(w, r)
		return
	}

	for i := 0; i < insertTries; i++ {
		code := generateCode()
		switch err := sv.store.Save(code, u.String()); {
		case err == nil:
			writeJSON(w, http.StatusCreated, map[string]string{
				"code":      code,
				"short_url": sv.base + "/" + code,
			})
			return
		case errors.Is(err, ErrCodeTaken):
			continue // generated-code collision; astronomically rare — retry
		default:
			log.Printf("shorty: save: %v", err)
			writeError(http.StatusInternalServerError, "internal server error")(w, r)
			return
		}
	}
	writeError(http.StatusInternalServerError, "could not allocate code")(w, r)
}

func (sv *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	n, err := sv.store.Count()
	if err != nil {
		log.Printf("shorty: stats: %v", err)
		writeError(http.StatusInternalServerError, "internal server error")(w, r)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int64{"count": n})
}

func (sv *Server) handleRedirect(w http.ResponseWriter, r *http.Request) {
	target, err := sv.store.Resolve(r.PathValue("code"))
	switch {
	case errors.Is(err, ErrNotFound):
		writeError(http.StatusNotFound, "not found")(w, r)
	case err != nil:
		log.Printf("shorty: redirect: %v", err)
		writeError(http.StatusInternalServerError, "internal server error")(w, r)
	default:
		http.Redirect(w, r, target, http.StatusFound)
	}
}

// statusRecorder captures the status code written by downstream handlers so
// RequestLogger can log it.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// RequestLogger returns middleware that writes one line per request to w:
// "<method> <path> <status> <duration>".
func RequestLogger(w io.Writer) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := &statusRecorder{ResponseWriter: rw, status: http.StatusOK}
			next.ServeHTTP(rec, r)
			fmt.Fprintf(w, "%s %s %d %v\n", r.Method, r.URL.Path, rec.status, time.Since(start))
		})
	}
}

// Recoverer returns middleware that recovers panics from downstream
// handlers, responds 500 with {"error":"internal server error"}, and logs
// the panic value plus stack trace to log.
func Recoverer(out io.Writer) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					fmt.Fprintf(out, "shorty: recovered from panic: %v\n%s\n", rec, debug.Stack())
					writeJSON(rw, http.StatusInternalServerError, map[string]string{
						"error": "internal server error",
					})
				}
			}()
			next.ServeHTTP(rw, r)
		})
	}
}

// generateCode returns a cryptographically random base62 code of length
// codeLen.
func generateCode() string {
	raw := make([]byte, codeLen)
	if _, err := rand.Read(raw); err != nil {
		panic(fmt.Sprintf("shorty: rand.Read: %v", err)) // crypto/rand failure is unrecoverable
	}
	out := make([]byte, codeLen)
	for i, v := range raw {
		out[i] = base62[int(v)%len(base62)]
	}
	return string(out)
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(body)
}

func writeError(status int, msg string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, status, map[string]string{"error": msg})
	}
}
