package config

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/viper"
)

// DefaultAPIKeyFileName is the filename used to persist an auto-generated gateway API key.
const DefaultAPIKeyFileName = "conductor.api_key"

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
	Routing   RoutingConfig               `mapstructure:"routing"`
	Circuit   CircuitBreakerConfig        `mapstructure:"circuit_breaker"`
	Cache     CacheConfig                 `mapstructure:"cache"`
	Stream    StreamConfig                `mapstructure:"stream"`
	Execution ExecutionConfig             `mapstructure:"execution"`

	// APIKeyJustGenerated is true when Load created and persisted a new gateway key
	// because none was configured via env, YAML, or an existing key file.
	APIKeyJustGenerated bool `mapstructure:"-"`

	// DisplayNames maps ModelID → human-friendly label used in /v1/models
	// responses. Unmapped models fall back to the provider-stripped ID.
	DisplayNames map[string]string `mapstructure:"display_names"`

	// Agent controls the single-agent multi-step loop for task execution.
	Agent AgentConfig `mapstructure:"agent"`
}

// AgentConfig holds configuration for the agent loop.
type AgentConfig struct {
	// MaxSteps is the maximum number of LLM calls allowed per task. Default 10.
	MaxSteps int `mapstructure:"max_steps"`
	// WorkspaceRoot is the absolute path that file tools (read/write) treat as
	// the root boundary. Paths resolving outside this directory are rejected.
	WorkspaceRoot string `mapstructure:"workspace_root"`
	// MaxOutputBytes is the hard cap on tool output size (read_file, shell).
	// Default 65536 (64 KiB).
	MaxOutputBytes int `mapstructure:"max_output_bytes"`
	// MaxWriteBytes is the hard cap on write_file content size. Default 1048576 (1 MiB).
	MaxWriteBytes int `mapstructure:"max_write_bytes"`
	// Shell controls the shell tool security policy.
	Shell ShellConfig `mapstructure:"shell"`
	// Git controls the git tool workspace boundaries.
	Git GitConfig `mapstructure:"git"`
	// WorkerCount is the number of async task workers. 0 = sync-only (default).
	WorkerCount int `mapstructure:"worker_count"`
	// PollInterval is how often workers check for eligible tasks. Default 1s.
	PollInterval time.Duration `mapstructure:"poll_interval"`
	// LeaseDuration is how long a worker holds a task lease. Default 5m.
	LeaseDuration time.Duration `mapstructure:"lease_duration"`
}

// ShellConfig holds security parameters for the shell tool.
type ShellConfig struct {
	// Enabled enables the shell tool. Default false (most deploys should not
	// expose a shell tool unless explicitly intended).
	Enabled bool `mapstructure:"enabled"`
	// WorkingDir is the absolute path the shell tool resolves relative paths
	// against. Commands running outside this directory are rejected.
	WorkingDir string `mapstructure:"working_dir"`
	// Timeout is the per-command timeout. Default 30s.
	Timeout time.Duration `mapstructure:"timeout"`
	// MaxOutputBytes caps stdout/stderr output. Default 65536.
	MaxOutputBytes int `mapstructure:"max_output_bytes"`
	// AllowList is the allowlist of executable basenames. Empty means deny all;
	// "*" allows every executable. When non-empty, only listed programs may run.
	AllowList []string `mapstructure:"allow_list"`
	// DeniedCommands are basename patterns that are always rejected regardless
	// of AllowList. Matches are case-sensitive.
	DeniedCommands []string `mapstructure:"denied_commands"`
	// EnvWhitelist lists environment variable names that are forwarded from the
	// gateway process to the child. All other variables are stripped.
	EnvWhitelist []string `mapstructure:"env_whitelist"`
}

