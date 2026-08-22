package handler

import (
	"go.uber.org/zap"

	"github.com/EffNine/conductor/internal/failure"
)

// classField classifies err into the canonical failure taxonomy for
// structured logging (P4.1). It is observability-only: no component reads
// this field for behavior; P4 policies will consume the same classification.
func classField(err error) zap.Field {
	class, _ := failure.Classify(err)
	return zap.String("failure_class", string(class))
}
