package openai

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/EffNine/conductor/internal/provider"
)

// MapError converts an HTTP response to a normalized ProviderError.
func MapError(providerName string, resp *http.Response) *provider.ProviderError {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return provider.NewProviderError(providerName, resp.StatusCode,
			provider.ErrorTypeServerError, "failed to read error response", err)
	}

	var openAIErr struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
			Code    string `json:"code"`
			Param   string `json:"param"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &openAIErr); err == nil && openAIErr.Error.Message != "" {
		errType := openAIErr.Error.Type
		if errType == "" {
			errType = mapHTTPErrorType(resp.StatusCode, openAIErr.Error.Code)
		}
		return provider.NewProviderError(providerName, resp.StatusCode, errType, openAIErr.Error.Message, nil)
	}

	return provider.NewProviderError(providerName, resp.StatusCode,
		mapHTTPErrorType(resp.StatusCode, ""),
		fmt.Sprintf("provider returned status %d: %s", resp.StatusCode, string(body)), nil)
}

func mapHTTPErrorType(statusCode int, code string) string {
	switch statusCode {
	case 400:
		return provider.ErrorTypeInvalidRequest
	case 401, 403:
		return provider.ErrorTypeAuthentication
	case 404:
		return provider.ErrorTypeInvalidRequest
	case 408:
		return provider.ErrorTypeProviderUnavailable
	case 409:
		return provider.ErrorTypeInvalidRequest
	case 422:
		return provider.ErrorTypeInvalidRequest
	case 429:
		return provider.ErrorTypeRateLimit
	case 500:
		if strings.EqualFold(code, "context_length_exceeded") {
			return provider.ErrorTypeContextLength
		}
		return provider.ErrorTypeServerError
	case 502:
		return provider.ErrorTypeProviderUnavailable
	case 503:
		return provider.ErrorTypeProviderUnavailable
	case 504:
		return provider.ErrorTypeProviderUnavailable
	default:
		return provider.ErrorTypeServerError
	}
}

// ParseAuthError extracts error details from an OpenAI auth response.
func ParseAuthError(body []byte) (string, string) {
	msg, errType, code, ok := parseOpenAIError(body)
	if !ok {
		return "", ""
	}
	if code == "" {
		code = errType
	}
	return msg, code
}

func parseOpenAIError(body []byte) (message, errType, code string, ok bool) {
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

// BuildAuthHeader constructs the Authorization header value.
func BuildAuthHeader(apiKey string) string {
	return "Bearer " + apiKey
}

// ValidateAuth checks that authentication is configured.
func ValidateAuth(apiKey string) error {
	if apiKey == "" {
		return fmt.Errorf("OpenAI API key is required")
	}
	return nil
}
