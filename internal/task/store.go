package task

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/EffNine/conductor/internal/database"
	"gorm.io/gorm"
)

// workerIDContextKey is the context key for the current worker ID.
type workerIDContextKey struct{}

// workerExecutionContextKey is the context key that signals worker-executed
// tasks. When set, the executor must not finalize terminal state — the caller
// (worker) owns retry/failure policy.
type workerExecutionContextKey struct{}

// WithWorkerID returns a context carrying the given worker ID.
func WithWorkerID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, workerIDContextKey{}, id)
}

// WorkerIDFromContext returns the worker ID stored in ctx, if any.
func WorkerIDFromContext(ctx context.Context) string {
	id, _ := ctx.Value(workerIDContextKey{}).(string)
	return id
}

// WithWorkerExecution marks ctx as a worker-driven execution so the executor
// leaves failure handling to the caller instead of finalizing terminal state.
func WithWorkerExecution(ctx context.Context) context.Context {
	return context.WithValue(ctx, workerExecutionContextKey{}, true)
}

// IsWorkerContext reports whether ctx was created by the worker pool.
func IsWorkerContext(ctx context.Context) bool {
	_, ok := ctx.Value(workerExecutionContextKey{}).(bool)
	return ok
}

// ErrTaskNotFound is returned when a task does not exist.
var ErrTaskNotFound = errors.New("task not found")

// Store provides persistence operations for tasks.
type Store interface {
	// CreateTask inserts a new task.
	CreateTask(task *Task) error

	// GetTask retrieves a task by ID.
	GetTask(id string) (*Task, error)

	// UpdateTask replaces a task in full. Caller must supply a complete Task.
	UpdateTask(task *Task) error

	// DeleteTask removes a task and all associated steps, events, and tool calls.
	DeleteTask(id string) error

	// ListTasks returns tasks paginated by limit/offset, ordered by created_at desc.
	ListTasks(limit, offset int) ([]Task, error)

	// ListTasksByStatus returns tasks with the given status, paginated.
	ListTasksByStatus(status Status, limit, offset int) ([]Task, error)

	// UpdateStatus transitions a task to a new status with validation.
	UpdateStatus(id string, newStatus Status) error

	// SaveCheckpoint persists agent state for pause/resume.
	SaveCheckpoint(id string, data []byte) error

	// IncrementRetry bumps RetryCount and returns the new count.
	IncrementRetry(id string) (int, error)

	// CreateTaskStep inserts a new task step.
	CreateTaskStep(step *TaskStep) error

	// CreateTaskEvent inserts a new task event.
	CreateTaskEvent(evt *TaskEvent) error

	// CreateTaskToolCall inserts a new tool call record.
	CreateTaskToolCall(tc *TaskToolCall) error

	// FailTask marks a task as failed with the given error message.
	FailTask(id string, errMsg string) error

	// ClaimTask atomically transitions a eligible task from queued/failed-to-retry
	// to running and assigns a worker lease. Returns the claimed task or ErrTaskNotFound
	// / ErrNoEligibleTask when no task is available.
	ClaimTask(workerID string, leaseDuration time.Duration) (*Task, error)

	// ReleaseLease clears the lease fields on a task, returning it to queued.
	// When workerID is non-empty, the release only succeeds if the caller
	// currently holds the lease (prevents stale-worker interference).
	ReleaseLease(id string, workerID ...string) error

	// UpdateLease extends the current lease for a task.
	UpdateLease(id string, workerID string, leaseUntil time.Time) error

	// ExpireStaleLeases marks running tasks with expired leases as queued so they
	// can be reclaimed by another worker. A lease is expired when lease_until < now.
	ExpireStaleLeases() (int64, error)

	// MakeRetryable marks a failed task as queued with a computed NextRetryAt,
	// incrementing RetryCount. Returns the new retry count.
	MakeRetryable(id string, backoff time.Duration) (int, error)

	// ReadyRetries finds tasks whose status is queued or whose NextRetryAt <= now
	// and status is failed, returning their IDs.
	ReadyRetries(limit int) ([]string, error)

	// RecoverPendingTasks transitions pending tasks with no claimed_by to queued.
	RecoverPendingTasks() (int64, error)

	// ListChildTasks returns all tasks whose ParentID == parentID, ordered by created_at asc.
	ListChildTasks(parentID string, limit, offset int) ([]Task, error)

	// ListTasksByRootID returns all tasks sharing the given root_id, ordered by created_at asc.
	ListTasksByRootID(rootID string, limit, offset int) ([]Task, error)

	// UpdateCoordinatorState persists the coordinator's internal state blob.
	UpdateCoordinatorState(id string, state []byte) error

	// GetCoordinatorState retrieves the coordinator state blob for a task.
	GetCoordinatorState(id string) ([]byte, error)

	// DependenciesMet checks whether all dependency IDs in dependsOnJSON are in a terminal state.
	DependenciesMet(dependsOnJSON string) error

	// UpdateTaskSelective updates only non-zero fields of a task, avoiding
	// destructive zero-value overwrites from GORM Save.
	UpdateTaskSelective(task *Task) error
}

