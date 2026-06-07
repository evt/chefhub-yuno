package domain

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Money represents a monetary value in minor units (e.g. cents) to avoid
// floating-point drift during reconciliation. Currency is tracked separately
// on the owning Transaction.
type Money int64

// ErrInvalidAmount is returned when a string cannot be parsed into Money.
var ErrInvalidAmount = errors.New("invalid monetary amount")

// ParseMoney parses a decimal string like "12.34" or "0.05" into Money in
// minor units, assuming two-decimal precision. It accepts a leading sign and
// trims surrounding whitespace.
func ParseMoney(s string) (Money, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("parse money %q: %w", s, ErrInvalidAmount)
	}

	negative := false
	switch s[0] {
	case '-':
		negative = true
		s = s[1:]
	case '+':
		s = s[1:]
	}

	parts := strings.SplitN(s, ".", 2)
	whole, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse money whole part %q: %w", s, ErrInvalidAmount)
	}

	var frac int64
	if len(parts) == 2 {
		f := parts[1]
		switch {
		case len(f) > 2:
			// Round to two decimals to keep comparisons stable across sources.
			rounded, perr := strconv.ParseFloat("0."+f, 64)
			if perr != nil {
				return 0, fmt.Errorf("parse money fractional part %q: %w", s, ErrInvalidAmount)
			}
			frac = int64(math.Round(rounded * 100))
		case len(f) == 1:
			frac, err = strconv.ParseInt(f, 10, 64)
			if err != nil {
				return 0, fmt.Errorf("parse money fractional part %q: %w", s, ErrInvalidAmount)
			}
			frac *= 10
		default:
			frac, err = strconv.ParseInt(f, 10, 64)
			if err != nil {
				return 0, fmt.Errorf("parse money fractional part %q: %w", s, ErrInvalidAmount)
			}
		}
	}

	v := whole*100 + frac
	if negative {
		v = -v
	}
	return Money(v), nil
}

// Abs returns the absolute value of m.
func (m Money) Abs() Money {
	if m < 0 {
		return -m
	}
	return m
}

// Decimal returns m formatted as a fixed two-decimal string ("1234.56").
func (m Money) Decimal() string {
	neg := m < 0
	v := m
	if neg {
		v = -v
	}
	whole := int64(v) / 100
	frac := int64(v) % 100
	if neg {
		return fmt.Sprintf("-%d.%02d", whole, frac)
	}
	return fmt.Sprintf("%d.%02d", whole, frac)
}

// Format renders m alongside its currency code (e.g. "PHP 1234.56").
func (m Money) Format(currency string) string {
	return currency + " " + m.Decimal()
}

// MarshalJSON serializes Money as a fixed-precision decimal string so that
// downstream consumers (finance tools, spreadsheets) don't have to know about
// the minor-unit storage representation.
func (m Money) MarshalJSON() ([]byte, error) {
	return []byte(`"` + m.Decimal() + `"`), nil
}
