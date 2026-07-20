package config

import (
	"testing"
)

func TestAutoEnableProviders_OllamaCloudAPIKey(t *testing.T) {
	t.Setenv("OLLAMA_API_KEY", "ollama-cloud-key")
	t.Setenv("OLLAMA_BASE_URL", "")

	cfg := &Config{}
	cfg.Providers.Ollama.BaseURL = defaultOllamaBaseURL

	autoEnableProviders(cfg)

	if !cfg.Providers.Ollama.Enabled {
		t.Fatal("expected ollama enabled when OLLAMA_API_KEY is set")
	}
	if cfg.Providers.Ollama.APIKey != "ollama-cloud-key" {
		t.Fatalf("APIKey = %q, want ollama-cloud-key", cfg.Providers.Ollama.APIKey)
	}
	if cfg.Providers.Ollama.BaseURL != ollamaCloudBaseURL {
		t.Fatalf("BaseURL = %q, want %q", cfg.Providers.Ollama.BaseURL, ollamaCloudBaseURL)
	}
}

func TestAutoEnableProviders_OllamaCloudLegacyLocalBaseURL(t *testing.T) {
	t.Setenv("OLLAMA_API_KEY", "ollama-cloud-key")
	t.Setenv("OLLAMA_BASE_URL", "")

	cfg := &Config{}
	cfg.Providers.Ollama.BaseURL = "http://localhost:11434" // pre-/v1 default

	autoEnableProviders(cfg)

	if cfg.Providers.Ollama.BaseURL != ollamaCloudBaseURL {
		t.Fatalf("BaseURL = %q, want %q", cfg.Providers.Ollama.BaseURL, ollamaCloudBaseURL)
	}
}

func TestAutoEnableProviders_OllamaBaseURLAloneDoesNotEnable(t *testing.T) {
	t.Setenv("OLLAMA_API_KEY", "")
	t.Setenv("OLLAMA_BASE_URL", "http://host.docker.internal:11434/v1")

	cfg := &Config{}
	cfg.Providers.Ollama.BaseURL = defaultOllamaBaseURL

	autoEnableProviders(cfg)

	if cfg.Providers.Ollama.Enabled {
		t.Fatal("OLLAMA_BASE_URL alone must not enable ollama (compose ships a default)")
	}
	if cfg.Providers.Ollama.BaseURL != defaultOllamaBaseURL {
		t.Fatalf("BaseURL = %q, want %q while disabled", cfg.Providers.Ollama.BaseURL, defaultOllamaBaseURL)
	}
}

func TestAutoEnableProviders_OllamaBaseURLOverridesCloud(t *testing.T) {
	t.Setenv("OLLAMA_API_KEY", "ollama-cloud-key")
	t.Setenv("OLLAMA_BASE_URL", "http://host.docker.internal:11434/v1")

	cfg := &Config{}
	cfg.Providers.Ollama.BaseURL = defaultOllamaBaseURL

	autoEnableProviders(cfg)

	if !cfg.Providers.Ollama.Enabled {
		t.Fatal("expected ollama enabled")
	}
	want := "http://host.docker.internal:11434/v1"
	if cfg.Providers.Ollama.BaseURL != want {
		t.Fatalf("BaseURL = %q, want %q (OLLAMA_BASE_URL should win)", cfg.Providers.Ollama.BaseURL, want)
	}
	if cfg.Providers.Ollama.APIKey != "ollama-cloud-key" {
		t.Fatalf("APIKey = %q, want ollama-cloud-key", cfg.Providers.Ollama.APIKey)
	}
}

func TestAutoEnableProviders_OllamaBaseURLOverridesWhenYAMLEnabled(t *testing.T) {
	t.Setenv("OLLAMA_API_KEY", "")
	t.Setenv("OLLAMA_BASE_URL", "http://host.docker.internal:11434/v1")

	cfg := &Config{}
	cfg.Providers.Ollama.Enabled = true
	cfg.Providers.Ollama.BaseURL = defaultOllamaBaseURL

	autoEnableProviders(cfg)

	want := "http://host.docker.internal:11434/v1"
	if cfg.Providers.Ollama.BaseURL != want {
		t.Fatalf("BaseURL = %q, want %q", cfg.Providers.Ollama.BaseURL, want)
	}
}

func TestAutoEnableProviders_OllamaKeepsExplicitLocalWhenAlreadyEnabled(t *testing.T) {
	t.Setenv("OLLAMA_API_KEY", "ignored-locally")
	t.Setenv("OLLAMA_BASE_URL", "")

	cfg := &Config{}
	cfg.Providers.Ollama.Enabled = true
	cfg.Providers.Ollama.BaseURL = defaultOllamaBaseURL

	autoEnableProviders(cfg)

	if cfg.Providers.Ollama.BaseURL != defaultOllamaBaseURL {
		t.Fatalf("BaseURL = %q, want %q (already-enabled local must not switch to cloud)",
			cfg.Providers.Ollama.BaseURL, defaultOllamaBaseURL)
	}
	if cfg.Providers.Ollama.APIKey != "ignored-locally" {
		t.Fatalf("APIKey = %q, want ignored-locally", cfg.Providers.Ollama.APIKey)
	}
}

func TestAutoEnableProviders_OllamaPreservesCustomBaseURL(t *testing.T) {
	t.Setenv("OLLAMA_API_KEY", "some-key")
	t.Setenv("OLLAMA_BASE_URL", "")

	cfg := &Config{}
	cfg.Providers.Ollama.BaseURL = "http://ollama.internal:11434/v1"

	autoEnableProviders(cfg)

	want := "http://ollama.internal:11434/v1"
	if cfg.Providers.Ollama.BaseURL != want {
		t.Fatalf("BaseURL = %q, want %q", cfg.Providers.Ollama.BaseURL, want)
	}
}

func TestAutoEnableProviders_OllamaYAMLAPIKeyWins(t *testing.T) {
	t.Setenv("OLLAMA_API_KEY", "from-env")
	t.Setenv("OLLAMA_BASE_URL", "")

	cfg := &Config{}
	cfg.Providers.Ollama.APIKey = "from-yaml"
	cfg.Providers.Ollama.BaseURL = defaultOllamaBaseURL

	autoEnableProviders(cfg)

	if cfg.Providers.Ollama.APIKey != "from-yaml" {
		t.Fatalf("APIKey = %q, want from-yaml", cfg.Providers.Ollama.APIKey)
	}
	// Env still enables the provider; with no prior enable + local default host → cloud.
	if cfg.Providers.Ollama.BaseURL != ollamaCloudBaseURL {
		t.Fatalf("BaseURL = %q, want %q", cfg.Providers.Ollama.BaseURL, ollamaCloudBaseURL)
	}
}

func TestIsDefaultLocalOllamaBaseURL(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"", true},
		{"http://localhost:11434", true},
		{"http://localhost:11434/", true},
		{"http://localhost:11434/v1", true},
		{"http://localhost:11434/v1/", true},
		{"http://127.0.0.1:11434/v1", true},
		{"https://ollama.com/v1", false},
		{"http://host.docker.internal:11434/v1", false},
	}
	for _, tc := range cases {
		if got := isDefaultLocalOllamaBaseURL(tc.in); got != tc.want {
			t.Errorf("isDefaultLocalOllamaBaseURL(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}
