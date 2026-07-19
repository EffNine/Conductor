package main

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/gofiber/fiber/v2"
	"github.com/novexa/gateway/internal/auth"
	"github.com/novexa/gateway/internal/catalog"
	"github.com/novexa/gateway/internal/config"
	"github.com/novexa/gateway/internal/database"
	"github.com/novexa/gateway/internal/handler"
	"github.com/novexa/gateway/internal/health"
	"github.com/novexa/gateway/internal/middleware"
	"github.com/novexa/gateway/internal/provider"
	"github.com/novexa/gateway/internal/provider/anthropic"
	"github.com/novexa/gateway/internal/provider/deepseek"
	"github.com/novexa/gateway/internal/provider/gemini"
	"github.com/novexa/gateway/internal/provider/groq"
	"github.com/novexa/gateway/internal/provider/lmstudio"
	"github.com/novexa/gateway/internal/provider/nousportal"
	"github.com/novexa/gateway/internal/provider/nvidianim"
	"github.com/novexa/gateway/internal/provider/ollama"
	"github.com/novexa/gateway/internal/provider/openai"
	"github.com/novexa/gateway/internal/provider/opencode"
	"github.com/novexa/gateway/internal/provider/openrouter"
	"github.com/novexa/gateway/internal/router"
	"github.com/novexa/gateway/internal/usage"
	"go.uber.org/zap"
)

func main() {
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

	logger.Info("Starting Novexa Gateway",
		zap.Int("port", cfg.Server.Port),
		zap.String("log_level", cfg.Logging.Level),
	)

	// Initialize database
	db, err := database.Connect(&cfg.Database)
	if err != nil {
		logger.Fatal("Failed to connect to database", zap.Error(err))
	}

	if err := db.Migrate(); err != nil {
		logger.Fatal("Failed to run database migrations", zap.Error(err))
	}
	logger.Info("Database connected and migrated")

	// Initialize auth service
	authService := auth.NewService(cfg.APIKey)

	// Initialize provider registry
	registry := provider.NewRegistry()

	// Register providers
	registerProviders(cfg, registry, logger)

	// Log registered providers
	logger.Info("Registered providers", zap.Strings("providers", registry.Names()))

	// Initialize router
	routerEngine := router.NewEngine(cfg, registry)

	// Initialize model catalog
	modelCatalog := catalog.New(registry, catalog.StaticFromConfig(cfg))

	// Initialize cost estimator + usage tracker
	estimator := usage.NewEstimator(registry, usage.ManualRatesFromConfig(cfg))
	usageTracker := usage.NewTracker(db, estimator, logger)

	// Initialize health monitor
	healthMonitor := health.NewMonitor(registry, logger, cfg.Health.CheckInterval, cfg.Health.Timeout)
	healthMonitor.Start()
	defer healthMonitor.Stop()

	// Initialize Fiber app
	app := fiber.New(fiber.Config{
		ReadTimeout:  cfg.Server.ReadTimeout,
		WriteTimeout: cfg.Server.WriteTimeout,
		BodyLimit:    int(cfg.Server.MaxRequestSize),
	})

	// Register middleware
	middleware.Register(app, cfg, authService, logger)

	// Register handlers
	h := handler.New(routerEngine, registry, usageTracker, logger, modelCatalog, db)
	h.Register(app)

	// Graceful shutdown
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		logger.Info("Shutting down...")
		_ = app.Shutdown()
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
	// OpenAI
	if cfg.Providers.OpenAI.Enabled {
		p := openai.NewProvider(cfg.Providers.OpenAI.APIKey, cfg.Providers.OpenAI.BaseURL, cfg.Providers.OpenAI.Timeout)
		registry.Register(p)
		logger.Debug("Registered provider", zap.String("provider", p.Name()))
	}

	// Anthropic
	if cfg.Providers.Anthropic.Enabled {
		p := anthropic.NewProvider(cfg.Providers.Anthropic.APIKey, cfg.Providers.Anthropic.BaseURL, cfg.Providers.Anthropic.Timeout)
		registry.Register(p)
		logger.Debug("Registered provider", zap.String("provider", p.Name()))
	}

	// Gemini
	if cfg.Providers.Gemini.Enabled {
		p := gemini.NewProvider(cfg.Providers.Gemini.APIKey, cfg.Providers.Gemini.BaseURL, cfg.Providers.Gemini.Timeout)
		registry.Register(p)
		logger.Debug("Registered provider", zap.String("provider", p.Name()))
	}

	// DeepSeek
	if cfg.Providers.DeepSeek.Enabled {
		p := deepseek.NewProvider(cfg.Providers.DeepSeek.APIKey, cfg.Providers.DeepSeek.BaseURL, cfg.Providers.DeepSeek.Timeout)
		registry.Register(p)
		logger.Debug("Registered provider", zap.String("provider", p.Name()))
	}

	// OpenRouter
	if cfg.Providers.OpenRouter.Enabled {
		p := openrouter.NewProvider(cfg.Providers.OpenRouter.APIKey, cfg.Providers.OpenRouter.BaseURL, cfg.Providers.OpenRouter.Timeout)
		registry.Register(p)
		logger.Debug("Registered provider", zap.String("provider", p.Name()))
	}

	// Groq
	if cfg.Providers.Groq.Enabled {
		p := groq.NewProvider(cfg.Providers.Groq.APIKey, cfg.Providers.Groq.BaseURL, cfg.Providers.Groq.Timeout)
		registry.Register(p)
		logger.Debug("Registered provider", zap.String("provider", p.Name()))
	}

	// Ollama
	if cfg.Providers.Ollama.Enabled {
		p := ollama.NewProvider(cfg.Providers.Ollama.APIKey, cfg.Providers.Ollama.BaseURL, cfg.Providers.Ollama.Timeout)
		registry.Register(p)
		logger.Debug("Registered provider", zap.String("provider", p.Name()))
	}

	// LM Studio
	if cfg.Providers.LMStudio.Enabled {
		p := lmstudio.NewProvider(cfg.Providers.LMStudio.APIKey, cfg.Providers.LMStudio.BaseURL, cfg.Providers.LMStudio.Timeout)
		registry.Register(p)
		logger.Debug("Registered provider", zap.String("provider", p.Name()))
	}

	// OpenCode
	if cfg.Providers.Opencode.Enabled {
		p := opencode.NewProvider(cfg.Providers.Opencode.APIKey, cfg.Providers.Opencode.BaseURL, cfg.Providers.Opencode.Timeout)
		registry.Register(p)
		logger.Debug("Registered provider", zap.String("provider", p.Name()))
	}

	// NVIDIA NIM
	if cfg.Providers.NvidiaNim.Enabled {
		p := nvidianim.NewProvider(cfg.Providers.NvidiaNim.APIKey, cfg.Providers.NvidiaNim.BaseURL, cfg.Providers.NvidiaNim.Timeout)
		registry.Register(p)
		logger.Debug("Registered provider", zap.String("provider", p.Name()))
	}

	// Nous Portal
	if cfg.Providers.NousPortal.Enabled {
		p := nousportal.NewProvider(cfg.Providers.NousPortal.APIKey, cfg.Providers.NousPortal.BaseURL, cfg.Providers.NousPortal.Timeout)
		registry.Register(p)
		logger.Debug("Registered provider", zap.String("provider", p.Name()))
	}
}
