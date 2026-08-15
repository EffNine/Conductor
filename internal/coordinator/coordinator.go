package coordinator

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/EffNine/conductor/internal/eventbus"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// TaskInfo is a lightweight interface to the task model, breaking the import cycle
// between coordinator and task packages.
type TaskInfo struct {
	ID              string
	ParentID        *string
	RootID          string
	Status          string
	Input           string
	InputJSON       []byte
	Output          string
	OutputJSON      []byte
	Error           *string
	Provider        string
	Model           string
	StepCount       int
	PlanID          string
	Intent          string
	CurrentPlanStep int
	Role            string
	DependsOn       string
	ChildrenJSON    string
	CompletedAt     *time.Time
	RetryCount      int
	MaxRetries      int
}

// IsTerminal reports whether the task has reached a final state.
// Failed tasks with remaining retries are NOT considered terminal
// because the worker pool may retry them.
func (t *TaskInfo) IsTerminal() bool {
	switch t.Status {
	case "completed", "cancelled", "failed":
		return true
	}
	return false
}

// IsPermanentlyFailed reports whether the task has failed with no retries remaining.
func (t *TaskInfo) IsPermanentlyFailed() bool {
	if t.Status != "failed" {
		return false
	}
	// A failed task is only permanent if it has no retries remaining.
	if t.RetryCount < t.MaxRetries {
		return false
	}
	return true
}

// IsRetryable reports whether a failed task still has retries remaining.
func (t *TaskInfo) IsRetryable() bool {
	return t.Status == "failed" && t.RetryCount < t.MaxRetries
}

func (t *TaskInfo) IDPtr() *string {
	return &t.ID
}

// CoordinatorStore is the minimal store interface the coordinator requires.
type CoordinatorStore interface {
	GetTask(id string) (*TaskInfo, error)
	UpdateStatus(id string, newStatus string) error
	FailTask(id string, errMsg string) error
	CreateTask(task *TaskInfo) error
	UpdateTask(task *TaskInfo) error
	UpdateTaskSelective(task *TaskInfo) error
	ListChildTasks(parentID string) ([]*TaskInfo, error)
	ListTasksByRootID(rootID string) ([]*TaskInfo, error)
	SaveCheckpoint(id string, data []byte) error
	GetCoordinatorState(id string) ([]byte, error)
	UpdateCoordinatorState(id string, state []byte) error
	CreateTaskEvent(evt *TaskCoordEvent) error
}

// TaskCoordEvent is a lightweight event record for coordinator events.
type TaskCoordEvent struct {
	ID        string
	TaskID    string
	EventType string
	EventData []byte
}

// Default limits for bounded multi-agent execution.
const (
	DefaultMaxChildren     = 8
	DefaultMaxDepth        = 2
	DefaultMaxAgents       = 4
	defaultPollInterval    = 200 * time.Millisecond
	DefaultMaxChildContext = 4096 // max bytes of parent input forwarded to each child
)

// Config holds coordinator execution parameters.
type Config struct {
	MaxChildren     int
	MaxDepth        int
	MaxAgents       int
	PollInterval    time.Duration
	RequiredMode    bool // if true, all children must succeed for parent to complete
	MaxChildContext int  // max bytes of parent input forwarded to each child (0 = use default)
}

// NewConfig returns Config with sensible defaults.
func NewConfig() Config {
	return Config{
		MaxChildren:     DefaultMaxChildren,
		MaxDepth:        DefaultMaxDepth,
		MaxAgents:       DefaultMaxAgents,
		PollInterval:    defaultPollInterval,
		RequiredMode:    true,
		MaxChildContext: DefaultMaxChildContext,
	}
}

// ChildResult holds the outcome of one delegated child task.
type ChildResult struct {
	ChildID  string
	Role     string
	Status   string
	Output   string
	Error    string
	Provider string
	Model    string
	Steps    int
}

// AggregationResult holds the combined outcome of all children.
type AggregationResult struct {
	Children         []*ChildResult
	Summary          string
	AllSucceeded     bool
	AggregatedOutput string
}