// GitConfig holds configuration for the read-only git tool.
type GitConfig struct {
	// Enabled enables the git tool. Default false.
	Enabled bool `mapstructure:"enabled"`
	// RepoRoot is the absolute path to the git repository root. All git commands
	// must run inside this directory.
	RepoRoot string `mapstructure:"repo_root"`
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
	KiloCode   ProviderConfig `mapstructure:"kilocode"`
	Mistral    ProviderConfig `mapstructure:"mistral"`
	ZAI        ProviderConfig `mapstructure:"zai"`
	Cerebras   ProviderConfig `mapstructure:"cerebras"`
	Requesty   ProviderConfig `mapstructure:"requesty"`
	Cloudflare ProviderConfig `mapstructure:"cloudflare"`
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

// ExecutionConfig holds request-execution reliability settings (P4.4).
type ExecutionConfig struct {
	Budget   ExecutionBudgetConfig `mapstructure:"budget"`
	Attempts AttemptsConfig        `mapstructure:"attempts"`
}

// AttemptsConfig controls execution-attempt observability (P4.4.3/P4.4.4).
type AttemptsConfig struct {
	// Enabled persists one row per candidate attempt asynchronously.
	Enabled bool `mapstructure:"enabled"`
	// Retention bounds storage: rows older than this are pruned. 0
	// disables pruning. Default 168h.
	Retention time.Duration `mapstructure:"retention"`
}

// ExecutionBudgetConfig bounds a candidate chain. Disabled budgets restore
// pre-P4.4.2 behaviour exactly.
type ExecutionBudgetConfig struct {
	Enabled            bool          `mapstructure:"enabled"`
	TotalDeadline      time.Duration `mapstructure:"total_deadline"`
	MaxTotalAttempts   int           `mapstructure:"max_total_attempts"`
	MaxEstimatedTokens int64         `mapstructure:"max_estimated_tokens"`
}

// RetryConfig configures cause-aware same-provider retries (P4.2).
// Retryability is class-driven via internal/failure: only rate_limited,
// timeout, capacity, upstream_error, and network_error are retryable;
// auth_failed, invalid_request, and unknown never are. The legacy
// RetryableStatusCodes list is retained for config compatibility but ignored.
type RetryConfig struct {
	Enabled           bool          `mapstructure:"enabled"`
	MaxRetries        int           `mapstructure:"max_retries"`
	InitialBackoff    time.Duration `mapstructure:"initial_backoff"`
	MaxBackoff        time.Duration `mapstructure:"max_backoff"`
	BackoffMultiplier float64       `mapstructure:"backoff_multiplier"`
	HonorRetryAfter   bool          `mapstructure:"honor_retry_after"`
	MaxRetryAfterWait time.Duration `mapstructure:"max_retry_after_wait"`

	// Deprecated: superseded by failure-class policy; kept so existing YAML
	// files still parse. No longer read by any component.
	RetryableStatusCodes []int `mapstructure:"retryable_status_codes"`
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
	// StrictHealthy, when true, hides every model that is not in the healthy
	// state (degraded, unknown, recovering, unhealthy). Default true: only
	// models confirmed healthy via probing are advertised. Set to false to also
	// show degraded and unprobed models.
	StrictHealthy bool `mapstructure:"strict_healthy"`
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

// CircuitBreakerConfig holds global circuit breaker settings.
type CircuitBreakerConfig struct {
	Enabled          bool          `mapstructure:"enabled"`
	FailureThreshold int           `mapstructure:"failure_threshold"`
	RecoveryTimeout  time.Duration `mapstructure:"recovery_timeout"`
	SuccessThreshold int           `mapstructure:"success_threshold"`
}

// RoutingConfig controls the intelligent routing engine.
type RoutingConfig struct {
	// Enabled turns on dynamic provider scoring. Default false (legacy static routing).
	Enabled bool `mapstructure:"enabled"`
	// Weights defines how much each scoring dimension contributes to the final score.
	Weights RoutingWeights `mapstructure:"weights"`
	// DynamicFallback appends capability-matched alternate candidates after the
	// primary route and any configured static fallbacks, so a request only
	// fails when no eligible model can serve it.
	DynamicFallback DynamicFallbackConfig `mapstructure:"dynamic_fallback"`
	// AutoResolveBareModels lets a bare model ID (no provider prefix, no
	// route, no alias) resolve when exactly one enabled provider supports it,
	// so single-provider setups work with zero routing configuration.
	// Default true.
	AutoResolveBareModels bool `mapstructure:"auto_resolve_bare_models"`
}

// DynamicFallbackConfig controls automatic category-preserving failover.
// Alternates are ranked by the same health/latency/cost/capability scorer
// used by auto mode, with mode hard filters (vision, tools, context) applied,
// so fallbacks stay within the semantic category of the request.
type DynamicFallbackConfig struct {
	// Enabled turns on dynamic fallback candidate generation. Default true.
	Enabled bool `mapstructure:"enabled"`
	// MaxCandidates bounds how many dynamic alternates may be appended per
	// request. Default 3.
	MaxCandidates int `mapstructure:"max_candidates"`
}

// RoutingWeights defines the relative importance of each scoring dimension.
// Higher weight makes that signal more influential. Weights are normalized
// internally, so absolute scale does not matter.
type RoutingWeights struct {
	Health     float64 `mapstructure:"health"`
	Latency    float64 `mapstructure:"latency"`
	Cost       float64 `mapstructure:"cost"`
	Capability float64 `mapstructure:"capability"`
}

// DefaultRoutingWeights returns the default routing weights.
func DefaultRoutingWeights() RoutingWeights {
	return RoutingWeights{
		Health:     40,
		Latency:    25,
		Cost:       15,
		Capability: 20,
	}
}

// Normalized returns weights normalized to sum to 1.
func (w RoutingWeights) Normalized() (health, latency, cost, capability float64) {
	total := w.Health + w.Latency + w.Cost + w.Capability
	if total <= 0 {
		return 0.25, 0.25, 0.25, 0.25
	}
	return w.Health / total, w.Latency / total, w.Cost / total, w.Capability / total
}

// CacheConfig controls response caching behaviour.
type CacheConfig struct {
	Enabled        bool          `mapstructure:"enabled"`
	TTL            time.Duration `mapstructure:"ttl"`
	MaxEntries     int           `mapstructure:"max_entries"`
	EvictionPolicy string        `mapstructure:"eviction_policy"`
}

// StreamConfig controls streaming behaviour.
type StreamConfig struct {
	// IdleTimeout is the maximum time a stream may go without producing a
	// chunk before it is ended as a provider timeout. It is deliberately
	// generous so long-reasoning models are never cut off while making
	// progress. Values <= 0 disable the timeout entirely.
	IdleTimeout time.Duration `mapstructure:"idle_timeout"`
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

	// Execution budget defaults (P4.4.2): generous enough that normal
	// traffic is unaffected; pathological multi-fallback chains become
	// bounded and deterministic.
	v.SetDefault("execution.budget.enabled", true)
	v.SetDefault("execution.budget.total_deadline", 120*time.Second)
	v.SetDefault("execution.budget.max_total_attempts", 8)
	v.SetDefault("execution.budget.max_estimated_tokens", 200000)
	v.SetDefault("execution.attempts.enabled", true)
	v.SetDefault("execution.attempts.retention", 168*time.Hour)

	// Retry defaults (P4.2: cause-aware same-provider retries; conservative
	// single retry with bounded backoff, honoring Retry-After hints).
	v.SetDefault("retry.enabled", true)
	v.SetDefault("retry.max_retries", 1)
	v.SetDefault("retry.initial_backoff", 250*time.Millisecond)
	v.SetDefault("retry.max_backoff", 2*time.Second)
	v.SetDefault("retry.backoff_multiplier", 2.0)
	v.SetDefault("retry.honor_retry_after", true)
	v.SetDefault("retry.max_retry_after_wait", 30*time.Second)

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
	v.SetDefault("health.models.strict_healthy", true)

	// Usage defaults
	v.SetDefault("usage.enabled", true)

	// Circuit breaker defaults
	v.SetDefault("circuit_breaker.enabled", true)
	v.SetDefault("circuit_breaker.failure_threshold", 5)
	v.SetDefault("circuit_breaker.recovery_timeout", 30*time.Second)
	v.SetDefault("circuit_breaker.success_threshold", 2)

	// Routing defaults
	v.SetDefault("routing.enabled", false)
	v.SetDefault("routing.weights.health", 40.0)
	v.SetDefault("routing.weights.latency", 25.0)
	v.SetDefault("routing.weights.cost", 15.0)
	v.SetDefault("routing.weights.capability", 20.0)
	v.SetDefault("routing.dynamic_fallback.enabled", true)
	v.SetDefault("routing.dynamic_fallback.max_candidates", 3)
	v.SetDefault("routing.auto_resolve_bare_models", true)

	// Cache defaults
	v.SetDefault("cache.enabled", true)
	v.SetDefault("cache.ttl", 10*time.Minute)
	v.SetDefault("cache.max_entries", 10000)
	v.SetDefault("cache.eviction_policy", "lru")

	// Streaming defaults
	v.SetDefault("stream.idle_timeout", 5*time.Minute)

	// Agent defaults
	v.SetDefault("agent.max_steps", 10)
	v.SetDefault("agent.max_output_bytes", 65536)
	v.SetDefault("agent.max_write_bytes", 1048576)
	v.SetDefault("agent.shell.enabled", false)
	v.SetDefault("agent.shell.timeout", 30*time.Second)
	v.SetDefault("agent.shell.max_output_bytes", 65536)
	v.SetDefault("agent.git.enabled", false)
	v.SetDefault("agent.worker_count", 0)
	v.SetDefault("agent.poll_interval", 1*time.Second)
	v.SetDefault("agent.lease_duration", 5*time.Minute)

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