// SQLiteStore is a GORM-backed implementation of Store.
type SQLiteStore struct {
	db *database.Database
}

// NewSQLiteStore creates a Store backed by the given database.
func NewSQLiteStore(db *database.Database) *SQLiteStore {
	return &SQLiteStore{db: db}
}

func (s *SQLiteStore) CreateTask(task *Task) error {
	if task == nil {
		return fmt.Errorf("task is nil")
	}
	if task.ID == "" {
		return fmt.Errorf("task ID is required")
	}
	return s.db.DB.Create(task).Error
}

func (s *SQLiteStore) GetTask(id string) (*Task, error) {
	if id == "" {
		return nil, fmt.Errorf("task ID is required")
	}
	var task Task
	err := s.db.DB.Where("id = ?", id).First(&task).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrTaskNotFound
		}
		return nil, err
	}
	return &task, nil
}

func (s *SQLiteStore) UpdateTask(task *Task) error {
	if task == nil {
		return fmt.Errorf("task is nil")
	}
	if task.ID == "" {
		return fmt.Errorf("task ID is required")
	}
	if err := s.db.DB.Save(task).Error; err != nil {
		return err
	}
	// Verify it exists.
	var count int64
	s.db.DB.Model(&Task{}).Where("id = ?", task.ID).Count(&count)
	if count == 0 {
		return ErrTaskNotFound
	}
	return nil
}

// UpdateTaskSelective updates only the non-zero-value fields of a task.
// This avoids the destructive behavior of GORM's Save which writes zero values
// for all fields, potentially overwriting valid data.
func (s *SQLiteStore) UpdateTaskSelective(task *Task) error {
	if task == nil {
		return fmt.Errorf("task is nil")
	}
	if task.ID == "" {
		return fmt.Errorf("task ID is required")
	}
	updates := make(map[string]any)
	if task.Input != "" {
		updates["input"] = task.Input
	}
	if len(task.InputJSON) > 0 {
		updates["input_json"] = task.InputJSON
	}
	if task.Output != "" {
		updates["output"] = task.Output
	}
	if len(task.OutputJSON) > 0 {
		updates["output_json"] = task.OutputJSON
	}
	if task.Error != nil {
		updates["error"] = *task.Error
	}
	if task.ErrorCode != nil {
		updates["error_code"] = *task.ErrorCode
	}
	if task.Provider != "" {
		updates["provider"] = task.Provider
	}
	if task.Model != "" {
		updates["model"] = task.Model
	}
	if task.StepCount > 0 {
		updates["step_count"] = task.StepCount
	}
	if task.MaxSteps > 0 {
		updates["max_steps"] = task.MaxSteps
	}
	if task.Checkpoint != nil {
		updates["checkpoint"] = task.Checkpoint
	}
	if task.ClaimedBy != "" {
		updates["claimed_by"] = task.ClaimedBy
	}
	if task.ClaimedAt != nil {
		updates["claimed_at"] = task.ClaimedAt
	}
	if task.LeaseUntil != nil {
		updates["lease_until"] = task.LeaseUntil
	}
	if task.PlanID != "" {
		updates["plan_id"] = task.PlanID
	}
	if task.Intent != "" {
		updates["intent"] = task.Intent
	}
	if task.CurrentPlanStep > 0 {
		updates["current_plan_step"] = task.CurrentPlanStep
	}
	if task.Role != "" {
		updates["role"] = task.Role
	}
	if task.DependsOn != "" {
		updates["depends_on"] = task.DependsOn
	}
	if task.ChildrenJSON != "" {
		updates["children_json"] = task.ChildrenJSON
	}
	if len(updates) == 0 {
		return nil // nothing to update
	}
	updates["updated_at"] = time.Now().UTC()
	return s.db.DB.Model(&Task{}).Where("id = ?", task.ID).Updates(updates).Error
}

