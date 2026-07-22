// Package automode provides runtime automatic model selection for a provider.
// It scores candidate models by reachability, historical cost per token, and
// recent probe latency, then returns the best available catalog entry.
package automode

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/EffNine/conductor/internal/catalog"
	"github.com/EffNine/conductor/internal/config"
	"github.com/EffNine/conductor/internal/database"
	"github.com/EffNine/conductor/internal/health"
	"github.com/EffNine/conductor/internal/provider"
)

//go:generate go run github.com/maxbrunsfeld/counterfeiter/v6 -generate

// CatalogLister abstracts catalog access for testing.
type CatalogLister interface {
	ListAll(ctx context.Context) ([]catalog.Entry, error)
}

// StatusStore abstracts the health model status store.
type StatusStore interface {
	IsReachable(modelID string) (reachable bool, known bool)
	Get(modelID string) *health.ModelStatus
}

// HistoryQuerier abstracts usage/cost history queries.
type HistoryQuerier interface {
	AverageCostPerToken(ctx context.Context, modelID string, since time.Time) (float64, error)
}

// DBHistoryQuerier queries the SQLite usage_records table for historical cost.
type DBHistoryQuerier struct {
	db *database.Database
}

// NewDBHistoryQuerier creates a history querier backed by the usage database.
func NewDBHistoryQuerier(db *database.Database) *DBHistoryQuerier {
	return &DBHistoryQuerier{db: db}
}

// AverageCostPerToken returns the mean USD per total token for a given model ID
// since the provided time. Models with no history return 0 and no error.
func (q *DBHistoryQuerier) AverageCostPerToken(ctx context.Context, modelID string, since time.Time) (float64, error) {
	if q == nil || q.db == nil {
		return 0, nil
	}

	var result struct {
		TotalTokens *int64
		TotalCost   *float64
	}

	err := q.db.DB.WithContext(ctx).
		Model(&database.UsageRecord{}).
		Select("COALESCE(SUM(total_tokens), 0) AS total_tokens, COALESCE(SUM(estimated_cost_usd), 0) AS total_cost").
		Where("model_id = ? AND status_code = ? AND created_at >= ?", modelID, 200, since).
		Scan(&result).Error
	if err != nil {
		return 0, err
	}
	if result.TotalTokens == nil || *result.TotalTokens == 0 {
		return 0, nil
	}
	if result.TotalCost == nil || *result.TotalCost == 0 {
		return 0, nil
	}
	return *result.TotalCost / float64(*result.TotalTokens), nil
}

// Selector picks the best model for a provider at runtime.
type Selector struct {
	catalog  CatalogLister
	status   StatusStore
	history  HistoryQuerier
	registry *provider.Registry
}

// NewSelector creates a selector.
func NewSelector(cat CatalogLister, status StatusStore, history HistoryQuerier, registry *provider.Registry) *Selector {
	return &Selector{
		catalog:  cat,
		status:   status,
		history:  history,
		registry: registry,
	}
}

// ScoreResult carries the chosen entry plus scoring details for observability.
type ScoreResult struct {
	Entry         catalog.Entry `json:"entry"`
	Reachability  float64       `json:"reachability"`
	CostPerToken  float64       `json:"cost_per_token"`
	CostScore     float64       `json:"cost_score"`
	LatencyMs     int64         `json:"latency_ms"`
	LatencyScore  float64       `json:"latency_score"`
	WeightedScore float64       `json:"weighted_score"`
}

// Select returns the best model entry for the configured provider using the
// default task profile.
func (s *Selector) Select(ctx context.Context, cfg *config.AutoModeConfig) (ScoreResult, error) {
	return s.SelectWithTask(ctx, cfg, "")
}

