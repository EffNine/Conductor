package config

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/viper"
)

// Ollama OpenAI-compatible endpoints (local vs cloud).
const (
	defaultOllamaBaseURL = "http://localhost:11434/v1"
	ollamaCloudBaseURL   = "https://ollama.com/v1"
)

// DefaultNvidiaNimCuratedModels is applied when catalog.curated_only is on,
// NVIDIA NIM is enabled, and providers.nvidia_nim.models is empty. Keeps
// Fly/env-only deploys off the full ~180 NIM catalog without requiring YAML.
var DefaultNvidiaNimCuratedModels = []string{
	"deepseek-ai/deepseek-v4-flash",
	"meta/llama-3.1-8b-instruct",
	"meta/llama-3.1-70b-instruct",
	"meta/llama-3.2-11b-vision-instruct",
	"stepfun-ai/step-3.7-flash",
	"stepfun-ai/step-3.5-flash",
	"openai/gpt-oss-20b",
	"openai/gpt-oss-120b",
	"nvidia/nemotron-3-super-120b-a12b",
	"nvidia/llama-3.3-nemotron-super-49b-v1.5",
	"nvidia/llama-3.3-nemotron-super-49b-v1",
	"mistralai/mistral-large-3-675b-instruct-2512",
	"qwen/qwen3-next-80b-a3b-instruct",
}

// Config holds all configuration for the application
type Config struct {
	Server    ServerConfig                `mapstructure:"server"`
	APIKey    string                      `mapstructure:"api_key"`
	Providers ProvidersConfig             `mapstructure:"providers"`
	Catalog   CatalogConfig               `mapstructure:"catalog"`
	Routes    map[string]RouteConfig      `mapstructure:"routes"`
	Aliases   map[string]string           `mapstructure:"aliases"`
	Fallbacks map[string][]FallbackConfig `mapstructure:"fallbacks"`
	Retry     RetryConfig                 `mapstructure:"retry"`
	Database  DatabaseConfig              `mapstructure:"database"`
	Logging   LoggingConfig               `mapstructure:"logging"`
	RateLimit RateLimitConfig             `mapstructure:"rate_limit"`
	Health    HealthConfig                `mapstructure:"health"`
	Usage     UsageConfig                 `mapstructure:"usage"`
	Cost      CostConfig                  `mapstructure:"cost"`
}

// CatalogConfig controls how the merged Model Catalog is built for /v1/models.
type CatalogConfig struct {
	// CuratedOnly, when true, uses each provider's Static Model List (`models`)
	// as an allowlist when non-empty. Providers with an empty list still use
	// dynamic ListModels. Default false (Fly enables via env).
	CuratedOnly bool `mapstructure:"curated_only"`
}

// ServerConfig holds server configuration
type ServerConfig struct {
	Host           string        `mapstructure:"host"`
	Port           int           `mapstructure:"port"`
	ReadTimeout    time.Duration `mapstructure:"read_timeout"`
	WriteTimeout   time.Duration `mapstructure:"write_timeout"`
	MaxRequestSize int64         `mapstructure:"max_request_size"`
	CORS           CORSConfig    `mapstructure:"cors"`
}

// CORSConfig holds CORS configuration
type CORSConfig struct {
	Enabled bool     `mapstructure:"enabled"`
	Origins []string `mapstructure:"origins"`
	Methods []string `mapstructure:"methods"`
	Headers []string `mapstructure:"headers"`
}

// ProvidersConfig holds all provider configurations
type ProvidersConfig struct {
	OpenAI     ProviderConfig `mapstructure:"openai"`
	Anthropic  ProviderConfig `mapstructure:"anthropic"`
	Gemini     ProviderConfig `mapstructure:"gemini"`
	DeepSeek   ProviderConfig `mapstructure:"deepseek"`
	OpenRouter ProviderConfig `mapstructure:"openrouter"`
	Groq       ProviderConfig `mapstructure:"groq"`
	Ollama     ProviderConfig `mapstructure:"ollama"`
	LMStudio   ProviderConfig `mapstructure:"lmstudio"`
	Opencode   ProviderConfig `mapstructure:"opencode"`
	NvidiaNim  ProviderConfig `mapstructure:"nvidia_nim"`
	NousPortal ProviderConfig `mapstructure:"nous_portal"`
	XAI        ProviderConfig `mapstructure:"xai"`
	AgnesAI    ProviderConfig `mapstructure:"agnesai"`
}

