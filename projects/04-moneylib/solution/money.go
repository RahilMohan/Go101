// Package money provides currency-safe monetary arithmetic using minor
// units, teaching integer-vs-float precision, table-driven tests, and
// fuzzing.
//
// An Amount stores an int64 count of minor units (cents, sen, fils) plus
// the Currency it is denominated in. No floats are used anywhere: parsing,
// rendering, and arithmetic are exact, and every operation that could
// overflow or mix currencies reports an error instead of producing a wrong
// number.
package money

import (
	"errors"
	"fmt"
	"math"
	"math/bits"
	"strconv"
	"strings"
)

// Currency identifies a currency by its ISO 4217 alphabetic Code and its
// Exponent, the power of ten relating minor units to major units: USD has
// cents so Exponent 2, JPY has none so Exponent 0, and BHD has thirds of a
// dinar (fils) so Exponent 3.
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
// different currencies; such a combination has no meaningful result.
var ErrCurrencyMismatch = errors.New("money: currency mismatch")

// Amount is an immutable value: a count of minor units plus the currency it
// is denominated in. The zero Amount is not a valid monetary value; build
// Amounts with NewAmount or ParseAmount.
type Amount struct {
	minor    int64
	currency Currency
}

// NewAmount returns an Amount of minor minor units in currency c. It never
// fails: the caller is responsible for choosing sensible minor units.
func NewAmount(minor int64, c Currency) Amount {
	return Amount{minor: minor, currency: c}
}

// Currency returns the currency a is denominated in.
func (a Amount) Currency() Currency {
	return a.currency
}

// MinorUnits returns the raw minor-unit count of a, sign included.
func (a Amount) MinorUnits() int64 {
	return a.minor
}

// Add returns the sum of a and b as a new Amount. It returns an error
// wrapping ErrCurrencyMismatch when the currencies differ, and an error
// when the addition would overflow int64.
func (a Amount) Add(b Amount) (Amount, error) {
	if a.currency != b.currency {
		return Amount{}, fmt.Errorf("money: %w: %s + %s", ErrCurrencyMismatch, a.currency.Code, b.currency.Code)
	}
	sum := a.minor + b.minor
	if (a.minor > 0 && b.minor > 0 && sum < 0) || (a.minor < 0 && b.minor < 0 && sum >= 0) {
		return Amount{}, fmt.Errorf("money: add overflows int64: %d + %d", a.minor, b.minor)
	}
	return Amount{minor: sum, currency: a.currency}, nil
}

// Sub returns b subtracted from a as a new Amount. It returns an error
// wrapping ErrCurrencyMismatch when the currencies differ, and an error
// when the subtraction would overflow int64.
func (a Amount) Sub(b Amount) (Amount, error) {
	if a.currency != b.currency {
		return Amount{}, fmt.Errorf("money: %w: %s - %s", ErrCurrencyMismatch, a.currency.Code, b.currency.Code)
	}
	diff := a.minor - b.minor
	if (b.minor < 0 && a.minor > math.MaxInt64+b.minor) || (b.minor > 0 && a.minor < math.MinInt64+b.minor) {
		return Amount{}, fmt.Errorf("money: subtract overflows int64: %d - %d", a.minor, b.minor)
	}
	return Amount{minor: diff, currency: a.currency}, nil
}

// Mul scales a by n as a new Amount. It uses full 128-bit intermediate
// precision via math/bits and returns an error when the true product does
// not fit in an int64, rather than silently wrapping.
func (a Amount) Mul(n int64) (Amount, error) {
	if a.minor == 0 || n == 0 {
		return Amount{minor: 0, currency: a.currency}, nil
	}
	neg := (a.minor < 0) != (n < 0)
	hi, lo := bits.Mul64(uint64(abs64(a.minor)), uint64(abs64(n)))
	// Magnitude 2^63 is representable only as MinInt64, so it is legal only
	// for a negative result.
	maxMag := uint64(math.MaxInt64)
	if neg {
		maxMag = 1 << 63
	}
	if hi != 0 || lo > maxMag {
		return Amount{}, fmt.Errorf("money: multiply overflows int64: %d * %d", a.minor, n)
	}
	p := int64(lo)
	if neg {
		p = -p
	}
	return Amount{minor: p, currency: a.currency}, nil
}

// Alloc splits a into n parts as evenly as possible, giving each part the
// same base count and distributing the remainder one minor unit at a time
// to the earliest parts, so that the returned parts always sum to a
// exactly — including for negative amounts. It returns an error when
// n <= 0.
func (a Amount) Alloc(n int) ([]Amount, error) {
	if n <= 0 {
		return nil, fmt.Errorf("money: Alloc: n must be positive, got %d", n)
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
// currency exponent, e.g. NewAmount(123456, USD).String() == "1234.56 USD"
// and NewAmount(123456, JPY).String() == "123456 JPY". Negative amounts
// keep their sign and fractional digits are zero-padded to full exponent
// width, so String is canonical and round-trips through ParseAmount.
func (a Amount) String() string {
	var sb strings.Builder
	um := abs64(a.minor)
	if a.minor < 0 {
		sb.WriteByte('-')
	}
	if exp := a.currency.Exponent; exp > 0 {
		div := uint64(pow10(exp))
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
// "500 JPY" back into an Amount. It rejects unknown currency codes,
// malformed numbers, signs other than a leading minus, and numbers whose
// fractional digit count does not exactly match the currency exponent
// ("1.234 USD" and "1.2 USD" are both errors for USD).
func ParseAmount(s string) (Amount, error) {
	sp := strings.LastIndexByte(s, ' ')
	if sp < 0 {
		return Amount{}, fmt.Errorf("money: ParseAmount(%q): missing currency code", s)
	}
	numStr, codeStr := s[:sp], s[sp+1:]
	c, ok := byCode[codeStr]
	if !ok {
		return Amount{}, fmt.Errorf("money: ParseAmount(%q): unknown currency %q", s, codeStr)
	}

	body := numStr
	neg := strings.HasPrefix(body, "-")
	if neg {
		body = body[1:]
	}
	intPart, fracPart, hasDot := strings.Cut(body, ".")
	if !allDigits(intPart) || intPart == "" || (hasDot && !allDigits(fracPart)) {
		return Amount{}, fmt.Errorf("money: ParseAmount(%q): malformed number %q", s, numStr)
	}
	if len(fracPart) != c.Exponent {
		return Amount{}, fmt.Errorf(
			"money: ParseAmount(%q): %s needs exactly %d fractional digits, got %q",
			s, c.Code, c.Exponent, numStr)
	}
	minor, err := strconv.ParseInt(intPart+fracPart, 10, 64)
	if err != nil {
		return Amount{}, fmt.Errorf("money: ParseAmount(%q): out of range: %w", s, err)
	}
	if neg {
		minor = -minor
	}
	return Amount{minor: minor, currency: c}, nil
}

// pow10 returns 10^e for small non-negative exponents.
func pow10(e int) int64 {
	p := int64(1)
	for range e {
		p *= 10
	}
	return p
}

// allDigits reports whether s consists solely of ASCII decimal digits.
func allDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// abs64 returns |x| computed in uint64 space so MinInt64 is representable.
func abs64(x int64) uint64 {
	if x < 0 {
		return uint64(-(x + 1)) + 1
	}
	return uint64(x)
}
