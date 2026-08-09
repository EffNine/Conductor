package scheduler

import (
	"context"
	"testing"
	"time"
)

func TestJobTypeConstants(t *testing.T) {
	expected := []JobType{
		JobTypeHealthProbe,
		JobTypeCheckpoint,
		JobTypeCleanup,
		JobTypeLearning,
		JobTypeForecast,
		JobTypeRotation,
		JobTypeCatalogRefresh,
		JobTypeMetricsExport,
	}

	for _, jobType := range expected {
		if jobType == "" {
			t.Error("expected non-empty job type constant")
		}
	}
}

func TestFixedIntervalSchedule(t *testing.T) {
	schedule := FixedIntervalSchedule{
		Interval: 5 * time.Minute,
	}

	next := schedule.Next(time.Now())
	expected := time.Now().Add(5 * time.Minute)

	if next.Before(expected.Add(-time.Second)) || next.After(expected.Add(time.Second)) {
		t.Errorf("expected next run around %v, got %v", expected, next)
	}
}

func TestOneTimeSchedule(t *testing.T) {
	runAt := time.Now().Add(1 * time.Hour)
	schedule := OneTimeSchedule{
		RunAt: runAt,
	}

	next := schedule.Next(time.Now())
	if !next.Equal(runAt) {
		t.Errorf("expected %v, got %v", runAt, next)
	}
}

func TestJobRegistryRegister(t *testing.T) {
	registry := NewJobRegistry()
	job := &Job{
		ID:      "health-probe",
		Type:    JobTypeHealthProbe,
		Name:    "Health Probe",
		Handler: func(ctx context.Context, job *Job) error { return nil },
		Schedule: FixedIntervalSchedule{
			Interval: 1 * time.Minute,
		},
		Enabled: true,
	}

	err := registry.Register(job)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	retrieved, err := registry.Get("health-probe")
	if err != nil {
		t.Fatalf("expected to retrieve job, got %v", err)
	}
	if retrieved.ID != "health-probe" {
		t.Errorf("expected 'health-probe', got %s", retrieved.ID)
	}
}

func TestJobRegistryList(t *testing.T) {
	registry := NewJobRegistry()

	_ = registry.Register(&Job{
		ID:      "job-1",
		Type:    JobTypeHealthProbe,
		Handler: func(ctx context.Context, job *Job) error { return nil },
		Schedule: FixedIntervalSchedule{Interval: 1 * time.Minute},
	})
	_ = registry.Register(&Job{
		ID:      "job-2",
		Type:    JobTypeCleanup,
		Handler: func(ctx context.Context, job *Job) error { return nil },
		Schedule: FixedIntervalSchedule{Interval: 5 * time.Minute},
	})

	jobs := registry.List()
	if len(jobs) != 2 {
		t.Errorf("expected 2 jobs, got %d", len(jobs))
	}
}

func TestJobRegistryDisable(t *testing.T) {
	registry := NewJobRegistry()

	job := &Job{
		ID:      "test-job",
		Type:    JobTypeCleanup,
		Handler: func(ctx context.Context, job *Job) error { return nil },
		Schedule: FixedIntervalSchedule{Interval: 1 * time.Hour},
		Enabled: true,
	}
	_ = registry.Register(job)

	err := registry.Disable("test-job")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	retrieved, _ := registry.Get("test-job")
	if retrieved.Enabled {
		t.Error("expected job to be disabled")
	}
}

func TestJobRegistryUnregister(t *testing.T) {
	registry := NewJobRegistry()

	job := &Job{
		ID:      "to-remove",
		Type:    JobTypeCleanup,
		Handler: func(ctx context.Context, job *Job) error { return nil },
		Schedule: FixedIntervalSchedule{Interval: 1 * time.Hour},
	}
	_ = registry.Register(job)

	err := registry.Unregister("to-remove")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	_, err = registry.Get("to-remove")
	if err == nil {
		t.Error("expected job to be removed")
	}
}

func TestSchedulerErrors(t *testing.T) {
	registry := NewJobRegistry()

	// Missing ID
	err := registry.Register(&Job{
		Type:    JobTypeCleanup,
		Handler: func(ctx context.Context, job *Job) error { return nil },
		Schedule: FixedIntervalSchedule{Interval: 1 * time.Hour},
	})
	if err == nil {
		t.Error("expected error for missing job ID")
	}

	// Missing handler
	err = registry.Register(&Job{
		ID:       "no-handler",
		Type:     JobTypeCleanup,
		Schedule: FixedIntervalSchedule{Interval: 1 * time.Hour},
	})
	if err == nil {
		t.Error("expected error for missing handler")
	}

	// Missing schedule
	err = registry.Register(&Job{
		ID:      "no-schedule",
		Type:    JobTypeCleanup,
		Handler: func(ctx context.Context, job *Job) error { return nil },
	})
	if err == nil {
		t.Error("expected error for missing schedule")
	}

	// Not found
	_, err = registry.Get("nonexistent")
	if err == nil {
		t.Error("expected error for non-existent job")
	}
}
