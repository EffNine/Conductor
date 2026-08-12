package worker

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/EffNine/conductor/internal/task"
	"go.uber.org/zap"
)

// Config holds worker pool configuration.
type Config struct {
	WorkerCount  int
	PollInterval time.Duration
	LeaseDuration time.Duration
	ShutdownTimeout time.Duration
}

// DefaultConfig returns sensible defaults.
func DefaultConfig() Config {
	return Config{
		WorkerCount:     2,
		PollInterval:    1 * time.Second,
		LeaseDuration:   5 * time.Minute,
		ShutdownTimeout: 30 * time.Second,
	}
}

// Pool manages a set of workers that claim and execute tasks from the queue.
type Pool struct {
	cfg       Config
	store     task.Store
	executor  task.Executor
	logger    *zap.Logger
	running   atomic.Bool
	stopCh    chan struct{}
	wg        sync.WaitGroup
	workerIDs []string
}

// New creates a new worker pool.
func New(cfg Config, store task.Store, executor task.Executor, logger *zap.Logger) *Pool {
	if cfg.WorkerCount <= 0 {
		cfg.WorkerCount = DefaultConfig().WorkerCount
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = DefaultConfig().PollInterval
	}
	if cfg.LeaseDuration <= 0 {
		cfg.LeaseDuration = DefaultConfig().LeaseDuration
	}
	workerIDs := make([]string, cfg.WorkerCount)
	for i := range workerIDs {
		workerIDs[i] = fmt.Sprintf("worker-%d", i+1)
	}
	return &Pool{
		cfg:       cfg,
		store:     store,
		executor:  executor,
		logger:    logger,
		stopCh:    make(chan struct{}),
		workerIDs: workerIDs,
	}
}

// Start launches the configured number of workers.
func (p *Pool) Start() {
	if p.running.Swap(true) {
		return // already running
	}
	p.logger.Info("starting worker pool",
		zap.Int("workers", len(p.workerIDs)),
		zap.Duration("poll_interval", p.cfg.PollInterval),
		zap.Duration("lease_duration", p.cfg.LeaseDuration),
	)
	for _, id := range p.workerIDs {
		p.wg.Add(1)
		go p.runWorker(id)
	}
}

// Stop signals all workers to stop and waits up to ShutdownTimeout.
func (p *Pool) Stop() {
	if !p.running.Swap(false) {
		return
	}
	close(p.stopCh)
	done := make(chan struct{})
	go func() {
		p.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		p.logger.Info("worker pool stopped")
	case <-time.After(p.cfg.ShutdownTimeout):
		p.logger.Warn("worker pool shutdown timed out, forcing release of leases")
		// Leases will expire naturally; running tasks will be reclaimed on next poll.
	}
}

func (p *Pool) runWorker(id string) {
	defer p.wg.Done()
	ticker := time.NewTicker(p.cfg.PollInterval)
	defer ticker.Stop()
	p.logger.Info("worker started", zap.String("worker_id", id))
	for {
		select {
		case <-p.stopCh:
			p.logger.Info("worker stopping", zap.String("worker_id", id))
			return
		case <-ticker.C:
			p.tick(id)
		}
	}
}

func (p *Pool) tick(workerID string) {
	task_, err := p.store.ClaimTask(workerID, p.cfg.LeaseDuration)
	if err != nil {
		if err == task.ErrNoEligibleTask {
			return // nothing to do this cycle
		}
		p.logger.Warn("failed to claim task", zap.Error(err))
		return
	}
	p.logger.Info("task claimed",
		zap.String("worker_id", workerID),
		zap.String("task_id", task_.ID),
		zap.String("status", string(task_.Status)),
	)
	p.execute(workerID, task_.ID)
}

func (p *Pool) execute(workerID, taskID string) {
	ctx := task.WithWorkerID(context.Background(), workerID)

	err := p.executor.Execute(ctx, taskID)
	if err != nil {
		p.logger.Error("task execution failed",
			zap.String("worker_id", workerID),
			zap.String("task_id", taskID),
			zap.Error(err),
		)
		p.handleFailure(workerID, taskID, err)
		return
	}
	p.logger.Info("task completed",
		zap.String("worker_id", workerID),
		zap.String("task_id", taskID),
	)
	// Only release lease if executor hasn't already completed the task.
	p.releaseLeaseIfStillRunning(workerID, taskID)
}

func (p *Pool) releaseLeaseIfStillRunning(workerID, taskID string) {
	t, err := p.store.GetTask(taskID)
	if err != nil || t.Status.IsTerminal() {
		return // executor already handled terminal state
	}
	if err := p.store.ReleaseLease(taskID, workerID); err != nil {
		p.logger.Warn("failed to release lease", zap.Error(err))
	}
}

func (p *Pool) handleFailure(workerID, taskID string, err error) {
	// Check if the task is already terminal (e.g. cancelled by another actor).
	t, getErr := p.store.GetTask(taskID)
	if getErr != nil {
		p.logger.Warn("failed to get task after execution error", zap.Error(getErr))
		return
	}
	if t.Status.IsTerminal() {
		p.releaseLeaseIfStillRunning(workerID, taskID)
		return
	}
	// Attempt retry if retries remain.
	if t.MaxRetries > 0 && t.RetryCount < t.MaxRetries {
		backoff := computeBackoff(t.RetryCount)
		if _, incErr := p.store.MakeRetryable(taskID, backoff); incErr != nil {
			p.logger.Error("failed to make task retryable", zap.Error(incErr))
		}
		p.releaseLeaseIfStillRunning(workerID, taskID)
		return
	}
	// Max retries exceeded or no retries configured — fail permanently.
	if updateErr := p.store.UpdateStatus(taskID, task.StatusFailed); updateErr != nil {
		p.logger.Error("failed to mark task as failed", zap.Error(updateErr))
	}
	p.releaseLeaseIfStillRunning(workerID, taskID)
}

// computeBackoff returns exponential backoff duration for retry N (0-indexed).
func computeBackoff(retryCount int) time.Duration {
	const (
		base     = 5 * time.Second
		maxDelay = 15 * time.Minute
	)
	d := base
	for i := 0; i < retryCount; i++ {
		d *= 3
		if d >= maxDelay {
			d = maxDelay
			break
		}
	}
	return d
}
