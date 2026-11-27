// Package money is the exact-arithmetic model of amounts: a signed 64-bit
// count of minor currency units. There is no floating point anywhere; every
// operation that could leave the int64 range reports an error instead of
// wrapping.
package money

import (
	"math"
	"strconv"
	"strings"

	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/apperr"
)

// Amount is a number of minor currency units. The currency is a display
// concern of the installation and is not part of the value.
type Amount int64

// Largest and smallest representable amounts.
const (
	Max Amount = math.MaxInt64
	Min Amount = math.MinInt64
)

// MaxDecimals is the largest supported number of fractional digits.
const MaxDecimals = 18

func invalidAmount(msg string) error {
	return apperr.New(apperr.InvalidAmount, msg).WithField("amount", string(apperr.InvalidAmount))
}

// ParseAmount parses a string of 1..19 ASCII digits without leading zeros
// (except "0" itself) whose value does not exceed Max.
func ParseAmount(s string) (Amount, error) {
	if s == "" || len(s) > 19 {
		return 0, invalidAmount("amount must be a string of 1 to 19 digits")
	}
	if len(s) > 1 && s[0] == '0' {
		return 0, invalidAmount("amount must not have leading zeros")
	}
	v, ok := parseDigits(s)
	if !ok {
		return 0, invalidAmount("amount must contain digits only and fit into 64 bits")
	}
	return Amount(v), nil
}

// parseDigits converts an all-digit string into a non-negative int64; it
// reports false for a non-digit or a value above MaxInt64.
func parseDigits(s string) (int64, bool) {
	if s == "" {
		return 0, false
	}
	var v int64
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < '0' || c > '9' {
			return 0, false
		}
		d := int64(c - '0')
		if v > (math.MaxInt64-d)/10 {
			return 0, false
		}
		v = v*10 + d
	}
	return v, true
}

func pow10(n int) int64 {
	v := int64(1)
	for i := 0; i < n; i++ {
		v *= 10
	}
	return v
}

// ParseDecimal parses a decimal string such as "10.50" into minor units for a
// currency with the given number of fractional digits (0..MaxDecimals). The
// string has an integer part without leading zeros and, optionally, a point
// followed by 1..decimals digits. Digits beyond decimals are an error, never
// rounded; signs, exponents and spaces are errors too.
func ParseDecimal(s string, decimals int) (Amount, error) {
	if decimals < 0 || decimals > MaxDecimals {
		return 0, invalidAmount("unsupported number of decimals")
	}
	intPart, fracPart, hasPoint := strings.Cut(s, ".")
	if intPart == "" || (len(intPart) > 1 && intPart[0] == '0') {
		return 0, invalidAmount("amount must have an integer part without leading zeros")
	}
	if hasPoint && (fracPart == "" || len(fracPart) > decimals) {
		return 0, invalidAmount("amount has a wrong number of fractional digits")
	}
	ip, ok := parseDigits(intPart)
	if !ok {
		return 0, invalidAmount("amount must contain digits only and fit into 64 bits")
	}
	var fp int64
	if hasPoint {
		fp, ok = parseDigits(fracPart)
		if !ok {
			return 0, invalidAmount("amount must contain digits only")
		}
		fp *= pow10(decimals - len(fracPart))
	}
	scale := pow10(decimals)
	if ip > (math.MaxInt64-fp)/scale {
		return 0, invalidAmount("amount does not fit into 64 bits")
	}
	return Amount(ip*scale + fp), nil
}

// FormatDecimal renders a with exactly decimals fractional digits (no point for
// decimals == 0). It is the exact inverse of ParseDecimal for non-negative
// amounts; a negative amount gets a leading minus.
func (a Amount) FormatDecimal(decimals int) string {
	if decimals < 0 || decimals > MaxDecimals {
		decimals = 0
	}
	neg := a < 0
	var digits string
	switch {
	case a == Min:
		digits = "9223372036854775808" // -Min does not fit into int64
	case neg:
		digits = strconv.FormatInt(-int64(a), 10)
	default:
		digits = strconv.FormatInt(int64(a), 10)
	}
	if decimals > 0 {
		if len(digits) <= decimals {
			digits = strings.Repeat("0", decimals-len(digits)+1) + digits
		}
		cut := len(digits) - decimals
		digits = digits[:cut] + "." + digits[cut:]
	}
	if neg {
		return "-" + digits
	}
	return digits
}

// String returns the amount as a plain integer of minor units.
func (a Amount) String() string { return strconv.FormatInt(int64(a), 10) }

// Minor returns the number of minor units.
func (a Amount) Minor() int64 { return int64(a) }

// Positive checks that the amount is usable as an operation amount (> 0).
func (a Amount) Positive() error {
	if a <= 0 {
		return apperr.New(apperr.AmountMustBePositive, "amount must be greater than zero").
			WithField("amount", string(apperr.AmountMustBePositive))
	}
	return nil
}

// Add returns a+b, or balance_overflow if the sum leaves the int64 range.
func (a Amount) Add(b Amount) (Amount, error) {
	if (b > 0 && a > Max-b) || (b < 0 && a < Min-b) {
		return 0, apperr.New(apperr.BalanceOverflow, "balance would exceed the maximum amount")
	}
	return a + b, nil
}

// Sub returns a-b for balance arithmetic: insufficient_funds if the result
// would be negative, balance_overflow if subtracting a negative b overflows.
func (a Amount) Sub(b Amount) (Amount, error) {
	if b < 0 {
		if a > Max+b {
			return 0, apperr.New(apperr.BalanceOverflow, "balance would exceed the maximum amount")
		}
		return a - b, nil
	}
	if a < b {
		return 0, apperr.New(apperr.InsufficientFunds, "insufficient funds")
	}
	return a - b, nil
}

// Cmp returns -1, 0 or 1 as a is less than, equal to or greater than b.
func (a Amount) Cmp(b Amount) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

// Less reports whether a < b.
func (a Amount) Less(b Amount) bool { return a < b }

// MinOf returns the smaller of a and b.
func MinOf(a, b Amount) Amount {
	if b < a {
		return b
	}
	return a
}
