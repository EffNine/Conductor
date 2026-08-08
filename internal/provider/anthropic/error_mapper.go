package anthropic

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/EffNine/conductor/internal/provider"
)

// MapError converts an HTTP response to a normalized ProviderError using Anthropic error format.
func MapError(providerName string, resp *http.Response) *provider.ProviderError {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return provider.NewProviderError(providerName, resp.StatusCode,
			provider.ErrorTypeServerError, "failed to read error response", err)
	}

	var anthropicErr struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
			Code    string `json:"code"`
			Param   string `json:"param"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &anthropicErr); err == nil && anthropicErr.Error.Message != "" {
		errType := anthropicErr.Error.Type
		if errType == "" {
			errType = mapAnthropicErrorCode(anthropicErr.Error.Code)
		}
		if errType != "" {
			errType = mapAnthropicErrorType(errType)
		}
		if errType == "" {
			errType = mapHTTPErrorType(resp.StatusCode)
		}
		return provider.NewProviderError(providerName, resp.StatusCode, errType, anthropicErr.Error.Message, nil)
	}

	return provider.NewProviderError(providerName, resp.StatusCode,
		mapHTTPErrorType(resp.StatusCode),
		fmt.Sprintf("provider returned status %d: %s", resp.StatusCode, string(body)), nil)
}

func mapAnthropicErrorCode(code string) string {
	switch code {
	case "authentication_error":
		return provider.ErrorTypeAuthentication
	case "permission_error":
		return provider.ErrorTypeAuthentication
	case "rate_limit_error":
		return provider.ErrorTypeRateLimit
	case "overloaded_error":
		return provider.ErrorTypeProviderUnavailable
	case "invalid_request_error":
		return provider.ErrorTypeInvalidRequest
	default:
		return ""
	}
}

func mapAnthropicErrorType(errType string) string {
	switch errType {
	case "authentication_error", "permission_error":
		return provider.ErrorTypeAuthentication
	case "rate_limit_error":
		return provider.ErrorTypeRateLimit
	case "overloaded_error":
		return provider.ErrorTypeProviderUnavailable
	case "invalid_request_error":
		return provider.ErrorTypeInvalidRequest
	default:
		return errType
	}
}

func mapHTTPErrorType(statusCode int) string {
	switch statusCode {
	case http.StatusBadRequest:
		return provider.ErrorTypeInvalidRequest
	case http.StatusUnauthorized, http.StatusForbidden:
		return provider.ErrorTypeAuthentication
	case http.StatusTooManyRequests:
		return provider.ErrorTypeRateLimit
	case http.StatusNotFound:
		return provider.ErrorTypeInvalidRequest
	case http.StatusRequestTimeout, http.StatusGatewayTimeout:
		return provider.ErrorTypeProviderUnavailable
	case http.StatusInternalServerError:
		return provider.ErrorTypeServerError
	case http.StatusBadGateway, http.StatusServiceUnavailable:
		return provider.ErrorTypeProviderUnavailable
	default:
		return provider.ErrorTypeServerError
	}
}

// ParseAuthError extracts error details from an Anthropic auth response.
func ParseAuthError(body []byte) (string, string) {
	msg, errType, _, ok := parseAnthropicError(body)
	if !ok {
		return "", ""
	}
	return msg, errType
}

func parseAnthropicError(body []byte) (message, errType, code string, ok bool) {
	var errResp struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
			Code    string `json:"code"`
			Param   string `json:"param"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &errResp); err != nil || errResp.Error.Message == "" {
		return "", "", "", false
	}
	return errResp.Error.Message, errResp.Error.Type, errResp.Error.Code, true
}

// ValidateAuth checks that authentication is configured.
func ValidateAuth(apiKey string) error {
	if apiKey == "" {
		return fmt.Errorf("Anthropic API key is required")
	}
	return nil
}

// BuildAuthHeader is provided for compatibility but Anthropic uses x-api-key directly.
func BuildAuthHeader(apiKey string) string {
	return apiKey
}

// isRetryableError determines if an error is retryable.
func isRetryableError(err *provider.ProviderError) bool {
	if err == nil {
		return false
	}
	switch err.Type {
	case provider.ErrorTypeRateLimit,
		provider.ErrorTypeProviderUnavailable,
		provider.ErrorTypeServerError:
		return true
	default:
		return false
	}
}

// isAnthropicContextExceeded checks if the error indicates context length exceeded.
func isAnthropicContextExceeded(err *provider.ProviderError) bool {
	if err == nil {
		return false
	}
	lower := strings.ToLower(err.Message)
	return strings.Contains(lower, "context length") ||
		strings.Contains(lower, "context_length") ||
		strings.Contains(lower, "too many tokens") ||
		strings.Contains(lower, "maximum context")
}