// ProviderConfig holds configuration for a single provider
type ProviderConfig struct {
	Enabled    bool            `mapstructure:"enabled"`
	APIKey     string          `mapstructure:"api_key"`
	BaseURL    string          `mapstructure:"base_url"`
	Timeout    time.Duration   `mapstructure:"timeout"`
	MaxRetries int             `mapstructure:"max_retries"`
	Models     []string        `mapstructure:"models"` // Static Model List when ListModels is unavailable
	AutoMode   *AutoModeConfig `mapstructure:"auto"`
}

// AutoModeConfig controls runtime automatic model selection for a provider.
// Currently used for NVIDIA NIM; the struct is provider-scoped so it can be
// enabled per provider in the future.
type AutoModeConfig struct {
	Enabled      bool                       `mapstructure:"enabled"`
	Provider     string                     `mapstructure:"provider"`
	Lookback     time.Duration              `mapstructure:"lookback"`
	Weights      AutoModeWeights            `mapstructure:"weights"`
	TaskProfiles map[string]AutoModeProfile `mapstructure:"task_profiles"`
}

// AutoModeWeights controls the scoring mix for auto model selection.
// Higher weight makes that signal more influential. Weights are normalized
// internally, so absolute scale does not matter.
type AutoModeWeights struct {
	Reachability float64 `mapstructure:"reachability"`
	Cost         float64 `mapstructure:"cost"`
	Latency      float64 `mapstructure:"latency"`
}

// AutoModeProfile is a task-specific model allowlist and weight override.
// When a task matches a profile, only those models are candidates and the
// profile weights are used. If no profile matches, the default weights and
// the full advertised catalog are used.
type AutoModeProfile struct {
	Models  []string        `mapstructure:"models"`
	Weights AutoModeWeights `mapstructure:"weights"`
}

// RouteConfig holds configuration for a model route
type RouteConfig struct {
	Provider string `mapstructure:"provider"`
	ModelID  string `mapstructure:"model_id"` // Optional: override model name for provider
}

// FallbackConfig holds configuration for fallback providers
type FallbackConfig struct {
	Provider string `mapstructure:"provider"`
	ModelID  string `mapstructure:"model_id"` // Optional: override model name
}

// RetryConfig holds retry configuration
type RetryConfig struct {
	MaxRetries           int           `mapstructure:"max_retries"`
	InitialBackoff       time.Duration `mapstructure:"initial_backoff"`
	MaxBackoff           time.Duration `mapstructure:"max_backoff"`
	BackoffMultiplier    float64       `mapstructure:"backoff_multiplier"`
	RetryableStatusCodes []int         `mapstructure:"retryable_status_codes"`
}

// DatabaseConfig holds database configuration
type DatabaseConfig struct {
	Driver       string `mapstructure:"driver"`
	DSN          string `mapstructure:"dsn"`
	MaxOpenConns int    `mapstructure:"max_open_conns"`
	MaxIdleConns int    `mapstructure:"max_idle_conns"`
}

// LoggingConfig holds logging configuration
type LoggingConfig struct {
	Level        string `mapstructure:"level"`
	Format       string `mapstructure:"format"`
	LogPrompts   bool   `mapstructure:"log_prompts"`
	LogResponses bool   `mapstructure:"log_responses"`
}

// RateLimitConfig holds rate limiting configuration
type RateLimitConfig struct {
	Enabled     bool             `mapstructure:"enabled"`
	Global      GlobalRateLimit  `mapstructure:"global"`
	PerProvider PerProviderLimit `mapstructure:"per_provider"`
}

// GlobalRateLimit holds global rate limit configuration
type GlobalRateLimit struct {
	RequestsPerMinute int `mapstructure:"requests_per_minute"`
}

// PerProviderLimit holds per-provider rate limit configuration
type PerProviderLimit struct {
	RequestsPerMinute int `mapstructure:"requests_per_minute"`
}

// HealthConfig holds health monitoring configuration
type HealthConfig struct {
	CheckInterval      time.Duration     `mapstructure:"check_interval"`
	Timeout            time.Duration     `mapstructure:"timeout"`
	UnhealthyThreshold int               `mapstructure:"unhealthy_threshold"`
	Models             ModelHealthConfig `mapstructure:"models"`
}

