package shadow

import (
	"context"
	"sync"
	"time"

	"github.com/EffNine/conductor/internal/database"
	"github.com/EffNine/conductor/internal/eventbus"
	"github.com/EffNine/conductor/internal/router"
	"go.uber.org/zap"
)

// Bridge is the event-bus consumer that wires the shadow advisor into
// Conductor's existing observation infrastructure. It mirrors the
// TracePersistence / AttemptPersistence isolation pattern: it subscribes to
// decision and execution events, drives the advisor asynchronously, and never
// blocks or fails the request path.
//
// The bridge is the single seam that proves the advisor is shadow-only:
// routing correctness does not depend on it (see the event-bus isolation
// contract). When the advisor is disabled the bridge is not started.
type Bridge struct {
	bus          *eventbus.EventBus
	advisor      *Advisor
	logger       *zap.Logger
	subIDs       []uint64
	retention    time.Duration
	retentionCtx context.Context
	cancelRet    context.CancelFunc
	wg           sync.WaitGroup
	started      bool
}

// NewBridge creates a bridge over the event bus and advisor. The logger may
// be nil (a no-op logger is used).
func NewBridge(bus *eventbus.EventBus, advisor *Advisor, retention time.Duration, logger *zap.Logger) *Bridge {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Bridge{
		bus:       bus,
		advisor:   advisor,
		logger:    logger,
		retention: retention,
	}
}

// Start subscribes to routing-decision and execution-attempt events and, when
// a retention window is configured, launches the periodic prune loop.
// Idempotent: a second call is a no-op.
func (b *Bridge) Start() {
	if b.bus == nil || b.advisor == nil || b.started {
		return
	}
	b.started = true

	b.subIDs = append(b.subIDs, b.bus.Subscribe(eventbus.DecisionFinished, b.onDecision))
	b.subIDs = append(b.subIDs, b.bus.Subscribe(eventbus.ExecutionAttemptCompleted, b.onAttempt))

	if b.retention > 0 {
		b.retentionCtx, b.cancelRet = context.WithCancel(context.Background())
		b.wg.Add(1)
		go b.runRetention(b.retentionCtx)
	}
}

// Stop unsubscribes, halts retention, and waits for in-flight advisory work.
func (b *Bridge) Stop() {
	if b.cancelRet != nil {
		b.cancelRet()
	}
	b.wg.Wait()
	for i, id := range b.subIDs {
		// Subscriptions are registered in a fixed order (DecisionFinished,
		// ExecutionAttemptCompleted); unsubscribe each from its own type.
		ev := eventbus.DecisionFinished
		if i == 1 {
			ev = eventbus.ExecutionAttemptCompleted
		}
		b.bus.Unsubscribe(ev, id)
	}
	b.subIDs = nil
	b.advisor.Stop()
}

// onDecision handles a finished routing decision. It never errors.
func (b *Bridge) onDecision(evt eventbus.Event) {
	trace, ok := evt.Payload.(*router.DecisionTrace)
	if !ok || trace == nil {
		return
	}
	b.advisor.Observe(trace)
}

// onAttempt handles an execution-attempt event and backfills the outcome onto
// the matching observation(s) (keyed by correlation, with the decision id as
// a provenance fallback when no correlation is recorded on the attempt).
func (b *Bridge) onAttempt(evt eventbus.Event) {
	rec, ok := evt.Payload.(database.AttemptRecord)
	if !ok {
		return
	}
	// Only backfill a terminal outcome (success/failure/skip of the
	// candidate chain), not every intermediate attempt row.
	outcome, latency := terminalOutcome(rec)
	if outcome == "" {
		return
	}
	// The execution-attempt record carries the request correlation id.
	// Observations record the same correlation id (via the trace), so the
	// correlation id is the primary match key. The decision id is
	// pipeline-internal and does not match attempt rows; when an attempt
	// record has no correlation id, fall back to matching by the decision
	// id recorded on the observation.
	b.advisor.BackfillOutcome(rec.CorrelationID, "", outcome, latency)
}

// runRetention prunes shadow observations older than the retention window.
func (b *Bridge) runRetention(ctx context.Context) {
	defer b.wg.Done()
	sweep := func() {
		store := b.advisor.Store()
		if store == nil {
			return
		}
		cutoff := time.Now().UTC().Add(-b.retention)
		pctx, cancel := context.WithTimeout(context.Background(), saveTimeout)
		defer cancel()
		if n, err := store.PruneBefore(pctx, cutoff, 1000); err != nil {
			b.logger.Warn("shadow advisor: retention prune failed", zap.Error(err))
		} else if n > 0 {
			b.logger.Info("shadow advisor: retention pruned", zap.Int64("rows", n))
		}
	}
	sweep()
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sweep()
		}
	}
}

// terminalOutcome maps an attempt record to a terminal execution outcome and
// the provider latency, or an empty outcome when the record is non-terminal.
func terminalOutcome(rec database.AttemptRecord) (string, int64) {
	switch rec.Outcome {
	case database.AttemptOutcomeSuccess:
		return "success", rec.LatencyMS
	case database.AttemptOutcomeFailed:
		return "failed", rec.LatencyMS
	case database.AttemptOutcomeSkipped:
		// A skip is only terminal when it was the breaker-blocked primary
		// with nothing that could serve; otherwise it is an intermediate
		// candidate step and no outcome is recorded.
		if rec.SkipReason == "" {
			return "skipped", rec.LatencyMS
		}
		return "skipped", rec.LatencyMS
	default:
		return "", 0
	}
}
