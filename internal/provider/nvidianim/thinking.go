package nvidianim

import (
	"strings"

	"github.com/EffNine/conductor/internal/apitypes"
)

// thinkingProfile describes how a NIM model expects chat_template_kwargs.
type thinkingProfile int

const (
	profileNone           thinkingProfile = iota
	profileThinkingBool                   // {thinking: bool} — DeepSeek V3/R1, Kimi, some Nemotron
	profileDeepSeekV4                     // {thinking, reasoning_effort} + top-level reasoning_effort
	profileEnableThinking                 // {enable_thinking: bool} — Qwen3, QwQ, Phi, Magistral, Nemotron-3
	profileGLM                            // {enable_thinking, clear_thinking}
	profileMiniMaxM3                      // {thinking_mode: enabled|disabled}
	profileInkling                        // {reasoning_effort} inside chat_template_kwargs
)

type thinkingConfig struct {
	profile            thinkingProfile
	sendTopLevelEffort bool // also set top-level reasoning_effort (DeepSeek V4, Kimi, Inkling)
}

// IsThinkingModel reports whether the Provider Model ID needs NIM chat_template_kwargs
// for reliable reasoning streams (empty content / hangs without them).
func IsThinkingModel(model string) bool {
	return classifyThinking(model).profile != profileNone
}

func classifyThinking(model string) thinkingConfig {
	m := strings.ToLower(strings.TrimSpace(model))
	if m == "" {
		return thinkingConfig{}
	}

	switch {
	case strings.Contains(m, "deepseek-v4"):
		return thinkingConfig{profile: profileDeepSeekV4, sendTopLevelEffort: true}

	case strings.Contains(m, "deepseek-v3"), strings.Contains(m, "deepseek-r1"):
		return thinkingConfig{profile: profileThinkingBool}

	case strings.Contains(m, "chatglm"):
		return thinkingConfig{}

	case strings.Contains(m, "z-ai/glm"), strings.Contains(m, "/glm4."), strings.Contains(m, "/glm5"),
		strings.Contains(m, "/glm-4"), strings.Contains(m, "/glm-5"):
		return thinkingConfig{profile: profileGLM}

	case strings.Contains(m, "kimi-k2.6"), strings.Contains(m, "kimi-k2.5"),
		strings.Contains(m, "kimi-k2-thinking"):
		return thinkingConfig{profile: profileThinkingBool, sendTopLevelEffort: true}

	case strings.Contains(m, "qwq-"), strings.Contains(m, "/qwq"):
		return thinkingConfig{profile: profileEnableThinking}

	case strings.Contains(m, "qwen3"):
		// Instruct-only Qwen3 variants (e.g. qwen3-next-80b-a3b-instruct) are not
		// in NIM thinking configs; only thinking/coder/235b-style IDs need kwargs.
		if strings.Contains(m, "thinking") || strings.Contains(m, "coder") || strings.Contains(m, "235b") {
			return thinkingConfig{profile: profileEnableThinking}
		}
		return thinkingConfig{}

	case strings.Contains(m, "phi") && strings.Contains(m, "reasoning"):
		return thinkingConfig{profile: profileEnableThinking}

	case strings.Contains(m, "magistral"):
		return thinkingConfig{profile: profileEnableThinking}

	case strings.Contains(m, "inkling"):
		return thinkingConfig{profile: profileInkling, sendTopLevelEffort: true}

	case strings.Contains(m, "minimax-m3"):
		return thinkingConfig{profile: profileMiniMaxM3}

	case strings.Contains(m, "nemotron"):
		if strings.Contains(m, "embed") || strings.Contains(m, "reward") ||
			strings.Contains(m, "safety") || strings.Contains(m, "parse") ||
			strings.Contains(m, "-vl-") || strings.HasSuffix(m, "-vl") {
			return thinkingConfig{}
		}
		// Nemotron-3 / nano use enable_thinking (NVIDIA NIM docs).
		if strings.Contains(m, "nemotron-3") || strings.Contains(m, "nemotron-nano") ||
			strings.Contains(m, "nvidia-nemotron-nano") {
			return thinkingConfig{profile: profileEnableThinking}
		}
		// llama-3.x-nemotron-ultra / super use thinking: true (pi-nvidia-nim).
		if strings.Contains(m, "ultra") || strings.Contains(m, "super") {
			return thinkingConfig{profile: profileThinkingBool}
		}
		return thinkingConfig{}
	}

	return thinkingConfig{}
}

