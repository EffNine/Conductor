package health_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/EffNine/conductor/internal/apitypes"
	"github.com/EffNine/conductor/internal/catalog"
	"github.com/EffNine/conductor/internal/config"
	"github.com/EffNine/conductor/internal/health"
	"github.com/EffNine/conductor/internal/provider"
	"go.uber.org/zap"
)

func TestProbeFailureRecoveryAndForceProbe(t *testing.T) {
	var failBad atomic.Bool
	failBad.Store(true)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/models":
			_ = json.NewEncoder(w).Encode(apitypes.ModelList{
				Object: "list",
				Data: []apitypes.ModelInfo{
					{ID: "good/model", Object: "model", OwnedBy: "nvidia"},
					{ID: "flaky/model", Object: "model", OwnedBy: "nvidia"},
				},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/chat/completions":
			var req apitypes.ChatCompletionRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			if req.Model == "flaky/model" && failBad.Load() {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"error":{"message":"model not found"}}`))
				return
			}
			_ = json.NewEncoder(w).Encode(apitypes.ChatCompletionResponse{
				ID:      "chatcmpl-1",
				Object:  "chat.completion",
				Choices: []apitypes.Choice{{Index: 0, Message: &apitypes.Message{Role: "assistant", Content: "ok"}}},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	reg := provider.NewRegistry()
	reg.Register(newProbeTestProvider("nvidia_nim", srv.URL+"/v1"))

	store := health.NewModelStatusStore(1, true)
	store.SetBackoff(config.ProbeBackoffConfig{
		Enabled: true, InitialDelay: time.Hour, MaxDelay: 12 * time.Hour, Multiplier: 3.5, JitterFraction: 0.01,
	})
	cat := catalog.New(reg, nil)
	cat.SetReachabilityFilter(store, true)

	prober := health.NewModelProber(cat, reg, store, zap.NewNop(), config.ModelHealthConfig{
		Enabled:            true,
		HideUnreachable:    true,
		Timeout:            2 * time.Second,
		Concurrency:        2,
		UnhealthyThreshold: 1,
		Providers:          []string{"nvidia_nim"},
		UnknownAsReachable: true,
		Backoff: config.ProbeBackoffConfig{
			Enabled: true, InitialDelay: time.Hour, MaxDelay: 12 * time.Hour, Multiplier: 3.5, JitterFraction: 0.01,
		},
	})

	prober.ProbeAll()

	st := store.Get("nvidia_nim/flaky/model")
	if st == nil || st.State != health.StateRecovering {
		t.Fatalf("expected recovering after failure, got %+v", st)
	}
	if store.ShouldAdvertise("nvidia_nim/flaky/model", "nvidia_nim") {
		t.Fatal("recovering model must be hidden")
	}

	// Provider recovers; force-probe bypasses long backoff.
	failBad.Store(false)
	prev, next, err := prober.ForceProbe("nvidia_nim/flaky/model")
	if err != nil {
		t.Fatalf("ForceProbe: %v", err)
	}
	if prev == nil || prev.State != health.StateRecovering {
		t.Fatalf("previous_state=%v", prev)
	}
	if next == nil || next.State != health.StateHealthy {
		t.Fatalf("new_state=%v", next)
	}
	if !store.ShouldAdvertise("nvidia_nim/flaky/model", "nvidia_nim") {
		t.Fatal("recovered model must be advertised")
	}
}

func TestBackoffRetryPassProbesDueModels(t *testing.T) {
	var fail atomic.Bool
	fail.Store(true)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/models":
			_ = json.NewEncoder(w).Encode(apitypes.ModelList{
				Object: "list",
				Data:   []apitypes.ModelInfo{{ID: "x", Object: "model"}},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/chat/completions":
			if fail.Load() {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"error":{"message":"model not found"}}`))
				return
			}
			_ = json.NewEncoder(w).Encode(apitypes.ChatCompletionResponse{
				ID: "1", Object: "chat.completion",
				Choices: []apitypes.Choice{{Index: 0, Message: &apitypes.Message{Role: "assistant", Content: "ok"}}},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	reg := provider.NewRegistry()
	reg.Register(newProbeTestProvider("openai", srv.URL+"/v1"))
	store := health.NewModelStatusStore(1, true)
	cat := catalog.New(reg, nil)
	prober := health.NewModelProber(cat, reg, store, zap.NewNop(), config.ModelHealthConfig{
		Enabled: true, Timeout: 2 * time.Second, Concurrency: 1, Providers: []string{"openai"},
		Backoff: config.ProbeBackoffConfig{
			Enabled: true, InitialDelay: time.Millisecond, MaxDelay: time.Second, Multiplier: 2, JitterFraction: 0.01,
		},
	})

	prober.ProbeAll()
	if st := store.Get("openai/x"); st == nil || st.State != health.StateRecovering {
		t.Fatalf("expected recovering, got %+v", st)
	}

	fail.Store(false)
	time.Sleep(5 * time.Millisecond)
	prober.ProbeModelsNeedingRetry()
	if st := store.Get("openai/x"); st == nil || st.State != health.StateHealthy {
		t.Fatalf("expected healthy after retry, got %+v", st)
	}
}