func (s *SQLiteStore) DeleteTask(id string) error {
	if id == "" {
		return fmt.Errorf("task ID is required")
	}
	return s.db.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("task_id = ?", id).Delete(&TaskStep{}).Error; err != nil {
			return err
		}
		if err := tx.Where("task_id = ?", id).Delete(&TaskEvent{}).Error; err != nil {
			return err
		}
		if err := tx.Where("task_id = ?", id).Delete(&TaskToolCall{}).Error; err != nil {
			return err
		}
		return tx.Delete(&Task{}, "id = ?", id).Error
	})
}

func (s *SQLiteStore) ListTasks(limit, offset int) ([]Task, error) {
	if limit <= 0 {
		limit = 20
	}
	var tasks []Task
	err := s.db.DB.
		Order("created_at DESC").
		Limit(limit).
		Offset(offset).
		Find(&tasks).Error
	return tasks, err
}

func (s *SQLiteStore) ListTasksByStatus(status Status, limit, offset int) ([]Task, error) {
	if limit <= 0 {
		limit = 20
	}
	var tasks []Task
	err := s.db.DB.
		Where("status = ?", status).
		Order("created_at DESC").
		Limit(limit).
		Offset(offset).
		Find(&tasks).Error
	return tasks, err
}

func (s *SQLiteStore) UpdateStatus(id string, newStatus Status) error {
	if id == "" {
		return fmt.Errorf("task ID is required")
	}
	if newStatus == "" {
		return fmt.Errorf("new status is required")
	}

	var task Task
	if err := s.db.DB.Where("id = ?", id).First(&task).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrTaskNotFound
		}
		return err
	}

	if err := ValidateTransition(task.Status, newStatus); err != nil {
		return err
	}
	// Cancelled is terminal — reject any transition away from it.
	if task.Status == StatusCancelled {
		return &TransitionError{From: StatusCancelled, To: newStatus}
	}

	now := time.Now().UTC()
	updates := map[string]any{
		"status":     newStatus,
		"updated_at": now,
	}
	switch newStatus {
	case StatusRunning:
		updates["started_at"] = now
	case StatusCompleted, StatusFailed, StatusCancelled:
		updates["completed_at"] = now
	}

	return s.db.DB.Model(&task).Updates(updates).Error
}

func (s *SQLiteStore) SaveCheckpoint(id string, data []byte) error {
	if id == "" {
		return fmt.Errorf("task ID is required")
	}
	return s.db.DB.Model(&Task{}).
		Where("id = ?", id).
		Update("checkpoint", data).Error
}

func (s *SQLiteStore) IncrementRetry(id string) (int, error) {
	if id == "" {
		return 0, fmt.Errorf("task ID is required")
	}
	var task Task
	if err := s.db.DB.Where("id = ?", id).First(&task).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, ErrTaskNotFound
		}
		return 0, err
	}
	task.RetryCount++
	if err := s.db.DB.Save(&task).Error; err != nil {
		return 0, err
	}
	return task.RetryCount, nil
}

func (s *SQLiteStore) CreateTaskStep(step *TaskStep) error {
	if step == nil {
		return fmt.Errorf("step is nil")
	}
	if step.ID == "" {
		return fmt.Errorf("step ID is required")
	}
	return s.db.DB.Create(step).Error
}

func (s *SQLiteStore) CreateTaskEvent(evt *TaskEvent) error {
	if evt == nil {
		return fmt.Errorf("event is nil")
	}
	if evt.ID == "" {
		return fmt.Errorf("event ID is required")
	}
	return s.db.DB.Create(evt).Error
}

func (s *SQLiteStore) CreateTaskToolCall(tc *TaskToolCall) error {
	if tc == nil {
		return fmt.Errorf("tool call is nil")
	}
	if tc.ID == "" {
		return fmt.Errorf("tool call ID is required")
	}
	return s.db.DB.Create(tc).Error
}

