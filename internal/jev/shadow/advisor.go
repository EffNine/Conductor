// Package shadow implements the JEV (TypeSafe System One) shadow advisor for
// Conductor routing decisions.
//
// The advisor is a PURE OBSERVER. It subscribes to routing-decision events on
// the event bus, asks JEV which candidate provider it would choose for the
// same routing state, and persists the advisory result alongside the
// deterministic Conductor decision and the eventual execution outcome.
//
// Isolation contract: the advisor NEVER feeds back into Conductor's routing
// selection, fallback, or retry logic. JEV failure, timeout, or
// misconfiguration MUST NOT alter production behavior. All work is
// asynchronous, bounded, and detached from the request path — the same
// isolation pattern used by TracePersistence and AttemptPersistence.
package shadow

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	jev "github.com/EffNine/conductor/internal/jev"
	"github.com/EffNine/conductor/internal/router"
	"go.uber.org/zap"
)

// saveTimeout bounds a single async observation save.
const saveTimeout = 5 * time.Second

// maxConcurrentSaves bounds concurrent SQLite inserts.
const maxConcurrentSaves = 8

// state is the structured routing state sent to JEV. It contains only
// non-sensitive, routing-relevant fields — never prompts, API keys, or
// authorization material. This mirrors the DecisionTrace safety contract.
type state struct {
	RequestedModel     string   `json:"requested_model"`
	ResolvedMode       string   `json:"resolved_mode"`
	ModeSource         string   `json:"mode_source,omitempty"`
	TaskType           string   `json:"task_type,omitempty"`
	Streaming          bool     `json:"streaming"`
	HasImage           bool     `json:"has_image"`
	HasTools           bool     `json:"has_tools"`
	CandidateCount     int      `json:"candidate_count"`
	CandidateProviders []string `json:"candidate_providers"`
	SelectedProvider   string   `json:"selected_provider"`
	SelectedModel      string   `json:"selected_model"`
	SelectedScore      float64  `json:"selected_score"`
}

// AdvisoryStatus values record the outcome of a JEV advisory call.
const (
	AdvisoryOK            = "ok"
	AdvisoryTimeout       = "timeout"
	AdvisoryUnauthorized  = "unauthorized"
	AdvisoryRateLimited   = "rate_limited"
	AdvisoryMalformed     = "malformed"
	AdvisoryError         = "error"
	AdvisoryNotConfigured = "not_configured"
)

// Outcome values for the eventual execution result.
const (
	OutcomeUnknown  = "unknown"
	OutcomeSelected = "selected"
	OutcomeRejected = "rejected"
	OutcomeFailed   = "failed"
)

// Store is the persistence interface for shadow observations.
type Store interface {
	Migrate() error
	Save(ctx context.Context, obs *Observation) error
	// UpdateAdvisory persists the JEV advisory fields of an existing row
	// without touching the outcome/latency columns (so a backfill that
	// landed concurrently is not clobbered).
	UpdateAdvisory(ctx context.Context, obs *Observation) error
	BackfillOutcome(ctx context.Context, correlationID, decisionID, outcome string, latencyMs int64) (int, error)
	PruneBefore(ctx context.Context, cutoff time.Time, batchSize int) (int64, error)
	List(ctx context.Context, limit int) ([]Observation, error)
}

// AdvisorConfig configures the shadow advisor.
type AdvisorConfig struct {
	// Store persists observations. Nil = no persistence (in-memory only).
	Store Store
	// Logger is optional; a no-op logger is used when nil.
	Logger *zap.Logger
	// JEVClient performs advisory calls. Nil = advisor runs but records
	// "not_configured" advisory results (observation rows still persist).
	JEVClient *jev.Client
}

// Advisor observes routing decisions and records JEV advisory results.
// It is fully detached from the routing path: all methods are safe to call
// from event-bus subscribers or shutdown hooks.
type Advisor struct {
	cfg    AdvisorConfig
	mu     sync.Mutex
	closed bool
	sem    chan struct{}
	wg     sync.WaitGroup
}

// NewAdvisor creates a shadow advisor. The store must already be migrated
// (call Migrate() separately) before observations are saved.
func NewAdvisor(cfg AdvisorConfig) *Advisor {
	if cfg.Logger == nil {
		cfg.Logger = zap.NewNop()
	}
	return &Advisor{
		cfg: cfg,
		sem: make(chan struct{}, maxConcurrentSaves),
	}
}

