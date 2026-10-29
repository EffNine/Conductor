package handler

import (
	"sync"
	"time"

	"github.com/EffNine/conductor/internal/cache"
	"github.com/EffNine/conductor/internal/catalog"
	"github.com/EffNine/conductor/internal/config"
	"github.com/EffNine/conductor/internal/database"
	"github.com/EffNine/conductor/internal/health"
	"github.com/EffNine/conductor/internal/metrics"
	"github.com/EffNine/conductor/internal/middleware"
	"github.com/EffNine/conductor/internal/provider"
	"github.com/EffNine/conductor/internal/router"
	"github.com/EffNine/conductor/internal/runtime"
	runtimeadapter "github.com/EffNine/conductor/internal/runtime/adapter"
	"github.com/EffNine/conductor/internal/usage"
	"github.com/gofiber/fiber/v3"
	"go.uber.org/zap"
)

// Handler holds HTTP handlers for the gateway
type Handler struct {
	router            *router.Engine
	registry          *provider.Registry
	catalog           *catalog.Catalog
	usageTracker      *usage.Tracker
	db                *database.Database
	logger            *zap.Logger
	startTime         time.Time
	reloadFn          func() error
	modelProber       *health.ModelProber
	modelStatus       *health.ModelStatusStore
	metrics           *metrics.Collector
	routingEngine     *router.RouterEngine
	autoResolver      *router.AutoResolver
	virtualResolver   *router.VirtualResolver
	decisionPipeline  *router.DecisionPipeline
	cacheEngine       *cache.Engine
	streamIdleTimeout time.Duration
	usageAdapter      *runtimeadapter.UsageToRuntimeAdapter
	breakerAdapter    *runtimeadapter.BreakerToRuntimeAdapter
	executionAdapter  *runtimeadapter.ExecutionToRuntimeAdapter
	runtimeManager    runtime.Manager
	traceStore        router.TraceStore
	attemptStore      *database.AttemptStore
	attemptEmitter    func(database.AttemptRecord)
	cfg               *config.Config
	rankedMu          sync.Mutex
	rankedCache       map[string]rankedCacheEntry
}

// New creates a new Handler
func New(r *router.Engine, reg *provider.Registry, ut *usage.Tracker, logger *zap.Logger, cat *catalog.Catalog, db *database.Database) *Handler {
	return &Handler{
		router:       r,
		registry:     reg,
		catalog:      cat,
		usageTracker: ut,
		db:           db,
		logger:       logger,
		startTime:    time.Now(),
		metrics:      metrics.NewCollector(),
	}
}

// SetReloadFunc sets the optional config reload callback used by PUT /api/config/reload.
func (h *Handler) SetReloadFunc(fn func() error) {
	h.reloadFn = fn
}

// SetConfig stores the application config for the /api/config endpoint.
func (h *Handler) SetConfig(cfg *config.Config) {
	h.cfg = cfg
}

// SetModelStatus wires per-model reachability tracking (probe + reactive updates).
func (h *Handler) SetModelStatus(store *health.ModelStatusStore, prober *health.ModelProber) {
	h.modelStatus = store
	h.modelProber = prober
}

// SetMetrics wires an external metrics collector (for tests or shared collectors).
func (h *Handler) SetMetrics(m *metrics.Collector) {
	if m != nil {
		h.metrics = m
	}
}

// Metrics returns the handler's metrics collector.
func (h *Handler) Metrics() *metrics.Collector {
	return h.metrics
}

// SetAutoSelector wires runtime automatic model selection into the router.
func (h *Handler) SetAutoSelector(s router.AutoSelector) {
	h.router.SetAutoSelector(s)
}

// SetRoutingEngine wires the intelligent routing engine.
func (h *Handler) SetRoutingEngine(re *router.RouterEngine) {
	h.routingEngine = re
}

// SetAutoModelResolver wires the catalog-backed auto model resolver
// (model="auto"). Unlike the routing engine, it is available regardless of
// routing.enabled — the public auto contract is independent of the
// DecisionPipeline / intelligent routing feature.
func (h *Handler) SetAutoModelResolver(r *router.AutoResolver) {
	h.autoResolver = r
}

// SetVirtualResolver wires the catalog-backed virtual model resolver for
// capability-based virtual models (frontier, coding, reasoning, agentic,
// planning, long_horizon, fast, light, vision, auto). It is available
// regardless of routing.enabled — the virtual model contract is independent
// of the DecisionPipeline / intelligent routing feature.
func (h *Handler) SetVirtualResolver(r *router.VirtualResolver) {
	h.virtualResolver = r
}

