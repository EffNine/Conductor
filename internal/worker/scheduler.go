package worker

import (
	"encoding/json"
	"sync"
	"time"

	"github.com/EffNine/conductor/internal/task"
	"go.uber.org/zap"
)

// Scheduler wakes periodically and promotes retryable failed tasks to queued
// so the worker pool can claim them. It does NOT execute tasks itself.
type Scheduler struct {
	store   task.Store
	logger  *zap.Logger
	interval time.Duration
	running  bool
	stopCh   chan struct{}
	wg       sync.WaitGroup
}

// NewScheduler creates a task retry scheduler.
func NewScheduler(store task.Store, logger *zap.Logger) *Scheduler {
	return &Scheduler{
		store:    store,
		logger:   logger,
		interval: 500 * time.Millisecond,
		stopCh:   make(chan struct{}),
	}
}

// Start begins the scheduler loop.
func (s *Scheduler) Start() {
	if s.running {
		return
	}
	s.running = true
	s.wg.Add(1)
	go s.run()
	s.logger.Info("scheduler started")
}

// Stop signals the scheduler to stop and waits for it to finish.
func (s *Scheduler) Stop() {
	if !s.running {
		return
	}
	close(s.stopCh)
	s.wg.Wait()
	s.running = false
	s.logger.Info("scheduler stopped")
}

func (s *Scheduler) run() {
	defer s.wg.Done()
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		select {
		case <-s.stopCh:
			return
		case <-ticker.C:
			s.tick()
		}
	}
}

func (s *Scheduler) tick() {
	ids, err := s.store.ReadyRetries(100)
	if err != nil {
		s.logger.Warn("scheduler: failed to query ready retries", zap.Error(err))
		return
	}
	if len(ids) == 0 {
		return
	}
	for _, id := range ids {
		// Skip tasks with unmet dependencies — they should not be promoted yet.
		t, err := s.store.GetTask(id)
		if err != nil {
			s.logger.Warn("scheduler: failed to get task for dependency check",
				zap.String("task_id", id), zap.Error(err))
			continue
		}
		if t.DependsOn != "" {
			if depErr := s.store.DependenciesMet(t.DependsOn); depErr != nil {
				s.logger.Debug("scheduler: skipping promotion due to unmet dependencies",
					zap.String("task_id", id))
				continue
			}
		}
		// Transition failed→queued only if still failed (no-op otherwise).
		if transErr := s.store.UpdateStatus(id, task.StatusQueued); transErr != nil {
			s.logger.Warn("scheduler: failed to promote task",
				zap.String("task_id", id), zap.Error(transErr))
		}
	}
	// Also expire stale leases so crashed workers' tasks can be reclaimed.
	if expired, expErr := s.store.ExpireStaleLeases(); expErr != nil {
		s.logger.Warn("scheduler: failed to expire stale leases", zap.Error(expErr))
	} else if expired > 0 {
		s.logger.Info("scheduler: expired stale leases", zap.Int64("count", expired))
	}
}

func uuidNew() string { return "sched-uuid-placeholder" }

func mustMarshal(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return b
}
