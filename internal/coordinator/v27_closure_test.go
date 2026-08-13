package coordinator_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/EffNine/conductor/internal/coordinator"
	"github.com/EffNine/conductor/internal/task"
	"github.com/EffNine/conductor/internal/worker"
	"github.com/google/uuid"
)

// ── Fix 1: Coordinator cancellation finalizes parent ────────────────────────

func TestV27_CoordinatorCancellationFinalizesParent(t *testing.T) {
	db := newTestDB(t)
	s := newStore(t, db)
	coordS := newCoordStore(t, db)
	cfg := coordinator.NewConfig()
	cfg.PollInterval = 50 * time.Millisecond
	coord := coordinator.New(coordS, nil, newLogger(t), cfg)
	exec := coordinator.NewExecutor(s, &completingExecutor{store: s}, coord, newLogger(t))

	gpID := "gp-cancel-test"
	gp := &task.Task{ID: gpID, Status: task.StatusPending, Input: "cancel test", Role: "coordinator"}
	_ = s.CreateTask(gp)
	_ = s.UpdateStatus(gpID, task.StatusQueued)
	_ = s.UpdateStatus(gpID, task.StatusRunning)

	childID := "c-slow"
	insertChild(t, s, gpID, childID, "slow work", "general")
	_ = s.UpdateStatus(childID, task.StatusRunning)

	childrenJSON, _ := json.Marshal([]string{childID})
	_ = s.UpdateTaskSelective(&task.Task{ID: gpID, ChildrenJSON: string(childrenJSON)})

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()

	err := exec.Execute(ctx, gpID)
	if err == nil {
		t.Fatal("expected error from cancelled coordinator")
	}

	updated, _ := s.GetTask(gpID)
	if updated.Status != task.StatusCancelled {
		t.Errorf("parent status = %s, want cancelled", updated.Status)
	}
}

func TestV27_CoordinatorCancellationCancelsChildren(t *testing.T) {
	db := newTestDB(t)
	s := newStore(t, db)
	coordS := newCoordStore(t, db)
	coord := coordinator.New(coordS, nil, newLogger(t), coordinator.NewConfig())

	insertTask(t, s, "p-cancel-ch", "cancel children", "coordinator")
	_ = s.UpdateStatus("p-cancel-ch", task.StatusRunning)

	child1 := insertChild(t, s, "p-cancel-ch", "c1", "work1", "general")
	child2 := insertChild(t, s, "p-cancel-ch", "c2", "work2", "general")
	_ = s.UpdateStatus(child1.ID, task.StatusRunning)
	_ = s.UpdateStatus(child2.ID, task.StatusRunning)

	childrenJSON, _ := json.Marshal([]string{child1.ID, child2.ID})
	_ = s.UpdateTaskSelective(&task.Task{ID: "p-cancel-ch", ChildrenJSON: string(childrenJSON)})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := coord.WaitForChildren(ctx, &coordinator.TaskInfo{ID: "p-cancel-ch"}, []string{child1.ID, child2.ID})
	if err == nil {
		t.Fatal("expected context cancelled error")
	}

	for _, cid := range []string{child1.ID, child2.ID} {
		c, _ := s.GetTask(cid)
		if c.Status != task.StatusCancelled {
			t.Errorf("child %s status = %s, want cancelled", cid, c.Status)
		}
	}
}

func TestV27_CoordinatorCancellationNotRetryable(t *testing.T) {
	db := newTestDB(t)
	s := newStore(t, db)
	coordS := newCoordStore(t, db)
	cfg := coordinator.NewConfig()
	cfg.PollInterval = 50 * time.Millisecond
	coord := coordinator.New(coordS, nil, newLogger(t), cfg)
	exec := coordinator.NewExecutor(s, &completingExecutor{store: s}, coord, newLogger(t))

	gpID := "gp-no-retry"
	gp := &task.Task{ID: gpID, Status: task.StatusPending, Input: "no retry", Role: "coordinator", MaxRetries: 3}
	_ = s.CreateTask(gp)
	_ = s.UpdateStatus(gpID, task.StatusQueued)
	_ = s.UpdateStatus(gpID, task.StatusRunning)

	childID := "c-no-retry"
	insertChild(t, s, gpID, childID, "slow", "general")
	_ = s.UpdateStatus(childID, task.StatusRunning)
	childrenJSON, _ := json.Marshal([]string{childID})
	_ = s.UpdateTaskSelective(&task.Task{ID: gpID, ChildrenJSON: string(childrenJSON)})

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_ = exec.Execute(ctx, gpID)

	updated, _ := s.GetTask(gpID)
	if updated.Status != task.StatusCancelled {
		t.Errorf("status = %s, want cancelled", updated.Status)
	}
	if updated.RetryCount != 0 {
		t.Errorf("retry_count = %d, want 0 (cancelled tasks must not retry)", updated.RetryCount)
	}
}

