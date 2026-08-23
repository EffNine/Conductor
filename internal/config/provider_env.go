package config

import (
	"fmt"
	"os"
	"strings"
)

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
	hydrate(&cfg.Providers.KiloCode, "KILO_API_KEY")
	hydrate(&cfg.Providers.Mistral, "MISTRAL_API_KEY")
	hydrate(&cfg.Providers.ZAI, "ZAI_API_KEY")
	hydrate(&cfg.Providers.Cerebras, "CEREBRAS_API_KEY")
	hydrate(&cfg.Providers.Requesty, "REQUESTY_API_KEY")

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
	apply(&cfg.Providers.KiloCode, "CONDUCTOR_PROVIDERS_KILOCODE_MODELS")
	apply(&cfg.Providers.Mistral, "CONDUCTOR_PROVIDERS_MISTRAL_MODELS")
	apply(&cfg.Providers.ZAI, "CONDUCTOR_PROVIDERS_ZAI_MODELS")
	apply(&cfg.Providers.Cerebras, "CONDUCTOR_PROVIDERS_CEREBRAS_MODELS")
	apply(&cfg.Providers.Requesty, "CONDUCTOR_PROVIDERS_REQUESTY_MODELS")
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
	if err := resolveAPIKey(cfg); err != nil {
		return err
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
