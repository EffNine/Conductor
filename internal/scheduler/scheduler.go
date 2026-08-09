// Package scheduler provides a job registration and execution framework.
//
// This package defines the interfaces and types for scheduled tasks such as
// health probes, checkpoints, cleanup, and future learning/forecasting jobs.
// No job implementations are provided here.
package scheduler

import (
	"context"
	"time"
)

// JobType represents the type of scheduled job.
type JobType string

const (
	// JobTypeHealthProbe runs provider health checks.
	JobTypeHealthProbe JobType = "health_probe"
	// JobTypeCheckpoint saves state to persistence.
	JobTypeCheckpoint JobType = "checkpoint"
	// JobTypeCleanup removes stale data.
	JobTypeCleanup JobType = "cleanup"
	// JobTypeLearning runs learning engine updates.
	JobTypeLearning JobType = "learning"
	// JobTypeForecast runs demand/cost forecasting.
	JobTypeForecast JobType = "forecast"
	// JobTypeRotation rotates provider credentials.
	JobTypeRotation JobType = "rotation"
	// JobTypeCatalogRefresh refreshes the model catalog.
	JobTypeCatalogRefresh JobType = "catalog_refresh"
	// JobTypeMetricsExport exports metrics to external systems.
	JobTypeMetricsExport JobType = "metrics_export"
)

// Job represents a scheduled task.
type Job struct {
	ID        string
	Type      JobType
	Name      string
	Handler   JobHandler
	Schedule  Schedule
	Enabled   bool
	Metadata  map[string]any
	CreatedAt time.Time
	UpdatedAt time.Time
}

// JobHandler is the function executed when a job runs.
type JobHandler func(ctx context.Context, job *Job) error

// Schedule defines when a job should run.
type Schedule interface {
	// Next returns the next time the job should run.
	Next(from time.Time) time.Time

	// Type returns the schedule type.
	Type() string
}

// FixedIntervalSchedule runs a job at fixed intervals.
type FixedIntervalSchedule struct {
	Interval time.Duration
}

func (s FixedIntervalSchedule) Next(from time.Time) time.Time {
	return from.Add(s.Interval)
}

func (s FixedIntervalSchedule) Type() string {
	return "fixed_interval"
}

// CronSchedule runs a job according to a cron expression.
type CronSchedule struct {
	Expression string
}

func (s CronSchedule) Next(from time.Time) time.Time {
	// TODO: Implement cron parsing
	return from.Add(1 * time.Hour)
}

func (s CronSchedule) Type() string {
	return "cron"
}

// OneTimeSchedule runs a job once at a specific time.
type OneTimeSchedule struct {
	RunAt time.Time
}

func (s OneTimeSchedule) Next(from time.Time) time.Time {
	if from.Before(s.RunAt) {
		return s.RunAt
	}
	return s.RunAt // Will be in the past, scheduler should handle
}

func (s OneTimeSchedule) Type() string {
	return "one_time"
}

// JobRegistry manages registered jobs.
type JobRegistry struct {
	jobs map[string]*Job
}

// NewJobRegistry creates a new job registry.
func NewJobRegistry() *JobRegistry {
	return &JobRegistry{
		jobs: make(map[string]*Job),
	}
}

// Register adds a job to the registry.
func (r *JobRegistry) Register(job *Job) error {
	if job.ID == "" {
		return ErrInvalidJobID
	}
	if job.Handler == nil {
		return ErrInvalidJobHandler
	}
	if job.Schedule == nil {
		return ErrInvalidJobSchedule
	}
	r.jobs[job.ID] = job
	return nil
}

// Unregister removes a job from the registry.
func (r *JobRegistry) Unregister(id string) error {
	if _, ok := r.jobs[id]; !ok {
		return ErrJobNotFound
	}
	delete(r.jobs, id)
	return nil
}

// Get retrieves a job by ID.
func (r *JobRegistry) Get(id string) (*Job, error) {
	job, ok := r.jobs[id]
	if !ok {
		return nil, ErrJobNotFound
	}
	return job, nil
}

// List returns all registered jobs.
func (r *JobRegistry) List() []*Job {
	jobs := make([]*Job, 0, len(r.jobs))
	for _, job := range r.jobs {
		jobs = append(jobs, job)
	}
	return jobs
}

// Enable turns on a job.
func (r *JobRegistry) Enable(id string) error {
	job, err := r.Get(id)
	if err != nil {
		return err
	}
	job.Enabled = true
	job.UpdatedAt = time.Now()
	return nil
}

// Disable turns off a job.
func (r *JobRegistry) Disable(id string) error {
	job, err := r.Get(id)
	if err != nil {
		return err
	}
	job.Enabled = false
	job.UpdatedAt = time.Now()
	return nil
}

// Scheduler executes jobs according to their schedules.
type Scheduler interface {
	// Start begins executing scheduled jobs.
	Start(ctx context.Context) error

	// Stop gracefully shuts down the scheduler.
	Stop(ctx context.Context) error

	// Registry returns the job registry.
	Registry() *JobRegistry
}

// Errors
var (
	ErrInvalidJobID       = &SchedulerError{Code: "invalid_job_id", Message: "job ID is required"}
	ErrInvalidJobHandler  = &SchedulerError{Code: "invalid_job_handler", Message: "job handler is required"}
	ErrInvalidJobSchedule = &SchedulerError{Code: "invalid_job_schedule", Message: "job schedule is required"}
	ErrJobNotFound        = &SchedulerError{Code: "job_not_found", Message: "job not found"}
)

// SchedulerError represents an error from the scheduler.
type SchedulerError struct {
	Code    string
	Message string
}

func (e *SchedulerError) Error() string {
	return e.Message
}