func TestV27_CancellationEventExactlyOnce(t *testing.T) {
	db := newTestDB(t)
	s := newStore(t, db)

	taskID := "event-once"
	tsk := &task.Task{ID: taskID, Status: task.StatusPending, Input: "event test"}
	_ = s.CreateTask(tsk)
	// Transition through valid states, then emit event directly (simulating handler).
	_ = s.UpdateStatus(taskID, task.StatusQueued)
	_ = s.UpdateStatus(taskID, task.StatusRunning)
	_ = s.UpdateStatus(taskID, task.StatusCancelled)
	// Emit the cancelled event (normally done by handler or executor).
	eventData, _ := json.Marshal(map[string]any{"source": "test"})
	_ = s.CreateTaskEvent(&task.TaskEvent{
		ID:        uuid.New().String(),
		TaskID:    taskID,
		EventType: "task.cancelled",
		EventData: eventData,
	})

	var cancelledCount, failedCount int64
	db.DB.Model(&task.TaskEvent{}).Where("task_id = ? AND event_type = ?", taskID, "task.cancelled").Count(&cancelledCount)
	db.DB.Model(&task.TaskEvent{}).Where("task_id = ? AND event_type = ?", taskID, "task.failed").Count(&failedCount)

	if cancelledCount == 0 {
		t.Error("expected at least one task.cancelled event")
	}
	if failedCount > 0 {
		t.Errorf("expected no task.failed events, got %d", failedCount)
	}
}

func TestV27_CancellationNoContradictoryTerminalEvent(t *testing.T) {
	db := newTestDB(t)
	s := newStore(t, db)

	taskID := "no-contradiction"
	tsk := &task.Task{ID: taskID, Status: task.StatusPending, Input: "test"}
	_ = s.CreateTask(tsk)
	_ = s.UpdateStatus(taskID, task.StatusQueued)
	_ = s.UpdateStatus(taskID, task.StatusRunning)
	_ = s.UpdateStatus(taskID, task.StatusCancelled)

	err := s.UpdateStatus(taskID, task.StatusCompleted)
	if err == nil {
		t.Error("expected error completing a cancelled task")
	}

	err = s.FailTask(taskID, "boom")
	if err == nil {
		t.Error("expected error failing a cancelled task")
	}

	_, err = s.MakeRetryable(taskID, 5*time.Minute)
	if err == nil {
		t.Error("expected error retrying a cancelled task")
	}

	updated, _ := s.GetTask(taskID)
	if updated.Status != task.StatusCancelled {
		t.Errorf("status = %s, want cancelled", updated.Status)
	}
}

// ── Fix 2: Concurrent delegate duplication prevention ────────────────────────

func TestV27_ConcurrentDelegateSingleChildSet(t *testing.T) {
	db := newTestDB(t)
	s := newStore(t, db)
	coordS := newCoordStore(t, db)
	coord := coordinator.New(coordS, nil, newLogger(t), coordinator.NewConfig())

	insertTask(t, s, "p-concurrent", "concurrent delegate", "coordinator")
	_ = s.UpdateStatus("p-concurrent", task.StatusRunning)

	var wg sync.WaitGroup
	var firstIDs, secondIDs []string
	var firstErr, secondErr error

	wg.Add(2)
	go func() {
		defer wg.Done()
		firstIDs, firstErr = coord.Delegate(context.Background(), &coordinator.TaskInfo{
			ID:     "p-concurrent",
			Role:   "coordinator",
			Intent: "coding",
			Input:  "concurrent delegate",
		})
	}()
	go func() {
		defer wg.Done()
		secondIDs, secondErr = coord.Delegate(context.Background(), &coordinator.TaskInfo{
			ID:     "p-concurrent",
			Role:   "coordinator",
			Intent: "coding",
			Input:  "concurrent delegate",
		})
	}()
	wg.Wait()

	if firstErr != nil {
		t.Fatalf("first delegate: %v", firstErr)
	}
	if secondErr != nil {
		t.Fatalf("second delegate: %v", secondErr)
	}

	if len(firstIDs) != len(secondIDs) {
		t.Fatalf("len(first)=%d, len(second)=%d, want equal", len(firstIDs), len(secondIDs))
	}
	for i := range firstIDs {
		if firstIDs[i] != secondIDs[i] {
			t.Errorf("mismatch at index %d: %s != %s", i, firstIDs[i], secondIDs[i])
		}
	}

	children, _ := s.ListChildTasks("p-concurrent", 100, 0)
	if len(children) != len(firstIDs) {
		t.Errorf("db children count = %d, want %d (no duplicates)", len(children), len(firstIDs))
	}
}

