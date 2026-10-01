package money

import "testing"

func TestParseIntegral(t *testing.T) {
	good := map[string]int64{"1000": 1000, "1000.0": 1000, "1e3": 1000, "-5": -5, "0": 0}
	for in, want := range good {
		if got, ok := ParseIntegral(in); !ok || got != want {
			t.Errorf("ParseIntegral(%q) = %d,%v", in, got, ok)
		}
	}
	for _, in := range []string{"1.5", "1e-1", "1e999999", "99999999999999999999", ""} {
		if _, ok := ParseIntegral(in); ok {
			t.Errorf("ParseIntegral(%q) accepted", in)
		}
	}
}
