package handler

import (
	"go.uber.org/zap"

	"github.com/EffNine/conductor/internal/config"
	"github.com/EffNine/conductor/internal/resilience"
)

// retryPolicy resolves the same-provider retry policy from configuration.
//
// An unset config yields the zero-value resilience.RetryPolicy, which is
// disabled: handlers constructed without configuration (all unit tests)
// keep legacy behavior exactly. Production wiring comes from config.yaml
// via main.go's SetConfig.
func (h *Handler) retryPolicy() resilience.RetryPolicy {
	return retryPolicyFromConfig(h.cfg)
}

func retryPolicyFromConfig(cfg *config.Config) resilience.RetryPolicy {
	if cfg == nil || !cfg.Retry.Enabled {
		return resilience.RetryPolicy{}
	}
	rc := cfg.Retry
	p := resilience.RetryPolicy{
		Enabled:           true,
		MaxRetries:        rc.MaxRetries,
		InitialBackoff:    rc.InitialBackoff,
		MaxBackoff:        rc.MaxBackoff,
		BackoffMultiplier: rc.BackoffMultiplier,
		HonorRetryAfter:   rc.HonorRetryAfter,
		MaxRetryAfterWait: rc.MaxRetryAfterWait,
	}
	if p.MaxRetries < 0 {
		p.MaxRetries = 0
	}
	return p
}

// logRetries emits one structured line when a candidate needed more than
// its first attempt. Purely observability; no control flow depends on it.
func (h *Handler) logRetries(providerName string, attempts int) {
	if h.logger == nil || attempts <= 1 {
		return
	}
	h.logger.Warn("request:same_provider_retries",
		zap.String("provider", providerName),
		zap.Int("attempts", attempts),
	)
}