// SelectWithTask classifies the request text, picks a matching task profile,
// and returns the best model entry for the configured provider.
// If cfg is nil or auto mode is disabled, it returns an error.
func (s *Selector) SelectWithTask(ctx context.Context, cfg *config.AutoModeConfig, taskText string) (ScoreResult, error) {
	if cfg == nil || !cfg.Enabled {
		return ScoreResult{}, errors.New("auto mode is not enabled")
	}

	providerName := cfg.Provider
	if providerName == "" {
		providerName = "nvidia_nim"
	}

	if s.registry != nil {
		if _, ok := s.registry.Get(providerName); !ok {
			return ScoreResult{}, fmt.Errorf("auto mode provider '%s' is not registered", providerName)
		}
	}

	entries, err := s.catalog.ListAll(ctx)
	if err != nil {
		return ScoreResult{}, fmt.Errorf("failed to list catalog: %w", err)
	}

	candidates := make([]catalog.Entry, 0, len(entries))
	for _, e := range entries {
		if e.Provider == providerName {
			candidates = append(candidates, e)
		}
	}
	if len(candidates) == 0 {
		return ScoreResult{}, fmt.Errorf("no models found for provider '%s'", providerName)
	}

	profiles := mergeProfiles(cfg.TaskProfiles)
	taskProfile := ClassifyTask(taskText)
	profile, ok := profiles[taskProfile]
	if !ok {
		profile, ok = profiles[string(TaskDefault)]
		if !ok {
			profile = config.AutoModeProfile{Weights: config.AutoModeWeights{
				Reachability: 10.0,
				Cost:         3.0,
				Latency:      2.0,
			}}
		}
	}

	if len(profile.Models) > 0 {
		allowed := make(map[string]struct{}, len(profile.Models))
		for _, m := range profile.Models {
			allowed[m] = struct{}{}
		}
		filtered := make([]catalog.Entry, 0, len(candidates))
		for _, e := range candidates {
			if _, ok := allowed[e.ProviderModelID]; ok {
				filtered = append(filtered, e)
			}
		}
		if len(filtered) > 0 {
			candidates = filtered
		}
	}

	lookback := cfg.Lookback
	if lookback <= 0 {
		lookback = 24 * time.Hour
	}
	since := time.Now().UTC().Add(-lookback)

	weights := profile.Weights
	if weights.Reachability == 0 && weights.Cost == 0 && weights.Latency == 0 {
		weights = cfg.Weights
	}
	if weights.Reachability == 0 && weights.Cost == 0 && weights.Latency == 0 {
		weights = config.AutoModeWeights{
			Reachability: 10.0,
			Cost:         3.0,
			Latency:      2.0,
		}
	}
	wSum := weights.Reachability + weights.Cost + weights.Latency

	costs, latencies := gatherCostLatency(ctx, s, candidates, since)

	var best ScoreResult
	var bestScore float64 = -1
	for _, e := range candidates {
		reachable, _ := s.status.IsReachable(e.ModelID)
		if !reachable {
			continue
		}

		cost := costs[e.ProviderModelID]
		costScore := normalizeInverse(costs, cost)

		lat, latKnown := latencies[e.ModelID]
		latencyScore := 1.0
		if latKnown {
			latencyScore = normalizeInverseInt64(latencies, lat)
		}

		score := (weights.Reachability*1.0 + weights.Cost*costScore + weights.Latency*latencyScore) / wSum
		if score > bestScore {
			bestScore = score
			best = ScoreResult{
				Entry:         e,
				Reachability:  1.0,
				CostPerToken:  cost,
				CostScore:     costScore,
				LatencyMs:     lat,
				LatencyScore:  latencyScore,
				WeightedScore: score,
			}
		}
	}

	if best.Entry.ModelID == "" {
		return ScoreResult{}, fmt.Errorf("no reachable models for provider '%s'", providerName)
	}

	return best, nil
}

// RouterAdapter binds a Selector to an AutoModeConfig so it satisfies the
// router.AutoSelector interface used by the HTTP routing layer.
type RouterAdapter struct {
	selector *Selector
	cfg      *config.AutoModeConfig
}

// NewRouterAdapter creates an adapter for the router.
func NewRouterAdapter(selector *Selector, cfg *config.AutoModeConfig) *RouterAdapter {
	return &RouterAdapter{selector: selector, cfg: cfg}
}

// Select implements router.AutoSelector. providerName is ignored because the
// config already scopes the selection. The task string is the joined request
// messages used for task classification.
func (a *RouterAdapter) Select(ctx context.Context, task string) (string, error) {
	res, err := a.selector.SelectWithTask(ctx, a.cfg, task)
	if err != nil {
		return "", err
	}
	return res.Entry.ProviderModelID, nil
}

func gatherCostLatency(ctx context.Context, s *Selector, candidates []catalog.Entry, since time.Time) (map[string]float64, map[string]int64) {
	costs := make(map[string]float64, len(candidates))
	latencies := make(map[string]int64, len(candidates))
	for _, e := range candidates {
		if s.history != nil {
			c, err := s.history.AverageCostPerToken(ctx, e.ProviderModelID, since)
			if err == nil {
				costs[e.ProviderModelID] = c
			}
		}
		if st := s.status.Get(e.ModelID); st != nil && st.Reachable {
			latencies[e.ModelID] = st.LatencyMs
		}
	}
	return costs, latencies
}

// normalizeInverse returns a score in [0,1] where the smallest value scores 1.
// A zero or single-value set returns 1 for the only value.
func normalizeInverse(values map[string]float64, value float64) float64 {
	var minV, maxV float64
	first := true
	for _, v := range values {
		if first {
			minV, maxV = v, v
			first = false
			continue
		}
		if v < minV {
			minV = v
		}
		if v > maxV {
			maxV = v
		}
	}
	if first {
		return 1.0
	}
	if maxV == minV {
		return 1.0
	}
	return 1.0 - (value-minV)/(maxV-minV)
}

func normalizeInverseInt64(values map[string]int64, value int64) float64 {
	var minV, maxV int64
	first := true
	for _, v := range values {
		if first {
			minV, maxV = v, v
			first = false
			continue
		}
		if v < minV {
			minV = v
		}
		if v > maxV {
			maxV = v
		}
	}
	if first {
		return 1.0
	}
	if maxV == minV {
		return 1.0
	}
	return 1.0 - float64(value-minV)/float64(maxV-minV)
}
