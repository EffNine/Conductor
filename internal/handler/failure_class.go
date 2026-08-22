package handler

import (
	"go.uber.org/zap"

	"github.com/EffNine/conductor/internal/breaker"
	"github.com/EffNine/conductor/internal/failure"
	"github.com/EffNine/conductor/internal/metrics"
)

// classField classifies err into the canonical failure taxonomy for
// structured logging (P4.1). It is observability-only: no component reads
// this field for behavior; P4 policies will consume the same classification.
func classField(err error) zap.Field {
	class, _ := failure.Classify(err)
	return zap.String("failure_class", string(class))
}

// recordStreamBreakerOutcome applies classification-aware breaker accounting
// once a streaming attempt reaches its terminal state (P4.3).
//
//	completed              -> success credit
//	provider error chunk   -> classified outcome (canonical taxonomy)
//	disconnect w/o [DONE]  -> network_error (counts)
//	idle timeout           -> timeout (counts)
//	client disconnect or server shutdown -> ignored: neither reflects
//	provider health
//
// A nil breaker disables accounting.
func recordStreamBreakerOutcome(b *breaker.Breaker, outcome metrics.StreamOutcome, clientDisconnected bool, streamErr error) {
	if b == nil {
		return
	}
	switch outcome {
	case metrics.StreamCompleted:
		b.RecordSuccess()
	case metrics.StreamError:
		if streamErr != nil {
			class, _ := failure.Classify(streamErr)
			b.RecordOutcome(class)
			return
		}
		// Provider closed the channel without [DONE]: truncated stream.
		b.RecordOutcome(failure.ClassNetworkError)
	case metrics.StreamTimeout:
		b.RecordOutcome(failure.ClassTimeout)
	default:
		// metrics.StreamCancelled: the client went away (clientDisconnected)
		// or the server is shutting down — not provider-health signals.
		_ = clientDisconnected
	}
}