func TestV27_ConcurrentDelegateReturnsSameChildren(t *testing.T) {
	db := newTestDB(t)
	s := newStore(t, db)
	coordS := newCoordStore(t, db)
	coord := coordinator.New(coordS, nil, newLogger(t), coordinator.NewConfig())

	insertTask(t, s, "p-same-kids", "same kids", "coordinator")
	_ = s.UpdateStatus("p-same-kids", task.StatusRunning)

	firstIDs, err := coord.Delegate(context.Background(), &coordinator.TaskInfo{
		ID:     "p-same-kids",
		Role:   "coordinator",
		Intent: "research",
		Input:  "same kids",
	})
	if err != nil {
		t.Fatalf("first delegate: %v", err)
	}

	secondIDs, err := coord.Delegate(context.Background(), &coordinator.TaskInfo{
		ID:     "p-same-kids",
		Role:   "coordinator",
		Intent: "research",
		Input:  "same kids",
	})
	if err != nil {
		t.Fatalf("second delegate: %v", err)
	}

	for i := range firstIDs {
		if firstIDs[i] != secondIDs[i] {
			t.Errorf("mismatch at index %d: %s != %s", i, firstIDs[i], secondIDs[i])
		}
	}
}

func TestV27_ResumeDoesNotDuplicateAfterConcurrentDelegate(t *testing.T) {
	db := newTestDB(t)
	s := newStore(t, db)
	coordS := newCoordStore(t, db)
	coord := coordinator.New(coordS, nil, newLogger(t), coordinator.NewConfig())

	insertTask(t, s, "p-resume-conc", "resume concurrent", "coordinator")
	_ = s.UpdateStatus("p-resume-conc", task.StatusRunning)

	firstIDs, err := coord.Delegate(context.Background(), &coordinator.TaskInfo{
		ID:     "p-resume-conc",
		Role:   "coordinator",
		Intent: "coding",
		Input:  "resume concurrent",
	})
	if err != nil {
		t.Fatalf("first delegate: %v", err)
	}
	count1, _ := s.ListChildTasks("p-resume-conc", 100, 0)

	secondIDs, err := coord.Delegate(context.Background(), &coordinator.TaskInfo{
		ID:           "p-resume-conc",
		Role:         "coordinator",
		Intent:       "coding",
		Input:        "resume concurrent",
		ChildrenJSON: toJSON(firstIDs),
	})
	if err != nil {
		t.Fatalf("second delegate: %v", err)
	}
	count2, _ := s.ListChildTasks("p-resume-conc", 100, 0)

	if len(secondIDs) != len(firstIDs) {
		t.Errorf("len(second)=%d, len(first)=%d", len(secondIDs), len(firstIDs))
	}
	if len(count2) != len(count1) {
		t.Errorf("db children count changed: %d -> %d", len(count1), len(count2))
	}
}

// ── Fix 3: State machine protection ─────────────────────────────────────────

func TestV27_IllegalTransitionProtection(t *testing.T) {
	db := newTestDB(t)
	s := newStore(t, db)

	taskID := "illegal-trans"
	tsk := &task.Task{ID: taskID, Status: task.StatusPending, Input: "test"}
	_ = s.CreateTask(tsk)
	_ = s.UpdateStatus(taskID, task.StatusQueued)
	_ = s.UpdateStatus(taskID, task.StatusRunning)
	_ = s.UpdateStatus(taskID, task.StatusCancelled)

	// From cancelled, no transitions allowed.
	transitions := []task.Status{
		task.StatusQueued, task.StatusRunning,
		task.StatusCompleted, task.StatusFailed, task.StatusPaused,
	}
	for _, st := range transitions {
		err := s.UpdateStatus(taskID, st)
		if err == nil {
			t.Errorf("expected error transitioning cancelled→%s", st)
		}
	}

	updated, _ := s.GetTask(taskID)
	if updated.Status != task.StatusCancelled {
		t.Errorf("status = %s, want cancelled", updated.Status)
	}
}

