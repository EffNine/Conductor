package anthropic

// AuthConfig holds Anthropic authentication configuration.
type AuthConfig struct {
	APIKey      string
	APIVersion  string
	BetaHeaders []string
}

// DefaultAPIVersion is the Anthropic API version used when none is configured.
const DefaultAPIVersion = "2023-06-01"

// NewAuthConfig creates an AuthConfig with sensible defaults.
func NewAuthConfig(apiKey string) *AuthConfig {
	return &AuthConfig{
		APIKey:     apiKey,
		APIVersion: DefaultAPIVersion,
	}
}
