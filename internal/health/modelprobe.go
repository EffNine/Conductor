package health

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/EffNine/conductor/internal/apitypes"
	"github.com/EffNine/conductor/internal/catalog"
	"github.com/EffNine/conductor/internal/config"
	"github.com/EffNine/conductor/internal/provider"
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
	skipProviders  map[string]struct{}
	stopCh         chan struct{}
	wg             sync.WaitGroup
	probing        sync.Mutex
	batcher        *CatalogBatcher
	errorTracker   *ErrorTracker
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
		cfg.CheckInterval = 2 * time.Hour
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 60 * time.Second
	}
	if cfg.Concurrency <= 0 {
		cfg.Concurrency = 3
	}
	if cfg.RetryInterval <= 0 {
		cfg.RetryInterval = 30 * time.Second
	}
	if cfg.CatalogBatchWindow <= 0 {
		cfg.CatalogBatchWindow = 100 * time.Millisecond
	}
	// Zero-value backoff (common in unit tests) → designed defaults with retries on.
	if cfg.Backoff == (config.ProbeBackoffConfig{}) {
		cfg.Backoff = BackoffDefaults
	} else {
		cfg.Backoff = NormalizeBackoff(cfg.Backoff)
	}
	if cfg.ErrorTracking == (config.ErrorTrackingConfig{}) {
		// Leave disabled unless explicitly configured; Load() sets defaults via viper.
		cfg.ErrorTracking.Enabled = false
	} else {
		cfg.ErrorTracking = NormalizeErrorTracking(cfg.ErrorTracking)
	}

	filter := make(map[string]struct{}, len(cfg.Providers))
	for _, name := range cfg.Providers {
		if name != "" {
			filter[name] = struct{}{}
		}
	}

	p := &ModelProber{
		catalog:        cat,
		registry:       registry,
		store:          store,
		logger:         logger,
		cfg:            cfg,
		providerFilter: filter,
		skipProviders:  make(map[string]struct{}),
		stopCh:         make(chan struct{}),
	}
	if store != nil {
		store.Configure(cfg)
	}
	p.batcher = NewCatalogBatcher(cfg.CatalogBatchWindow, func(results []ProbeResult) {
		if store != nil {
			store.ApplyBatch(results)
		}
	})
	p.errorTracker = NewErrorTracker(cfg.ErrorTracking, store)
	return p
}

// ErrorTracker returns the live request error tracker (may be nil-disabled).
func (p *ModelProber) ErrorTracker() *ErrorTracker {
	if p == nil {
		return nil
	}
	return p.errorTracker
}

// SkipProviders marks providers that should not be probed (e.g. loopback ollama
// on Fly). Skipped providers are still marked filter-ready after a pass so their
// catalog entries follow unknown_as_reachable (available-only hides them when
// they were never successfully probed).
func (p *ModelProber) SkipProviders(names ...string) {
	if p.skipProviders == nil {
		p.skipProviders = make(map[string]struct{})
	}
	for _, name := range names {
		if name != "" {
			p.skipProviders[name] = struct{}{}
		}
	}
}

