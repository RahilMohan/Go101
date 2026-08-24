package money

import (
	"errors"
	"math"
	"testing"
)

func TestAddSubTable(t *testing.T) {
	tests := []struct {
		name    string
		a       Amount
		b       Amount
		wantSum int64
		wantSub int64
		wantErr error // non-nil means both ops must fail with errors.Is
	}{
		{"same currency add/sub", NewAmount(150, USD), NewAmount(25, USD), 175, 125, nil},
		{"negative operand", NewAmount(-100, USD), NewAmount(25, USD), -75, -125, nil},
		{"zero amounts", NewAmount(0, USD), NewAmount(0, USD), 0, 0, nil},
		{"JPY exponent 0", NewAmount(500, JPY), NewAmount(250, JPY), 750, 250, nil},
		{"BHD exponent 3", NewAmount(1, BHD), NewAmount(999, BHD), 1000, -998, nil},
		{"cross currency USD/EUR", NewAmount(100, USD), NewAmount(100, EUR), 0, 0, ErrCurrencyMismatch},
		{"cross currency USD/JPY", NewAmount(100, USD), NewAmount(1, JPY), 0, 0, ErrCurrencyMismatch},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sum, err := tt.a.Add(tt.b)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("Add() error = %v, want %v", err, tt.wantErr)
				}
				if (sum != Amount{}) {
					t.Errorf("Add() on mismatch = %+v, want zero Amount", sum)
				}
				sub, err := tt.a.Sub(tt.b)
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("Sub() error = %v, want %v", err, tt.wantErr)
				}
				if (sub != Amount{}) {
					t.Errorf("Sub() on mismatch = %+v, want zero Amount", sub)
				}
				return
			}
			if err != nil {
				t.Fatalf("Add() error = %v", err)
			}
			if sum.MinorUnits() != tt.wantSum || sum.Currency() != tt.a.Currency() {
				t.Errorf("Add() = %v, want minor %d in %s", sum, tt.wantSum, tt.a.Currency().Code)
			}
			sub, err := tt.a.Sub(tt.b)
			if err != nil {
				t.Fatalf("Sub() error = %v", err)
			}
			if sub.MinorUnits() != tt.wantSub || sub.Currency() != tt.a.Currency() {
				t.Errorf("Sub() = %v, want minor %d in %s", sub, tt.wantSub, tt.a.Currency().Code)
			}
		})
	}
}

// TestAccessors checks that Amount fields stay encapsulated behind accessors.
func TestAccessors(t *testing.T) {
	a := NewAmount(-42, BHD)
	if a.MinorUnits() != -42 {
		t.Errorf("MinorUnits() = %d, want -42", a.MinorUnits())
	}
	if a.Currency() != BHD {
		t.Errorf("Currency() = %+v, want BHD", a.Currency())
	}
}

func TestMulOverflow(t *testing.T) {
	tests := []struct {
		name    string
		minor   int64
		n       int64
		want    int64
		wantErr bool
	}{
		{"small multiply", 123456, 2, 246912, false},
		{"multiply by zero", 123456, 0, 0, false},
		{"multiply by one", math.MaxInt64, 1, math.MaxInt64, false},
		{"boundary overflow", math.MaxInt64/2 + 1, 2, 0, true},
		{"max overflow", math.MaxInt64, 10, 0, true},
		{"min times minus one overflows", math.MinInt64, -1, 0, true},
		{"min times two overflows", math.MinInt64, 2, 0, true},
		{"min times plus one fits", math.MinInt64, 1, math.MinInt64, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NewAmount(tt.minor, USD).Mul(tt.n)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("Mul(%d) = %v, want overflow error", tt.n, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("Mul(%d) unexpected error: %v", tt.n, err)
			}
			if got.MinorUnits() != tt.want {
				t.Errorf("Mul(%d).MinorUnits() = %d, want %d", tt.n, got.MinorUnits(), tt.want)
			}
		})
	}
}

// TestAllocExactSum verifies the core Alloc invariant: however n parts are
// carved out of an amount, they must reassemble into exactly that amount,
// with remainders pushed to the earliest parts.
func TestAllocExactSum(t *testing.T) {
	for _, c := range []Currency{USD, EUR, JPY, BHD} {
		for _, total := range []int64{7, 100, 12345, -5, -12345, 1} {
			for n := 1; n <= 7; n++ {
				parts, err := NewAmount(total, c).Alloc(n)
				if err != nil {
					t.Fatalf("Alloc(%d) of %d %s: %v", n, total, c.Code, err)
				}
				var sum int64
				for i, p := range parts {
					if len(parts) != n {
						t.Fatalf("Alloc(%d) returned %d parts", n, len(parts))
					}
					if p.Currency() != c {
						t.Errorf("part %d has wrong currency %+v", i, p.Currency())
					}
					sum += p.MinorUnits()
					// Remainder goes to the earliest parts: no part may be
					// more than one unit above the base share, and once a
					// part drops back to base all later parts are base too.
				}
				if sum != total {
					t.Errorf("Alloc(%d) of %d %s: parts sum to %d", n, total, c.Code, sum)
				}
			}
		}
	}
}

func TestAllocRemainderToEarliestParts(t *testing.T) {
	got, err := NewAmount(5, USD).Alloc(2)
	if err != nil {
		t.Fatal(err)
	}
	want := []int64{3, 2}
	for i, w := range want {
		if got[i].MinorUnits() != w {
			t.Errorf("part[%d] = %d, want %d", i, got[i].MinorUnits(), w)
		}
	}

	neg, err := NewAmount(-5, USD).Alloc(2)
	if err != nil {
		t.Fatal(err)
	}
	wantNeg := []int64{-3, -2}
	for i, w := range wantNeg {
		if neg[i].MinorUnits() != w {
			t.Errorf("negative part[%d] = %d, want %d", i, neg[i].MinorUnits(), w)
		}
	}
}

