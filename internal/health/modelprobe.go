package health

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/novexa/gateway/internal/apitypes"
	"github.com/novexa/gateway/internal/catalog"
	"github.com/novexa/gateway/internal/config"
	"github.com/novexa/gateway/internal/provider"
	"go.uber.org/zap"
)

// ModelProber periodically probes model reachability via minimal chat completions.
type ModelProber struct {
	catalog  *catalog.Catalog
	registry *provider.Registry
	store    *ModelStatusStore
	logger   *zap.Logger
	cfg      config.ModelHealthConfig

	providerFilter map[string]struct{}
	stopCh         chan struct{}
	wg             sync.WaitGroup
	probing        sync.Mutex
}

// NewModelProber creates a background model reachability prober.
func NewModelProber(
	cat *catalog.Catalog,
	registry *provider.Registry,
	store *ModelStatusStore,
	logger *zap.Logger,
	cfg config.ModelHealthConfig,
) *ModelProber {
	if cfg.CheckInterval <= 0 {
		cfg.CheckInterval = 12 * time.Hour
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 60 * time.Second
	}
	if cfg.Concurrency <= 0 {
		cfg.Concurrency = 3
	}

	filter := make(map[string]struct{}, len(cfg.Providers))
	for _, name := range cfg.Providers {
		if name != "" {
			filter[name] = struct{}{}
		}
	}

	return &ModelProber{
		catalog:        cat,
		registry:       registry,
		store:          store,
		logger:         logger,
		cfg:            cfg,
		providerFilter: filter,
		stopCh:         make(chan struct{}),
	}
}

// Start begins periodic probing. Safe to call once.
// Runs an immediate full pass on startup (covers redeploys), then every CheckInterval.
func (p *ModelProber) Start() {
	if !p.cfg.Enabled {
		return
	}
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()

		scope := "all providers"
		if len(p.providerFilter) > 0 {
			names := make([]string, 0, len(p.providerFilter))
			for name := range p.providerFilter {
				names = append(names, name)
			}
			scope = "providers=" + strings.Join(names, ",")
		}
		p.logger.Info("model probe: starting",
			zap.String("scope", scope),
			zap.Duration("interval", p.cfg.CheckInterval),
		)

		// Initial pass on startup/redeploy so /v1/models reflects reachability quickly.
		p.ProbeAll()

		ticker := time.NewTicker(p.cfg.CheckInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				p.ProbeAll()
			case <-p.stopCh:
				return
			}
		}
	}()
}

// Stop halts background probing and waits for the loop to exit.
func (p *ModelProber) Stop() {
	select {
	case <-p.stopCh:
		// already closed
	default:
		close(p.stopCh)
	}
	p.wg.Wait()
}

// ProbeAll lists the catalog and probes each matching model.
// Skips if a previous pass is still running.
func (p *ModelProber) ProbeAll() {
	if !p.probing.TryLock() {
		p.logger.Debug("model probe: previous pass still running, skipping")
		return
	}
	defer p.probing.Unlock()

	entries, err := p.catalog.ListAll(context.Background())
	if err != nil {
		p.logger.Warn("model probe: catalog list failed", zap.Error(err))
		return
	}

	sem := make(chan struct{}, p.cfg.Concurrency)
	var wg sync.WaitGroup
	probed := 0

	for _, e := range entries {
		if !p.shouldProbe(e.Provider) {
			continue
		}
		probed++
		entry := e
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			p.ProbeModel(entry)
		}()
	}
	wg.Wait()
	p.store.MarkFilterReady()
	p.logger.Info("model probe: pass complete", zap.Int("probed", probed), zap.Int("catalog", len(entries)))
}

// ProbeModel sends a minimal chat completion to test reachability.
func (p *ModelProber) ProbeModel(entry catalog.Entry) {
	prov, ok := p.registry.Get(entry.Provider)
	if !ok {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), p.cfg.Timeout)
	defer cancel()

	// Lightweight reachability check: enough tokens for a one-word reply,
	// thinking_budget 0 so reasoning models (Seed-OSS, Nemotron) answer directly
	// without a long thinking phase or empty content from max_tokens: 1.
	maxTokens := 16
	thinkingBudget := 0
	req := &apitypes.ChatCompletionRequest{
		Model: entry.ProviderModelID,
		Messages: []apitypes.Message{
			{Role: "user", Content: "ping"},
		},
		MaxTokens:      &maxTokens,
		ThinkingBudget: &thinkingBudget,
	}

	start := time.Now()
	_, err := prov.ChatCompletion(ctx, req)
	latency := time.Since(start).Milliseconds()

	if err == nil {
		p.store.RecordSuccess(entry.ModelID, entry.Provider, entry.ProviderModelID, latency)
		p.logger.Debug("model reachable",
			zap.String("model", entry.ModelID),
			zap.Int64("latency_ms", latency),
		)
		return
	}

	statusCode, msg := classifyProbeError(err)
	p.logger.Debug("model probe failed",
		zap.String("model", entry.ModelID),
		zap.Int("status_code", statusCode),
		zap.String("error", msg),
	)

	if IsNeutralProbeFailure(statusCode, msg) || IsInconclusiveProbeFailure(statusCode, msg) {
		return
	}
	if IsUnreachableProbeFailure(statusCode, msg) {
		p.store.RecordFailure(entry.ModelID, entry.Provider, entry.ProviderModelID, msg, statusCode)
	}
}

func (p *ModelProber) shouldProbe(providerName string) bool {
	if len(p.providerFilter) == 0 {
		return true
	}
	_, ok := p.providerFilter[providerName]
	return ok
}

func classifyProbeError(err error) (statusCode int, msg string) {
	if err == nil {
		return 0, ""
	}
	msg = err.Error()
	var pe *provider.ProviderError
	if errors.As(err, &pe) {
		return pe.StatusCode, pe.Message
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return 0, "timeout"
	}
	return 0, msg
}

// RecordLiveResult updates status from a real chat completion outcome.
// Call after primary provider attempts so reactive hide works without waiting
// for the next probe cycle.
func (p *ModelProber) RecordLiveResult(modelID, providerName, providerModelID string, err error, latencyMs int64) {
	if p == nil || p.store == nil {
		return
	}
	if !p.shouldProbe(providerName) {
		return
	}
	if err == nil {
		p.store.RecordSuccess(modelID, providerName, providerModelID, latencyMs)
		return
	}
	statusCode, msg := classifyProbeError(err)
	if IsNeutralProbeFailure(statusCode, msg) || IsInconclusiveProbeFailure(statusCode, msg) {
		return
	}
	if IsUnreachableProbeFailure(statusCode, msg) {
		p.store.RecordFailure(modelID, providerName, providerModelID, msg, statusCode)
	}
}