// Observe prepares an observation from a finished decision trace and
// schedules it for async persistence + JEV advisory. It returns immediately
// after scheduling; it never blocks the caller on JEV or the database.
//
// The deterministic decision (trace.Winner / CandidateScores) is recorded
// regardless of JEV availability. JEV is called in a background goroutine;
// its result is attached to the persisted row.
func (a *Advisor) Observe(trace *router.DecisionTrace) {
	if a == nil || trace == nil {
		return
	}
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return
	}
	select {
	case a.sem <- struct{}{}:
		a.wg.Add(1)
	default:
		a.mu.Unlock()
		a.cfg.Logger.Warn("shadow advisor: concurrency limit reached, dropping observation",
			zap.String("decision_id", string(trace.DecisionID)))
		return
	}
	a.mu.Unlock()

	go a.process(trace)
}

// process persists the observation and then runs the JEV advisory call.
//
// Ordering contract: the INSERT happens BEFORE the (potentially slow) JEV
// call. The backfill UPDATE is keyed by correlation id and only touches the
// outcome/latency columns, so it is commutative with the insert: an
// early-arriving backfill updates the (already inserted) row, and a later
// insert (OnConflict DoNothing) never clobbers a backfilled outcome.
// Running JEV before the insert would open a lost-update window: a
// backfill racing in between would match no row yet and its result would
// be lost when the insert finally lands.
func (a *Advisor) process(trace *router.DecisionTrace) {
	defer func() { <-a.sem }()
	defer a.wg.Done()

	obs := a.buildObservation(trace)

	// Persist the observation first (idempotent on decision_id) so that a
	// backfill arriving before the JEV advisory call lands still updates
	// an existing row rather than matching nothing.
	ctx, cancel := context.WithTimeout(context.Background(), saveTimeout)
	defer cancel()
	if a.cfg.Store != nil {
		if err := a.cfg.Store.Save(ctx, obs); err != nil {
			a.cfg.Logger.Warn("shadow advisor: persist observation failed",
				zap.String("decision_id", string(trace.DecisionID)),
				zap.Error(err),
			)
		}
	}

	// Call JEV after the row is durable. The advisory call never affects
	// the deterministic decision already captured; its result is recorded
	// on the observation fields and persisted with a follow-up advisory
	// UPDATE so a backfill that landed in the meantime is not clobbered.
	a.callJEV(trace, obs)

	// Persist the advisory fields (status, provider, confidence,
	// probabilities) without touching the outcome/latency columns.
	if a.cfg.Store != nil && obs.AdvisoryStatus != AdvisoryNotConfigured {
		advCtx, advCancel := context.WithTimeout(context.Background(), saveTimeout)
		defer advCancel()
		if err := a.cfg.Store.UpdateAdvisory(advCtx, obs); err != nil {
			a.cfg.Logger.Warn("shadow advisor: advisory persist failed",
				zap.String("decision_id", string(trace.DecisionID)),
				zap.Error(err),
			)
		}
	}
}

// buildObservation extracts the deterministic routing state from the trace.
// It never touches the request body, secrets, or credentials.
func (a *Advisor) buildObservation(trace *router.DecisionTrace) *Observation {
	obs := &Observation{
		DecisionID:     string(trace.DecisionID),
		RequestedModel: trace.RequestedModel,
		ResolvedMode:   string(trace.ResolvedMode),
		AdvisoryStatus: AdvisoryNotConfigured,
		Outcome:        OutcomeUnknown,
	}
	if trace.CorrelationID != "" {
		// The correlation id is the primary backfill key: execution-attempt
		// rows carry the request correlation id (middleware-generated),
		// while the decision id is pipeline-internal. The decision id stays
		// on the row for provenance and uniqueness.
		obs.CorrelationID = trace.CorrelationID
	}
	if trace.Winner != nil {
		obs.SelectedProvider = trace.Winner.ProviderName
		obs.SelectedModel = trace.Winner.ProviderModelID
	}
	obs.CandidateCount = len(trace.CandidateScores)
	providers := make([]string, 0, len(trace.CandidateScores))
	for _, cs := range trace.CandidateScores {
		providers = append(providers, cs.Provider)
	}
	obs.CandidateProviders = strings.Join(providers, ",")

	for _, cs := range trace.CandidateScores {
		if cs.Selected {
			obs.SelectedScore = cs.TotalScore
			break
		}
	}
	// Derive the routing-decision outcome (selected/rejected/failed)
	// matching the trace store's own derivation.
	switch {
	case trace.Winner != nil:
		obs.Outcome = OutcomeSelected
	case stageFailed(trace):
		obs.Outcome = OutcomeFailed
	default:
		obs.Outcome = OutcomeRejected
	}
	return obs
}

