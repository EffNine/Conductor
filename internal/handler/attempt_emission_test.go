package handler

import (
	"net/http"
	"testing"
	"time"

	"github.com/EffNine/conductor/internal/database"
	"github.com/EffNine/conductor/internal/failure"
	"github.com/EffNine/conductor/internal/metrics"
	"github.com/EffNine/conductor/internal/provider"
	"github.com/EffNine/conductor/internal/resilience"
	"github.com/EffNine/conductor/internal/router"
	"go.uber.org/zap"
)

func TestChatPlanSinkEmitsAttemptRecords(t *testing.T) {
	var records []database.AttemptRecord
	h := &Handler{
		logger:         zap.NewNop(),
		metrics:        metrics.NewCollector(),
		attemptEmitter: func(rec database.AttemptRecord) { records = append(records, rec) },
	}
	routes := []*router.ResolvedRoute{
		{ProviderName: "primary", ModelID: "frontier", ProviderModelID: "gpt-4o"},
		{ProviderName: "groq", ModelID: "frontier", ProviderModelID: "llama-3.1-8b-instruct"},
	}
	sink := &chatPlanSink{
		h:             h,
		requestID:     "req-1",
		correlationID: "corr-1",
		mode:          "auto",
		start:         time.Now(),
		routes:        routes,
		usageModelID:  "frontier",
	}

	// Skipped candidate.
	sink.CandidateSkipped(resilience.Candidate{Index: 0, ProviderName: "primary"}, resilience.SkipCircuitBreakerOpen)
	if len(records) != 1 {
		t.Fatalf("skip emission missing")
	}
	if records[0].Outcome != database.AttemptOutcomeSkipped ||
		records[0].SkipReason != string(resilience.SkipCircuitBreakerOpen) ||
		records[0].CandidateIndex != 0 || records[0].Mode != "auto" ||
		records[0].VirtualModel != "frontier" || records[0].RequestID != "req-1" {
		t.Fatalf("skip record wrong: %+v", records[0])
	}

	// Failed candidate with a classifiable provider error (429 → throttle).
	rateErr := provider.NewProviderError("groq", http.StatusTooManyRequests, provider.ErrorTypeRateLimit, "slow down", nil)
	failAttempts := []resilience.Attempt{{RetryWait: 250 * time.Millisecond, HintHonored: true}, {}}
	sink.CandidateFailed(resilience.Candidate{Index: 1, ProviderName: "groq", ProviderModelID: "llama-3.1-8b-instruct"}, rateErr, failAttempts, time.Second)
	if len(records) != 2 {
		t.Fatalf("failure emission missing")
	}
	failed := records[1]
	class, _ := failure.Classify(rateErr)
	if failed.Outcome != database.AttemptOutcomeFailed ||
		failed.FailureClass != string(class) ||
		failed.HTTPStatus != http.StatusTooManyRequests ||
		!failed.RetryAfterHonored || failed.RetryWaitMS != 250 ||
		failed.ProviderModelID != "llama-3.1-8b-instruct" {
		t.Fatalf("failed record wrong: %+v", failed)
	}

	// Succeeded candidate.
	sink.CandidateSucceeded(resilience.Candidate{Index: 1, ProviderName: "groq", ProviderModelID: "llama-3.1-8b-instruct"}, []resilience.Attempt{{}}, time.Second)
	if len(records) != 3 {
		t.Fatalf("success emission missing")
	}
	okRec := records[2]
	if okRec.Outcome != database.AttemptOutcomeSuccess || okRec.HTTPStatus != http.StatusOK {
		t.Fatalf("success record wrong: %+v", okRec)
	}

	// Nil emitter must be a no-op (persistence disabled).
	h.attemptEmitter = nil
	sink.CandidateSkipped(resilience.Candidate{Index: 1, ProviderName: "groq"}, resilience.SkipDeadlineExceeded)
	if len(records) != 3 {
		t.Fatalf("disabled emitter still emitted")
	}
}
