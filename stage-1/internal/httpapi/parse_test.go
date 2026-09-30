package httpapi

import "testing"

func TestFingerprintIgnoresOrderAndNumberSpelling(t *testing.T) {
	a, _ := decodeObject([]byte(`{"amount": 1000, "note":"x", "n": {"b":1,"a":2}}`))
	b, _ := decodeObject([]byte(`{"n":{"a":2,"b":1},"note":"x","amount":1e3}`))
	c, _ := decodeObject([]byte(`{"n":{"a":2,"b":1},"note":"x","amount":"1000"}`))
	if fingerprint(a) != fingerprint(b) {
		t.Error("equal values fingerprint differently")
	}
	if fingerprint(a) == fingerprint(c) {
		t.Error("number and string collide")
	}
}
