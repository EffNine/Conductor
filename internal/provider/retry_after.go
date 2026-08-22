package provider

import (
	"net/http"
	"strconv"
	"time"
)

// ParseRetryAfter parses the value of a Retry-After response header.
//
// Both RFC 9110 §10.2.3 formats are supported:
//   - delay-seconds: a non-negative decimal integer number of seconds
//   - HTTP-date: an absolute timestamp after which a retry may be attempted
//
// It returns 0 for empty, malformed, negative, overflowing, or past-dated
// values. Callers treat 0 as "no hint provided". The parsed duration is
// advisory metadata only; no Conductor component acts on it today.
func ParseRetryAfter(header string, now time.Time) time.Duration {
	if header == "" {
		return 0
	}
	if secs, err := strconv.Atoi(header); err == nil {
		if secs <= 0 {
			return 0
		}
		d := time.Duration(secs) * time.Second
		if d < 0 {
			// Absurdly large values overflow to negative; discard them.
			return 0
		}
		return d
	}
	if when, err := http.ParseTime(header); err == nil {
		d := when.Sub(now)
		if d <= 0 {
			return 0
		}
		return d
	}
	return 0
}
