package solution

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

const validLine = `127.0.0.1 - alice [10/Oct/2000:13:55:36 -0700] "GET /index.html HTTP/1.0" 200 2326`

func TestAnalyzeSingleLine(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    Stats
		wantErr bool
	}{
		{
			name:  "valid minimal entry",
			input: "1.2.3.4 - - [10/Oct/2000:13:55:36 -0700] \"GET / HTTP/1.1\" 200 -\n",
			want: Stats{
				Total:    1,
				Skipped:  0,
				ByStatus: map[int]int{200: 1},
				ByPath:   map[string]int{"/": 1},
			},
		},
		{
			name:  "entry with bytes",
			input: validLine + "\n",
			want: Stats{
				Total:    1,
				ByStatus: map[int]int{200: 1},
				ByPath:   map[string]int{"/index.html": 1},
			},
		},
		{
			name:  "CRLF line endings tolerated",
			input: validLine + "\r\n",
			want: Stats{
				Total:    1,
				ByStatus: map[int]int{200: 1},
				ByPath:   map[string]int{"/index.html": 1},
			},
		},
		{
			name:    "non-matching line is skipped, not an error",
			input:   "this is not a log line\n",
			want:    Stats{Skipped: 1, ByStatus: map[int]int{}, ByPath: map[string]int{}},
			wantErr: false,
		},
		{
			name:  "junk protocol tail is skipped, path not counted",
			input: "1.2.3.4 - - [t] \"GET /a b HTTP/1.1\" 200 5\n",
			want:  Stats{Skipped: 1, ByStatus: map[int]int{}, ByPath: map[string]int{}},
		},
		{
			name:  "status with sign prefix is skipped",
			input: "1.2.3.4 - - [t] \"GET /a HTTP/1.1\" +200 5\n",
			want:  Stats{Skipped: 1, ByStatus: map[int]int{}, ByPath: map[string]int{}},
		},
		{
			name:  "status with leading zero padding is skipped",
			input: "1.2.3.4 - - [t] \"GET /a HTTP/1.1\" 0200 5\n",
			want:  Stats{Skipped: 1, ByStatus: map[int]int{}, ByPath: map[string]int{}},
		},
		{
			name:  "negative status is skipped",
			input: "1.2.3.4 - - [t] \"GET /a HTTP/1.1\" -5 5\n",
			want:  Stats{Skipped: 1, ByStatus: map[int]int{}, ByPath: map[string]int{}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Analyze(strings.NewReader(tt.input))
			if (err != nil) != tt.wantErr {
				t.Fatalf("Analyze() error = %v, wantErr %v", err, tt.wantErr)
			}
			if got.Total != tt.want.Total || got.Skipped != tt.want.Skipped {
				t.Errorf("Total/Skipped = %d/%d, want %d/%d", got.Total, got.Skipped, tt.want.Total, tt.want.Skipped)
			}
			if len(got.ByStatus) != len(tt.want.ByStatus) {
				t.Fatalf("ByStatus = %v, want %v", got.ByStatus, tt.want.ByStatus)
			}
			for code, n := range tt.want.ByStatus {
				if got.ByStatus[code] != n {
					t.Errorf("ByStatus[%d] = %d, want %d", code, got.ByStatus[code], n)
				}
			}
			for p, n := range tt.want.ByPath {
				if got.ByPath[p] != n {
					t.Errorf("ByPath[%q] = %d, want %d", p, got.ByPath[p], n)
				}
			}
		})
	}
}