// ModelHealthConfig controls per-model reachability probing and auto-hide.
// Especially useful for NVIDIA NIM, where /models lists free and unreachable
// endpoints without distinguishing them.
type ModelHealthConfig struct {
	// Enabled turns on background per-model probes. Default true.
	Enabled bool `mapstructure:"enabled"`
	// HideUnreachable removes models that fail the unhealthy threshold from
	// /v1/models and /api/models. Default true.
	HideUnreachable bool `mapstructure:"hide_unreachable"`
	// CheckInterval between full probe passes. Default 2h.
	CheckInterval time.Duration `mapstructure:"check_interval"`
	// Timeout per individual model probe. Default 60s.
	Timeout time.Duration `mapstructure:"timeout"`
	// Concurrency is max parallel probes. Default 3 (stay under NIM free-tier RPM).
	Concurrency int `mapstructure:"concurrency"`
	// UnhealthyThreshold consecutive failures before a model is considered
	// unreachable. Default 1. Hiding only applies after the first full probe
	// pass finishes, so the catalog does not flicker during the pass.
	UnhealthyThreshold int `mapstructure:"unhealthy_threshold"`
	// Providers limits probing to these provider names.
	// Empty list (default) means all registered providers.
	Providers []string `mapstructure:"providers"`
	// UnknownAsReachable keeps never-probed models visible after the first
	// probe pass. Default true: err toward availability so a missed probe does
	// not empty the catalog. During the first pass (FilterReady=false) the full
	// catalog is still shown to avoid an empty flicker on cold start.
	UnknownAsReachable bool `mapstructure:"unknown_as_reachable"`
	// StartupProbeInParallel runs the startup pass with configured concurrency
	// (always true historically; kept for config compatibility).
	StartupProbeInParallel bool `mapstructure:"startup_probe_in_parallel"`
	// CatalogBatchWindow collects probe results before an atomic catalog apply.
	// Default 100ms.
	CatalogBatchWindow time.Duration `mapstructure:"catalog_batch_window"`
	// RetryInterval is how often the prober checks for models whose backoff
	// NextProbeTime has elapsed. Default 30s.
	RetryInterval time.Duration `mapstructure:"retry_interval"`
	// Backoff controls exponential retry after probe failures.
	Backoff ProbeBackoffConfig `mapstructure:"backoff"`
	// ErrorTracking feeds live request outcomes into degraded/healthy state.
	ErrorTracking ErrorTrackingConfig `mapstructure:"error_tracking"`
}

// ProbeBackoffConfig schedules retries after consecutive probe failures.
type ProbeBackoffConfig struct {
	Enabled        bool          `mapstructure:"enabled"`
	InitialDelay   time.Duration `mapstructure:"initial_delay"`
	MaxDelay       time.Duration `mapstructure:"max_delay"`
	Multiplier     float64       `mapstructure:"multiplier"`
	JitterFraction float64       `mapstructure:"jitter_fraction"`
}

// ErrorTrackingConfig tracks live request error rates per model.
type ErrorTrackingConfig struct {
	Enabled            bool          `mapstructure:"enabled"`
	Window             time.Duration `mapstructure:"window"`
	UnhealthyThreshold float64       `mapstructure:"unhealthy_threshold"`
	RecoveryThreshold  float64       `mapstructure:"recovery_threshold"`
}

// UsageConfig holds usage tracking configuration
type UsageConfig struct {
	Enabled bool `mapstructure:"enabled"`
}

// CostConfig holds cost tracking configuration
type CostConfig struct {
	Enabled  bool             `mapstructure:"enabled"`
	Currency string           `mapstructure:"currency"`
	Rates    []ManualCostRate `mapstructure:"rates"`
}

// ManualCostRate is a configured fallback Cost Rate.
type ManualCostRate struct {
	Provider        string  `mapstructure:"provider"`
	ProviderModelID string  `mapstructure:"provider_model_id"`
	UnitType        string  `mapstructure:"unit_type"` // token, request, minute, character
	UnitSize        int64   `mapstructure:"unit_size"`
	InputPrice      float64 `mapstructure:"input_price"`
	OutputPrice     float64 `mapstructure:"output_price"`
}

