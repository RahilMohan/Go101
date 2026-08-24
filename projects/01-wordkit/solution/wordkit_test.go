package solution

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestAnalyze(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    Stats
		wantErr bool
	}{
		{
			name:  "empty input",
			input: "",
			want:  Stats{Lines: 0, Words: 0, Bytes: 0, Chars: 0},
		},
		{
			name:  "single line",
			input: "hello\n",
			want:  Stats{Lines: 1, Words: 1, Bytes: 6, Chars: 6},
		},
		{
			name:  "multi-line with mixed whitespace",
			input: "one two\tthree\nfour  five\n\nlast",
			want:  Stats{Lines: 4, Words: 6, Bytes: 30, Chars: 30},
		},
		{
			name:  "unicode chars differ from bytes",
			input: "héllo wörld ✓\n",
			want:  Stats{Lines: 1, Words: 3, Bytes: 18, Chars: 14},
		},
		{
			name:  "invalid utf8 bytes count as one char each",
			input: "h\xffi\n",
			want:  Stats{Lines: 1, Words: 1, Bytes: 4, Chars: 4},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Analyze(strings.NewReader(tt.input))
			if (err != nil) != tt.wantErr {
				t.Fatalf("Analyze() error = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("Analyze() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestAnalyzeLargeInput(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 10000; i++ {
		fmt.Fprintf(&b, "line %d padding padding padding\n", i)
	}
	input := b.String()

	got, err := Analyze(strings.NewReader(input))
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	want := Stats{
		Lines: 10000,
		Words: 50000,
		Bytes: int64(len(input)),
		Chars: int64(len(input)),
	}
	if got != want {
		t.Errorf("Analyze() = %+v, want %+v", got, want)
	}
}

// errReader always fails, exercising Analyze's error propagation.
type errReader struct{ err error }

func (r errReader) Read([]byte) (int, error) { return 0, r.err }

func TestAnalyzePropagatesReaderError(t *testing.T) {
	sentinel := errors.New("read failed")
	stats, err := Analyze(errReader{err: sentinel})
	if !errors.Is(err, sentinel) {
		t.Fatalf("Analyze() error = %v, want %v", err, sentinel)
	}
	if stats != (Stats{}) {
		t.Errorf("Analyze() stats = %+v on error, want zeroed Stats", stats)
	}
}

// midReadReader yields some data first, then fails — pinning that Analyze
// zeroes partial stats even after bytes were successfully consumed.
type midReadReader struct {
	data []byte
	err  error
}

func (r *midReadReader) Read(p []byte) (int, error) {
	if len(r.data) > 0 {
		n := copy(p, r.data)
		r.data = r.data[n:]
		return n, nil
	}
	return 0, r.err
}

func TestAnalyzeMidReadErrorZeroesPartialStats(t *testing.T) {
	sentinel := errors.New("mid-read failure")
	stats, err := Analyze(&midReadReader{
		data: []byte("partial data\nand more"),
		err:  sentinel,
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("Analyze() error = %v, want %v", err, sentinel)
	}
	if stats != (Stats{}) {
		t.Errorf("Analyze() stats = %+v on mid-read error, want zeroed Stats", stats)
	}
}

func TestFormat(t *testing.T) {
	got := Format(Stats{Lines: 12, Words: 3456, Bytes: 789012, Chars: 34})
	want := "      12    3456  789012      34"
	if got != want {
		t.Errorf("Format() = %q, want %q", got, want)
	}
}
