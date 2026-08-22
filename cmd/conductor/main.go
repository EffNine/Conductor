package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/EffNine/conductor/internal/agent"
	"github.com/EffNine/conductor/internal/auth"
	"github.com/EffNine/conductor/internal/automode"
	"github.com/EffNine/conductor/internal/breaker"
	"github.com/EffNine/conductor/internal/cache"
	"github.com/EffNine/conductor/internal/catalog"
	"github.com/EffNine/conductor/internal/config"
	"github.com/EffNine/conductor/internal/coordinator"
	"github.com/EffNine/conductor/internal/database"
	"github.com/EffNine/conductor/internal/eventbus"
	"github.com/EffNine/conductor/internal/handler"
	"github.com/EffNine/conductor/internal/health"
	"github.com/EffNine/conductor/internal/middleware"
	"github.com/EffNine/conductor/internal/provider"
	"github.com/EffNine/conductor/internal/provider/agnesai"
	"github.com/EffNine/conductor/internal/provider/anthropic"
	"github.com/EffNine/conductor/internal/provider/cerebras"
	"github.com/EffNine/conductor/internal/provider/cloudflare"
	"github.com/EffNine/conductor/internal/provider/deepseek"
	"github.com/EffNine/conductor/internal/provider/gemini"
	"github.com/EffNine/conductor/internal/provider/groq"
	"github.com/EffNine/conductor/internal/provider/kilocode"
	"github.com/EffNine/conductor/internal/provider/lmstudio"
	"github.com/EffNine/conductor/internal/provider/mistral"
	"github.com/EffNine/conductor/internal/provider/nousportal"
	"github.com/EffNine/conductor/internal/provider/nvidianim"
	"github.com/EffNine/conductor/internal/provider/ollama"
	"github.com/EffNine/conductor/internal/provider/openai"
	"github.com/EffNine/conductor/internal/provider/opencode"
	"github.com/EffNine/conductor/internal/provider/openrouter"
	"github.com/EffNine/conductor/internal/provider/requesty"
	"github.com/EffNine/conductor/internal/provider/xai"
	"github.com/EffNine/conductor/internal/provider/zai"
	"github.com/EffNine/conductor/internal/router"
	"github.com/EffNine/conductor/internal/runtime"
	adapter "github.com/EffNine/conductor/internal/runtime/adapter"
	"github.com/EffNine/conductor/internal/task"
	toolregistry "github.com/EffNine/conductor/internal/tool"
	toolfs "github.com/EffNine/conductor/internal/tool/fs"
	toolgit "github.com/EffNine/conductor/internal/tool/git"
	toolshell "github.com/EffNine/conductor/internal/tool/shell"
	"github.com/EffNine/conductor/internal/usage"
	"github.com/EffNine/conductor/internal/worker"
	"github.com/gofiber/fiber/v2"
	"go.uber.org/zap"
)

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "gen-key", "generate-api-key":
			key, err := auth.Generate()
			if err != nil {
				log.Fatalf("Failed to generate API key: %v", err)
			}
			fmt.Println(key)
			return
		case "help", "-h", "--help":
			printUsage()
			return
		}
	}

	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}

	// Initialize logger
	logger, err := initLogger(cfg)
	if err != nil {
		log.Fatalf("Failed to initialize logger: %v", err)
	}
	defer func() { _ = logger.Sync() }()

	if cfg.APIKeyJustGenerated {
		keyPath := config.APIKeyFilePath(cfg)
		logger.Warn("Generated new gateway API key; read it from the saved file and set CONDUCTOR_API_KEY in production",
			zap.String("path", keyPath),
		)
	}

	logger.Info("Starting Conductor",
		zap.Int("port", cfg.Server.Port),
		zap.String("log_level", cfg.Logging.Level),
	)

	// Initialize database
	db, dbErr := database.Connect(&cfg.Database)
	if dbErr != nil {
		logger.Fatal("Failed to connect to database", zap.Error(dbErr))
	}
	// Ensure the SQLite handle is released on exit: closes WAL cleanly so
	// the -wal/-shm files do not linger and the next start finds a
	// consistent database.
	defer func() {
		if closeErr := db.Close(); closeErr != nil {
			logger.Warn("database close failed during shutdown", zap.Error(closeErr))
		}
	}()

	if migrateErr := db.Migrate(); migrateErr != nil {
		logger.Fatal("Failed to run database migrations", zap.Error(migrateErr))
	}
	if migrateErr := task.MigrateTasks(db.DB); migrateErr != nil {
		logger.Fatal("Failed to run task migrations", zap.Error(migrateErr))
	}
	logger.Info("Database connected and migrated")

	// Initialize auth service
	authService := auth.NewService(cfg.APIKey)

	// Initialize provider registry
	registry := provider.NewRegistry()

	// Initialize event bus for cross-subsystem communication
	eventBus := eventbus.NewEventBus()

	// Register providers
	registerProviders(cfg, registry, logger)

	// Log registered providers
	logger.Info("Registered providers", zap.Strings("providers", registry.Names()))

	// Publish provider registration events
	for _, p := range registry.All() {
		eventBus.PublishSync(context.Background(), eventbus.Event{
			Type:    eventbus.ProviderRegistered,
			Payload: p.Name(),
		})
	}

	// Initialize router
	routerEngine, err := router.NewEngine(cfg, registry)
	if err != nil {
		logger.Fatal("Failed to initialize router", zap.Error(err))
	}

	// Initialize runtime store and manager
	runtimeStore := runtime.NewRuntimeStore(eventBus)
	runtimeManager := runtime.NewManager(runtimeStore)

	// Register a runtime instance for each provider
	for _, p := range registry.All() {
		r := runtime.NewProviderRuntime(p.Name(), p)
		if err := runtimeStore.Register(r); err != nil {
			logger.Warn("failed to register runtime for provider",
				zap.String("provider", p.Name()), zap.Error(err))
		}
	}
	logger.Info("Runtime store initialized", zap.Int("providers", runtimeManager.Count()))

	// Initialize model catalog
	modelCatalog := catalog.New(registry, catalog.StaticFromConfig(cfg))
	modelCatalog.SetDisplayNames(cfg.DisplayNames)
	modelCatalog.SetCuratedOnly(cfg.Catalog.CuratedOnly)
	if cfg.Catalog.CuratedOnly {
		logger.Info("Catalog curated_only enabled; providers with models use allowlists, others stay dynamic")
	}

	// Initialize cost estimator + usage tracker
	estimator := usage.NewEstimator(registry, usage.ManualRatesFromConfig(cfg))
	usageTracker := usage.NewTracker(db, estimator, logger)

	// Initialize health monitor
	healthMonitor := health.NewMonitor(registry, logger, cfg.Health.CheckInterval, cfg.Health.Timeout)
	healthMonitor.Start()
	defer healthMonitor.Stop()

	// Per-model reachability (especially NVIDIA NIM free vs unreachable endpoints)
	modelStatus := health.NewModelStatusStore(cfg.Health.Models.UnhealthyThreshold, cfg.Health.Models.UnknownAsReachable)
	modelStatus.Configure(cfg.Health.Models)
	modelStatus.SetStrictHealthy(cfg.Health.Models.StrictHealthy)
	if persist := health.NewDBStatusPersistence(db); persist != nil {
		modelStatus.SetPersistence(persist)
		if n, err := health.RestoreModelStatusStore(modelStatus, db); err != nil {
			logger.Warn("model status: failed to restore from database", zap.Error(err))
		} else if n > 0 || modelStatus.FilterReady() {
			logger.Info("model status: restored from database",
				zap.Int("models", n),
				zap.Bool("filter_ready", modelStatus.FilterReady()),
			)
		}
	}
	modelCatalog.SetReachabilityFilter(modelStatus, cfg.Health.Models.HideUnreachable)
	modelCatalog.SetStrictHealthy(cfg.Health.Models.StrictHealthy)
	modelProber := health.NewModelProber(modelCatalog, registry, modelStatus, logger, cfg.Health.Models)
	// Wire health → runtime adapter through the batcher callback.
	healthAdapter := adapter.NewHealthToRuntimeAdapter(runtimeStore)
	modelProber.SetOnBatch(func(results []health.ProbeResult) {
		for _, r := range results {
			healthAdapter.OnProbeResult(r)
		}
	})
	// Skip probes against loopback-only providers so remote deploys (Fly) finish
	// the available-only pass instead of hanging on localhost ollama/lmstudio.
	var skipLocal []string
	if cfg.Providers.Ollama.Enabled && config.IsLoopbackBaseURL(cfg.Providers.Ollama.BaseURL) {
		skipLocal = append(skipLocal, "ollama")
	}
	if cfg.Providers.LMStudio.Enabled && config.IsLoopbackBaseURL(cfg.Providers.LMStudio.BaseURL) {
		skipLocal = append(skipLocal, "lmstudio")
	}
	if len(skipLocal) > 0 {
		modelProber.SkipProviders(skipLocal...)
		logger.Info("model probe: skipping loopback providers", zap.Strings("providers", skipLocal))
	}
	modelProber.Start()
	defer modelProber.Stop()

	// Runtime auto model selection (currently NVIDIA NIM only)
	if cfg.Providers.NvidiaNim.Enabled && cfg.Providers.NvidiaNim.AutoMode != nil && cfg.Providers.NvidiaNim.AutoMode.Enabled {
		history := automode.NewDBHistoryQuerier(db)
		selector := automode.NewSelector(modelCatalog, modelStatus, history, registry)
		autoSelector := automode.NewRouterAdapter(selector, cfg.Providers.NvidiaNim.AutoMode)
		routerEngine.SetAutoSelector(autoSelector)
		logger.Info("auto mode enabled for provider", zap.String("provider", "nvidia_nim"))
	}

	// Catalog-backed auto model selection (model="auto") is a first-class
	// virtual model. The resolver is constructed whenever the catalog exists —
	// it does NOT depend on routing.enabled, the DecisionPipeline, or NIM
	// automode. The routing engine reuses the same instance when enabled so
	// weights, capability overrides, and scoring state stay consistent.
	autoResolver := router.NewAutoResolver(router.AutoResolverConfig{
		Registry:    registry,
		Catalog:     modelCatalog,
		Runtime:     runtimeManager,
		BreakerPool: routerEngine.BreakerPool(),
		Weights:     cfg.Routing.Weights,
		Logger:      logger,
	})

	// Catalog-backed virtual model selection for all capability-based virtual
	// models (frontier, coding, reasoning, agentic, planning, long_horizon,
	// fast, light, vision, auto). This is a first-class feature independent
	// of routing.enabled and the DecisionPipeline.
	virtualResolver := router.NewVirtualResolver(router.VirtualModelResolverConfig{
		Registry:    registry,
		Catalog:     modelCatalog,
		Runtime:     runtimeManager,
		BreakerPool: routerEngine.BreakerPool(),
		Weights:     cfg.Routing.Weights,
		Logger:      logger,
	})

	// Initialize Fiber app
	app := fiber.New(fiber.Config{
		ReadTimeout:  cfg.Server.ReadTimeout,
		WriteTimeout: cfg.Server.WriteTimeout,
		BodyLimit:    int(cfg.Server.MaxRequestSize),
	})

	// Initialize intelligent routing engine (optional, enabled by config)
	var routingEngine *router.RouterEngine
	var decisionPipeline *router.DecisionPipeline
	var traceStore router.TraceStore
	if cfg.Routing.Enabled {
		routingEngine = router.NewRouterEngine(router.RouterEngineConfig{
			Registry:     registry,
			MetricsStore: router.NewMetricsStore(),
			BreakerPool:  routerEngine.BreakerPool(),
			Runtime:      runtimeManager,
			Logger:       logger,
			Weights:      cfg.Routing.Weights,
			Catalog:      modelCatalog,
			AutoResolver: autoResolver,
		})
		logger.Info("intelligent routing engine enabled",
			zap.Float64("health_weight", cfg.Routing.Weights.Health),
			zap.Float64("latency_weight", cfg.Routing.Weights.Latency),
			zap.Float64("cost_weight", cfg.Routing.Weights.Cost),
			zap.Float64("capability_weight", cfg.Routing.Weights.Capability),
		)

		// DecisionPipeline wraps RouterEngine as the final provider-selection authority.
		decisionPipeline = router.NewDecisionPipeline(router.PipelineConfig{
			RoutingEngine:  routingEngine,
			RuntimeManager: runtimeManager,
			EventBus:       eventBus,
			BreakerPool:    routerEngine.BreakerPool(),
			Logger:         logger,
			Weights:        cfg.Routing.Weights,
		})

		// Persist completed routing decisions asynchronously. DecisionPipeline
		// publishes DecisionFinished with the final DecisionTrace; the
		// consumer saves it to SQLite off the request path. Persistence
		// failures never affect routing. The same store instance also backs
		// the read-only trace query API (GET /api/routing/traces).
		traceStore = database.NewSQLiteTraceStore(db)
		tracePersistence := database.NewTracePersistence(eventBus, traceStore, logger)
		tracePersistence.Start()
		defer tracePersistence.Stop()
		logger.Info("routing trace persistence enabled")
	}

	// Register middleware
	middleware.Register(app, cfg, authService, logger)

	// Register handlers
	h := handler.New(routerEngine, registry, usageTracker, logger, modelCatalog, db)
	h.SetConfig(cfg)
	h.SetModelStatus(modelStatus, modelProber)
	h.SetAutoModelResolver(autoResolver)
	h.SetVirtualResolver(virtualResolver)
	if routingEngine != nil {
		h.SetRoutingEngine(routingEngine)
	}
	if decisionPipeline != nil {
		h.SetDecisionPipeline(decisionPipeline)
	}
	// Wire usage → runtime adapter so live traffic updates runtime stats.
	usageAdapter := adapter.NewUsageToRuntimeAdapter(runtimeStore)
	h.SetUsageAdapter(usageAdapter)
	// Wire breaker → runtime adapter.
	breakerAdapter := adapter.NewBreakerToRuntimeAdapter(runtimeStore)
	h.SetBreakerAdapter(breakerAdapter)
	// Wire breaker state changes → runtime so operational state is observable.
	if bp := routerEngine.BreakerPool(); bp != nil {
		bp.SetStateChangeCallback(func(name string, state breaker.State) {
			breakerAdapter.OnBreakerStateChange(name, breaker.BreakerStats{State: state})
		})
	}
	// Expose runtime manager for the /api/runtime endpoint.
	h.SetRuntimeManager(runtimeManager)
	// Expose the trace store for the read-only trace query API. Nil when
	// routing is disabled: the endpoints then answer 503.
	h.SetTraceStore(traceStore)

	// Persist chat execution attempts asynchronously (P4.4.3): the handler
	// publishes AttemptRecord events; the consumer saves them to SQLite off
	// the request path. Persistence failures never affect requests.
	if cfg.Execution.Attempts.Enabled {
		attemptStore := database.NewAttemptStore(db)
		attemptPersistence := database.NewAttemptPersistence(eventBus, attemptStore, logger)
		attemptPersistence.Start()
		defer attemptPersistence.Stop()
		h.SetAttemptEmitter(func(rec database.AttemptRecord) {
			eventBus.Publish(context.Background(), eventbus.Event{
				Type:    eventbus.ExecutionAttemptCompleted,
				Payload: rec,
			})
		})
		logger.Info("execution attempt persistence enabled")
	}

	cacheEngine := cache.NewEngine(cfg.Cache, h.Metrics(), logger)
	h.SetCacheEngine(cacheEngine)
	h.SetStreamIdleTimeout(cfg.Stream.IdleTimeout)
	h.Register(app)

	// Register task API handlers
	taskStore := task.NewSQLiteStore(db)
	toolReg := toolregistry.NewRegistry()

	// Register filesystem tools when workspace is configured.
	if cfg.Agent.WorkspaceRoot != "" {
		readTool := toolfs.New(cfg.Agent.WorkspaceRoot, cfg.Agent.MaxOutputBytes)
		writeTool := toolfs.NewWrite(cfg.Agent.WorkspaceRoot, cfg.Agent.MaxWriteBytes)
		if err := toolReg.Register(readTool); err != nil {
			logger.Warn("failed to register read_file tool", zap.Error(err))
		}
		if err := toolReg.Register(writeTool); err != nil {
			logger.Warn("failed to register write_file tool", zap.Error(err))
		}
	}

	// Register shell tool when enabled.
	if cfg.Agent.Shell.Enabled {
		shellCfg := toolshell.Config{
			WorkingDir:   cfg.Agent.Shell.WorkingDir,
			Timeout:      cfg.Agent.Shell.Timeout,
			MaxOutput:    cfg.Agent.Shell.MaxOutputBytes,
			AllowList:    cfg.Agent.Shell.AllowList,
			Denied:       cfg.Agent.Shell.DeniedCommands,
			EnvWhitelist: cfg.Agent.Shell.EnvWhitelist,
		}
		if err := toolReg.Register(toolshell.New(shellCfg)); err != nil {
			logger.Warn("failed to register shell tool", zap.Error(err))
		}
	}

	// Register git tools when enabled.
	if cfg.Agent.Git.Enabled && cfg.Agent.Git.RepoRoot != "" {
		gitCfg := toolgit.Config{
			RepoRoot:  cfg.Agent.Git.RepoRoot,
			MaxOutput: cfg.Agent.MaxOutputBytes,
		}
		if err := toolgit.RegisterTools(toolReg, gitCfg); err != nil {
			logger.Warn("failed to register git tools", zap.Error(err))
		}
	}

	agentCfg := agent.Config{
		MaxSteps:        cfg.Agent.MaxSteps,
		WorkspaceRoot:   cfg.Agent.WorkspaceRoot,
		MaxOutputBytes:  cfg.Agent.MaxOutputBytes,
		MaxWriteBytes:   cfg.Agent.MaxWriteBytes,
		ShellEnabled:    cfg.Agent.Shell.Enabled,
		ShellWorkingDir: cfg.Agent.Shell.WorkingDir,
		ShellTimeout:    cfg.Agent.Shell.Timeout,
		ShellMaxOutput:  cfg.Agent.Shell.MaxOutputBytes,
		ShellAllowList:  cfg.Agent.Shell.AllowList,
		ShellDenied:     cfg.Agent.Shell.DeniedCommands,
		ShellEnvWhite:   cfg.Agent.Shell.EnvWhitelist,
		GitEnabled:      cfg.Agent.Git.Enabled,
		GitRepoRoot:     cfg.Agent.Git.RepoRoot,
	}
	// Pass worker count from config for async execution.
	if cfg.Agent.WorkerCount == 0 {
		cfg.Agent.WorkerCount = 2 // default when not explicitly set but used by pool
	}
	agentImpl := agent.New(agentCfg, task.NewStoreAdapter(taskStore), routerEngine, toolReg, usageTracker, logger)
	taskExec := task.NewTaskExecutor(taskStore, routerEngine, agentImpl, modelCatalog, usageTracker, logger)
	// Wire orchestration pipeline for intelligent task planning.
	if routingEngine != nil {
		taskExec.WithOrchestration(registry, toolReg, routingEngine, eventBus)
		logger.Info("orchestration pipeline enabled for task execution")
	}

	// V2.6: Agent role registry.
	agentReg := agent.NewRegistry()
	if err := agentReg.Register(agent.AgentDefinition{
		Name:             "research",
		Description:      "Research and analysis tasks requiring reasoning",
		SystemPromptHint: "You are a research agent. Your role is to analyze, compare, and synthesize information. Focus on accuracy and completeness.",
		PreferredTools:   []string{},
		RoutingHints:     agent.RoutingHints{PreferredCapabilities: []string{"reasoning"}},
	}); err != nil {
		logger.Warn("failed to register research agent role", zap.Error(err))
	}
	if err := agentReg.Register(agent.AgentDefinition{
		Name:             "coding",
		Description:      "Coding and implementation tasks requiring filesystem access",
		SystemPromptHint: "You are a coding agent. Your role is to implement, modify, and debug code. Use filesystem and shell tools as needed.",
		PreferredTools:   []string{"read_file", "write_file", "edit_file", "list_files", "shell_exec", "git_status", "git_diff", "git_add", "git_commit"},
		RoutingHints:     agent.RoutingHints{PreferredCapabilities: []string{"tool_calling"}},
	}); err != nil {
		logger.Warn("failed to register coding agent role", zap.Error(err))
	}
	if err := agentReg.Register(agent.AgentDefinition{
		Name:             "testing",
		Description:      "Testing and verification tasks",
		SystemPromptHint: "You are a testing agent. Your role is to verify correctness, run tests, and validate outputs. Focus on edge cases and failure modes.",
		PreferredTools:   []string{"shell_exec", "read_file", "list_files"},
		RoutingHints:     agent.RoutingHints{PreferredCapabilities: []string{"tool_calling"}},
	}); err != nil {
		logger.Warn("failed to register testing agent role", zap.Error(err))
	}
	if err := agentReg.Register(agent.AgentDefinition{
		Name:             "general",
		Description:      "General-purpose tasks with no specific role",
		SystemPromptHint: "",
	}); err != nil {
		logger.Warn("failed to register general agent role", zap.Error(err))
	}
	agentImpl.WithRoleRegistry(agentReg)
	taskExec.WithRoleRegistry(agentReg)

	// V2.6: Coordinator for multi-agent orchestration.
	coordStore := coordinator.NewStoreAdapter(taskStore)
	coordCfg := coordinator.NewConfig()
	coord := coordinator.New(coordStore, eventBus, logger, coordCfg)
	exec := coordinator.NewExecutor(taskStore, taskExec, coord, logger)

	// Start async worker pool + scheduler when enabled by config.
	var pool *worker.Pool
	var sched *worker.Scheduler
	if cfg.Agent.WorkerCount > 0 {
		poolCfg := worker.Config{
			WorkerCount:   cfg.Agent.WorkerCount,
			PollInterval:  cfg.Agent.PollInterval,
			LeaseDuration: cfg.Agent.LeaseDuration,
		}
		pool = worker.New(poolCfg, taskStore, exec, logger)
		sched = worker.NewScheduler(taskStore, logger)
		if err := pool.Recover(); err != nil {
			logger.Warn("startup recovery failed (non-fatal)", zap.Error(err))
		}
		pool.Start()
		sched.Start()
		defer func() {
			sched.Stop()
			pool.Stop()
		}()
		logger.Info("async worker pool started",
			zap.Int("workers", poolCfg.WorkerCount),
			zap.Duration("poll_interval", poolCfg.PollInterval),
		)
	}

	taskHandler := task.NewHandler(taskStore, exec, logger)
	if pool != nil {
		taskHandler.WithCancelPool(pool)
	}
	taskHandler.Register(app)

	// Graceful shutdown
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		logger.Info("Shutting down...")
		// Bound the drain window: Fiber's Shutdown waits for in-flight
		// requests indefinitely, and long-lived SSE/streaming responses
		// would otherwise block process exit forever. 60s is generous
		// enough for most completions; after that we force exit.
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		if err := app.ShutdownWithContext(shutdownCtx); err != nil {
			logger.Warn("graceful shutdown timed out or failed; forcing exit", zap.Error(err))
		}
	}()

	// Start server
	addr := fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)
	logger.Info("Gateway listening", zap.String("address", addr))
	if err := app.Listen(addr); err != nil {
		logger.Fatal("Server error", zap.Error(err))
	}
}