// Load loads configuration from file and environment variables
func Load() (*Config, error) {
	// Rebrand: NOVEXA_* secrets/env still work as CONDUCTOR_* aliases.
	bridgeLegacyNovexaEnv()

	v := viper.New()

	// Set defaults
	setDefaults(v)

	// Config file
	v.SetConfigName("config")
	v.SetConfigType("yaml")
	v.AddConfigPath(".")
	v.AddConfigPath("./config")
	v.AddConfigPath("/etc/conductor")

	// Environment variables
	v.AutomaticEnv()
	v.SetEnvPrefix("CONDUCTOR")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))

	// Read config file (optional)
	if err := v.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return nil, fmt.Errorf("failed to read config file: %w", err)
		}
	}

	// Unmarshal config
	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}

	// Auto-enable providers if their API key env vars are set
	autoEnableProviders(&cfg)

	// Provider model allowlists from CONDUCTOR_PROVIDERS_*_MODELS (comma-separated)
	hydrateProviderModelsFromEnv(&cfg)

	// When curated-only is on and NIM has no models list, apply a short default
	// allowlist so Fly deploys without config.yaml do not advertise ~180 models.
	applyDefaultCuratedModels(&cfg)

	// Validate config
	if err := validate(&cfg); err != nil {
		return nil, fmt.Errorf("config validation failed: %w", err)
	}

	return &cfg, nil
}