// Coordinator orchestrates child task creation, dependency resolution,
// and result aggregation for a parent task with Role=="coordinator".
type Coordinator struct {
	store    CoordinatorStore
	eventBus *eventbus.EventBus
	logger   *zap.Logger
	cfg      Config
	// delegateMu guards concurrent Delegate calls per parent to prevent
	// duplicate child creation.
	delegateMu sync.Map // key: parentID string, value: *sync.Mutex
}

// New creates a Coordinator.
func New(store CoordinatorStore, eb *eventbus.EventBus, logger *zap.Logger, cfg Config) *Coordinator {
	if cfg.MaxChildren <= 0 {
		cfg.MaxChildren = DefaultMaxChildren
	}
	if cfg.MaxDepth <= 0 {
		cfg.MaxDepth = DefaultMaxDepth
	}
	if cfg.MaxAgents <= 0 {
		cfg.MaxAgents = DefaultMaxAgents
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = defaultPollInterval
	}
	return &Coordinator{
		store:    store,
		eventBus: eb,
		logger:   logger,
		cfg:      cfg,
	}
}

// Delegate creates child tasks from the parent's input and returns the
// list of child IDs. It parses the DependsOn field for explicit dependencies;
// if empty, it creates a flat list of children from the input.
// Concurrent calls for the same parent are serialized to prevent duplicate children.
func (c *Coordinator) Delegate(ctx context.Context, parent *TaskInfo) ([]string, error) {
	if parent.IsTerminal() {
		return nil, fmt.Errorf("parent task %s is terminal: %s", parent.ID, parent.Status)
	}

	// Acquire per-parent lock to prevent concurrent delegate duplication.
	muIface, _ := c.delegateMu.LoadOrStore(parent.ID, &sync.Mutex{})
	mu := muIface.(*sync.Mutex)
	mu.Lock()
	defer mu.Unlock()

	var depIDs []string

	// Parse existing dependencies if present.
	if parent.DependsOn != "" {
		if err := json.Unmarshal([]byte(parent.DependsOn), &depIDs); err != nil {
			return nil, fmt.Errorf("parse depends_on: %w", err)
		}
	}

	// If this is a resume (already has children recorded), return them.
	var children []string
	if parent.ChildrenJSON != "" {
		if err := json.Unmarshal([]byte(parent.ChildrenJSON), &children); err != nil {
			return nil, fmt.Errorf("parse children_json: %w", err)
		}
		if len(children) > 0 {
			c.logger.Info("coordinator resuming with existing children",
				zap.String("parent_id", parent.ID),
				zap.Int("child_count", len(children)),
			)
			return children, nil
		}
	}

	// Check depth bound.
	depth := computeDepth(parent, c.store)
	if depth >= c.cfg.MaxDepth {
		return nil, fmt.Errorf("coordination depth limit (%d) reached", c.cfg.MaxDepth)
	}

	// Check for already-created children in the DB (crash recovery).
	existingChildren, err := c.store.ListChildTasks(parent.ID)
	if err == nil && len(existingChildren) > 0 {
		existingIDs := make([]string, 0, len(existingChildren))
		for _, ch := range existingChildren {
			existingIDs = append(existingIDs, ch.ID)
		}
		c.logger.Info("coordinator resuming from DB children",
			zap.String("parent_id", parent.ID),
			zap.Int("child_count", len(existingIDs)),
		)
		childrenJSON, _ := json.Marshal(existingIDs)
		if err := c.store.UpdateTaskSelective(&TaskInfo{ID: parent.ID, ChildrenJSON: string(childrenJSON)}); err != nil {
			c.logger.Error("failed to persist coordinator children on resume",
				zap.String("parent_id", parent.ID),
				zap.Error(err),
			)
		}
		return existingIDs, nil
	}

	// Enforce max children bound before creation.
	maxChildren := c.cfg.MaxChildren
	if maxChildren <= 0 {
		maxChildren = DefaultMaxChildren
	}

	// Create children based on intent/role.
	switch {
	case parent.Role == "coordinator" && len(depIDs) > 0:
		// Explicit dependency-based delegation — respect bound.
		if len(depIDs) > maxChildren {
			depIDs = depIDs[:maxChildren]
		}
		children = depIDs
	case parent.Intent == "coding" || parent.Intent == "elite":
		children = c.createCodingChildren(ctx, parent, maxChildren)
	case parent.Intent == "reasoning" || parent.Intent == "research":
		children = c.createResearchChildren(ctx, parent, maxChildren)
	default:
		children = c.createDefaultChildren(ctx, parent, maxChildren)
	}

	// Enforce max children bound (safety net).
	if len(children) > maxChildren {
		children = children[:maxChildren]
	}

	// Persist children list using a targeted update to avoid overwriting
	// other parent fields (input, role, intent, etc.).
	childrenJSON, _ := json.Marshal(children)
	if err := c.store.UpdateTaskSelective(&TaskInfo{
		ID:           parent.ID,
		ChildrenJSON: string(childrenJSON),
	}); err != nil {
		return nil, fmt.Errorf("persist children: %w", err)
	}

	// Emit delegation event.
	_ = c.store.CreateTaskEvent(&TaskCoordEvent{
		ID:        uuid.New().String(),
		TaskID:    parent.ID,
		EventType: "task.delegated",
		EventData: mustMarshal(map[string]any{
			"children": children,
			"intent":   parent.Intent,
			"role":     parent.Role,
		}),
	})
	if c.eventBus != nil {
		c.eventBus.PublishSync(ctx, eventbus.Event{
			Type:      eventbus.TaskDelegated,
			Payload:   map[string]any{"parent_id": parent.ID, "children": children},
			Timestamp: time.Now().UnixNano(),
		})
	}

	c.logger.Info("coordinator delegated",
		zap.String("parent_id", parent.ID),
		zap.Int("children", len(children)),
		zap.Strings("child_ids", children),
	)
	return children, nil
}

