package gemini

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/EffNine/conductor/internal/failure"
)

func TestMapErrorCapturesRetryAfter(t *testing.T) {
	rec := httptest.NewRecorder()
	rec.Header().Set("Retry-After", "12")
	rec.Code = http.StatusTooManyRequests
	rec.Body.WriteString(`{"error":{"code":429,"message":"Resource exhausted","status":"RESOURCE_EXHAUSTED"}}`)
	resp := rec.Result()

	pe := MapError("gemini", resp)
	if pe == nil {
		t.Fatal("MapError returned nil")
	}
	if pe.RetryAfter != 12*time.Second {
		t.Fatalf("RetryAfter = %v, want 12s", pe.RetryAfter)
	}

	class, hint := failure.Classify(pe)
	if class != failure.ClassRateLimited || hint != 12*time.Second {
		t.Fatalf("Classify = (%q, %v), want (rate_limited, 12s)", class, hint)
	}
}
