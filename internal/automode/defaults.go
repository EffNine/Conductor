package automode

import (
	"github.com/novexa/gateway/internal/config"
)

// DefaultNIMProfiles are derived from the model comparison tiers for NVIDIA NIM.
// They are used when the config does not provide its own task_profiles.
func DefaultNIMProfiles() map[string]config.AutoModeProfile {
	return map[string]config.AutoModeProfile{
		"elite": {
			Models: []string{
				"mistralai/mistral-large-3-675b-instruct-2512",
				"nvidia/nemotron-3-super-120b-a12b",
				"openai/gpt-oss-120b",
				"qwen/qwen3-next-80b-a3b-instruct",
				"nvidia/llama-3.3-nemotron-super-49b-v1.5",
			},
			Weights: config.AutoModeWeights{
				Reachability: 10.0,
				Cost:         1.0,
				Latency:      2.0,
			},
		},
		"coding": {
			Models: []string{
				"stepfun-ai/step-3.7-flash",
				"meta/llama-3.1-70b-instruct",
				"nvidia/llama-3.3-nemotron-super-49b-v1",
				"stepfun-ai/step-3.5-flash",
				"openai/gpt-oss-20b",
			},
			Weights: config.AutoModeWeights{
				Reachability: 10.0,
				Cost:         2.0,
				Latency:      3.0,
			},
		},
		"reasoning": {
			Models: []string{
				"mistralai/mistral-large-3-675b-instruct-2512",
				"nvidia/nemotron-3-super-120b-a12b",
				"openai/gpt-oss-120b",
				"qwen/qwen3-next-80b-a3b-instruct",
				"nvidia/llama-3.3-nemotron-super-49b-v1.5",
			},
			Weights: config.AutoModeWeights{
				Reachability: 10.0,
				Cost:         2.0,
				Latency:      1.0,
			},
		},
		"vision": {
			Models: []string{
				"meta/llama-3.2-11b-vision-instruct",
			},
			Weights: config.AutoModeWeights{
				Reachability: 10.0,
				Cost:         1.0,
				Latency:      1.0,
			},
		},
		"fast": {
			Models: []string{
				"stepfun-ai/step-3.7-flash",
				"stepfun-ai/step-3.5-flash",
				"openai/gpt-oss-20b",
				"meta/llama-3.1-70b-instruct",
			},
			Weights: config.AutoModeWeights{
				Reachability: 10.0,
				Cost:         1.0,
				Latency:      5.0,
			},
		},
		"default": {
			Models: nil,
			Weights: config.AutoModeWeights{
				Reachability: 10.0,
				Cost:         3.0,
				Latency:      2.0,
			},
		},
	}
}

// mergeProfiles returns the built-in default profiles overlaid with any user
// task_profiles. User profiles replace defaults for the same task type, while
// unspecified task types keep their default allowlists and weights.
func mergeProfiles(user map[string]config.AutoModeProfile) map[string]config.AutoModeProfile {
	merged := DefaultNIMProfiles()
	for task, profile := range user {
		merged[task] = profile
	}
	return merged
}
