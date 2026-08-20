package router_test

import (
	"context"
	"testing"
	"time"

	"github.com/EffNine/conductor/internal/apitypes"
	"github.com/EffNine/conductor/internal/router"
	"github.com/EffNine/conductor/internal/runtime"
)

func TestDecisionContextLifecycle(t *testing.T) {
	req := &apitypes.ChatCompletionRequest{
		Model:    "gpt-4o",
		Messages: []apitypes.Message{{Role: "user", Content: "hello"}},
	}

	dc := router.NewDecisionContext(
		req,
		runtime.RuntimeSnapshot{},
		router.ConfigSnapshot{},
		router.TaskMetadata{ModelID: "gpt-4o"},
		router.Environment{},
		nil,
		nil,
	)

	// Context() is live after creation.
	if err := dc.Context().Err(); err != nil {
		t.Fatalf("context should be live after NewDecisionContext, got %v", err)
	}
	if dc.ID() == "" {
		t.Fatal("expected decision ID to be set")
	}

	dc.Close()

	// Context() is cancelled after Close().
	if err := dc.Context().Err(); err != context.Canceled {
		t.Fatalf("context should be cancelled after Close, got %v", err)
	}

	// Close() is safe more than once.
	dc.Close()

	// Lifecycle guarantees: Context must not be used after Close (documented);
	// the context reports cancellation so downstream work fails-fast.
	if deadline, ok := dc.Context().Deadline(); !ok {
		t.Fatal("decision context should have a deadline")
	} else if time.Until(deadline) <= 0 {
		t.Fatalf("decision context deadline should be in the future, got %v", deadline)
	}
}