func thinkingDesired(req *apitypes.ChatCompletionRequest) (enabled bool, effort string) {
	effort = "high"
	if req.ReasoningEffort != "" {
		effort = mapNIMReasoningEffort(req.ReasoningEffort)
	} else if req.Reasoning != nil && req.Reasoning.Effort != "" {
		effort = mapNIMReasoningEffort(req.Reasoning.Effort)
	} else if req.ChatTemplateKwargs != nil {
		if v, ok := req.ChatTemplateKwargs["reasoning_effort"].(string); ok && v != "" {
			effort = mapNIMReasoningEffort(v)
		}
		if thinking, ok := req.ChatTemplateKwargs["thinking"].(bool); ok && !thinking {
			return false, "none"
		}
		if enable, ok := req.ChatTemplateKwargs["enable_thinking"].(bool); ok && !enable {
			return false, "none"
		}
		if mode, ok := req.ChatTemplateKwargs["thinking_mode"].(string); ok &&
			strings.EqualFold(mode, "disabled") {
			return false, "none"
		}
	}
	if effort == "none" {
		return false, "none"
	}
	return true, effort
}

// applyThinkingChatTemplate injects per-model chat_template_kwargs when omitted so
// OpenCode and other plain-OpenAI clients do not hang or aggregate empty content.
func applyThinkingChatTemplate(req *apitypes.ChatCompletionRequest) {
	if req == nil {
		return
	}
	cfg := classifyThinking(req.Model)
	if cfg.profile == profileNone {
		return
	}

	enabled, effort := thinkingDesired(req)
	kwargs := map[string]any{}
	if req.ChatTemplateKwargs != nil {
		for k, v := range req.ChatTemplateKwargs {
			kwargs[k] = v
		}
	}

	switch cfg.profile {
	case profileDeepSeekV4:
		if _, ok := kwargs["thinking"]; !ok {
			kwargs["thinking"] = enabled
		}
		if enabled {
			if _, ok := kwargs["reasoning_effort"]; !ok {
				kwargs["reasoning_effort"] = effort
			}
		}
	case profileThinkingBool:
		if _, ok := kwargs["thinking"]; !ok {
			kwargs["thinking"] = enabled
		}
	case profileEnableThinking:
		if _, ok := kwargs["enable_thinking"]; !ok {
			kwargs["enable_thinking"] = enabled
		}
	case profileGLM:
		if _, ok := kwargs["enable_thinking"]; !ok {
			kwargs["enable_thinking"] = enabled
		}
		if enabled {
			if _, ok := kwargs["clear_thinking"]; !ok {
				kwargs["clear_thinking"] = false
			}
		}
	case profileMiniMaxM3:
		if _, ok := kwargs["thinking_mode"]; !ok {
			if enabled {
				kwargs["thinking_mode"] = "enabled"
			} else {
				kwargs["thinking_mode"] = "disabled"
			}
		}
	case profileInkling:
		if _, ok := kwargs["reasoning_effort"]; !ok {
			kwargs["reasoning_effort"] = effort
		}
	}

	req.ChatTemplateKwargs = kwargs

	// Always normalize top-level effort for families that accept it (maps
	// minimal/xhigh onto NIM's none|high|max).
	if cfg.sendTopLevelEffort {
		req.ReasoningEffort = effort
	}
}