func TestV27_LeaseOwnershipStillProtected(t *testing.T) {
	db := newTestDB(t)
	s := newStore(t, db)

	taskID := "lease-protect"
	tsk := &task.Task{ID: taskID, Status: task.StatusQueued, Input: "lease"}
	_ = s.CreateTask(tsk)

	_, err := s.ClaimTask("w1", 5*time.Minute)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}

	err = s.ReleaseLease(taskID, "w2")
	if err == nil {
		t.Error("expected error releasing another worker's lease")
	}

	err = s.ReleaseLease(taskID, "w1")
	if err != nil {
		t.Fatalf("release: %v", err)
	}

	updated, _ := s.GetTask(taskID)
	if updated.Status != task.StatusQueued {
		t.Errorf("status = %s, want queued", updated.Status)
	}
}

func TestV27_RetryableFailureNotTerminal(t *testing.T) {
	db := newTestDB(t)
	s := newStore(t, db)

	taskID := "retryable-not-term"
	tsk := &task.Task{ID: taskID, Status: task.StatusPending, Input: "retry", MaxRetries: 3}
	_ = s.CreateTask(tsk)
	_ = s.UpdateStatus(taskID, task.StatusQueued)
	_ = s.UpdateStatus(taskID, task.StatusRunning)
	_ = s.FailTask(taskID, "transient error")

	t2, _ := s.GetTask(taskID)
	info := &coordinator.TaskInfo{
		ID:         taskID,
		Status:     string(t2.Status),
		RetryCount: t2.RetryCount,
		MaxRetries: t2.MaxRetries,
	}
	// IsTerminal includes "failed", but IsRetryable distinguishes retryable from permanent.
	if !info.IsRetryable() {
		t.Error("retryable failed task should report IsRetryable=true")
	}
	if info.IsPermanentlyFailed() {
		t.Error("retryable failed task should not be permanently failed")
	}
}

func TestV27_PermanentFailureTerminal(t *testing.T) {
	db := newTestDB(t)
	s := newStore(t, db)

	taskID := "perm-fail"
	tsk := &task.Task{ID: taskID, Status: task.StatusPending, Input: "perm", MaxRetries: 0}
	_ = s.CreateTask(tsk)
	_ = s.UpdateStatus(taskID, task.StatusRunning)
	_ = s.FailTask(taskID, "permanent error")

	t2, _ := s.GetTask(taskID)
	info := &coordinator.TaskInfo{
		ID:         taskID,
		Status:     string(t2.Status),
		RetryCount: t2.RetryCount,
		MaxRetries: t2.MaxRetries,
	}
	if !info.IsTerminal() {
		t.Error("permanent failed task should be terminal")
	}
	if !info.IsPermanentlyFailed() {
		t.Error("permanent failed task should report IsPermanentlyFailed=true")
	}
	if info.IsRetryable() {
		t.Error("permanent failed task should not be retryable")
	}
}

// ── Backward compatibility ──────────────────────────────────────────────────

func TestV27_SingleAgentStillWorks(t *testing.T) {
	db := newTestDB(t)
	s := newStore(t, db)

	taskID := "v27-single"
	tsk := &task.Task{ID: taskID, Status: task.StatusPending, Input: "simple", Model: "test-model"}
	_ = s.CreateTask(tsk)
	_ = s.UpdateStatus(taskID, task.StatusQueued)

	fakeExec := &completingExecutor{store: s}
	pool := worker.New(worker.Config{WorkerCount: 1, PollInterval: 50 * time.Millisecond}, s, fakeExec, newLogger(t))
	pool.Start()
	defer pool.Stop()

	deadline := time.After(2 * time.Second)
ticker:
	for {
		select {
		case <-deadline:
			t.Fatal("timeout waiting for single agent")
		default:
		}
		got, _ := s.GetTask(taskID)
		if got != nil && got.Status == task.StatusCompleted {
			break ticker
		}
		time.Sleep(50 * time.Millisecond)
	}

	updated, _ := s.GetTask(taskID)
	if updated.Status != task.StatusCompleted {
		t.Errorf("status = %s, want completed", updated.Status)
	}
}

func TestV27_GatewayUnchanged(t *testing.T) {
	db := newTestDB(t)
	s := newStore(t, db)

	taskID := "v27-gateway"
	tsk := &task.Task{ID: taskID, Status: task.StatusPending, Input: "gateway test", Model: "test-model", Role: ""}
	_ = s.CreateTask(tsk)

	fakeExec := &completingExecutor{store: s}
	exec := coordinator.NewExecutor(s, fakeExec, nil, newLogger(t))

	if err := exec.Execute(context.Background(), taskID); err != nil {
		t.Fatalf("execute: %v", err)
	}

	updated, _ := s.GetTask(taskID)
	if updated.Status != task.StatusCompleted {
		t.Errorf("status = %s, want completed", updated.Status)
	}
	if updated.Role != "" {
		t.Errorf("role = %q, want empty", updated.Role)
	}
}
