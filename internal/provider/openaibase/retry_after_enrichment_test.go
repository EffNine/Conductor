package openaibase

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/EffNine/conductor/internal/failure"
)

// TestHandleErrorResponseCapturesRetryAfter verifies the adapter boundary
// attaches the server-advised backoff hint to every error path that sees the
// upstream HTTP response.
func TestHandleErrorResponseCapturesRetryAfter(t *testing.T) {
	b := &Base{name: "test"}

	tests := []struct {
		name string
		body string
	}{
		{"openai envelope", `{"error":{"message":"Rate limit reached","type":"rate_limit_error"}}`},
		{"non-json body", `plain text overload`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			rec.Header().Set("Retry-After", "17")
			rec.Code = http.StatusTooManyRequests
			rec.Body.WriteString(tt.body)
			resp := rec.Result()

			pe := b.handleErrorResponse(resp)
			if pe == nil {
				t.Fatal("handleErrorResponse returned nil")
			}
			if pe.StatusCode != http.StatusTooManyRequests {
				t.Fatalf("status = %d, want 429", pe.StatusCode)
			}
			if pe.RetryAfter != 17*time.Second {
				t.Fatalf("RetryAfter = %v, want 17s", pe.RetryAfter)
			}

			class, hint := failure.Classify(pe)
			if class != failure.ClassRateLimited || hint != 17*time.Second {
				t.Fatalf("Classify = (%q, %v), want (rate_limited, 17s)", class, hint)
			}
		})
	}

	t.Run("no header means no hint", func(t *testing.T) {
		rec := httptest.NewRecorder()
		rec.Code = http.StatusInternalServerError
		rec.Body.WriteString(`{"error":{"message":"boom","type":"server_error"}}`)

		pe := b.handleErrorResponse(rec.Result())
		if pe == nil {
			t.Fatal("handleErrorResponse returned nil")
		}
		if pe.RetryAfter != 0 {
			t.Fatalf("RetryAfter = %v, want 0", pe.RetryAfter)
		}
	})
}
