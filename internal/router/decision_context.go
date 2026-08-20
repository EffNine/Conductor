package router

import (
	"context"
	"fmt"
	"time"

	"github.com/EffNine/conductor/internal/apitypes"
	"github.com/EffNine/conductor/internal/config"
	"github.com/EffNine/conductor/internal/eventbus"
	"github.com/EffNine/conductor/internal/policy"
	"github.com/EffNine/conductor/internal/runtime"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// DecisionID is a deterministic request identifier used throughout the pipeline.
type DecisionID string

// NewDecisionID generates a new unique decision ID.
func NewDecisionID() DecisionID {
	return DecisionID(uuid.New().String())
}

// ConfigSnapshot is a point-in-time copy of the routing configuration.
type ConfigSnapshot struct {
	Routes    map[string]Route
	Aliases   map[string]string
	Fallbacks map[string][]Fallback
	Weights   config.RoutingWeights
}

// Environment holds gateway environment constants.
type Environment struct {
	HealthCheckInterval   time.Duration
	HealthTimeout         time.Duration
	UnknownAsReachable    bool
	CircuitBreakerEnabled bool
}

// DecisionContext is the mutable context that flows through every pipeline stage.
//
// # Lifecycle
//
//   - The context is created by NewDecisionContext, which also creates the
//     pipeline-scoped Go context returned by Context().
//   - Context() returns the pipeline-scoped context. It is a single deadline
//     (contextTimeout) shared by every downstream call made during the decision.
//   - Close() terminates the decision context by cancelling it. It is called by
//     the pipeline (defer dc.Close()) and is safe to call more than once.
//   - A DecisionContext must NOT be used after Execute returns / Close() has
//     been called: Context() will observe the cancellation, so downstream work
//     started from it will fail.
//   - Asynchronous consumers must NOT retain the context returned by Context()
//     beyond the lifetime of the DecisionContext. If asynchronous consumers
//     become a requirement, the lifecycle semantics must be explicitly
//     redesigned (e.g. a background-scoped context) before use.
//
// DecisionContext is not safe for concurrent use; treat it as single-goroutine.
type DecisionContext struct {
	id               DecisionID
	timestamp        time.Time
	request          *apitypes.ChatCompletionRequest
	runtimeSnap      runtime.RuntimeSnapshot
	configSnap       ConfigSnapshot
	taskMeta         TaskMetadata
	environment      Environment
	intent           *policy.Intent
	capability       *policy.CapabilityRequirement
	selection        *SelectionResult
	candidateScores  []ProviderScoreView
	candidates       []ResolvedRoute
	policyRef        string
	modeProfile      *ModeProfile
	effectiveWeights Weights
	modeSource       string
	requestedMode    string
	contextReq       int // estimated required context tokens (set by Capability stage for LongHorizon)
	logger           *zap.Logger
	eventBus         *eventbus.EventBus
	ctx              context.Context
	cancel           context.CancelFunc
}

// contextTimeout bounds how long any single downstream call derived from a
// decision may run before giving up.
const contextTimeout = 30 * time.Second

// TaskMetadata carries high-level metadata about the request.
type TaskMetadata struct {
	ModelID      string
	ProviderHint string
	IsStream     bool
	HasImage     bool
	HasTools     bool
	MessageCount int
}

// NewDecisionContext creates the initial context for a request.
func NewDecisionContext(
	req *apitypes.ChatCompletionRequest,
	snap runtime.RuntimeSnapshot,
	cfg ConfigSnapshot,
	taskMeta TaskMetadata,
	env Environment,
	logger *zap.Logger,
	eventBus *eventbus.EventBus,
) *DecisionContext {
	ctx, cancel := context.WithTimeout(context.Background(), contextTimeout)
	return &DecisionContext{
		id:          NewDecisionID(),
		timestamp:   time.Now().UTC(),
		request:     req,
		runtimeSnap: snap,
		configSnap:  cfg,
		taskMeta:    taskMeta,
		environment: env,
		policyRef:   "default",
		logger:      logger,
		eventBus:    eventBus,
		ctx:         ctx,
		cancel:      cancel,
	}
}

// ID returns the decision ID.
func (c *DecisionContext) ID() DecisionID { return c.id }

// Timestamp returns the context creation time.
func (c *DecisionContext) Timestamp() time.Time { return c.timestamp }

// Request returns the original chat completion request.
func (c *DecisionContext) Request() *apitypes.ChatCompletionRequest { return c.request }

// RuntimeSnapshot returns the authoritative runtime snapshot.
func (c *DecisionContext) RuntimeSnapshot() runtime.RuntimeSnapshot { return c.runtimeSnap }

// ConfigSnapshot returns the configuration snapshot.
func (c *DecisionContext) ConfigSnapshot() ConfigSnapshot { return c.configSnap }

// TaskMetadata returns the task metadata.
func (c *DecisionContext) TaskMetadata() TaskMetadata { return c.taskMeta }

// Environment returns the gateway environment.
func (c *DecisionContext) Environment() Environment { return c.environment }

// PolicyReference returns the active policy reference.
func (c *DecisionContext) PolicyReference() string { return c.policyRef }

// Intent returns the resolved intent (set by Intent stage).
func (c *DecisionContext) Intent() *policy.Intent { return c.intent }

// SetIntent stores the resolved intent in the context.
func (c *DecisionContext) SetIntent(i *policy.Intent) { c.intent = i }

// Capability returns the resolved capability requirement (set by Capability stage).
func (c *DecisionContext) Capability() *policy.CapabilityRequirement { return c.capability }

// SetCapability stores the resolved capability in the context.
func (c *DecisionContext) SetCapability(cr *policy.CapabilityRequirement) { c.capability = cr }

// Selection returns the selection result (set by Selection stage).
func (c *DecisionContext) Selection() *SelectionResult { return c.selection }

// SetSelection stores the selection result in the context.
func (c *DecisionContext) SetSelection(r *SelectionResult) { c.selection = r }

// CandidateScores returns the scored candidates (set by Candidate stage).
func (c *DecisionContext) CandidateScores() []ProviderScoreView { return c.candidateScores }

// SetCandidateScores stores candidate scores in the context.
func (c *DecisionContext) SetCandidateScores(scores []ProviderScoreView) { c.candidateScores = scores }

// Candidates returns pre-resolved candidate routes (set by the handler before Execute).
func (c *DecisionContext) Candidates() []ResolvedRoute { return c.candidates }

// SetCandidates stores pre-resolved candidate routes in the context.
func (c *DecisionContext) SetCandidates(cands []ResolvedRoute) { c.candidates = cands }

// Logger returns the context logger.
func (c *DecisionContext) Logger() *zap.Logger { return c.logger }

// EventBus returns the event bus.
func (c *DecisionContext) EventBus() *eventbus.EventBus { return c.eventBus }

// Context returns a Go context for downstream use.
//
// The returned context is valid only while the DecisionContext is alive: it is
// cancelled by Close(), so callers must not use it after the decision has
// finished (see the lifecycle docs).
func (c *DecisionContext) Context() context.Context {
	return c.ctx
}

// ModeProfile returns the resolved mode profile for this decision.
// Set by the Intent stage based on canonical classification.
func (c *DecisionContext) ModeProfile() *ModeProfile { return c.modeProfile }

// SetModeProfile stores the resolved mode profile in the context.
func (c *DecisionContext) SetModeProfile(mp *ModeProfile) { c.modeProfile = mp }

// EffectiveWeights returns the per-decision normalized weights derived from
// the mode profile (or global defaults when no mode profile is active).
func (c *DecisionContext) EffectiveWeights() Weights { return c.effectiveWeights }

// SetEffectiveWeights stores the per-decision weights in the context.
func (c *DecisionContext) SetEffectiveWeights(w Weights) { c.effectiveWeights = w }

// ModeBonuses returns the per-decision capability bonuses from the mode profile.
func (c *DecisionContext) ModeBonuses() CapabilityBonuses {
	if c.modeProfile == nil {
		return CapabilityBonuses{}
	}
	return c.modeProfile.CapabilityBonuses
}

// ModeSource reports how the resolved mode was determined ("explicit" or "classifier").
func (c *DecisionContext) ModeSource() string { return c.modeSource }

// SetModeSource stores the mode resolution source in the context.
func (c *DecisionContext) SetModeSource(src string) { c.modeSource = src }

// RequestedMode returns the raw mode string from the request, if any.
func (c *DecisionContext) RequestedMode() string { return c.requestedMode }

// SetRequestedMode stores the raw request mode string in the context.
func (c *DecisionContext) SetRequestedMode(s string) { c.requestedMode = s }

// ContextRequirement returns the estimated token budget required by the
// request, set by the Capability stage when the resolved mode is LongHorizon.
// Zero means no context budget constraint was applied.
func (c *DecisionContext) ContextRequirement() int { return c.contextReq }

// SetContextRequirement stores the estimated context requirement in the context.
func (c *DecisionContext) SetContextRequirement(n int) { c.contextReq = n }

// Close releases the decision context, cancelling any in-flight downstream
// calls. Safe to call multiple times. After Close, DecisionContext must not be
// used again (see the lifecycle docs).
func (c *DecisionContext) Close() {
	if c.cancel != nil {
		c.cancel()
		c.cancel = nil
	}
}

// Publish publishes an event on the pipeline's event bus.
func (c *DecisionContext) Publish(typ eventbus.EventType, payload any) {
	if c.eventBus == nil {
		return
	}
	c.eventBus.Publish(c.Context(), eventbus.Event{
		Type:      typ,
		Payload:   payload,
		Timestamp: time.Now().UnixNano(),
	})
}

// PublishSync publishes an event synchronously on the pipeline's event bus.
func (c *DecisionContext) PublishSync(typ eventbus.EventType, payload any) {
	if c.eventBus == nil {
		return
	}
	c.eventBus.PublishSync(c.Context(), eventbus.Event{
		Type:      typ,
		Payload:   payload,
		Timestamp: time.Now().UnixNano(),
	})
}

// Err returns an error wrapped with the decision ID for traceability.
func (c *DecisionContext) Err(msg string, err error) error {
	return fmt.Errorf("[decision=%s] %s: %w", c.id, msg, err)
}