// WaitForChildren polls until all children reach a terminal status or the
// context is cancelled. It returns the aggregated results.
func (c *Coordinator) WaitForChildren(ctx context.Context, parent *TaskInfo, childIDs []string) (*AggregationResult, error) {
	agg := &AggregationResult{}
	resumeState := c.LoadResumeState(parent.ID)

	// Restore already-completed children.
	for _, cr := range resumeState.CompletedChildren {
		agg.Children = append(agg.Children, &ChildResult{
			ChildID: cr.ChildID,
			Status:  cr.Status,
			Output:  cr.Output,
		})
	}

	pending := make([]string, 0, len(childIDs))
	for _, id := range childIDs {
		found := false
		for _, cr := range resumeState.CompletedChildren {
			if cr.ChildID == id {
				found = true
				break
			}
		}
		if !found {
			pending = append(pending, id)
		}
	}

	ticker := time.NewTicker(c.cfg.PollInterval)
	defer ticker.Stop()

	allDone := false
	for {
		select {
		case <-ctx.Done():
			c.cancelRunningChildren(ctx, parent.ID, pending)
			return nil, ctx.Err()
		case <-ticker.C:
			allDone = true
			rebuildPending := make([]string, 0)

			for _, childID := range childIDs {
				child, err := c.store.GetTask(childID)
				if err != nil {
					c.logger.Warn("coordinator: child not found",
						zap.String("child_id", childID),
						zap.Error(err),
					)
					continue
				}

				if child.IsTerminal() {
					cr := &ChildResult{
						ChildID:  childID,
						Role:     child.Role,
						Status:   child.Status,
						Output:   child.Output,
						Provider: child.Provider,
						Model:    child.Model,
						Steps:    child.StepCount,
					}
					if child.Error != nil {
						cr.Error = *child.Error
					}
					agg.Children = append(agg.Children, cr)
					continue
				}

				// Failed children without remaining retries are treated as terminal.
				if child.IsPermanentlyFailed() && !child.IsRetryable() {
					cr := &ChildResult{
						ChildID: childID,
						Role:    child.Role,
						Status:  "failed",
						Error:   "task permanently failed",
					}
					if child.Error != nil {
						cr.Error = *child.Error
					}
					agg.Children = append(agg.Children, cr)
					continue
				}

				// Retryable failures remain pending — wait for scheduler to promote.
				if child.IsRetryable() {
					allDone = false
					rebuildPending = append(rebuildPending, childID)
					continue
				}

				allDone = false
				rebuildPending = append(rebuildPending, childID)
			}
			pending = rebuildPending

			if allDone || len(pending) == 0 {
				break
			}

			// Save partial state for resume.
			c.savePartialState(parent.ID, agg)
		}

		if allDone {
			break
		}
	}

	// Final aggregation.
	agg = c.aggregate(ctx, parent, agg)
	return agg, nil
}