// setDefaults sets default configuration values
func setDefaults(v *viper.Viper) {
	// Server defaults
	v.SetDefault("server.host", "0.0.0.0")
	v.SetDefault("server.port", 8080)
	v.SetDefault("server.read_timeout", 30*time.Second)
	v.SetDefault("server.write_timeout", 120*time.Second)
	v.SetDefault("server.max_request_size", 10*1024*1024) // 10MB
	v.SetDefault("server.cors.enabled", true)
	v.SetDefault("server.cors.origins", []string{"*"})
	v.SetDefault("server.cors.methods", []string{"GET", "POST", "OPTIONS"})
	v.SetDefault("server.cors.headers", []string{"Authorization", "Content-Type"})

	// Provider defaults
	v.SetDefault("providers.openai.enabled", true)
	v.SetDefault("providers.openai.base_url", "https://api.openai.com/v1")
	v.SetDefault("providers.openai.timeout", 60*time.Second)
	v.SetDefault("providers.openai.max_retries", 3)

	v.SetDefault("providers.anthropic.enabled", false)
	v.SetDefault("providers.anthropic.base_url", "https://api.anthropic.com")
	v.SetDefault("providers.anthropic.timeout", 60*time.Second)
	v.SetDefault("providers.anthropic.max_retries", 3)

	v.SetDefault("providers.gemini.enabled", false)
	v.SetDefault("providers.gemini.base_url", "https://generativelanguage.googleapis.com/v1beta")
	v.SetDefault("providers.gemini.timeout", 60*time.Second)
	v.SetDefault("providers.gemini.max_retries", 3)

	v.SetDefault("providers.deepseek.enabled", false)
	v.SetDefault("providers.deepseek.base_url", "https://api.deepseek.com/v1")
	v.SetDefault("providers.deepseek.timeout", 60*time.Second)
	v.SetDefault("providers.deepseek.max_retries", 3)

	v.SetDefault("providers.openrouter.enabled", false)
	v.SetDefault("providers.openrouter.base_url", "https://openrouter.ai/api/v1")
	v.SetDefault("providers.openrouter.timeout", 60*time.Second)
	v.SetDefault("providers.openrouter.max_retries", 3)

	v.SetDefault("providers.groq.enabled", false)
	v.SetDefault("providers.groq.base_url", "https://api.groq.com/openai/v1")
	v.SetDefault("providers.groq.timeout", 30*time.Second)
	v.SetDefault("providers.groq.max_retries", 3)

	v.SetDefault("providers.ollama.enabled", false)
	v.SetDefault("providers.ollama.base_url", defaultOllamaBaseURL)
	v.SetDefault("providers.ollama.timeout", 120*time.Second)
	v.SetDefault("providers.ollama.max_retries", 1)

	v.SetDefault("providers.lmstudio.enabled", false)
	v.SetDefault("providers.lmstudio.base_url", "http://localhost:1234/v1")
	v.SetDefault("providers.lmstudio.timeout", 120*time.Second)
	v.SetDefault("providers.lmstudio.max_retries", 1)

	v.SetDefault("providers.opencode.enabled", false)
	v.SetDefault("providers.opencode.base_url", "https://opencode.ai/zen/v1")
	v.SetDefault("providers.opencode.timeout", 60*time.Second)
	v.SetDefault("providers.opencode.max_retries", 3)

	v.SetDefault("providers.nvidia_nim.enabled", false)
	v.SetDefault("providers.nvidia_nim.base_url", "https://integrate.api.nvidia.com/v1")
	v.SetDefault("providers.nvidia_nim.timeout", 180*time.Second)
	v.SetDefault("providers.nvidia_nim.max_retries", 3)
	v.SetDefault("providers.nvidia_nim.auto.enabled", false)
	v.SetDefault("providers.nvidia_nim.auto.provider", "nvidia_nim")
	v.SetDefault("providers.nvidia_nim.auto.lookback", 24*time.Hour)
	v.SetDefault("providers.nvidia_nim.auto.weights.reachability", 10.0)
	v.SetDefault("providers.nvidia_nim.auto.weights.cost", 3.0)
	v.SetDefault("providers.nvidia_nim.auto.weights.latency", 1.0)

	v.SetDefault("providers.nous_portal.enabled", false)
	v.SetDefault("providers.nous_portal.base_url", "https://inference-api.nousresearch.com/v1")
	v.SetDefault("providers.nous_portal.timeout", 60*time.Second)
	v.SetDefault("providers.nous_portal.max_retries", 3)

	v.SetDefault("providers.xai.enabled", false)
	v.SetDefault("providers.xai.base_url", "https://api.x.ai/v1")
	v.SetDefault("providers.xai.timeout", 60*time.Second)
	v.SetDefault("providers.xai.max_retries", 3)

	v.SetDefault("providers.agnesai.enabled", false)
	v.SetDefault("providers.agnesai.base_url", "https://apihub.agnes-ai.com/v1")
	v.SetDefault("providers.agnesai.timeout", 60*time.Second)
	v.SetDefault("providers.agnesai.max_retries", 3)

	// Retry defaults
	v.SetDefault("retry.max_retries", 3)
	v.SetDefault("retry.initial_backoff", 100*time.Millisecond)
	v.SetDefault("retry.max_backoff", 5*time.Second)
	v.SetDefault("retry.backoff_multiplier", 2.0)
	v.SetDefault("retry.retryable_status_codes", []int{429, 500, 502, 503})

	// Database defaults
	v.SetDefault("database.driver", "sqlite")
	v.SetDefault("database.dsn", "./data/conductor.db")
	v.SetDefault("database.max_open_conns", 10)
	v.SetDefault("database.max_idle_conns", 5)

	// Logging defaults
	v.SetDefault("logging.level", "info")
	v.SetDefault("logging.format", "json")
	v.SetDefault("logging.log_prompts", false)
	v.SetDefault("logging.log_responses", false)

	// Rate limit defaults
	v.SetDefault("rate_limit.enabled", true)
	v.SetDefault("rate_limit.global.requests_per_minute", 1000)
	v.SetDefault("rate_limit.per_provider.requests_per_minute", 100)

	// Catalog defaults
	v.SetDefault("catalog.curated_only", false)

	// Health defaults
	v.SetDefault("health.check_interval", 60*time.Second)
	v.SetDefault("health.timeout", 10*time.Second)
	v.SetDefault("health.unhealthy_threshold", 3)
	v.SetDefault("health.models.enabled", true)
	v.SetDefault("health.models.hide_unreachable", true)
	v.SetDefault("health.models.check_interval", 2*time.Hour)
	v.SetDefault("health.models.timeout", 60*time.Second)
	v.SetDefault("health.models.concurrency", 3)
	v.SetDefault("health.models.unhealthy_threshold", 1)
	// Empty = probe all registered providers.
	v.SetDefault("health.models.providers", []string{})
	// After the first pass, keep never-probed models visible (err toward availability).
	v.SetDefault("health.models.unknown_as_reachable", true)
	v.SetDefault("health.models.startup_probe_in_parallel", true)
	v.SetDefault("health.models.catalog_batch_window", 100*time.Millisecond)
	v.SetDefault("health.models.retry_interval", 30*time.Second)
	v.SetDefault("health.models.backoff.enabled", true)
	v.SetDefault("health.models.backoff.initial_delay", 30*time.Second)
	v.SetDefault("health.models.backoff.max_delay", 12*time.Hour)
	v.SetDefault("health.models.backoff.multiplier", 3.5)
	v.SetDefault("health.models.backoff.jitter_fraction", 0.2)
	v.SetDefault("health.models.error_tracking.enabled", true)
	v.SetDefault("health.models.error_tracking.window", 5*time.Minute)
	v.SetDefault("health.models.error_tracking.unhealthy_threshold", 0.15)
	v.SetDefault("health.models.error_tracking.recovery_threshold", 0.05)

	// Usage defaults
	v.SetDefault("usage.enabled", true)

	// Cost defaults
	v.SetDefault("cost.enabled", true)
	v.SetDefault("cost.currency", "USD")
}

