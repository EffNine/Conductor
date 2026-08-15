package provider

import "time"

// Metadata holds static information about a provider.
type Metadata struct {
	Name             string
	DisplayName      string
	Description      string
	BaseURL          string
	APIVersion       string
	Website          string
	DocumentationURL string
	Capabilities     Capabilities
	Models           []string // Known model IDs supported by this provider
	MaxContextLength int      // 0 means unknown
	Pricing          map[string]PricingInfo
	RegistrationTime time.Time
	Enabled          bool
}

// Capabilities describes what a provider supports.
type Capabilities struct {
	Streaming   bool
	Vision      bool
	Reasoning   bool
	ToolCalling bool
	Structured  bool
	LongContext bool
	Embeddings  bool
	Images      bool
	Audio       bool
	Functions   bool
}

// NewMetadata creates a Metadata with the given name and capabilities.
func NewMetadata(name string, caps Capabilities) Metadata {
	return Metadata{
		Name:             name,
		Capabilities:     caps,
		RegistrationTime: time.Now().UTC(),
		Enabled:          true,
	}
}

// HasCapability returns true if the provider has the given capability.
func (m Metadata) HasCapability(cap string) bool {
	switch cap {
	case "streaming":
		return m.Capabilities.Streaming
	case "vision":
		return m.Capabilities.Vision
	case "reasoning":
		return m.Capabilities.Reasoning
	case "tools":
		return m.Capabilities.ToolCalling || m.Capabilities.Functions
	case "structured":
		return m.Capabilities.Structured
	case "long-context":
		return m.Capabilities.LongContext
	case "embeddings":
		return m.Capabilities.Embeddings
	case "images":
		return m.Capabilities.Images
	case "audio":
		return m.Capabilities.Audio
	default:
		return false
	}
}

// SupportedCapabilities returns a list of capability names this provider supports.
func (m Metadata) SupportedCapabilities() []string {
	caps := make([]string, 0, 10)
	if m.Capabilities.Streaming {
		caps = append(caps, "streaming")
	}
	if m.Capabilities.Vision {
		caps = append(caps, "vision")
	}
	if m.Capabilities.Reasoning {
		caps = append(caps, "reasoning")
	}
	if m.Capabilities.ToolCalling || m.Capabilities.Functions {
		caps = append(caps, "tools")
	}
	if m.Capabilities.Structured {
		caps = append(caps, "structured")
	}
	if m.Capabilities.LongContext {
		caps = append(caps, "long-context")
	}
	if m.Capabilities.Embeddings {
		caps = append(caps, "embeddings")
	}
	if m.Capabilities.Images {
		caps = append(caps, "images")
	}
	if m.Capabilities.Audio {
		caps = append(caps, "audio")
	}
	return caps
}

// GetMetadata returns metadata for a provider.
// Providers implementing MetadataProvider return their metadata directly.
// Otherwise, default metadata is constructed from the provider name.
func GetMetadata(p Provider) Metadata {
	if mp, ok := p.(MetadataProvider); ok {
		meta := mp.GetMetadata()
		meta.Name = p.Name()
		if meta.RegistrationTime.IsZero() {
			meta.RegistrationTime = time.Now().UTC()
		}
		return meta
	}
	return DefaultMetadata(p.Name())
}

// MetadataProvider is an optional interface that providers can implement
// to supply rich metadata about themselves.
type MetadataProvider interface {
	GetMetadata() Metadata
}

// DefaultMetadata returns default metadata for a provider name.
func DefaultMetadata(name string) Metadata {
	caps := defaultCapabilities(name)
	return Metadata{
		Name:             name,
		DisplayName:      displayName(name),
		Description:      description(name),
		Capabilities:     caps,
		RegistrationTime: time.Now().UTC(),
		Enabled:          true,
	}
}