func (s *SQLiteStore) FailTask(id string, errMsg string) error {
	if id == "" {
		return fmt.Errorf("task ID is required")
	}
	var task Task
	if err := s.db.DB.Where("id = ?", id).First(&task).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrTaskNotFound
		}
		return err
	}
	if task.Status == StatusCancelled || task.Status == StatusCompleted {
		return fmt.Errorf("cannot fail task %s in %s state", id, task.Status)
	}
	now := time.Now().UTC()
	return s.db.DB.Model(&Task{}).
		Where("id = ?", id).
		Updates(map[string]any{"status": StatusFailed, "error": errMsg, "completed_at": now}).Error
}

// ClaimTask atomically claims a queued task for a worker.
// QUEUED means "ready to execute immediately." Failed tasks are promoted
// to queued by the scheduler only when their backoff has expired.
// The UPDATE itself carries the eligibility predicate, making the claim
// atomic: if another worker claimed the task between SELECT and UPDATE,
// RowsAffected == 0 and ErrNoEligibleTask is returned.
func (s *SQLiteStore) ClaimTask(workerID string, leaseDuration time.Duration) (*Task, error) {
	if workerID == "" {
		return nil, fmt.Errorf("workerID is required")
	}
	now := time.Now().UTC()
	until := now.Add(leaseDuration)

	var task Task
	err := s.db.DB.Transaction(func(tx *gorm.DB) error {
		// Only claim queued tasks (ready to execute immediately).
		// Failed tasks with backoff are promoted by the scheduler.
		err := tx.Where("status = ? AND (claimed_by = '' OR claimed_by IS NULL)", StatusQueued).
			Order("created_at ASC").
			First(&task).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrNoEligibleTask
			}
			return err
		}

		// Enforce dependency eligibility: a task with unmet DependsOn must not be claimable.
		if task.DependsOn != "" {
			var depIDs []string
			if depErr := json.Unmarshal([]byte(task.DependsOn), &depIDs); depErr == nil && len(depIDs) > 0 {
				var nonTerminal int64
				tx.Model(&Task{}).Where("id IN ? AND status != ?", depIDs, string(StatusCompleted)).Count(&nonTerminal)
				if nonTerminal > 0 {
					return ErrDependenciesNotMet
				}
			}
		}

		updates := map[string]any{
			"status":      StatusRunning,
			"claimed_by":  workerID,
			"claimed_at":  now,
			"lease_until": until,
			"updated_at":  now,
		}
		result := tx.Model(&Task{}).Where("id = ? AND status = ? AND (claimed_by = '' OR claimed_by IS NULL)", task.ID, StatusQueued).Updates(updates)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return ErrNoEligibleTask
		}
		// Reload to get updated values.
		return tx.First(&task, "id = ?", task.ID).Error
	})
	if err != nil {
		return nil, err
	}
	return &task, nil
}

// ReleaseLease clears lease fields, returning the task to queued.
// When workerID is non-empty, the release only succeeds if the caller
// currently holds the lease (prevents stale-worker interference).
func (s *SQLiteStore) ReleaseLease(id string, workerID ...string) error {
	now := time.Now().UTC()
	where := "id = ?"
	args := []any{id}
	if len(workerID) > 0 && workerID[0] != "" {
		where += " AND claimed_by = ?"
		args = append(args, workerID[0])
	}
	// Do not overwrite a terminal status — if the task has already been
	// finalised (completed, failed, cancelled) by another actor, preserve it.
	var t Task
	if err := s.db.DB.Where(where, args...).First(&t).Error; err != nil {
		return err
	}
	if t.Status.IsTerminal() {
		// Still clear lease fields so the task is no longer "owned".
		return s.db.DB.Model(&Task{}).Where(where, args...).Updates(map[string]any{
			"claimed_by":  "",
			"claimed_at":  nil,
			"lease_until": nil,
			"updated_at":  now,
		}).Error
	}
	result := s.db.DB.Model(&Task{}).
		Where(where, args...).
		Updates(map[string]any{
			"status":      StatusQueued,
			"claimed_by":  "",
			"claimed_at":  nil,
			"lease_until": nil,
			"updated_at":  now,
		})
	if result.Error != nil {
		return result.Error
	}
	if len(workerID) > 0 && workerID[0] != "" && result.RowsAffected == 0 {
		return fmt.Errorf("lease release: task not found or not owned by worker %q", workerID[0])
	}
	return nil
}