// bridgeLegacyNovexaEnv copies NOVEXA_* env vars to CONDUCTOR_* when the
// Conductor-prefixed key is unset. Preserves Fly secrets set before the rebrand.
func bridgeLegacyNovexaEnv() {
	const legacyPrefix = "NOVEXA_"
	const currentPrefix = "CONDUCTOR_"
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, legacyPrefix) {
			continue
		}
		eq := strings.IndexByte(entry, '=')
		if eq < 0 {
			continue
		}
		legacyKey := entry[:eq]
		val := entry[eq+1:]
		currentKey := currentPrefix + strings.TrimPrefix(legacyKey, legacyPrefix)
		if os.Getenv(currentKey) == "" {
			_ = os.Setenv(currentKey, val)
		}
	}
}

// IsLoopbackBaseURL reports whether u points at localhost / 127.0.0.1.
// Used to skip model probes against local-only providers (ollama, lmstudio) when
// the gateway runs remotely (e.g. Fly.io), so the probe pass can finish.
func IsLoopbackBaseURL(u string) bool {
	u = strings.ToLower(strings.TrimSpace(u))
	if u == "" {
		return false
	}
	return strings.Contains(u, "://localhost") ||
		strings.Contains(u, "://127.0.0.1") ||
		strings.Contains(u, "://[::1]") ||
		strings.Contains(u, "://0.0.0.0")
}

// autoEnableProviders enables providers and fills API keys from well-known env vars.
// Viper's CONDUCTOR_ prefix does not map OPENAI_API_KEY / NVIDIA_NIM_API_KEY / etc.,
// so we hydrate those explicitly when the config field is empty.
func autoEnableProviders(cfg *Config) {
	hydrate := func(p *ProviderConfig, envKey string) {
		if key := os.Getenv(envKey); key != "" {
			p.Enabled = true
			if p.APIKey == "" {
				p.APIKey = key
			}
		}
	}

	hydrate(&cfg.Providers.OpenAI, "OPENAI_API_KEY")
	hydrate(&cfg.Providers.Anthropic, "ANTHROPIC_API_KEY")
	hydrate(&cfg.Providers.Gemini, "GEMINI_API_KEY")
	hydrate(&cfg.Providers.DeepSeek, "DEEPSEEK_API_KEY")
	hydrate(&cfg.Providers.OpenRouter, "OPENROUTER_API_KEY")
	hydrate(&cfg.Providers.Groq, "GROQ_API_KEY")
	hydrate(&cfg.Providers.Opencode, "OPENCODE_API_KEY")
	hydrate(&cfg.Providers.NvidiaNim, "NVIDIA_NIM_API_KEY")
	hydrate(&cfg.Providers.NousPortal, "NOUS_PORTAL_API_KEY")
	hydrate(&cfg.Providers.XAI, "XAI_API_KEY")
	hydrate(&cfg.Providers.AgnesAI, "AGNES_API_KEY")

	// Ollama: local by default; OLLAMA_API_KEY enables Ollama Cloud when no host is set.
	// OLLAMA_BASE_URL only overrides the host (compose always sets a default — do not
	// treat it as an enable signal by itself).
	ollama := &cfg.Providers.Ollama
	wasEnabled := ollama.Enabled
	hydrate(ollama, "OLLAMA_API_KEY")
	if base := os.Getenv("OLLAMA_BASE_URL"); base != "" && (ollama.Enabled || ollama.APIKey != "") {
		ollama.BaseURL = base
	} else if !wasEnabled && ollama.APIKey != "" && isDefaultLocalOllamaBaseURL(ollama.BaseURL) {
		ollama.BaseURL = ollamaCloudBaseURL
	}
}