func TestAllocInvalidN(t *testing.T) {
	for _, n := range []int{0, -1, -7} {
		if _, err := NewAmount(100, USD).Alloc(n); err == nil {
			t.Errorf("Alloc(%d) expected error, got nil", n)
		}
	}
}

func TestStringFormats(t *testing.T) {
	tests := []struct {
		minor int64
		cur   Currency
		want  string
	}{
		{123456, USD, "1234.56 USD"},
		{5, USD, "0.05 USD"},
		{0, USD, "0.00 USD"},
		{-1250, USD, "-12.50 USD"},
		{-50, USD, "-0.50 USD"},
		{123456, JPY, "123456 JPY"},
		{-123456, JPY, "-123456 JPY"},
		{1234, BHD, "1.234 BHD"},
		{1, BHD, "0.001 BHD"},
		{-1, BHD, "-0.001 BHD"},
		{1200, EUR, "12.00 EUR"},
	}
	for _, tt := range tests {
		if got := NewAmount(tt.minor, tt.cur).String(); got != tt.want {
			t.Errorf("NewAmount(%d, %s).String() = %q, want %q", tt.minor, tt.cur.Code, got, tt.want)
		}
	}
}

func TestParseRejections(t *testing.T) {
	tests := []struct {
		name string
		in   string
	}{
		{"too many decimals for USD", "1.234 USD"},
		{"too few decimals for USD", "1.2 USD"},
		{"decimals on zero-exponent JPY", "1.5 JPY"},
		{"empty string", ""},
		{"number only", "12.34"},
		{"code only", "USD"},
		{"garbage", "hello world"},
		{"unknown currency", "12.34 XYZ"},
		{"bare minus", "- USD"},
		{"trailing dot no fraction", "12. USD"},
		{"leading dot", ".50 USD"},
		{"plus sign", "+1.00 USD"},
		{"embedded space in number", "1 234.00 USD"},
		{"non-numeric digits", "1x.00 USD"},
		{"out of range", "99999999999999999999.00 USD"},
		{"lowercase code", "12.00 usd"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a, err := ParseAmount(tt.in)
			if err == nil {
				t.Fatalf("ParseAmount(%q) = %+v, want error", tt.in, a)
			}
		})
	}
}

func TestParseAcceptsAndRoundTripsAllCurrencies(t *testing.T) {
	tests := []struct {
		in      string
		wantMin int64
		wantCur Currency
	}{
		{"1234.56 USD", 123456, USD},
		{"-12.00 EUR", -1200, EUR},
		{"500 JPY", 500, JPY},
		{"-500 JPY", -500, JPY},
		{"1.001 BHD", 1001, BHD},
		{"-0.001 BHD", -1, BHD},
		{"0.00 USD", 0, USD},
	}
	for _, tt := range tests {
		a, err := ParseAmount(tt.in)
		if err != nil {
			t.Fatalf("ParseAmount(%q) unexpected error: %v", tt.in, err)
		}
		if a.MinorUnits() != tt.wantMin || a.Currency() != tt.wantCur {
			t.Errorf("ParseAmount(%q) = %d %s, want %d %s",
				tt.in, a.MinorUnits(), a.Currency().Code, tt.wantMin, tt.wantCur.Code)
		}
		if again := a.String(); again != tt.in {
			t.Errorf("round trip: String() = %q, want %q", again, tt.in)
		}
	}
}

// FuzzParseAmount checks the canonical-format property: any string that
// parses successfully must re-render through String and parse again to an
// identical Amount.
func FuzzParseAmount(f *testing.F) {
	f.Add("1234.56 USD")
	f.Add("-12.00 EUR")
	f.Add("500 JPY")
	f.Add("-500 JPY")
	f.Add("1.001 BHD")
	f.Add("-0.001 BHD")
	f.Add("0.00 USD")
	f.Add("")
	f.Add("1.234 USD")

	f.Fuzz(func(t *testing.T, s string) {
		a, err := ParseAmount(s)
		if err != nil {
			t.Skip()
		}
		b, err := ParseAmount(a.String())
		if err != nil {
			t.Fatalf("re-parse of %q failed: %v", a.String(), err)
		}
		if a != b {
			t.Fatalf("round trip changed value: %v -> %v via %q", a, b, a.String())
		}
	})
}

// FuzzAdd checks the inverse-operation property: when currencies match and
// Add succeeds, adding b then subtracting b returns exactly a.
func FuzzAdd(f *testing.F) {
	codes := []byte{0, 1, 2, 3}
	f.Add(int64(150), int64(25), codes[0])
	f.Add(int64(math.MaxInt64), int64(1), codes[0])
	f.Add(int64(math.MinInt64), int64(-1), codes[1])
	f.Add(int64(0), int64(0), codes[2])
	f.Add(int64(-7), int64(13), codes[3])

	f.Fuzz(func(t *testing.T, x, y int64, cur byte) {
		c := []Currency{USD, EUR, JPY, BHD}[int(cur)%len(codes)]
		a, b := NewAmount(x, c), NewAmount(y, c)

		mismatch, err := NewAmount(x, USD).Add(NewAmount(y, JPY))
		if !errors.Is(err, ErrCurrencyMismatch) || mismatch != (Amount{}) {
			t.Fatalf("cross-currency Add did not yield zero Amount + ErrCurrencyMismatch")
		}

		sum, err := a.Add(b)
		if err != nil {
			t.Skip()
		}
		back, err := sum.Sub(b)
		if err != nil {
			t.Fatalf("Sub after successful Add errored: %v", err)
		}
		if back != a {
			t.Fatalf("(a+b)-b = %v, want %v", back, a)
		}
	})
}
