// Package money parses integral minor-unit amounts from JSON numbers.
package money

import (
	"math/big"
	"strings"
)

// MaxSafe is the largest magnitude any balance may reach (2^53).
const MaxSafe = int64(1) << 53

// MaxAmount is the largest amount accepted on a single request.
const MaxAmount = 1_000_000_000

// ParseIntegral parses a JSON number literal whose value is a whole number,
// so "1000", "1000.0" and "1e3" all yield 1000. It reports false for
// fractions, values beyond ±2^53 and literals that are not numbers.
func ParseIntegral(s string) (int64, bool) {
	if s == "" || len(s) > 64 {
		return 0, false
	}
	if i := strings.IndexAny(s, "eE"); i >= 0 && len(s)-i > 6 {
		return 0, false
	}
	r, ok := new(big.Rat).SetString(s)
	if !ok || !r.IsInt() {
		return 0, false
	}
	n := r.Num()
	if !n.IsInt64() {
		return 0, false
	}
	v := n.Int64()
	if v > MaxSafe || v < -MaxSafe {
		return 0, false
	}
	return v, true
}

// Canonical returns a normalised text form of a JSON number so that equal
// values compare equal regardless of how they were written.
func Canonical(s string) string {
	if i := strings.IndexAny(s, "eE"); (i >= 0 && len(s)-i > 6) || len(s) > 64 {
		return s
	}
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		return s
	}
	return r.RatString()
}