// callJEV runs the advisory question. All failures are captured into the
// observation's advisory fields; they never propagate.
func (a *Advisor) callJEV(trace *router.DecisionTrace, obs *Observation) {
	if a.cfg.JEVClient == nil {
		obs.AdvisoryStatus = AdvisoryNotConfigured
		return
	}

	// Candidate set comes from the trace.
	candidates := make([]string, 0, len(trace.CandidateScores))
	for _, cs := range trace.CandidateScores {
		candidates = append(candidates, cs.Provider)
	}
	if len(candidates) == 0 {
		// No candidate set: JEV has nothing to choose among. Record and stop.
		obs.AdvisoryStatus = AdvisoryMalformed
		return
	}

	s := state{
		RequestedModel:     obs.RequestedModel,
		ResolvedMode:       obs.ResolvedMode,
		SelectedProvider:   obs.SelectedProvider,
		SelectedModel:      obs.SelectedModel,
		SelectedScore:      obs.SelectedScore,
		CandidateCount:     len(candidates),
		CandidateProviders: candidates,
	}
	if trace.Intent != nil {
		s.TaskType = string(trace.Intent.TaskType)
	}

	// Build the choice criteria over the candidate providers.
	criteria := make(map[string]any, len(candidates))
	for _, c := range candidates {
		// null rubric: option names are self-explanatory provider ids.
		criteria[c] = nil
	}

	questions := map[string]jev.Question{
		"best_provider": {
			Choice: &jev.ChoiceQuestion{
				Instructions: "Given the routing state, which candidate provider should serve this request? " +
					"Consider health, latency, cost, and capability fit. " +
					"The deterministic selection is provided for context but you should judge independently.",
				Criteria: criteria,
			},
		},
	}

	deadline := a.cfg.JEVClient.Timeout()
	if deadline <= 0 {
		deadline = 5 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), deadline)
	defer cancel()
	start := time.Now()
	answers, usage, err := a.cfg.JEVClient.Evaluate(ctx, s, questions)
	obs.JEVDurationMs = time.Since(start).Milliseconds()

	if err != nil {
		obs.AdvisoryStatus = classifyJevError(err)
		a.cfg.Logger.Debug("shadow advisor: JEV call failed",
			zap.String("decision_id", string(trace.DecisionID)),
			zap.String("status", obs.AdvisoryStatus),
		)
		return
	}
	if usage != nil {
		obs.JEVInputTokens = usage.InputTokens
		obs.JEVOutputTokens = usage.OutputTokens
	}

	ans, ok := answers["best_provider"]
	if !ok {
		obs.AdvisoryStatus = AdvisoryMalformed
		return
	}
	obs.AdvisoryProvider = ans.Choice
	obs.AdvisoryConfidence = ans.Confidence
	obs.AdvisoryProbabilities = marshalProbabilities(ans.Probabilities)
	obs.JEVModel = a.cfg.JEVClient.Model()
	obs.AdvisoryStatus = AdvisoryOK
}

// BackfillOutcome associates an execution outcome with prior observations.
// The correlation id is the primary key; the decision id (when supplied) is
// the fallback match for rows recorded before correlation ids existed.
func (a *Advisor) BackfillOutcome(correlationID, decisionID, outcome string, latencyMs int64) {
	if a == nil || (correlationID == "" && decisionID == "") || a.cfg.Store == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), saveTimeout)
	defer cancel()
	if n, err := a.cfg.Store.BackfillOutcome(ctx, correlationID, decisionID, outcome, latencyMs); err != nil {
		a.cfg.Logger.Warn("shadow advisor: outcome backfill failed",
			zap.String("correlation_id", correlationID),
			zap.String("decision_id", decisionID),
			zap.Error(err))
	} else if n == 0 {
		a.cfg.Logger.Debug("shadow advisor: outcome backfill matched no observation",
			zap.String("correlation_id", correlationID),
		)
	}
}

// Store returns the advisor's persistence store.
func (a *Advisor) Store() Store { return a.cfg.Store }

// Stop waits for in-flight advisory work to finish. Safe to call once.
func (a *Advisor) Stop() {
	a.mu.Lock()
	a.closed = true
	a.mu.Unlock()
	a.wg.Wait()
}

// stageFailed reports whether any pipeline stage failed in the trace.
func stageFailed(trace *router.DecisionTrace) bool {
	for _, sr := range trace.StageResults {
		if sr != nil && sr.Status == router.StageStatusFailed {
			return true
		}
	}
	return false
}

// classifyJevError maps a JEV call error to an advisory status.
func classifyJevError(err error) string {
	var re *jev.RequestError
	if errors.As(err, &re) {
		switch re.Kind {
		case "timeout":
			return AdvisoryTimeout
		case "unauthorized":
			return AdvisoryUnauthorized
		case "rate_limited":
			return AdvisoryRateLimited
		case "malformed":
			return AdvisoryMalformed
		default:
			return AdvisoryError
		}
	}
	return AdvisoryError
}
