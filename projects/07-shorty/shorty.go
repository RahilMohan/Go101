// Package shorty provides a URL shortener REST API, teaching Go 1.22+
// http.ServeMux method+path patterns ("POST /api/shorten", "GET /{code}"),
// middleware chaining with the func(http.Handler) http.Handler idiom,
// database/sql idioms against SQLite (placeholders, connection pooling),
// and httptest-based HTTP testing.
//
// Storage uses modernc.org/sqlite: a pure-Go (cgo-free) SQLite driver,
// trading raw speed for zero toolchain friction — no C compiler needed
// on any platform.
package shorty

import (
	"errors"
	"io"
	"net/http"
)

// ErrCodeTaken is returned by SaveCode when the code already exists.
var ErrCodeTaken = errors.New("shorty: code already taken")

// ErrNotFound is returned when Resolve cannot find the code. Resolve
// converts sql.ErrNoRows into it (a new chain, not a wrap), so
// errors.Is(err, sql.ErrNoRows) will NOT match — callers check
// errors.Is(err, ErrNotFound).
var ErrNotFound = errors.New("shorty: not found")

// Store is the SQLite-backed URL store.
type Store struct {
	// TODO: Implement
	_ struct{}
}

// OpenStore opens (creating if necessary) the SQLite database at dbPath,
// enables WAL journaling, and runs schema migration
// (CREATE TABLE IF NOT EXISTS).
func OpenStore(dbPath string) (*Store, error) {
	// TODO: Implement
	return &Store{}, nil
}

// Close releases the underlying database connections.
func (s *Store) Close() error {
	// TODO: Implement
	return nil
}

// Save inserts code->url if code is unused; returns ErrCodeTaken if
// duplicate. It delegates to SaveCode.
func (s *Store) Save(code, url string) error {
	// TODO: Implement
	return nil
}

// SaveCode inserts an explicit code->url mapping; returns ErrCodeTaken if
// code already exists. Used by tests to exercise conflicts deterministically
// and internally by Save.
func (s *Store) SaveCode(code, url string) error {
	// TODO: Implement
	return nil
}

// Resolve returns the target URL for code, or ErrNotFound when unknown.
func (s *Store) Resolve(code string) (string, error) {
	// TODO: Implement
	return "", nil
}

// Count returns how many URLs are stored.
func (s *Store) Count() (int64, error) {
	// TODO: Implement
	return 0, nil
}

// Server wires routes and middleware around a Store.
type Server struct {
	// TODO: Implement
	_ struct{}
}

// NewServer returns a Server serving store, using baseURL (e.g.
// "http://localhost:8080") to build Location-style short_url responses.
func NewServer(store *Store, baseURL string) *Server {
	// TODO: Implement
	return &Server{}
}

// Handler returns the fully-wired http.Handler: RequestLogger ->
// Recoverer -> ServeMux with Go 1.22 method+path patterns.
func (sv *Server) Handler() http.Handler {
	// TODO: Implement
	return http.NotFoundHandler()
}

// RequestLogger returns middleware that writes one line per request to w:
// "<method> <path> <status> <duration>".
func RequestLogger(w io.Writer) func(http.Handler) http.Handler {
	// TODO: Implement
	return func(next http.Handler) http.Handler { return next }
}

// Recoverer returns middleware that recovers panics from downstream
// handlers, responds 500 with {"error":"internal server error"}, and logs
// the panic value plus stack trace to log.
func Recoverer(log io.Writer) func(http.Handler) http.Handler {
	// TODO: Implement
	return func(next http.Handler) http.Handler { return next }
}
