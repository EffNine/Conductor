package gemini

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/EffNine/conductor/internal/provider"
)

// geminiErrorBody is the Google standard error envelope returned by the
// generativelanguage API.
type geminiErrorBody struct {
	Error struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Status  string `json:"status"` // INVALID_ARGUMENT, UNAUTHENTICATED, ...
	} `json:"error"`
}

// MapError converts an HTTP response into a normalized ProviderError using the
// Google standard error format.
func MapError(providerName string, resp *http.Response) *provider.ProviderError {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return provider.NewProviderError(providerName, resp.StatusCode,
			provider.ErrorTypeServerError, "failed to read error response", err)
	}

	var gErr geminiErrorBody
	if err := json.Unmarshal(body, &gErr); err == nil && (gErr.Error.Status != "" || gErr.Error.Message != "") {
		status := gErr.Error.Status
		if status == "" {
			status = statusFromHTTPStatus(gErr.Error.Code)
		}
		errType := mapGeminiStatus(status)
		if errType == "" {
			errType = mapHTTPErrorType(resp.StatusCode)
		}
		msg := gErr.Error.Message
		if msg == "" {
			msg = fmt.Sprintf("provider returned status %d", resp.StatusCode)
		}
		if errType != provider.ErrorTypeContextLength && isContextMessage(msg) {
			errType = provider.ErrorTypeContextLength
		}
		return provider.NewProviderError(providerName, resp.StatusCode, errType, msg, nil)
	}

	return provider.NewProviderError(providerName, resp.StatusCode,
		mapHTTPErrorType(resp.StatusCode),
		fmt.Sprintf("provider returned status %d: %s", resp.StatusCode, string(body)), nil)
}

// mapGeminiStatus maps Google canonical status strings to ProviderError types.
func mapGeminiStatus(status string) string {
	switch status {
	case "INVALID_ARGUMENT", "FAILED_PRECONDITION", "OUT_OF_RANGE", "NOT_FOUND", "ALREADY_EXISTS":
		return provider.ErrorTypeInvalidRequest
	case "UNAUTHENTICATED", "PERMISSION_DENIED":
		return provider.ErrorTypeAuthentication
	case "RESOURCE_EXHAUSTED":
		return provider.ErrorTypeRateLimit
	case "UNAVAILABLE", "DEADLINE_EXCEEDED":
		return provider.ErrorTypeProviderUnavailable
	case "INTERNAL", "UNKNOWN", "DATA_LOSS", "ABORTED":
		return provider.ErrorTypeServerError
	default:
		return ""
	}
}

// statusFromHTTPStatus derives a status name when the JSON body lacked one.
func statusFromHTTPStatus(code int) string {
	switch code {
	case http.StatusBadRequest:
		return "INVALID_ARGUMENT"
	case http.StatusUnauthorized:
		return "UNAUTHENTICATED"
	case http.StatusForbidden:
		return "PERMISSION_DENIED"
	case http.StatusNotFound:
		return "NOT_FOUND"
	case http.StatusTooManyRequests:
		return "RESOURCE_EXHAUSTED"
	case http.StatusInternalServerError:
		return "INTERNAL"
	case http.StatusServiceUnavailable:
		return "UNAVAILABLE"
	case http.StatusGatewayTimeout:
		return "DEADLINE_EXCEEDED"
	default:
		return ""
	}
}

// mapHTTPErrorType is the fallback used when the body is not the standard
// Google error shape.
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

// ParseAuthError extracts error details from a Gemini auth response body.
func ParseAuthError(body []byte) (string, string) {
	var gErr geminiErrorBody
	if err := json.Unmarshal(body, &gErr); err != nil || gErr.Error.Message == "" {
		return "", ""
	}
	return gErr.Error.Message, gErr.Error.Status
}

// isContextMessage reports whether an error message mentions context limits.
func isContextMessage(msg string) bool {
	lower := strings.ToLower(msg)
	return strings.Contains(lower, "token limit") ||
		strings.Contains(lower, "context length") ||
		strings.Contains(lower, "context_length") ||
		strings.Contains(lower, "too many tokens") ||
		strings.Contains(lower, "maximum input") ||
		strings.Contains(lower, "input is too long")
}