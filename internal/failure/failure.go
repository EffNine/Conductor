// Package failure provides the canonical failure taxonomy for Conductor.
//
// Every failed provider attempt can be classified exactly once into a Class,
// together with policy-facing metadata describing how reliability layers
// (retry engine, circuit breakers, fallback ordering) should treat it.
//
// Classification is descriptive only: nothing in this package changes request
// behavior, and it deliberately depends on nothing but the provider error
// contract. Policies consume classes starting with P4.2.
package failure

import (
	"context"
	"errors"
	"net"
	"net/http"
	"time"

	"github.com/EffNine/conductor/internal/provider"
)

// Class is the canonical classification of a failed provider attempt.
type Class string

const (
	// ClassRateLimited means the provider throttled the request
	// (HTTP 429 or an equivalent provider-specific signal).
	ClassRateLimited Class = "rate_limited"

	// ClassTimeout means the attempt exceeded its deadline without an answer.
	ClassTimeout Class = "timeout"

	// ClassAuthFailed means credentials or permissions were rejected.
	ClassAuthFailed Class = "auth_failed"

	// ClassCapacity means the provider signaled overload or no capacity
	// (HTTP 503 or an overloaded signal such as Anthropic's 529).
	ClassCapacity Class = "capacity"

	// ClassUpstreamError means a generic upstream 5xx failure.
	ClassUpstreamError Class = "upstream_error"

	// ClassNetworkError means a connection-level failure occurred before or
	// without an HTTP answer (DNS, refused, reset, TLS).
	ClassNetworkError Class = "network_error"

	// ClassInvalidRequest means the gateway sent something the provider
	// rejected (4xx semantics, including context-length rejections).
	ClassInvalidRequest Class = "invalid_request"

	// ClassUnknown means the failure is not attributable to any known class.
	ClassUnknown Class = "unknown"
)

// statusOverloaded is the Anthropic-style overloaded signal adopted by
// several gateways; it indicates capacity exhaustion rather than a generic
// upstream fault.
const statusOverloaded = 529

// BreakerImpact describes how a class affects circuit-breaker accounting.
type BreakerImpact string

const (
	// BreakerImpactNone never influences breaker state.
	BreakerImpactNone BreakerImpact = "none"

	// BreakerImpactCount counts toward opening the breaker.
	BreakerImpactCount BreakerImpact = "count"

	// BreakerImpactThrottleOnly records throttling separately and does not
	// count toward opening the breaker: a rate-limited provider is healthy,
	// just busy. P4.3 consumes this distinction.
	BreakerImpactThrottleOnly BreakerImpact = "throttle_only"
)

// Policy carries the classification metadata reliability layers consult.
type Policy struct {
	// Retryable reports whether this class may be retried on the same
	// provider by a future retry engine.
	Retryable bool

	// BreakerImpact describes how the class affects breaker accounting.
	BreakerImpact BreakerImpact

	// SuggestedBackoffSupported reports whether providers may attach a
	// server-advised wait duration to failures of this class.
	SuggestedBackoffSupported bool
}

var policies = map[Class]Policy{
	ClassRateLimited: {
		Retryable:                 true,
		BreakerImpact:             BreakerImpactThrottleOnly,
		SuggestedBackoffSupported: true,
	},
	ClassTimeout:        {Retryable: true, BreakerImpact: BreakerImpactCount},
	ClassAuthFailed:     {BreakerImpact: BreakerImpactNone},
	ClassCapacity:       {Retryable: true, BreakerImpact: BreakerImpactCount, SuggestedBackoffSupported: true},
	ClassUpstreamError:  {Retryable: true, BreakerImpact: BreakerImpactCount},
	ClassNetworkError:   {Retryable: true, BreakerImpact: BreakerImpactCount},
	ClassInvalidRequest: {BreakerImpact: BreakerImpactNone},
	ClassUnknown:        {BreakerImpact: BreakerImpactCount},
}

// PolicyFor returns the metadata for c. Every Class constant has an entry.
func PolicyFor(c Class) Policy {
	return policies[c]
}

// String implements fmt.Stringer.
func (c Class) String() string { return string(c) }

// String implements fmt.Stringer.
func (b BreakerImpact) String() string { return string(b) }

// Classify inspects err and returns its canonical Class plus any
// server-advised backoff carried by the error chain.
//
// A nil error yields (ClassUnknown, 0); callers should only classify failures.
// The classifier never panics and never mutates the error chain.
func Classify(err error) (Class, time.Duration) {
	if err == nil {
		return ClassUnknown, 0
	}

	var pe *provider.ProviderError
	if errors.As(err, &pe) {
		return classifyProviderError(pe), pe.RetryAfter
	}
	if errors.Is(err, context.DeadlineExceeded) || isTimeout(err) {
		return ClassTimeout, 0
	}
	return ClassUnknown, 0
}

// classifyProviderError maps a normalized provider error onto the taxonomy.
// The Type constants set by the adapter boundary take precedence; unknown
// type strings fall back to status-code heuristics so that every current
// error path still produces a Class.
func classifyProviderError(pe *provider.ProviderError) Class {
	switch pe.Type {
	case provider.ErrorTypeRateLimit:
		return ClassRateLimited
	case provider.ErrorTypeAuthentication:
		return ClassAuthFailed
	case provider.ErrorTypeInvalidRequest, provider.ErrorTypeContextLength:
		return ClassInvalidRequest
	case provider.ErrorTypeProviderUnavailable:
		// Transport-layer failures carry a non-nil cause chain from
		// client.Do; split deadlines from connectivity problems.
		if pe.Err != nil {
			if errors.Is(pe.Err, context.DeadlineExceeded) || isTimeout(pe.Err) {
				return ClassTimeout
			}
			return ClassNetworkError
		}
		// Response-derived unavailability (adapters pass a nil cause when
		// the upstream answered with an HTTP error): honor status semantics
		// so overload and gateway timeouts are not misread as network faults.
		switch pe.StatusCode {
		case http.StatusRequestTimeout, http.StatusGatewayTimeout:
			return ClassTimeout
		case http.StatusServiceUnavailable, statusOverloaded:
			return ClassCapacity
		}
		if pe.StatusCode >= 500 {
			return ClassUpstreamError
		}
		return ClassNetworkError
	case provider.ErrorTypeServerError:
		if pe.StatusCode == http.StatusServiceUnavailable || pe.StatusCode == statusOverloaded {
			return ClassCapacity
		}
		return ClassUpstreamError
	default:
		return classifyByStatus(pe.StatusCode, pe.Err)
	}
}

// classifyByStatus applies HTTP semantics when no canonical Type matched.
func classifyByStatus(statusCode int, cause error) Class {
	switch {
	case statusCode == http.StatusTooManyRequests:
		return ClassRateLimited
	case statusCode == http.StatusUnauthorized || statusCode == http.StatusForbidden:
		return ClassAuthFailed
	case statusCode >= 400 && statusCode < 500:
		return ClassInvalidRequest
	case statusCode == http.StatusServiceUnavailable || statusCode == statusOverloaded:
		return ClassCapacity
	case statusCode >= 500:
		return ClassUpstreamError
	case cause != nil && (errors.Is(cause, context.DeadlineExceeded) || isTimeout(cause)):
		return ClassTimeout
	default:
		return ClassUnknown
	}
}

// isTimeout reports whether the error chain contains a net.Error reporting a
// timeout (e.g. *url.Error wrapping http.Client transport timeouts).
func isTimeout(err error) bool {
	var netErr net.Error
	if errors.As(err, &netErr) {
		return netErr.Timeout()
	}
	return false
}
