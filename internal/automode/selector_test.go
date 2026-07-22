package automode

import (
	"context"
	"testing"
	"time"

	"github.com/novexa/gateway/internal/catalog"
	"github.com/novexa/gateway/internal/config"
	"github.com/novexa/gateway/internal/health"
)

type fakeCatalog struct {
	entries []catalog.Entry
}

func (f *fakeCatalog) ListAll(_ context.Context) ([]catalog.Entry, error) {
	return append([]catalog.Entry(nil), f.entries...), nil
}

type fakeStatus struct {
	reachable map[string]bool
	latencies map[string]int64
}

func (f *fakeStatus) IsReachable(modelID string) (bool, bool) {
	r, ok := f.reachable[modelID]
	return r, ok
}

func (f *fakeStatus) Get(modelID string) *health.ModelStatus {
	if lat, ok := f.latencies[modelID]; ok {
		return &health.ModelStatus{ModelID: modelID, Reachable: f.reachable[modelID], LatencyMs: lat}
	}
	return nil
}

type fakeHistory struct {
	costs map[string]float64
}

func (f *fakeHistory) AverageCostPerToken(_ context.Context, modelID string, _ time.Time) (float64, error) {
	if c, ok := f.costs[modelID]; ok {
		return c, nil
	}
	return 0, nil
}

func TestSelectReturnsBestReachableModel(t *testing.T) {
	entries := []catalog.Entry{
		{ModelID: "nvidia_nim/cheap", Provider: "nvidia_nim", ProviderModelID: "cheap"},
		{ModelID: "nvidia_nim/fast", Provider: "nvidia_nim", ProviderModelID: "fast"},
		{ModelID: "nvidia_nim/dead", Provider: "nvidia_nim", ProviderModelID: "dead"},
	}
	status := &fakeStatus{
		reachable: map[string]bool{"nvidia_nim/cheap": true, "nvidia_nim/fast": true, "nvidia_nim/dead": false},
		latencies: map[string]int64{"nvidia_nim/cheap": 1000, "nvidia_nim/fast": 200},
	}
	history := &fakeHistory{costs: map[string]float64{
		"cheap": 0.0005,
		"fast":  0.0005,
	}}

	s := NewSelector(&fakeCatalog{entries: entries}, status, history, nil)
	cfg := &config.AutoModeConfig{
		Enabled:  true,
		Provider: "nvidia_nim",
		Weights: config.AutoModeWeights{
			Reachability: 10.0,
			Cost:         0.0,
			Latency:      2.0,
		},
	}

	res, err := s.Select(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if res.Entry.ProviderModelID != "fast" {
		t.Fatalf("selected %q, want fast (lower latency + reachable)", res.Entry.ProviderModelID)
	}
}

func TestSelectReturnsErrorWhenNoReachableModels(t *testing.T) {
	entries := []catalog.Entry{
		{ModelID: "nvidia_nim/dead", Provider: "nvidia_nim", ProviderModelID: "dead"},
	}
	status := &fakeStatus{
		reachable: map[string]bool{"nvidia_nim/dead": false},
	}
	s := NewSelector(&fakeCatalog{entries: entries}, status, &fakeHistory{}, nil)
	cfg := &config.AutoModeConfig{Enabled: true, Provider: "nvidia_nim"}

	_, err := s.Select(context.Background(), cfg)
	if err == nil {
		t.Fatal("expected error when no models are reachable")
	}
}

func TestSelectReturnsErrorWhenAutoDisabled(t *testing.T) {
	s := NewSelector(&fakeCatalog{}, &fakeStatus{}, &fakeHistory{}, nil)
	cfg := &config.AutoModeConfig{Enabled: false}

	_, err := s.Select(context.Background(), cfg)
	if err == nil {
		t.Fatal("expected error when auto mode disabled")
	}
}

func TestSelectPrefersHistoryForCost(t *testing.T) {
	entries := []catalog.Entry{
		{ModelID: "nvidia_nim/a", Provider: "nvidia_nim", ProviderModelID: "a"},
		{ModelID: "nvidia_nim/b", Provider: "nvidia_nim", ProviderModelID: "b"},
	}
	status := &fakeStatus{
		reachable: map[string]bool{"nvidia_nim/a": true, "nvidia_nim/b": true},
		latencies: map[string]int64{"nvidia_nim/a": 100, "nvidia_nim/b": 100},
	}
	history := &fakeHistory{costs: map[string]float64{
		"a": 0.001,
		"b": 0.0001,
	}}

	s := NewSelector(&fakeCatalog{entries: entries}, status, history, nil)
	cfg := &config.AutoModeConfig{
		Enabled:  true,
		Provider: "nvidia_nim",
		Weights:  config.AutoModeWeights{Reachability: 1.0, Cost: 10.0, Latency: 0.0},
	}

	res, err := s.Select(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if res.Entry.ProviderModelID != "b" {
		t.Fatalf("selected %q, want b (cheaper)", res.Entry.ProviderModelID)
	}
}

func TestSelectWithTaskRestrictsToProfileModels(t *testing.T) {
	entries := []catalog.Entry{
		{ModelID: "nvidia_nim/elite", Provider: "nvidia_nim", ProviderModelID: "elite"},
		{ModelID: "nvidia_nim/fast", Provider: "nvidia_nim", ProviderModelID: "fast"},
	}
	status := &fakeStatus{
		reachable: map[string]bool{"nvidia_nim/elite": true, "nvidia_nim/fast": true},
		latencies: map[string]int64{"nvidia_nim/elite": 500, "nvidia_nim/fast": 100},
	}
	profiles := map[string]config.AutoModeProfile{
		"elite": {
			Models:  []string{"elite"},
			Weights: config.AutoModeWeights{Reachability: 1.0, Cost: 0.0, Latency: 0.0},
		},
	}

	s := NewSelector(&fakeCatalog{entries: entries}, status, &fakeHistory{}, nil)
	cfg := &config.AutoModeConfig{
		Enabled:      true,
		Provider:     "nvidia_nim",
		TaskProfiles: profiles,
	}

	res, err := s.SelectWithTask(context.Background(), cfg, "architect and implement a complex end-to-end distributed system with reasoning")
	if err != nil {
		t.Fatalf("SelectWithTask: %v", err)
	}
	if res.Entry.ProviderModelID != "elite" {
		t.Fatalf("selected %q, want elite (task profile restricts candidates)", res.Entry.ProviderModelID)
	}
}

func TestSelectWithTaskFallsBackToDefault(t *testing.T) {
	entries := []catalog.Entry{
		{ModelID: "nvidia_nim/fast", Provider: "nvidia_nim", ProviderModelID: "fast"},
	}
	status := &fakeStatus{
		reachable: map[string]bool{"nvidia_nim/fast": true},
		latencies: map[string]int64{"nvidia_nim/fast": 100},
	}

	s := NewSelector(&fakeCatalog{entries: entries}, status, &fakeHistory{}, nil)
	cfg := &config.AutoModeConfig{
		Enabled:  true,
		Provider: "nvidia_nim",
		Weights:  config.AutoModeWeights{Reachability: 1.0, Cost: 0.0, Latency: 0.0},
	}

	res, err := s.SelectWithTask(context.Background(), cfg, "hi there")
	if err != nil {
		t.Fatalf("SelectWithTask: %v", err)
	}
	if res.Entry.ProviderModelID != "fast" {
		t.Fatalf("selected %q, want fast (default profile)", res.Entry.ProviderModelID)
	}
}