// hydrateProviderModelsFromEnv fills providers.*.models from comma-separated
// CONDUCTOR_PROVIDERS_<NAME>_MODELS when the YAML list is empty.
func hydrateProviderModelsFromEnv(cfg *Config) {
	apply := func(p *ProviderConfig, envKey string) {
		if len(p.Models) > 0 {
			return
		}
		raw := strings.TrimSpace(os.Getenv(envKey))
		if raw == "" {
			return
		}
		p.Models = splitCSV(raw)
	}

	apply(&cfg.Providers.OpenAI, "CONDUCTOR_PROVIDERS_OPENAI_MODELS")
	apply(&cfg.Providers.Anthropic, "CONDUCTOR_PROVIDERS_ANTHROPIC_MODELS")
	apply(&cfg.Providers.Gemini, "CONDUCTOR_PROVIDERS_GEMINI_MODELS")
	apply(&cfg.Providers.DeepSeek, "CONDUCTOR_PROVIDERS_DEEPSEEK_MODELS")
	apply(&cfg.Providers.OpenRouter, "CONDUCTOR_PROVIDERS_OPENROUTER_MODELS")
	apply(&cfg.Providers.Groq, "CONDUCTOR_PROVIDERS_GROQ_MODELS")
	apply(&cfg.Providers.Ollama, "CONDUCTOR_PROVIDERS_OLLAMA_MODELS")
	apply(&cfg.Providers.LMStudio, "CONDUCTOR_PROVIDERS_LMSTUDIO_MODELS")
	apply(&cfg.Providers.Opencode, "CONDUCTOR_PROVIDERS_OPENCODE_MODELS")
	apply(&cfg.Providers.NvidiaNim, "CONDUCTOR_PROVIDERS_NVIDIA_NIM_MODELS")
	apply(&cfg.Providers.NousPortal, "CONDUCTOR_PROVIDERS_NOUS_PORTAL_MODELS")
	apply(&cfg.Providers.XAI, "CONDUCTOR_PROVIDERS_XAI_MODELS")
	apply(&cfg.Providers.AgnesAI, "CONDUCTOR_PROVIDERS_AGNESAI_MODELS")
}

func splitCSV(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// applyDefaultCuratedModels fills an empty NIM models list when curated_only is on.
func applyDefaultCuratedModels(cfg *Config) {
	if !cfg.Catalog.CuratedOnly {
		return
	}
	if !cfg.Providers.NvidiaNim.Enabled {
		return
	}
	if len(cfg.Providers.NvidiaNim.Models) > 0 {
		return
	}
	cfg.Providers.NvidiaNim.Models = append([]string(nil), DefaultNvidiaNimCuratedModels...)
}

// isDefaultLocalOllamaBaseURL reports whether u is empty or the built-in local Ollama host.
func isDefaultLocalOllamaBaseURL(u string) bool {
	switch strings.TrimRight(strings.TrimSpace(u), "/") {
	case "",
		"http://localhost:11434",
		"http://localhost:11434/v1",
		"http://127.0.0.1:11434",
		"http://127.0.0.1:11434/v1":
		return true
	default:
		return false
	}
}

// validate validates the configuration
func validate(cfg *Config) error {
	// API key is required
	if cfg.APIKey == "" {
		cfg.APIKey = os.Getenv("CONDUCTOR_API_KEY")
	}
	if cfg.APIKey == "" {
		return fmt.Errorf("api_key is required (set CONDUCTOR_API_KEY environment variable)")
	}

	// Validate server config
	if cfg.Server.Port <= 0 || cfg.Server.Port > 65535 {
		return fmt.Errorf("invalid server port: %d", cfg.Server.Port)
	}

	// Validate logging level
	validLevels := map[string]bool{"debug": true, "info": true, "warn": true, "error": true}
	if !validLevels[cfg.Logging.Level] {
		return fmt.Errorf("invalid logging level: %s", cfg.Logging.Level)
	}

	// Validate logging format
	validFormats := map[string]bool{"json": true, "console": true}
	if !validFormats[cfg.Logging.Format] {
		return fmt.Errorf("invalid logging format: %s", cfg.Logging.Format)
	}

	// Validate database driver
	validDrivers := map[string]bool{"sqlite": true, "postgres": true}
	if !validDrivers[cfg.Database.Driver] {
		return fmt.Errorf("invalid database driver: %s", cfg.Database.Driver)
	}

	return nil
}