func defaultCapabilities(name string) Capabilities {
	switch name {
	case "openai":
		return Capabilities{Streaming: true, Vision: true, Reasoning: true, ToolCalling: true, Structured: true, LongContext: true, Embeddings: true}
	case "anthropic":
		return Capabilities{Streaming: true, Reasoning: true, ToolCalling: true, LongContext: true, Images: true}
	case "gemini":
		return Capabilities{Streaming: true, Vision: true, ToolCalling: true, LongContext: true, Images: true}
	case "deepseek":
		return Capabilities{Streaming: true, Reasoning: true, LongContext: true}
	case "openrouter":
		return Capabilities{Streaming: true, Vision: true, Reasoning: true, ToolCalling: true, Structured: true, LongContext: true}
	case "groq":
		return Capabilities{Streaming: true, ToolCalling: true, LongContext: true}
	case "ollama":
		return Capabilities{Streaming: true, LongContext: false}
	case "lmstudio":
		return Capabilities{Streaming: true, LongContext: false}
	case "opencode":
		return Capabilities{Streaming: true, Vision: true, Reasoning: true, ToolCalling: true, LongContext: true}
	case "nvidia_nim":
		return Capabilities{Streaming: true, Vision: true, Reasoning: true, ToolCalling: true, LongContext: true}
	case "nous_portal":
		return Capabilities{Streaming: true, ToolCalling: true, LongContext: false}
	case "xai":
		return Capabilities{Streaming: true, Reasoning: true, ToolCalling: true, LongContext: true}
	case "agnesai":
		return Capabilities{Streaming: true, ToolCalling: true, LongContext: true}
	case "generic":
		return Capabilities{Streaming: true, ToolCalling: true}
	case "kilocode":
		return Capabilities{Streaming: true, Vision: true, Reasoning: true, ToolCalling: true, Structured: true, LongContext: true, Embeddings: true}
	case "mistral":
		return Capabilities{Streaming: true, Vision: true, Reasoning: true, ToolCalling: true, Structured: true, LongContext: true, Embeddings: true}
	case "zai":
		return Capabilities{Streaming: true, Vision: true, Reasoning: true, ToolCalling: true, Structured: true, LongContext: true}
	case "cerebras":
		return Capabilities{Streaming: true, Reasoning: true, ToolCalling: true, LongContext: true}
	case "requesty":
		return Capabilities{Streaming: true, Vision: true, Reasoning: true, ToolCalling: true, Structured: true, LongContext: true, Embeddings: true}
	case "cloudflare":
		return Capabilities{Streaming: false, Reasoning: true, LongContext: true}
	default:
		return Capabilities{Streaming: true}
	}
}

func displayName(name string) string {
	switch name {
	case "openai":
		return "OpenAI"
	case "anthropic":
		return "Anthropic"
	case "gemini":
		return "Google Gemini"
	case "deepseek":
		return "DeepSeek"
	case "openrouter":
		return "OpenRouter"
	case "groq":
		return "Groq"
	case "ollama":
		return "Ollama"
	case "lmstudio":
		return "LM Studio"
	case "opencode":
		return "OpenCode"
	case "nvidia_nim":
		return "NVIDIA NIM"
	case "nous_portal":
		return "Nous Portal"
	case "xai":
		return "xAI"
	case "agnesai":
		return "Agnes AI"
	case "generic":
		return "Generic"
	case "kilocode":
		return "KiloCode"
	case "mistral":
		return "Mistral AI"
	case "zai":
		return "Z.AI"
	case "cerebras":
		return "Cerebras"
	case "requesty":
		return "Requesty"
	case "cloudflare":
		return "Cloudflare Workers AI"
	default:
		return name
	}
}

func description(name string) string {
	switch name {
	case "openai":
		return "OpenAI chat completion and embeddings API"
	case "anthropic":
		return "Anthropic Claude messages API"
	case "gemini":
		return "Google Gemini API"
	case "deepseek":
		return "DeepSeek chat completion API"
	case "openrouter":
		return "OpenRouter multi-provider gateway"
	case "groq":
		return "Groq fast inference API"
	case "ollama":
		return "Local Ollama inference server"
	case "lmstudio":
		return "Local LM Studio server"
	case "opencode":
		return "OpenCode Zen multi-provider gateway"
	case "nvidia_nim":
		return "NVIDIA NIM inference microservices"
	case "nous_portal":
		return "Nous Portal API"
	case "xai":
		return "xAI Grok API"
	case "agnesai":
		return "Agnes AI API"
	case "generic":
		return "Generic OpenAI-compatible API endpoint"
	case "kilocode":
		return "KiloCode multi-provider gateway"
	case "mistral":
		return "Mistral AI chat, embeddings, and vision API"
	case "zai":
		return "Z.AI (01.AI) GLM chat completion API"
	case "cerebras":
		return "Cerebras Wafer-Scale Engine inference API"
	case "requesty":
		return "Requesty multi-provider routing gateway"
	case "cloudflare":
		return "Cloudflare Workers AI inference on the edge"
	default:
		return "AI provider"
	}
}