// UpdateLease extends the lease for an already-claimed task.
func (s *SQLiteStore) UpdateLease(id string, workerID string, leaseUntil time.Time) error {
	return s.db.DB.Model(&Task{}).
		Where("id = ? AND claimed_by = ?", id, workerID).
		Update("lease_until", leaseUntil).Error
}

// ExpireStaleLeases marks running tasks with expired leases as queued.
// A lease is expired when lease_until < now (no additional age offset).
func (s *SQLiteStore) ExpireStaleLeases() (int64, error) {
	result := s.db.DB.Model(&Task{}).
		Where("status = ? AND lease_until IS NOT NULL AND lease_until < ?",
			StatusRunning, time.Now().UTC()).
		Updates(map[string]any{
			"status":      StatusQueued,
			"claimed_by":  "",
			"claimed_at":  nil,
			"lease_until": nil,
		})
	return result.RowsAffected, result.Error
}

// MakeRetryable increments retry count, schedules the next attempt,
// clears any active lease, and transitions the task to failed.
// The scheduler is responsible for promoting failed+due tasks back to queued.
func (s *SQLiteStore) MakeRetryable(id string, backoff time.Duration) (int, error) {
	if id == "" {
		return 0, fmt.Errorf("task ID is required")
	}
	var task Task
	if err := s.db.DB.Where("id = ?", id).First(&task).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, ErrTaskNotFound
		}
		return 0, err
	}
	if task.Status == StatusCancelled || task.Status == StatusCompleted {
		return 0, fmt.Errorf("cannot retry task %s in %s state", id, task.Status)
	}
	task.RetryCount++
	nextRetry := time.Now().UTC().Add(backoff)
	task.NextRetryAt = &nextRetry

	now := time.Now().UTC()
	result := s.db.DB.Model(&Task{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"retry_count":   task.RetryCount,
			"next_retry_at": nextRetry,
			"status":        StatusFailed,
			"claimed_by":    "",
			"claimed_at":    nil,
			"lease_until":   nil,
			"updated_at":    now,
		})
	if result.Error != nil {
		return 0, result.Error
	}
	return task.RetryCount, nil
}

// ReadyRetries returns IDs of failed tasks whose next_retry_at has arrived
// and which still have retry budget remaining (retry_count < max_retries).
// MaxRetries=0 means no retries are allowed, so such tasks are never returned.
func (s *SQLiteStore) ReadyRetries(limit int) ([]string, error) {
	if limit <= 0 {
		limit = 100
	}
	var ids []string
	err := s.db.DB.Model(&Task{}).
		Where("status = ? AND next_retry_at IS NOT NULL AND next_retry_at <= ? AND retry_count < max_retries AND max_retries > 0",
			StatusFailed, time.Now().UTC()).
		Limit(limit).
		Pluck("id", &ids).Error
	return ids, err
}

// RecoverPendingTasks transitions pending tasks with no claimed_by to queued.
// This is used during startup recovery to reclaim orphaned pending tasks.
func (s *SQLiteStore) RecoverPendingTasks() (int64, error) {
	result := s.db.DB.Model(&Task{}).
		Where("status = ? AND (claimed_by = '' OR claimed_by IS NULL)", StatusPending).
		Updates(map[string]any{
			"status":     StatusQueued,
			"updated_at": time.Now().UTC(),
		})
	return result.RowsAffected, result.Error
}

// Plan persistence methods.

func (s *SQLiteStore) CreatePlan(plan *Plan) error {
	if plan == nil {
		return fmt.Errorf("plan is nil")
	}
	if plan.ID == "" {
		return fmt.Errorf("plan ID is required")
	}
	return s.db.DB.Create(plan).Error
}

func (s *SQLiteStore) GetPlan(id string) (*Plan, error) {
	if id == "" {
		return nil, fmt.Errorf("plan ID is required")
	}
	var plan Plan
	err := s.db.DB.Where("id = ?", id).First(&plan).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrTaskNotFound
		}
		return nil, err
	}
	return &plan, nil
}

func (s *SQLiteStore) GetPlanByTaskID(taskID string) (*Plan, error) {
	if taskID == "" {
		return nil, fmt.Errorf("task ID is required")
	}
	var plan Plan
	err := s.db.DB.Where("task_id = ?", taskID).Order("created_at DESC").First(&plan).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &plan, nil
}

