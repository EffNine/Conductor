package handler

import (
	"time"

	"github.com/EffNine/conductor/internal/apitypes"
	"github.com/EffNine/conductor/internal/provider"
	"github.com/EffNine/conductor/internal/router"
	"github.com/EffNine/conductor/internal/usage"
	"github.com/gofiber/fiber/v2"
	"go.uber.org/zap"
)

// trackUsage records usage data
func (h *Handler) trackUsage(requestID, modelID, providerModelID, provider string, usageData *apitypes.Usage, duration time.Duration, statusCode int, isStream bool, err error) {
	if h.usageTracker == nil {
		return
	}

	record := &usage.Record{
		RequestID:       requestID,
		ModelID:         modelID,
		ProviderModelID: providerModelID,
		Provider:        provider,
		Requests:        1,
		DurationMs:      duration.Milliseconds(),
		LatencyMs:       duration.Milliseconds(),
		StatusCode:      statusCode,
		IsStream:        isStream,
		CreatedAt:       time.Now(),
	}

	if usageData != nil {
		record.PromptTokens = usageData.PromptTokens
		record.CompletionTokens = usageData.CompletionTokens
		record.TotalTokens = usageData.TotalTokens
		h.metrics.RecordPromptTokens(usageData.PromptTokens)
		h.metrics.RecordCompletionTokens(usageData.CompletionTokens)
		h.metrics.RecordTotalTokens(usageData.TotalTokens)
	}

	if err != nil {
		errMsg := err.Error()
		record.ErrorMessage = &errMsg
	}

	if h.usageAdapter != nil {
		h.usageAdapter.OnUsageRecord(record)
	}

	h.usageTracker.Record(record)
}

// recordModelResult updates per-model reachability from live chat traffic.
func (h *Handler) recordModelResult(resolved *router.ResolvedRoute, err error, latencyMs int64) {
	if h.modelProber == nil || resolved == nil {
		return
	}
	catalogID := resolved.ProviderName + "/" + resolved.ProviderModelID
	h.modelProber.RecordLiveResult(catalogID, resolved.ProviderName, resolved.ProviderModelID, err, latencyMs)
}

// recordExecutionTelemetry records execution outcome on the runtime adapter.
// retryCount is the number of fallback retries consumed (0 for primary).
// modelID is the provider model ID; empty string records at provider level only.
func (h *Handler) recordExecutionTelemetry(providerName string, modelID string, success bool, retryCount int) {
	if h.executionAdapter == nil {
		return
	}
	h.executionAdapter.OnExecutionFinished(providerName, modelID, success, retryCount)
}

// logRequestComplete logs the completion of a request with timing and outcome.
func (h *Handler) logRequestComplete(correlationID, requestID string, resolved *router.ResolvedRoute, latencyMs int64, statusCode int, isStream bool, err error) {
	fields := []zap.Field{
		zap.String("correlation_id", correlationID),
		zap.String("request_id", requestID),
		zap.Int("status_code", statusCode),
		zap.Int64("latency_ms", latencyMs),
		zap.Bool("stream", isStream),
	}
	if resolved != nil {
		fields = append(fields,
			zap.String("provider", resolved.ProviderName),
			zap.String("provider_model", resolved.ProviderModelID),
			zap.String("model", resolved.ModelID),
		)
	}
	if err != nil {
		fields = append(fields, zap.Error(err))
		h.logger.Warn("request:complete", fields...)
	} else {
		h.logger.Info("request:complete", fields...)
	}
}

// providerErrorResponse returns a normalized error response
func (h *Handler) providerErrorResponse(c *fiber.Ctx, err error) error {
	if providerErr, ok := err.(*provider.ProviderError); ok {
		return c.Status(providerErr.StatusCode).JSON(apitypes.ErrorResponse{
			Error: apitypes.ErrorDetail{
				Message: providerErr.Message,
				Type:    providerErr.Type,
				Code:    providerErr.Type,
			},
		})
	}

	return c.Status(fiber.StatusBadGateway).JSON(apitypes.ErrorResponse{
		Error: apitypes.ErrorDetail{
			Message: "Provider returned an error",
			Type:    "provider_error",
			Code:    "provider_unavailable",
		},
	})
}

// logRoutingDecision logs a routing decision when intelligent routing is active.
func (h *Handler) logRoutingDecision(modelID, providerName string) {
	if h.routingEngine == nil {
		return
	}
	decision := h.routingEngine.GetDecision()
	if decision == nil {
		return
	}
	h.logger.Info("routing_decision",
		zap.String("model", modelID),
		zap.String("selected_provider", decision.SelectedProvider),
		zap.String("selected_model", decision.SelectedProviderID),
		zap.Int64("routing_duration_ms", decision.RoutingDurationMs),
	)
}