// initLogger initializes the Zap logger
func initLogger(cfg *config.Config) (*zap.Logger, error) {
	var zapCfg zap.Config

	switch cfg.Logging.Format {
	case "json":
		zapCfg = zap.NewProductionConfig()
	case "console":
		zapCfg = zap.NewDevelopmentConfig()
	default:
		zapCfg = zap.NewProductionConfig()
	}

	switch cfg.Logging.Level {
	case "debug":
		zapCfg.Level = zap.NewAtomicLevelAt(zap.DebugLevel)
	case "info":
		zapCfg.Level = zap.NewAtomicLevelAt(zap.InfoLevel)
	case "warn":
		zapCfg.Level = zap.NewAtomicLevelAt(zap.WarnLevel)
	case "error":
		zapCfg.Level = zap.NewAtomicLevelAt(zap.ErrorLevel)
	default:
		zapCfg.Level = zap.NewAtomicLevelAt(zap.InfoLevel)
	}

	return zapCfg.Build()
}

// registerProviders registers all enabled providers
func registerProviders(cfg *config.Config, registry *provider.Registry, logger *zap.Logger) {
	registerOne := func(name string, p provider.Provider) {
		registry.Register(p)
		logger.Info("provider_registered",
			zap.String("provider", p.Name()),
			zap.String("display_name", provider.GetMetadata(p).DisplayName),
		)
	}

	// OpenAI
	if cfg.Providers.OpenAI.Enabled {
		registerOne("openai", openai.NewProvider(cfg.Providers.OpenAI.APIKey, cfg.Providers.OpenAI.BaseURL, cfg.Providers.OpenAI.Timeout))
	}

	// Anthropic
	if cfg.Providers.Anthropic.Enabled {
		registerOne("anthropic", anthropic.NewProvider(cfg.Providers.Anthropic.APIKey, cfg.Providers.Anthropic.BaseURL, cfg.Providers.Anthropic.Timeout))
	}

	// Gemini
	if cfg.Providers.Gemini.Enabled {
		registerOne("gemini", gemini.NewProvider(cfg.Providers.Gemini.APIKey, cfg.Providers.Gemini.BaseURL, cfg.Providers.Gemini.Timeout))
	}

	// DeepSeek
	if cfg.Providers.DeepSeek.Enabled {
		registerOne("deepseek", deepseek.NewProvider(cfg.Providers.DeepSeek.APIKey, cfg.Providers.DeepSeek.BaseURL, cfg.Providers.DeepSeek.Timeout))
	}

	// OpenRouter
	if cfg.Providers.OpenRouter.Enabled {
		registerOne("openrouter", openrouter.NewProvider(cfg.Providers.OpenRouter.APIKey, cfg.Providers.OpenRouter.BaseURL, cfg.Providers.OpenRouter.Timeout))
	}

	// Groq
	if cfg.Providers.Groq.Enabled {
		registerOne("groq", groq.NewProvider(cfg.Providers.Groq.APIKey, cfg.Providers.Groq.BaseURL, cfg.Providers.Groq.Timeout))
	}

	// Ollama
	if cfg.Providers.Ollama.Enabled {
		registerOne("ollama", ollama.NewProvider(cfg.Providers.Ollama.APIKey, cfg.Providers.Ollama.BaseURL, cfg.Providers.Ollama.Timeout))
	}

	// LM Studio
	if cfg.Providers.LMStudio.Enabled {
		registerOne("lmstudio", lmstudio.NewProvider(cfg.Providers.LMStudio.APIKey, cfg.Providers.LMStudio.BaseURL, cfg.Providers.LMStudio.Timeout))
	}

	// OpenCode
	if cfg.Providers.Opencode.Enabled {
		registerOne("opencode", opencode.NewProvider(cfg.Providers.Opencode.APIKey, cfg.Providers.Opencode.BaseURL, cfg.Providers.Opencode.Timeout))
	}

	// NVIDIA NIM
	if cfg.Providers.NvidiaNim.Enabled {
		registerOne("nvidia_nim", nvidianim.NewProvider(cfg.Providers.NvidiaNim.APIKey, cfg.Providers.NvidiaNim.BaseURL, cfg.Providers.NvidiaNim.Timeout))
	}

	// Nous Portal
	if cfg.Providers.NousPortal.Enabled {
		registerOne("nous_portal", nousportal.NewProvider(cfg.Providers.NousPortal.APIKey, cfg.Providers.NousPortal.BaseURL, cfg.Providers.NousPortal.Timeout))
	}

	// xAI
	if cfg.Providers.XAI.Enabled {
		registerOne("xai", xai.NewProvider(cfg.Providers.XAI.APIKey, cfg.Providers.XAI.BaseURL, cfg.Providers.XAI.Timeout))
	}

	// Agnes AI
	if cfg.Providers.AgnesAI.Enabled {
		registerOne("agnesai", agnesai.NewProvider(cfg.Providers.AgnesAI.APIKey, cfg.Providers.AgnesAI.BaseURL, cfg.Providers.AgnesAI.Timeout))
	}

	// KiloCode
	if cfg.Providers.KiloCode.Enabled {
		registerOne("kilocode", kilocode.NewProvider(cfg.Providers.KiloCode.APIKey, cfg.Providers.KiloCode.BaseURL, cfg.Providers.KiloCode.Timeout))
	}

	// Mistral AI
	if cfg.Providers.Mistral.Enabled {
		registerOne("mistral", mistral.NewProvider(cfg.Providers.Mistral.APIKey, cfg.Providers.Mistral.BaseURL, cfg.Providers.Mistral.Timeout))
	}

	// Z.AI
	if cfg.Providers.ZAI.Enabled {
		registerOne("zai", zai.NewProvider(cfg.Providers.ZAI.APIKey, cfg.Providers.ZAI.BaseURL, cfg.Providers.ZAI.Timeout))
	}

	// Cerebras
	if cfg.Providers.Cerebras.Enabled {
		registerOne("cerebras", cerebras.NewProvider(cfg.Providers.Cerebras.APIKey, cfg.Providers.Cerebras.BaseURL, cfg.Providers.Cerebras.Timeout))
	}

	// Requesty
	if cfg.Providers.Requesty.Enabled {
		registerOne("requesty", requesty.NewProvider(cfg.Providers.Requesty.APIKey, cfg.Providers.Requesty.BaseURL, cfg.Providers.Requesty.Timeout))
	}

	// Cloudflare Workers AI
	if cfg.Providers.Cloudflare.Enabled {
		registerOne("cloudflare", cloudflare.NewProvider(cfg.Providers.Cloudflare.APIKey, cfg.Providers.Cloudflare.BaseURL, cfg.Providers.Cloudflare.Timeout))
	}
}

func printUsage() {
	fmt.Fprintf(os.Stderr, `Conductor — OpenAI-compatible AI gateway

Usage:
  conductor                 Start the gateway
  conductor gen-key         Print a new random gateway API key
  conductor help            Show this help

Environment:
  CONDUCTOR_API_KEY         Gateway API key (auto-generated on first boot if unset)
`)
}