func TestAnalyzeMixedStreaming(t *testing.T) {
	var b strings.Builder
	b.WriteString(validLine + "\n")
	b.WriteString("garbage that matches nothing\n")
	b.WriteString(`10.0.0.9 - bob [x] "POST /api/x HTTP/1.1" 404 10` + "\n")
	b.WriteString(`10.0.0.2 - - [y] "GET /broken HTTP/1.1" 500 abc` + "\n") // malformed bytes
	b.WriteString("\n")                                                     // empty line -> skipped

	stats, err := Analyze(strings.NewReader(b.String()))
	if err == nil {
		t.Fatal("Analyze() expected error for malformed line, got nil")
	}
	if stats == nil {
		t.Fatal("Analyze() returned nil stats alongside error; partial stats required")
	}
	var le *LineError
	if !errors.As(err, &le) {
		t.Fatalf("error %T (%v) is not a *LineError", err, err)
	}
	if le.Line != 4 {
		t.Errorf("LineError.Line = %d, want 4", le.Line)
	}
	if stats.Total != 2 || stats.Skipped != 1 {
		t.Errorf("partial stats Total/Skipped = %d/%d, want 2/1", stats.Total, stats.Skipped)
	}
	if stats.ByStatus[200] != 1 || stats.ByStatus[404] != 1 {
		t.Errorf("ByStatus = %v, want 200:1 404:1", stats.ByStatus)
	}
}

func TestSentinelErrors(t *testing.T) {
	// Status is regex-validated as exactly 3 digits, so malformed
	// fields surface via the bytes field instead.
	_, err := Analyze(strings.NewReader(
		"1.2.3.4 - - [t] \"GET /a HTTP/1.1\" 200 notanumber\n"))
	if err == nil {
		t.Fatal("expected malformed-line error")
	}
	if !errors.Is(err, ErrMalformed) {
		t.Errorf("errors.Is(err, ErrMalformed) = false through LineError chain; err = %v", err)
	}
	var le *LineError
	if !errors.As(err, &le) || le.Unwrap() == nil {
		t.Fatalf("LineError unwrap chain broken: %v", err)
	}
}

func TestAnalyzeLargeInput(t *testing.T) {
	const n = 100_000
	var b strings.Builder
	b.Grow(n * 64)
	codes := []int{200, 201, 301, 404, 500}
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, "10.0.0.%d - u%d [t] \"GET /p%d HTTP/1.1\" %d %d\n",
			i%250, i%7, i%1000, codes[i%len(codes)], i)
	}
	stats, err := Analyze(strings.NewReader(b.String()))
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	if stats.Total != n || stats.Skipped != 0 {
		t.Errorf("Total/Skipped = %d/%d, want %d/0", stats.Total, stats.Skipped, n)
	}
	sum := 0
	for _, c := range stats.ByStatus {
		sum += c
	}
	if sum != n {
		t.Errorf("sum of ByStatus = %d, want %d", sum, n)
	}
}

func TestTopPathsOrderingAndClamp(t *testing.T) {
	s := &Stats{
		ByPath: map[string]int{
			"/b": 3,
			"/a": 3,
			"/c": 5,
			"/d": 1,
		},
	}
	got := TopPaths(s, 3)
	want := []PathCount{
		{"/c", 5},
		{"/a", 3}, // tie with /b broken by path asc
		{"/b", 3},
	}
	if len(got) != len(want) {
		t.Fatalf("len(TopPaths) = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("TopPaths[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
	all := TopPaths(s, 100) // clamp to len(ByPath)
	if len(all) != 4 {
		t.Errorf("clamped len = %d, want 4", len(all))
	}
	if empty := TopPaths(s, 0); len(empty) != 0 {
		t.Errorf("n=0 should yield empty slice, got %v", empty)
	}
}

func TestLargeSingleLineWithinLimit(t *testing.T) {
	longPath := "/" + strings.Repeat("a", 800_000) // proves the raised Scanner limit
	line := fmt.Sprintf("1.2.3.4 - - [t] \"GET %s HTTP/1.1\" 200 -\n", longPath)
	stats, err := Analyze(strings.NewReader(line))
	if err != nil {
		t.Fatalf("Analyze() error on ~800KB line: %v", err)
	}
	if stats.Total != 1 || stats.ByPath[longPath] != 1 {
		t.Errorf("long line not parsed: Total=%d ByPath=%d", stats.Total, stats.ByPath[longPath])
	}
}

func TestCompilePattern(t *testing.T) {
	re := CompilePattern()
	if re == nil {
		t.Fatal("CompilePattern() returned nil")
	}
	if !re.MatchString(validLine) {
		t.Errorf("compiled pattern does not match a valid log line: %s", validLine)
	}
}