// SetDecisionPipeline wires the decision pipeline for chat completion routing.
func (h *Handler) SetDecisionPipeline(p *router.DecisionPipeline) {
	h.decisionPipeline = p
}

// SetCacheEngine wires the response cache engine.
func (h *Handler) SetCacheEngine(e *cache.Engine) {
	h.cacheEngine = e
}

// SetStreamIdleTimeout configures the streaming idle timeout. Values <= 0
// disable the timeout entirely. Defaults to 5 minutes when unset.
func (h *Handler) SetStreamIdleTimeout(d time.Duration) {
	h.streamIdleTimeout = d
}

// SetUsageAdapter wires the usage-to-runtime adapter.
func (h *Handler) SetUsageAdapter(a *runtimeadapter.UsageToRuntimeAdapter) {
	h.usageAdapter = a
}

// SetBreakerAdapter wires the breaker-to-runtime adapter.
func (h *Handler) SetBreakerAdapter(a *runtimeadapter.BreakerToRuntimeAdapter) {
	h.breakerAdapter = a
}

// SetExecutionAdapter wires the execution-to-runtime adapter.
func (h *Handler) SetExecutionAdapter(a *runtimeadapter.ExecutionToRuntimeAdapter) {
	h.executionAdapter = a
}

// SetRuntimeManager exposes the runtime manager for dashboard access.
func (h *Handler) SetRuntimeManager(m runtime.Manager) {
	h.runtimeManager = m
}

// Register registers all HTTP routes with scope-aware authorization.
// Authentication is handled globally by middleware.Auth (except /health).
// This method adds authorization (scope) checks per route: 401 for invalid token,
// 403 for valid token lacking required scope. Admin scope implies all.
func (h *Handler) Register(app *fiber.App) {
	// Public (no auth)
	app.Get("/health", h.HandleHealth)

	// OpenAI-compatible endpoints — scoped
	app.Post("/v1/chat/completions", middleware.RequireScope("conductor:execute"), h.HandleChatCompletion)
	app.Post("/v1/embeddings", middleware.RequireScope("conductor:execute"), h.HandleEmbeddings)
	app.Get("/v1/models", middleware.RequireScope("conductor:read"), h.HandleListModels)

	// Dashboard — read vs admin
	app.Get("/api/models", middleware.RequireScope("conductor:read"), h.HandleDashboardModels)
	app.Get("/api/models/status", middleware.RequireScope("conductor:read"), h.HandleModelStatus)
	app.Post("/api/models/reset-status", middleware.RequireScope("conductor:admin"), h.HandleResetModelStatus)
	app.Post("/api/models/force-probe", middleware.RequireScope("conductor:admin"), h.HandleForceProbe)
	app.Get("/api/auto/status", middleware.RequireScope("conductor:read"), h.HandleAutoStatus)
	app.Get("/api/health", middleware.RequireScope("conductor:read"), h.HandleProviderHealth)
	app.Get("/api/providers", middleware.RequireScope("conductor:read"), h.HandleListProviders)
	app.Get("/api/usage", middleware.RequireScope("conductor:read"), h.HandleUsage)
	app.Get("/api/usage/costs", middleware.RequireScope("conductor:read"), h.HandleCosts)
	app.Get("/api/logs", middleware.RequireScope("conductor:read"), h.HandleLogs)
	app.Get("/api/config", middleware.RequireScope("conductor:read"), h.HandleConfig)
	app.Put("/api/config/reload", middleware.RequireScope("conductor:admin"), h.HandleReloadConfig)
	app.Get("/api/metrics", middleware.RequireScope("conductor:read"), h.HandleMetrics)
	app.Get("/api/circuit-breaker", middleware.RequireScope("conductor:read"), h.HandleCircuitBreakerStatus)
	app.Get("/api/routing", middleware.RequireScope("conductor:read"), h.HandleRouting)
	app.Get("/api/routing/traces", middleware.RequireScope("conductor:read"), h.HandleRoutingTraces)
	app.Get("/api/routing/traces/:id", middleware.RequireScope("conductor:read"), h.HandleRoutingTraceByID)
	app.Get("/api/failures", middleware.RequireScope("conductor:read"), h.HandleFailures)
	app.Get("/api/failures/summary", middleware.RequireScope("conductor:read"), h.HandleFailuresSummary)
	app.Get("/api/cache", middleware.RequireScope("conductor:read"), h.HandleCacheStatus)
	app.Get("/api/streams", middleware.RequireScope("conductor:read"), h.HandleStreamStatus)
	app.Get("/api/runtime", middleware.RequireScope("conductor:read"), h.HandleRuntime)
}
