package config

// Redacted returns a shallow copy of c with all API keys replaced by "[REDACTED]".
// The original config is not modified.
func (c *Config) Redacted() *Config {
	if c == nil {
		return nil
	}
	p := c.Providers
	return &Config{
		Server: c.Server,
		APIKey: "[REDACTED]",
		Providers: ProvidersConfig{
			OpenAI:     redactProvider(p.OpenAI),
			Anthropic:  redactProvider(p.Anthropic),
			Gemini:     redactProvider(p.Gemini),
			DeepSeek:   redactProvider(p.DeepSeek),
			OpenRouter: redactProvider(p.OpenRouter),
			Groq:       redactProvider(p.Groq),
			Ollama:     redactProvider(p.Ollama),
			LMStudio:   redactProvider(p.LMStudio),
			Opencode:   redactProvider(p.Opencode),
			NvidiaNim:  redactProvider(p.NvidiaNim),
			NousPortal: redactProvider(p.NousPortal),
			XAI:        redactProvider(p.XAI),
			AgnesAI:    redactProvider(p.AgnesAI),
			KiloCode:   redactProvider(p.KiloCode),
			Mistral:    redactProvider(p.Mistral),
			ZAI:        redactProvider(p.ZAI),
			Cerebras:   redactProvider(p.Cerebras),
			Requesty:   redactProvider(p.Requesty),
			Cloudflare: redactProvider(p.Cloudflare),
		},
		Catalog:             c.Catalog,
		Routes:              c.Routes,
		Aliases:             c.Aliases,
		Fallbacks:           c.Fallbacks,
		Retry:               c.Retry,
		Database:            c.Database,
		Logging:             c.Logging,
		RateLimit:           c.RateLimit,
		Health:              c.Health,
		Usage:               c.Usage,
		Cost:                c.Cost,
		Routing:             c.Routing,
		Circuit:             c.Circuit,
		Cache:               c.Cache,
		Stream:              c.Stream,
		APIKeyJustGenerated: c.APIKeyJustGenerated,
		DisplayNames:        c.DisplayNames,
		Agent:               c.Agent,
	}
}

func redactProvider(p ProviderConfig) ProviderConfig {
	if p.APIKey != "" {
		p.APIKey = "[REDACTED]"
	}
	return p
}
