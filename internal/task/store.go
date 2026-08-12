package task

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/EffNine/conductor/internal/database"
	"gorm.io/gorm"
)

// workerIDContextKey is the context key for the current worker ID.
type workerIDContextKey struct{}

// WithWorkerID returns a context carrying the given worker ID.
func WithWorkerID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, workerIDContextKey{}, id)
}

// WorkerIDFromContext returns the worker ID stored in ctx, if any.
func WorkerIDFromContext(ctx context.Context) string {
	id, _ := ctx.Value(workerIDContextKey{}).(string)
	return id
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

	now := time.Now().UTC()
	updates := map[string]any{
		"status":      newStatus,
		"updated_at":  now,
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
	now := time.Now().UTC()
	return s.db.DB.Model(&Task{}).
		Where("id = ?", id).
		Updates(map[string]any{"status": StatusFailed, "error": errMsg, "completed_at": now}).Error
}

// ClaimTask atomically claims a queued or retryable task for a worker.
func (s *SQLiteStore) ClaimTask(workerID string, leaseDuration time.Duration) (*Task, error) {
	if workerID == "" {
		return nil, fmt.Errorf("workerID is required")
	}
	now := time.Now().UTC()
	until := now.Add(leaseDuration)

	var task Task
	err := s.db.DB.Transaction(func(tx *gorm.DB) error {
		// Find the oldest eligible task.
		err := tx.Where("status = ? AND (claimed_by = '' OR claimed_by IS NULL)", StatusQueued).
			Order("created_at ASC").
			First(&task).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				// Fall back to failed tasks with next_retry_at <= now.
				err = tx.Where("status = ? AND next_retry_at IS NOT NULL AND next_retry_at <= ?", StatusFailed, now).
					Order("next_retry_at ASC").
					First(&task).Error
			}
			if err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return ErrNoEligibleTask
				}
				return err
			}
		}

		updates := map[string]any{
			"status":      StatusRunning,
			"claimed_by":  workerID,
			"claimed_at":  now,
			"lease_until": until,
			"updated_at":  now,
		}
		if task.Status == StatusFailed {
			updates["next_retry_at"] = nil
		}
		result := tx.Model(&Task{}).Where("id = ?", task.ID).Updates(updates)
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
// clears any active lease, and transitions the task to queued.
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
	task.RetryCount++
	nextRetry := time.Now().UTC().Add(backoff)
	task.NextRetryAt = &nextRetry

	now := time.Now().UTC()
	result := s.db.DB.Model(&Task{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"retry_count":   task.RetryCount,
			"next_retry_at": nextRetry,
			"status":        StatusQueued,
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

// ReadyRetries returns IDs of failed tasks whose next_retry_at has arrived.
func (s *SQLiteStore) ReadyRetries(limit int) ([]string, error) {
	if limit <= 0 {
		limit = 100
	}
	var ids []string
	err := s.db.DB.Model(&Task{}).
		Where("status = ? AND next_retry_at IS NOT NULL AND next_retry_at <= ?", StatusFailed, time.Now().UTC()).
		Limit(limit).
		Pluck("id", &ids).Error
	return ids, err
}

// ErrNoEligibleTask is returned when there are no tasks to claim.
var ErrNoEligibleTask = errors.New("no eligible task available")

// MigrateTasks adds the task-related tables to the database.
func MigrateTasks(db *gorm.DB) error {
	return db.AutoMigrate(
		&Task{},
		&TaskStep{},
		&TaskEvent{},
		&TaskToolCall{},
	)
}
