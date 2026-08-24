// Package money provides currency-safe monetary arithmetic using minor
// units, teaching integer-vs-float precision, table-driven tests, and
// fuzzing.
package money

import "errors"

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

// ErrCurrencyMismatch is returned when an operation combines Amounts in
// different currencies.
var ErrCurrencyMismatch = errors.New("money: currency mismatch")

// Amount is an immutable value: a count of minor units plus its currency.
type Amount struct {
	minor    int64
	currency Currency
}

// NewAmount returns an Amount of minor minor units in currency c.
func NewAmount(minor int64, c Currency) Amount {
	// TODO: Implement
	return Amount{}
}

// Currency returns the currency of a.
func (a Amount) Currency() Currency {
	// TODO: Implement
	return Currency{}
}

// MinorUnits returns the minor-unit count of a.
func (a Amount) MinorUnits() int64 {
	// TODO: Implement
	return 0
}

// Add returns the sum of a and b, or ErrCurrencyMismatch wrapped in an error
// when the currencies differ.
func (a Amount) Add(b Amount) (Amount, error) {
	// TODO: Implement
	return Amount{}, nil
}

// Sub returns b subtracted from a, or ErrCurrencyMismatch wrapped in an
// error when the currencies differ.
func (a Amount) Sub(b Amount) (Amount, error) {
	// TODO: Implement
	return Amount{}, nil
}

// Mul scales a by n, returning an error instead of silently wrapping on
// int64 overflow.
func (a Amount) Mul(n int64) (Amount, error) {
	// TODO: Implement
	return Amount{}, nil
}

// Alloc splits a into n parts as evenly as possible, distributing any
// remainder to the earliest parts so that the parts sum to a exactly. It
// returns an error when n <= 0.
func (a Amount) Alloc(n int) ([]Amount, error) {
	// TODO: Implement
	return nil, nil
}

// String renders a in major units with decimal placement determined by the
// currency exponent, e.g. NewAmount(123456, USD).String() == "1234.56 USD".
func (a Amount) String() string {
	// TODO: Implement
	return ""
}

// ParseAmount parses strings like "1234.56 USD", "-12.00 EUR", and
// "500 JPY" back into an Amount, rejecting unknown currencies and numbers
// with more fractional digits than the currency exponent allows.
func ParseAmount(s string) (Amount, error) {
	// TODO: Implement
	return Amount{}, nil
}
