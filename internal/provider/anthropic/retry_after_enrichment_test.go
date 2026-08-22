package anthropic

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/EffNine/conductor/internal/failure"
)

func TestMapErrorCapturesRetryAfter(t *testing.T) {
	rec := httptest.NewRecorder()
	rec.Header().Set("Retry-After", "8")
	rec.Code = http.StatusTooManyRequests
	rec.Body.WriteString(`{"error":{"type":"rate_limit_error","message":"Number of requests too high"}}`)
	resp := rec.Result()

	pe := MapError("anthropic", resp)
	if pe == nil {
		t.Fatal("MapError returned nil")
	}
	if pe.RetryAfter != 8*time.Second {
		t.Fatalf("RetryAfter = %v, want 8s", pe.RetryAfter)
	}

	class, hint := failure.Classify(pe)
	if class != failure.ClassRateLimited || hint != 8*time.Second {
		t.Fatalf("Classify = (%q, %v), want (rate_limited, 8s)", class, hint)
	}
}