// aggregate combines child results into a compact summary.
func (c *Coordinator) aggregate(ctx context.Context, parent *TaskInfo, agg *AggregationResult) *AggregationResult {
	_ = ctx

	var successCount, failCount int
	var outputParts []string

	for _, cr := range agg.Children {
		switch cr.Status {
		case "completed":
			successCount++
			if cr.Output != "" {
				outputParts = append(outputParts, fmt.Sprintf("[%s] %s", cr.ChildID, truncateOutput(cr.Output, 500)))
			}
		case "failed", "cancelled":
			failCount++
		}
	}

	agg.AllSucceeded = failCount == 0

	// Required mode: any failure means parent failure.
	if c.cfg.RequiredMode && failCount > 0 {
		agg.Summary = fmt.Sprintf("coordinator completed with %d failures out of %d children", failCount, successCount+failCount)
	} else {
		agg.Summary = fmt.Sprintf("coordinator completed: %d succeeded, %d failed", successCount, failCount)
	}

	if len(outputParts) > 0 {
		agg.AggregatedOutput = strings.Join(outputParts, "\n---\n")
	}

	// Emit aggregation events.
	if c.eventBus != nil {
		c.eventBus.PublishSync(ctx, eventbus.Event{
			Type:      eventbus.TaskAggregationCompleted,
			Payload:   map[string]any{"parent_id": parent.ID, "summary": agg.Summary, "all_succeeded": agg.AllSucceeded},
			Timestamp: time.Now().UnixNano(),
		})
	}

	_ = c.store.CreateTaskEvent(&TaskCoordEvent{
		ID:        uuid.New().String(),
		TaskID:    parent.ID,
		EventType: "task.aggregation.completed",
		EventData: mustMarshal(map[string]any{
			"summary":       agg.Summary,
			"all_succeeded": agg.AllSucceeded,
			"children":      len(agg.Children),
		}),
	})

	return agg
}

// MarkParentFinal transitions the parent task based on aggregation results.
func (c *Coordinator) MarkParentFinal(ctx context.Context, parent *TaskInfo, agg *AggregationResult) error {
	if !agg.AllSucceeded && c.cfg.RequiredMode {
		errMsg := fmt.Sprintf("coordination failed: %s", agg.Summary)
		if err := c.store.FailTask(parent.ID, errMsg); err != nil {
			return err
		}
		_ = c.store.CreateTaskEvent(&TaskCoordEvent{
			ID:        uuid.New().String(),
			TaskID:    parent.ID,
			EventType: "task.coordination.failed",
			EventData: mustMarshal(map[string]any{"summary": agg.Summary}),
		})
		return fmt.Errorf("%s", errMsg)
	}

	now := time.Now().UTC()
	parent.Status = "completed"
	parent.Output = agg.AggregatedOutput
	parent.OutputJSON = mustMarshal(map[string]any{
		"summary":       agg.Summary,
		"children":      len(agg.Children),
		"all_succeeded": agg.AllSucceeded,
	})
	parent.CompletedAt = &now
	if err := c.store.UpdateTask(parent); err != nil {
		return err
	}

	_ = c.store.CreateTaskEvent(&TaskCoordEvent{
		ID:        uuid.New().String(),
		TaskID:    parent.ID,
		EventType: "task.coordination.completed",
		EventData: mustMarshal(map[string]any{"summary": agg.Summary, "children": len(agg.Children)}),
	})

	if c.eventBus != nil {
		c.eventBus.PublishSync(ctx, eventbus.Event{
			Type:      eventbus.TaskCoordinationCompleted,
			Payload:   map[string]any{"parent_id": parent.ID, "summary": agg.Summary},
			Timestamp: time.Now().UnixNano(),
		})
	}
	return nil
}

