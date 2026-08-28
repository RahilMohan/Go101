// Package money provides currency-safe monetary arithmetic using minor
// units, teaching integer-vs-float precision, table-driven tests, and
// fuzzing.
package money

import (
	"errors"
	"fmt"
	"math"
	"math/bits"
	"strconv"
	"strings"
)

// Currency identifies a currency by its ISO 4217 Code and Exponent, the
// power of ten relating minor units to major units: USD has 2 (cents), JPY
// has 0, BHD has 3 (fils).
type Currency struct {
	Code     string // ISO 4217 alphabetic code, e.g. "USD"
	Exponent int    // minor units per major unit is 10^Exponent
}

var (
	// USD is United States dollar with two decimal places (cents).
	USD = Currency{"USD", 2}
	// EUR is Euro with two decimal places (cents).
	EUR = Currency{"EUR", 2}
	// JPY is Japanese yen with no decimal places.
	JPY = Currency{"JPY", 0}
	// BHD is Bahraini dinar with three decimal places (fils), proving the
	// library generalizes beyond two-decimal currencies.
	BHD = Currency{"BHD", 3}
)

// byCode indexes the supported currencies for ParseAmount lookups.
var byCode = map[string]Currency{
	USD.Code: USD,
	EUR.Code: EUR,
	JPY.Code: JPY,
	BHD.Code: BHD,
}

// ErrCurrencyMismatch is returned when an operation combines Amounts in
// different currencies.
var ErrCurrencyMismatch = errors.New("money: currency mismatch")

// ErrBadFormat indicates a malformed amount string.
var ErrBadFormat = errors.New("money: bad format")

// ErrRange indicates a numeric value outside the int64 range.
var ErrRange = errors.New("money: out of range")

// Valid reports whether c is a plausible currency definition.
// Code must be non-empty and Exponent in [0,18]. In practice ISO 4217
// currencies use exponents 0..4 (e.g. JPY 0, USD 2, BHD 3); the upper bound
// 18 is the safe rendering limit before 10^Exponent overflows int64/uint64.
func (c Currency) Valid() error {
	if c.Code == "" {
		return fmt.Errorf("%w: empty currency code", ErrBadFormat)
	}
	if c.Exponent < 0 || c.Exponent > 18 {
		return fmt.Errorf("%w: exponent %d out of range [0,18]", ErrBadFormat, c.Exponent)
	}
	return nil
}

// Amount is an immutable value: a count of minor units plus its currency.
type Amount struct {
	minor    int64
	currency Currency
}

// NewAmount returns an Amount of minor minor units in currency c.
// For validated currencies use c.Valid() before construction; the library
// itself keeps the constructor infallible but String and ParseAmount guard
// against out-of-range exponents.
func NewAmount(minor int64, c Currency) Amount {
	return Amount{minor: minor, currency: c}
}

// Currency returns the currency of a.
func (a Amount) Currency() Currency {
	return a.currency
}

// MinorUnits returns the minor-unit count of a.
func (a Amount) MinorUnits() int64 {
	return a.minor
}

// Add returns the sum of a and b, or ErrCurrencyMismatch wrapped in an error
// when the currencies differ.
func (a Amount) Add(b Amount) (Amount, error) {
	if a.currency != b.currency {
		return Amount{}, fmt.Errorf("money: %w: %s + %s", ErrCurrencyMismatch, a.currency.Code, b.currency.Code)
	}
	sum := a.minor + b.minor
	if (a.minor > 0 && b.minor > 0 && sum < 0) || (a.minor < 0 && b.minor < 0 && sum >= 0) {
		return Amount{}, fmt.Errorf("money: add overflows int64: %d + %d: %w", a.minor, b.minor, ErrRange)
	}
	return Amount{minor: sum, currency: a.currency}, nil
}

// Sub returns b subtracted from a, or ErrCurrencyMismatch wrapped in an
// error when the currencies differ.
func (a Amount) Sub(b Amount) (Amount, error) {
	if a.currency != b.currency {
		return Amount{}, fmt.Errorf("money: %w: %s - %s", ErrCurrencyMismatch, a.currency.Code, b.currency.Code)
	}
	diff := a.minor - b.minor
	if (b.minor < 0 && a.minor > math.MaxInt64+b.minor) || (b.minor > 0 && a.minor < math.MinInt64+b.minor) {
		return Amount{}, fmt.Errorf("money: subtract overflows int64: %d - %d: %w", a.minor, b.minor, ErrRange)
	}
	return Amount{minor: diff, currency: a.currency}, nil
}

// Mul scales a by n, returning an error instead of silently wrapping on
// int64 overflow.
func (a Amount) Mul(n int64) (Amount, error) {
	if a.minor == 0 || n == 0 {
		return Amount{minor: 0, currency: a.currency}, nil
	}
	neg := (a.minor < 0) != (n < 0)
	hi, lo := bits.Mul64(uint64(abs64(a.minor)), uint64(abs64(n)))
	maxMag := uint64(math.MaxInt64)
	if neg {
		maxMag = 1 << 63
	}
	if hi != 0 || lo > maxMag {
		return Amount{}, fmt.Errorf("money: multiply overflows int64: %d * %d: %w", a.minor, n, ErrRange)
	}
	p := int64(lo)
	if neg {
		p = -p
	}
	return Amount{minor: p, currency: a.currency}, nil
}

