package breaker

import (
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

// State represents the circuit breaker state.
type State int32

const (
	StateClosed   State = 0
	StateOpen     State = 1
	StateHalfOpen State = 2
)

// String returns a human-readable name for the state.
func (s State) String() string {
	switch s {
	case StateClosed:
		return "closed"
	case StateOpen:
		return "open"
	case StateHalfOpen:
		return "half_open"
	default:
		return "unknown"
	}
}

// Config holds the circuit breaker configuration.
type Config struct {
	// FailureThreshold is the number of consecutive failures before opening.
	FailureThreshold int
	// RecoveryTimeout is how long to wait before transitioning from open to half-open.
	RecoveryTimeout time.Duration
	// SuccessThreshold is the number of consecutive successes in half-open before closing.
	SuccessThreshold int
}

// DefaultConfig returns sensible defaults.
func DefaultConfig() Config {
	return Config{
		FailureThreshold: 5,
		RecoveryTimeout:  30 * time.Second,
		SuccessThreshold: 2,
	}
}

// Result is returned by Allow() to indicate whether a request may proceed.
type Result int

const (
	ResultAllowed  Result = 0
	ResultRejected Result = 1
)

// ErrOpen is returned when the breaker is open and the request is rejected.
var ErrOpen = errors.New("circuit breaker is open")

// Breaker is a per-provider circuit breaker.
type Breaker struct {
	mu                   sync.Mutex
	state                State
	consecutiveFails     int
	consecutiveSucc      int
	openedAt             time.Time
	cfg                  Config
	failures             atomic.Int64
	successes            atomic.Int64
	rejections           atomic.Int64
	opens                atomic.Int64
	stateChangeCallbacks []func(State)
}

// New creates a breaker with the given config.
func New(cfg Config) *Breaker {
	if cfg.FailureThreshold <= 0 {
		cfg.FailureThreshold = DefaultConfig().FailureThreshold
	}
	if cfg.RecoveryTimeout <= 0 {
		cfg.RecoveryTimeout = DefaultConfig().RecoveryTimeout
	}
	if cfg.SuccessThreshold <= 0 {
		cfg.SuccessThreshold = DefaultConfig().SuccessThreshold
	}
	return &Breaker{
		state: StateClosed,
		cfg:   cfg,
	}
}

// Allow reports whether a request is permitted to proceed.
// Returns ResultAllowed when the circuit is closed or half-open.
// Returns ResultRejected when the circuit is open and recovery has not elapsed.
func (b *Breaker) Allow() Result {
	b.mu.Lock()
	defer b.mu.Unlock()

	oldState := b.state
	switch b.state {
	case StateClosed:
		return ResultAllowed
	case StateOpen:
		if time.Since(b.openedAt) >= b.cfg.RecoveryTimeout {
			b.state = StateHalfOpen
			b.consecutiveSucc = 0
			b.notifyStateChange(oldState, b.state)
			return ResultAllowed
		}
		b.rejections.Add(1)
		return ResultRejected
	case StateHalfOpen:
		return ResultAllowed
	default:
		return ResultRejected
	}
}

// RecordSuccess records a successful request.
func (b *Breaker) RecordSuccess() {
	b.mu.Lock()
	oldState := b.state
	switch b.state {
	case StateHalfOpen:
		b.consecutiveSucc++
		if b.consecutiveSucc >= b.cfg.SuccessThreshold {
			b.state = StateClosed
			b.consecutiveFails = 0
			b.consecutiveSucc = 0
			b.notifyStateChange(oldState, b.state)
		}
	case StateClosed:
		b.consecutiveFails = 0
	}
	b.mu.Unlock()
	b.successes.Add(1)
}

// RecordFailure records a failed request.
func (b *Breaker) RecordFailure() {
	b.mu.Lock()
	oldState := b.state
	switch b.state {
	case StateClosed:
		b.consecutiveFails++
		if b.consecutiveFails >= b.cfg.FailureThreshold {
			b.state = StateOpen
			b.openedAt = time.Now()
			b.consecutiveFails = 0
			b.consecutiveSucc = 0
			b.opens.Add(1)
			b.notifyStateChange(oldState, b.state)
		}
	case StateHalfOpen:
		b.state = StateOpen
		b.openedAt = time.Now()
		b.consecutiveFails = 0
		b.consecutiveSucc = 0
		b.opens.Add(1)
		b.notifyStateChange(oldState, b.state)
	default:
	}
	b.mu.Unlock()
	b.failures.Add(1)
}

// State returns the current breaker state.
func (b *Breaker) State() State {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.state
}

// Stats returns a snapshot of breaker statistics.
func (b *Breaker) Stats() BreakerStats {
	b.mu.Lock()
	state := b.state
	cfg := b.cfg
	fails := b.consecutiveFails
	succs := b.consecutiveSucc
	openedAt := b.openedAt
	b.mu.Unlock()

	return BreakerStats{
		State:            state,
		FailureThreshold: cfg.FailureThreshold,
		SuccessThreshold: cfg.SuccessThreshold,
		RecoveryTimeout:  cfg.RecoveryTimeout,
		ConsecutiveFails: fails,
		ConsecutiveSucc:  succs,
		OpenedAt:         openedAt,
		TotalFailures:    b.failures.Load(),
		TotalSuccesses:   b.successes.Load(),
		TotalRejections:  b.rejections.Load(),
		TotalOpens:       b.opens.Load(),
	}
}

// BreakerStats holds a snapshot of breaker statistics.
type BreakerStats struct {
	State            State
	FailureThreshold int
	SuccessThreshold int
	RecoveryTimeout  time.Duration
	ConsecutiveFails int
	ConsecutiveSucc  int
	OpenedAt         time.Time
	TotalFailures    int64
	TotalSuccesses   int64
	TotalRejections  int64
	TotalOpens       int64
}

// OnStateChange registers a callback that fires whenever the breaker state transitions.
// The callback receives the new state. It is invoked synchronously under the breaker lock,
// so callbacks must be fast and must not acquire the breaker lock.
func (b *Breaker) OnStateChange(fn func(State)) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.stateChangeCallbacks = append(b.stateChangeCallbacks, fn)
}

func (b *Breaker) notifyStateChange(from, to State) {
	if from == to {
		return
	}
	for _, fn := range b.stateChangeCallbacks {
		fn(to)
	}
}