// cancelRunningChildren propagates cancellation to non-terminal children.
func (c *Coordinator) cancelRunningChildren(ctx context.Context, parentID string, childIDs []string) {
	_ = ctx
	for _, id := range childIDs {
		child, err := c.store.GetTask(id)
		if err != nil || child.IsTerminal() {
			continue
		}
		if transErr := c.store.UpdateStatus(id, "cancelled"); transErr != nil {
			c.logger.Warn("coordinator: failed to cancel child",
				zap.String("child_id", id), zap.Error(transErr))
		}
		_ = c.store.CreateTaskEvent(&TaskCoordEvent{
			ID:        uuid.New().String(),
			TaskID:    id,
			EventType: "task.child.cancelled",
			EventData: mustMarshal(map[string]any{"parent_id": parentID}),
		})
	}
}

// --- resume/checkpoint helpers ---

type childStatusRecord struct {
	ChildID string `json:"child_id"`
	Status  string `json:"status"`
	Output  string `json:"output,omitempty"`
}

type ResumeState struct {
	CompletedChildren []ChildStatusRecord `json:"completed_children"`
	AggregatedOutput  string              `json:"aggregated_output,omitempty"`
}

type ChildStatusRecord struct {
	ChildID string `json:"child_id"`
	Status  string `json:"status"`
	Output  string `json:"output,omitempty"`
}

func (c *Coordinator) LoadResumeState(parentID string) ResumeState {
	state := ResumeState{}
	data, err := c.store.GetCoordinatorState(parentID)
	if err != nil || len(data) == 0 {
		return state
	}
	if unmarshalErr := json.Unmarshal(data, &state); unmarshalErr != nil {
		c.logger.Warn("coordinator: failed to unmarshal coord state", zap.Error(unmarshalErr))
		return ResumeState{}
	}
	return state
}

func (c *Coordinator) savePartialState(parentID string, agg *AggregationResult) {
	state := ResumeState{
		AggregatedOutput: agg.AggregatedOutput,
	}
	for _, cr := range agg.Children {
		// Only persist terminal, non-retryable outcomes.
		// Retryable failures must not be recorded as completed — they may be
		// resumed by the scheduler and completed successfully later.
		if cr.Status == "completed" || cr.Status == "cancelled" {
			rec := ChildStatusRecord{ChildID: cr.ChildID, Status: cr.Status, Output: cr.Output}
			state.CompletedChildren = append(state.CompletedChildren, rec)
		}
		// Permanent failures are also recorded so they are not re-polled.
		if cr.Status == "failed" {
			rec := ChildStatusRecord{ChildID: cr.ChildID, Status: cr.Status, Output: cr.Output}
			state.CompletedChildren = append(state.CompletedChildren, rec)
		}
	}
	data, _ := json.Marshal(state)
	_ = c.store.UpdateCoordinatorState(parentID, data)
}

// --- child creation helpers ---