func (s *SQLiteStore) UpdatePlan(plan *Plan) error {
	if plan == nil {
		return fmt.Errorf("plan is nil")
	}
	if plan.ID == "" {
		return fmt.Errorf("plan ID is required")
	}
	return s.db.DB.Save(plan).Error
}

func (s *SQLiteStore) CreatePlanEvent(evt *TaskPlanEvent) error {
	if evt == nil {
		return fmt.Errorf("event is nil")
	}
	if evt.ID == "" {
		return fmt.Errorf("event ID is required")
	}
	return s.db.DB.Create(evt).Error
}

func (s *SQLiteStore) ListChildTasks(parentID string, limit, offset int) ([]Task, error) {
	if parentID == "" {
		return nil, fmt.Errorf("parentID is required")
	}
	if limit <= 0 {
		limit = 100
	}
	var tasks []Task
	err := s.db.DB.
		Where("parent_id = ?", parentID).
		Order("created_at ASC").
		Limit(limit).
		Offset(offset).
		Find(&tasks).Error
	return tasks, err
}

func (s *SQLiteStore) ListTasksByRootID(rootID string, limit, offset int) ([]Task, error) {
	if rootID == "" {
		return nil, fmt.Errorf("rootID is required")
	}
	if limit <= 0 {
		limit = 100
	}
	var tasks []Task
	err := s.db.DB.
		Where("root_id = ?", rootID).
		Order("created_at ASC").
		Limit(limit).
		Offset(offset).
		Find(&tasks).Error
	return tasks, err
}

func (s *SQLiteStore) UpdateCoordinatorState(id string, state []byte) error {
	if id == "" {
		return fmt.Errorf("task ID is required")
	}
	return s.db.DB.Model(&Task{}).
		Where("id = ?", id).
		Update("coord_state", state).Error
}

func (s *SQLiteStore) GetCoordinatorState(id string) ([]byte, error) {
	if id == "" {
		return nil, fmt.Errorf("task ID is required")
	}
	var task Task
	if err := s.db.DB.Where("id = ?", id).First(&task).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrTaskNotFound
		}
		return nil, err
	}
	return task.CoordState, nil
}

// DependenciesMet checks whether all dependency IDs in dependsOnJSON are completed.
// Returns ErrDependenciesNotMet if any dependency is not yet completed.
func (s *SQLiteStore) DependenciesMet(dependsOnJSON string) error {
	if dependsOnJSON == "" {
		return nil
	}
	var depIDs []string
	if err := json.Unmarshal([]byte(dependsOnJSON), &depIDs); err != nil {
		return fmt.Errorf("parse depends_on: %w", err)
	}
	if len(depIDs) == 0 {
		return nil
	}
	var nonTerminal int64
	s.db.DB.Model(&Task{}).Where("id IN ? AND status != ?", depIDs, string(StatusCompleted)).Count(&nonTerminal)
	if nonTerminal > 0 {
		return fmt.Errorf("%w: %v", ErrDependenciesNotMet, depIDs)
	}
	return nil
}

// compile-time check: SQLiteStore satisfies Store.
var _ Store = (*SQLiteStore)(nil)
var _ PlanStore = (*SQLiteStore)(nil)

// ErrDependenciesNotMet is returned when a task has unmet dependencies.
var ErrDependenciesNotMet = errors.New("task has unmet dependencies")

// ErrCancelledTask is returned when attempting to claim a cancelled task.
var ErrCancelledTask = errors.New("task is cancelled")

// ErrNoEligibleTask is returned when there are no tasks to claim.
var ErrNoEligibleTask = errors.New("no eligible task available")

// PlanStore provides persistence operations for plans.
type PlanStore interface {
	CreatePlan(plan *Plan) error
	GetPlan(id string) (*Plan, error)
	GetPlanByTaskID(taskID string) (*Plan, error)
	UpdatePlan(plan *Plan) error
	CreatePlanEvent(evt *TaskPlanEvent) error
}

// MigrateTasks adds the task-related tables to the database.
func MigrateTasks(db *gorm.DB) error {
	return db.AutoMigrate(
		&Task{},
		&TaskStep{},
		&TaskEvent{},
		&TaskToolCall{},
	)
}

// MigrateAll adds all tables including plans.
func MigrateAll(db *gorm.DB) error {
	if err := MigrateTasks(db); err != nil {
		return err
	}
	return MigratePlans(db)
}