// Alloc splits a into n parts as evenly as possible, distributing any
// remainder to the earliest parts so that the parts sum to a exactly. It
// returns an error when n <= 0. To prevent OOM, n is capped at 1_000_000.
func (a Amount) Alloc(n int) ([]Amount, error) {
	if n <= 0 {
		return nil, fmt.Errorf("money: Alloc: n must be positive, got %d: %w", n, ErrBadFormat)
	}
	if n > 1_000_000 {
		return nil, fmt.Errorf("money: Alloc n too large: %d (cap 1_000_000): %w", n, ErrRange)
	}
	base := a.minor / int64(n)
	rem := a.minor % int64(n)
	step := int64(1)
	if rem < 0 {
		step = -1
	}
	parts := make([]Amount, n)
	for i := range parts {
		m := base
		if int64(i) < int64(abs64(rem)) {
			m += step
		}
		parts[i] = Amount{minor: m, currency: a.currency}
	}
	return parts, nil
}

// String renders a in major units with decimal placement determined by the
// currency exponent, e.g. NewAmount(123456, USD).String() == "1234.56 USD".
// If the currency exponent is outside [0,18], it falls back to raw minor
// units to avoid divide-by-zero from pow10 overflow.
func (a Amount) String() string {
	exp := a.currency.Exponent
	if exp < 0 || exp > 18 {
		return fmt.Sprintf("%d %s", a.minor, a.currency.Code)
	}
	var sb strings.Builder
	um := abs64(a.minor)
	if a.minor < 0 {
		sb.WriteByte('-')
	}
	if exp > 0 {
		div := uint64(pow10(exp))
		if div == 0 {
			return fmt.Sprintf("%d %s", a.minor, a.currency.Code)
		}
		sb.WriteString(strconv.FormatUint(um/div, 10))
		sb.WriteByte('.')
		frac := strconv.FormatUint(um%div, 10)
		for i := len(frac); i < exp; i++ {
			sb.WriteByte('0')
		}
		sb.WriteString(frac)
	} else {
		sb.WriteString(strconv.FormatUint(um, 10))
	}
	sb.WriteByte(' ')
	sb.WriteString(a.currency.Code)
	return sb.String()
}

// ParseAmount parses strings like "1234.56 USD", "-12.00 EUR", and
// "500 JPY" back into an Amount, rejecting unknown currencies and numbers
// with more fractional digits than the currency exponent allows.
func ParseAmount(s string) (Amount, error) {
	sp := strings.LastIndexByte(s, ' ')
	if sp < 0 {
		return Amount{}, fmt.Errorf("money: ParseAmount(%q): missing currency code: %w", s, ErrBadFormat)
	}
	numStr, codeStr := s[:sp], s[sp+1:]
	c, ok := byCode[codeStr]
	if !ok {
		return Amount{}, fmt.Errorf("money: ParseAmount(%q): unknown currency %q: %w", s, codeStr, ErrBadFormat)
	}
	body := numStr
	neg := strings.HasPrefix(body, "-")
	if neg {
		body = body[1:]
	}
	intPart, fracPart, hasDot := strings.Cut(body, ".")
	if hasDot && fracPart == "" {
		return Amount{}, fmt.Errorf("money: ParseAmount(%q): trailing dot in %q: %w", s, numStr, ErrBadFormat)
	}
	if !allDigits(intPart) || intPart == "" || (hasDot && !allDigits(fracPart)) {
		return Amount{}, fmt.Errorf("money: ParseAmount(%q): malformed number %q: %w", s, numStr, ErrBadFormat)
	}
	if len(fracPart) != c.Exponent {
		return Amount{}, fmt.Errorf(
			"money: ParseAmount(%q): %s needs exactly %d fractional digits, got %q: %w",
			s, c.Code, c.Exponent, numStr, ErrBadFormat)
	}
	digits := intPart + fracPart
	u, err := strconv.ParseUint(digits, 10, 64)
	if err != nil {
		return Amount{}, fmt.Errorf("money: ParseAmount(%q): out of range: %w: %w", s, err, ErrRange)
	}
	var minor int64
	if neg {
		if u > 1<<63 {
			return Amount{}, fmt.Errorf("money: ParseAmount(%q): out of range: %w", s, ErrRange)
		}
		if u == 1<<63 {
			minor = math.MinInt64
		} else {
			minor = -int64(u)
		}
	} else {
		if u > math.MaxInt64 {
			return Amount{}, fmt.Errorf("money: ParseAmount(%q): out of range: %w", s, ErrRange)
		}
		minor = int64(u)
	}
	return Amount{minor: minor, currency: c}, nil
}

// pow10 returns 10^e for small non-negative exponents. For e outside
// [0,18] it returns 0 to signal overflow (caller must guard).
func pow10(e int) int64 {
	if e < 0 || e > 18 {
		return 0
	}
	p := int64(1)
	for range e {
		p *= 10
	}
	return p
}

func allDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func abs64(x int64) uint64 {
	if x < 0 {
		return uint64(-(x + 1)) + 1
	}
	return uint64(x)
}