func (c *Coordinator) createCodingChildren(ctx context.Context, parent *TaskInfo, maxChildren int) []string {
	roles := []struct{ role, desc string }{
		{"research", "analyze requirements and existing code"},
		{"coding", "implement the solution"},
		{"testing", "verify the solution"},
	}
	var ids []string
	maxCtx := c.cfg.MaxChildContext
	if maxCtx <= 0 {
		maxCtx = DefaultMaxChildContext
	}
	for _, r := range roles {
		if len(ids) >= maxChildren {
			break
		}
		id := uuid.New().String()
		boundedInput := truncateInput(parent.Input, maxCtx)
		child := &TaskInfo{
			ID:       id,
			ParentID: parent.IDPtr(),
			Status:   "queued",
			Input:    fmt.Sprintf("%s\n\nRole: %s. Task: %s", boundedInput, r.role, r.desc),
			Role:     r.role,
			Intent:   parent.Intent,
			RootID:   parent.RootID,
		}
		if err := c.store.CreateTask(child); err != nil {
			c.logger.Error("coordinator: failed to create child",
				zap.String("role", r.role), zap.Error(err))
			continue
		}
		ids = append(ids, id)
		c.emitChildEvent(ctx, id, "started", parent.ID)
	}
	return ids
}

func (c *Coordinator) createResearchChildren(ctx context.Context, parent *TaskInfo, maxChildren int) []string {
	if maxChildren <= 0 {
		maxChildren = 1
	}
	maxCtx := c.cfg.MaxChildContext
	if maxCtx <= 0 {
		maxCtx = DefaultMaxChildContext
	}
	var ids []string
	for i := 0; i < maxChildren; i++ {
		id := uuid.New().String()
		child := &TaskInfo{
			ID:       id,
			ParentID: parent.IDPtr(),
			Status:   "queued",
			Input:    truncateInput(parent.Input, maxCtx),
			Role:     "research",
			Intent:   parent.Intent,
			RootID:   parent.RootID,
		}
		if err := c.store.CreateTask(child); err != nil {
			c.logger.Error("coordinator: failed to create research child", zap.Error(err))
			break
		}
		ids = append(ids, id)
		c.emitChildEvent(ctx, id, "started", parent.ID)
	}
	return ids
}

func (c *Coordinator) createDefaultChildren(ctx context.Context, parent *TaskInfo, maxChildren int) []string {
	if maxChildren <= 0 {
		maxChildren = 1
	}
	maxCtx := c.cfg.MaxChildContext
	if maxCtx <= 0 {
		maxCtx = DefaultMaxChildContext
	}
	var ids []string
	for i := 0; i < maxChildren; i++ {
		id := uuid.New().String()
		child := &TaskInfo{
			ID:       id,
			ParentID: parent.IDPtr(),
			Status:   "queued",
			Input:    truncateInput(parent.Input, maxCtx),
			Role:     "general",
			Intent:   parent.Intent,
			RootID:   parent.RootID,
		}
		if err := c.store.CreateTask(child); err != nil {
			c.logger.Error("coordinator: failed to create default child", zap.Error(err))
			break
		}
		ids = append(ids, id)
		c.emitChildEvent(ctx, id, "started", parent.ID)
	}
	return ids
}

// truncateInput bounds the parent input to max bytes, preserving a trailing ellipsis.
func truncateInput(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	return s[:max-3] + "..."
}

func (c *Coordinator) emitChildEvent(ctx context.Context, childID, eventType, parentID string) {
	eventTypeStr := "task.child." + eventType
	_ = c.store.CreateTaskEvent(&TaskCoordEvent{
		ID:        uuid.New().String(),
		TaskID:    childID,
		EventType: eventTypeStr,
		EventData: mustMarshal(map[string]any{"parent_id": parentID}),
	})
	if c.eventBus != nil {
		c.eventBus.PublishSync(ctx, eventbus.Event{
			Type:      eventbus.EventType(eventTypeStr),
			Payload:   map[string]any{"child_id": childID, "parent_id": parentID},
			Timestamp: time.Now().UnixNano(),
		})
	}
}

func computeDepth(parent *TaskInfo, store CoordinatorStore) int {
	if parent.ParentID == nil || *parent.ParentID == "" {
		return 0
	}
	p, err := store.GetTask(*parent.ParentID)
	if err != nil {
		return 0
	}
	return 1 + computeDepth(p, store)
}

func truncateOutput(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "...(truncated)"
}

func mustMarshal(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return b
}
