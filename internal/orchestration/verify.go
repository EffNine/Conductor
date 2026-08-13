package orchestration

import (
	"context"
	"strings"
)

// VerificationResult holds the outcome of a verification step.
type VerificationResult struct {
	Success      bool   `json:"success"`
	Message      string `json:"message"`
	VerifiedAt   string `json:"verified_at"`
}

// VerifyFunc is a function that verifies a task result.
type VerifyFunc func(ctx context.Context, input, output string, intent string) (*VerificationResult, error)

// DefaultVerifier provides basic verification based on intent type.
func DefaultVerifier(ctx context.Context, input, output, intent string) (*VerificationResult, error) {
	_ = ctx
	result := &VerificationResult{}

	switch intent {
	case "coding", "elite":
		// Check for common code indicators.
		if hasCodeIndicator(output) {
			result.Success = true
			result.Message = "output contains code indicators"
		} else if len(output) > 10 {
			result.Success = true
			result.Message = "output has sufficient length for coding task"
		} else {
			result.Success = false
			result.Message = "output too short for coding task"
		}
	case "debugging":
		if strings.Contains(strings.ToLower(output), "fix") || strings.Contains(strings.ToLower(output), "bug") || len(output) > 20 {
			result.Success = true
			result.Message = "debugging response looks reasonable"
		} else {
			result.Success = false
			result.Message = "response lacks debugging content"
		}
	case "research", "analysis":
		if len(output) > 50 {
			result.Success = true
			result.Message = "research output has sufficient depth"
		} else {
			result.Success = false
			result.Message = "research output too shallow"
		}
	default:
		// General: check for non-empty meaningful output.
		if len(strings.TrimSpace(output)) > 5 {
			result.Success = true
			result.Message = "output is non-empty"
		} else {
			result.Success = false
			result.Message = "output is empty or too short"
		}
	}

	result.VerifiedAt = "now"
	return result, nil
}

func hasCodeIndicator(s string) bool {
	indicators := []string{
		"```", "function ", "def ", "class ", "import ", "package ",
		"const ", "let ", "var ", "return ", "if ", "for ", "while ",
		"func ", "struct ", "interface ", "async ", "await ", "yield",
	}
	for _, ind := range indicators {
		if strings.Contains(s, ind) {
			return true
		}
	}
	return false
}