// Start begins periodic probing. Safe to call once.
// Runs an immediate full pass on startup (covers redeploys), then every CheckInterval.
// Also runs a short retry ticker for models in exponential backoff.
func (p *ModelProber) Start() {
	if !p.cfg.Enabled {
		return
	}
	if p.batcher != nil {
		p.batcher.Start()
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
			zap.Bool("backoff", p.cfg.Backoff.Enabled),
			zap.Duration("batch_window", p.cfg.CatalogBatchWindow),
		)

		// Initial pass on startup/redeploy so /v1/models reflects reachability quickly.
		p.ProbeAll()

		fullTicker := time.NewTicker(p.cfg.CheckInterval)
		defer fullTicker.Stop()
		retryTicker := time.NewTicker(p.cfg.RetryInterval)
		defer retryTicker.Stop()

		for {
			select {
			case <-fullTicker.C:
				p.ProbeAll()
			case <-retryTicker.C:
				p.ProbeModelsNeedingRetry()
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
	if p.batcher != nil {
		p.batcher.Stop()
	}
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
	probedProviders := make(map[string]struct{})
	skippedProviders := make(map[string]struct{})

	for _, e := range entries {
		if !p.shouldProbe(e.Provider) {
			if _, skip := p.skipProviders[e.Provider]; skip {
				skippedProviders[e.Provider] = struct{}{}
			}
			continue
		}
		probedProviders[e.Provider] = struct{}{}
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
	for name := range probedProviders {
		p.store.MarkProviderFilterReady(name)
	}
	// Loopback-skipped providers never get real probes; mark them ready so
	// unknown_as_reachable=false hides their unreachable local catalog entries.
	for name := range skippedProviders {
		p.store.MarkProviderFilterReady(name)
	}
	if len(p.providerFilter) == 0 {
		// A pass with no provider filter covered every registered provider
		// (probed or explicitly skipped as unreachable-local).
		p.store.MarkFilterReady()
	}
	p.logger.Info("model probe: pass complete", zap.Int("probed", probed), zap.Int("catalog", len(entries)))
}

// ProbeModelsNeedingRetry re-probes models whose backoff NextProbeTime has elapsed.
func (p *ModelProber) ProbeModelsNeedingRetry() {
	if p.store == nil || !p.cfg.Backoff.Enabled {
		return
	}
	if !p.probing.TryLock() {
		return
	}
	defer p.probing.Unlock()

	due := p.store.ModelsNeedingRetry(time.Now().UTC())
	if len(due) == 0 {
		return
	}

	sem := make(chan struct{}, p.cfg.Concurrency)
	var wg sync.WaitGroup
	for _, st := range due {
		if !p.shouldProbe(st.Provider) {
			continue
		}
		entry := catalog.Entry{
			ModelID:         st.ModelID,
			Provider:        st.Provider,
			ProviderModelID: st.ProviderModelID,
		}
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			p.ProbeModel(entry)
		}()
	}
	wg.Wait()
	p.logger.Debug("model probe: backoff retry pass complete", zap.Int("retried", len(due)))
}

// ProbeModel sends a minimal chat completion to test reachability.
func (p *ModelProber) ProbeModel(entry catalog.Entry) {
	_ = p.probeModel(entry, false)
}

// ForceProbe immediately probes one model, bypassing backoff schedule.
// Returns previous and new status snapshots.
func (p *ModelProber) ForceProbe(modelID string) (prev, next *ModelStatus, err error) {
	if p == nil || p.store == nil {
		return nil, nil, errors.New("model prober unavailable")
	}
	prev = p.store.Get(modelID)
	providerName := ""
	providerModelID := ""
	if prev != nil {
		providerName = prev.Provider
		providerModelID = prev.ProviderModelID
	}
	if providerName == "" || providerModelID == "" {
		// Best-effort parse provider-prefixed catalog ID.
		if i := strings.Index(modelID, "/"); i > 0 {
			providerName = modelID[:i]
			providerModelID = modelID[i+1:]
		}
	}
	if providerName == "" || providerModelID == "" {
		return prev, nil, errors.New("unknown model; cannot resolve provider")
	}
	if !p.shouldProbe(providerName) {
		return prev, nil, errors.New("provider is not in probe scope")
	}

	entry := catalog.Entry{
		ModelID:         modelID,
		Provider:        providerName,
		ProviderModelID: providerModelID,
	}
	// Apply synchronously so the HTTP response reflects the new state.
	result := p.probeModelResult(entry)
	if p.store != nil {
		p.store.ApplyBatch([]ProbeResult{result})
	}
	next = p.store.Get(modelID)
	return prev, next, nil
}

func (p *ModelProber) probeModel(entry catalog.Entry, syncApply bool) ProbeResult {
	result := p.probeModelResult(entry)
	if syncApply {
		if p.store != nil {
			p.store.ApplyBatch([]ProbeResult{result})
		}
		return result
	}
	if p.batcher != nil {
		p.batcher.Submit(result)
	} else if p.store != nil {
		p.store.ApplyBatch([]ProbeResult{result})
	}
	return result
}

func (p *ModelProber) probeModelResult(entry catalog.Entry) ProbeResult {
	result := ProbeResult{
		ModelID:         entry.ModelID,
		Provider:        entry.Provider,
		ProviderModelID: entry.ProviderModelID,
	}

	prov, ok := p.registry.Get(entry.Provider)
	if !ok {
		result.Skip = true
		result.ErrMsg = "provider not registered"
		return result
	}

	ctx, cancel := context.WithTimeout(context.Background(), p.cfg.Timeout)
	defer cancel()

	// Lightweight reachability check: enough tokens for a one-word reply.
	// Do NOT send thinking_budget: most NIM models reject it with 400/422
	// ("Unsupported parameter" / "Extra inputs are not permitted"). That is
	// neither success nor a definitive unreachable signal, so available-only
	// mode (unknown_as_reachable=false) was falsely hiding working models.
	// Reasoning models that honor thinking_budget still succeed here with
	// max_tokens>=16; live traffic can set thinking_budget when needed.
	maxTokens := 16
	req := &apitypes.ChatCompletionRequest{
		Model: entry.ProviderModelID,
		Messages: []apitypes.Message{
			{Role: "user", Content: "ping"},
		},
		MaxTokens: &maxTokens,
	}
	// DeepSeek V4 on NIM defaults to thinking mode; keep probes non-thinking so
	// max_tokens:16 is enough for a one-word reply instead of a reasoning trace.
	if strings.Contains(strings.ToLower(entry.ProviderModelID), "deepseek-v4") {
		req.ReasoningEffort = "none"
	}

	start := time.Now()
	_, err := prov.ChatCompletion(ctx, req)
	latency := time.Since(start).Milliseconds()

	if err == nil {
		result.Success = true
		result.LatencyMs = latency
		p.logger.Debug("model reachable",
			zap.String("model", entry.ModelID),
			zap.Int64("latency_ms", latency),
		)
		return result
	}

	statusCode, msg := classifyProbeError(err)
	result.StatusCode = statusCode
	result.ErrMsg = msg
	p.logger.Debug("model probe failed",
		zap.String("model", entry.ModelID),
		zap.Int("status_code", statusCode),
		zap.String("error", msg),
	)

	if IsNeutralProbeFailure(statusCode, msg) || IsInconclusiveProbeFailure(statusCode, msg) {
		result.Skip = true
		// For recovering models, inconclusive failures still advance backoff
		// via ApplyBatch's recovering branch when Skip is false — mark Skip
		// only for models that are not already down. ApplyBatch handles this
		// when Skip=false and inconclusive; keep Skip=true for first-seen
		// healthy models so timeouts never hide them.
		if st := p.store.Get(entry.ModelID); st != nil && (st.State == StateRecovering || st.State == StateUnhealthy) {
			result.Skip = false
		}
		return result
	}
	if !IsUnreachableProbeFailure(statusCode, msg) {
		// Non-definitive, non-transient (e.g. odd 400) — do not hide.
		result.Skip = true
		return result
	}
	return result
}

func (p *ModelProber) shouldProbe(providerName string) bool {
	if _, skip := p.skipProviders[providerName]; skip {
		return false
	}
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
// for the next probe cycle. Also feeds the error-rate tracker.
func (p *ModelProber) RecordLiveResult(modelID, providerName, providerModelID string, err error, latencyMs int64) {
	if p == nil || p.store == nil {
		return
	}
	if !p.shouldProbe(providerName) {
		return
	}

	success := err == nil
	if p.errorTracker != nil && p.errorTracker.Enabled() {
		p.errorTracker.RecordRequest(modelID, providerName, providerModelID, success)
	}

	if success {
		result := ProbeResult{
			ModelID:         modelID,
			Provider:        providerName,
			ProviderModelID: providerModelID,
			Success:         true,
			LatencyMs:       latencyMs,
			FromLive:        true,
		}
		if p.batcher != nil {
			p.batcher.Submit(result)
		} else {
			p.store.ApplyBatch([]ProbeResult{result})
		}
		return
	}
	statusCode, msg := classifyProbeError(err)
	if IsNeutralProbeFailure(statusCode, msg) || IsInconclusiveProbeFailure(statusCode, msg) {
		return
	}
	if IsUnreachableProbeFailure(statusCode, msg) {
		result := ProbeResult{
			ModelID:         modelID,
			Provider:        providerName,
			ProviderModelID: providerModelID,
			Success:         false,
			ErrMsg:          msg,
			StatusCode:      statusCode,
			FromLive:        true,
		}
		if p.batcher != nil {
			p.batcher.Submit(result)
		} else {
			p.store.ApplyBatch([]ProbeResult{result})
		}
	}
}
