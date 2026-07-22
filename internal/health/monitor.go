package health

import (
	"context"
	"sync"
	"time"

	"github.com/EffNine/conductor/internal/provider"
	"go.uber.org/zap"
)

// Monitor periodically checks provider health
type Monitor struct {
	registry *provider.Registry
	logger   *zap.Logger
	interval time.Duration
	timeout  time.Duration
	statuses map[string]*provider.HealthStatus
	mu       sync.RWMutex
	stopCh   chan struct{}
}

// NewMonitor creates a new health monitor
func NewMonitor(registry *provider.Registry, logger *zap.Logger, interval, timeout time.Duration) *Monitor {
	return &Monitor{
		registry: registry,
		logger:   logger,
		interval: interval,
		timeout:  timeout,
		statuses: make(map[string]*provider.HealthStatus),
		stopCh:   make(chan struct{}),
	}
}

// Start begins periodic health checks
func (m *Monitor) Start() {
	go func() {
		ticker := time.NewTicker(m.interval)
		defer ticker.Stop()

		// Run initial check immediately
		m.checkAll()

		for {
			select {
			case <-ticker.C:
				m.checkAll()
			case <-m.stopCh:
				return
			}
		}
	}()
}

// Stop stops periodic health checks
func (m *Monitor) Stop() {
	close(m.stopCh)
}

// checkAll checks health of all providers
func (m *Monitor) checkAll() {
	providers := m.registry.All()

	for _, p := range providers {
		go func(p provider.Provider) {
			ctx, cancel := context.WithTimeout(context.Background(), m.timeout)
			defer cancel()

			start := time.Now()
			status, err := p.HealthCheck(ctx)
			duration := time.Since(start)

			m.mu.Lock()
			if err != nil || status == nil {
				m.statuses[p.Name()] = &provider.HealthStatus{
					Provider:  p.Name(),
					IsHealthy: false,
					LatencyMs: duration.Milliseconds(),
					LastError: err.Error(),
					CheckedAt: time.Now(),
				}
				m.logger.Warn("provider unhealthy",
					zap.String("provider", p.Name()),
					zap.Error(err),
				)
			} else {
				m.statuses[p.Name()] = status
				m.logger.Debug("provider healthy",
					zap.String("provider", p.Name()),
					zap.Int64("latency_ms", status.LatencyMs),
				)
			}
			m.mu.Unlock()
		}(p)
	}
}

// GetStatus returns the health status of a provider
func (m *Monitor) GetStatus(providerName string) *provider.HealthStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.statuses[providerName]
}

// GetAllStatuses returns health statuses of all providers
func (m *Monitor) GetAllStatuses() []*provider.HealthStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()

	statuses := make([]*provider.HealthStatus, 0, len(m.statuses))
	for _, s := range m.statuses {
		statuses = append(statuses, s)
	}
	return statuses
}

// IsHealthy returns true if all providers are healthy
func (m *Monitor) IsHealthy() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, s := range m.statuses {
		if !s.IsHealthy {
			return false
		}
	}
	return true
}
