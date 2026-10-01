package store

import (
	"encoding/json"
	"time"
)

// instantLayout prints microsecond precision, dropping trailing zeros, with
// a numeric UTC offset ("+00:00").
const instantLayout = "2006-01-02T15:04:05.999999-07:00"

// Instant is a point in time that prints as an RFC 3339 string with an
// offset. A value parsed from text keeps that text, so a supplied
// timestamp is echoed back exactly as given.
type Instant struct {
	T   time.Time
	raw string
}

// NewInstant wraps a generated time, truncated to the microsecond.
func NewInstant(t time.Time) Instant {
	return Instant{T: t.UTC().Truncate(time.Microsecond)}
}

// ParseInstant parses an RFC 3339 instant that carries an offset (or Z).
func ParseInstant(s string) (Instant, error) {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return Instant{}, err
	}
	return Instant{T: t.UTC(), raw: s}, nil
}

func (i Instant) String() string {
	if i.raw != "" {
		return i.raw
	}
	return i.T.UTC().Format(instantLayout)
}

// MarshalJSON implements json.Marshaler.
func (i Instant) MarshalJSON() ([]byte, error) { return json.Marshal(i.String()) }

// UnmarshalJSON implements json.Unmarshaler.
func (i *Instant) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	parsed, err := ParseInstant(s)
	if err != nil {
		return err
	}
	*i = parsed
	return nil
}

// IsZero reports whether the instant was never set.
func (i Instant) IsZero() bool { return i.T.IsZero() }
