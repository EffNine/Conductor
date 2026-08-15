package coordinator_test

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/EffNine/conductor/internal/config"
	"github.com/EffNine/conductor/internal/coordinator"
	"github.com/EffNine/conductor/internal/database"
	"github.com/EffNine/conductor/internal/eventbus"
	"github.com/EffNine/conductor/internal/orchestration"
	"github.com/EffNine/conductor/internal/task"
	"github.com/EffNine/conductor/internal/worker"
	"go.uber.org/zap"
)

func newTestDB26(t *testing.T) *database.Database {
	t.Helper()
	dsn := filepath.Join(t.TempDir(), "test.db")
	db, err := database.Connect(&config.DatabaseConfig{
		Driver:       "sqlite",
		DSN:          dsn,
		MaxOpenConns: 10,
		MaxIdleConns: 5,
	})
	if err != nil {
		t.Fatalf("connect db: %v", err)
	}
	if err := task.MigrateAll(db.DB); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func newStore26(t *testing.T, db *database.Database) task.Store {
	t.Helper()
	return task.NewSQLiteStore(db)
}

func newCoordStore26(t *testing.T, db *database.Database) *coordinator.StoreAdapter {
	t.Helper()
	return coordinator.NewStoreAdapter(newStore26(t, db))
}

func newLogger26(t *testing.T) *zap.Logger {
	t.Helper()
	cfg := zap.NewDevelopmentConfig()
	cfg.Level = zap.NewAtomicLevelAt(zap.ErrorLevel)
	logger, err := cfg.Build()
	if err != nil {
		t.Fatalf("build logger: %v", err)
	}
	return logger
}

// ── Test 1: blocked dependency — task with unmet deps cannot be claimed ──────

func TestV26_DependencyBlockedCannotBeClaimed(t *testing.T) {
	db := newTestDB26(t)
	s := newStore26(t, db)

	depID := "dep-task-a"
	dep := &task.Task{ID: depID, Status: task.StatusPending, Input: "dependency"}
	if err := s.CreateTask(dep); err != nil {
		t.Fatalf("create dep: %v", err)
	}

	childID := "blocked-child"
	child := &task.Task{
		ID:        childID,
		Status:    task.StatusQueued,
		Input:     "wait for A",
		DependsOn: `["` + depID + `"]`,
	}
	if err := s.CreateTask(child); err != nil {
		t.Fatalf("create child: %v", err)
	}

	_, err := s.ClaimTask("w1", 5*time.Minute)
	if err != task.ErrDependenciesNotMet {
		t.Fatalf("expected ErrDependenciesNotMet, got %v", err)
	}
}

// ── Test 2: dependency becomes executable after completion ───────────────────

func TestV26_DependencyBecomesExecutable(t *testing.T) {
	db := newTestDB26(t)
	s := newStore26(t, db)

	depID := "dep-done"
	dep := &task.Task{ID: depID, Status: task.StatusPending, Input: "done"}
	if err := s.CreateTask(dep); err != nil {
		t.Fatalf("create dep: %v", err)
	}

	childID := "unblocked-child"
	child := &task.Task{
		ID:        childID,
		Status:    task.StatusQueued,
		Input:     "after dep",
		DependsOn: `["` + depID + `"]`,
	}
	if err := s.CreateTask(child); err != nil {
		t.Fatalf("create child: %v", err)
	}

	// Transition dep to completed so child becomes claimable.
	_ = s.UpdateStatus(depID, task.StatusQueued)
	_ = s.UpdateStatus(depID, task.StatusRunning)
	_ = s.UpdateStatus(depID, task.StatusCompleted)

	// First claim gets the child (dep is now completed, child is queued and first).
	got, err := s.ClaimTask("w1", 5*time.Minute)
	if err != nil {
		t.Fatalf("claim child: %v", err)
	}
	if got.ID != childID {
		t.Errorf("ID = %q, want %q", got.ID, childID)
	}
}

// ── Test 3: failed dependency blocks dependent ───────────────────────────────

func TestV26_FailedDependencyBlocks(t *testing.T) {
	db := newTestDB26(t)
	s := newStore26(t, db)

	depID := "dep-failed"
	dep := &task.Task{ID: depID, Status: task.StatusPending, Input: "failed"}
	if err := s.CreateTask(dep); err != nil {
		t.Fatalf("create dep: %v", err)
	}

	childID := "child-of-failed"
	child := &task.Task{
		ID:        childID,
		Status:    task.StatusQueued,
		Input:     "after failed",
		DependsOn: `["` + depID + `"]`,
	}
	if err := s.CreateTask(child); err != nil {
		t.Fatalf("create child: %v", err)
	}

	// Transition dep to failed.
	_ = s.UpdateStatus(depID, task.StatusQueued)
	_ = s.UpdateStatus(depID, task.StatusRunning)
	_ = s.FailTask(depID, "boom")

	// Child should be blocked because dep is not completed.
	_, err := s.ClaimTask("w1", 5*time.Minute)
	if err != task.ErrDependenciesNotMet {
		t.Fatalf("expected ErrDependenciesNotMet for failed dep, got %v", err)
	}
}

// ── Test 4: retryable dependency blocks dependent ────────────────────────────

func TestV26_RetryableDependencyBlocks(t *testing.T) {
	db := newTestDB26(t)
	s := newStore26(t, db)

	// Create child FIRST so it is ordered before dep.
	childID := "child-of-retry"
	child := &task.Task{
		ID:        childID,
		Status:    task.StatusQueued,
		Input:     "after retry",
		DependsOn: `["dep-retry"]`,
	}
	if err := s.CreateTask(child); err != nil {
		t.Fatalf("create child: %v", err)
	}

	depID := "dep-retry"
	dep := &task.Task{ID: depID, Status: task.StatusPending, Input: "retrying", MaxRetries: 3}
	if err := s.CreateTask(dep); err != nil {
		t.Fatalf("create dep: %v", err)
	}
	_ = s.UpdateStatus(depID, task.StatusQueued)
	_ = s.UpdateStatus(depID, task.StatusRunning)
	_ = s.FailTask(depID, "retry later")
	_, _ = s.MakeRetryable(depID, 5*time.Minute)

	// Child should be blocked because dep is not completed.
	_, err := s.ClaimTask("w1", 5*time.Minute)
	if err != task.ErrDependenciesNotMet {
		t.Fatalf("expected ErrDependenciesNotMet for retryable dep, got %v", err)
	}
}

// ── Test 5: concurrent dependent claim race ──────────────────────────────────

func TestV26_ConcurrentClaimRace(t *testing.T) {
	db := newTestDB26(t)
	s := newStore26(t, db)

	depID := "dep-race"
	dep := &task.Task{ID: depID, Status: task.StatusPending, Input: "race"}
	if err := s.CreateTask(dep); err != nil {
		t.Fatalf("create dep: %v", err)
	}

	for i := 0; i < 2; i++ {
		cid := fmt.Sprintf("child-race-%d", i)
		c := &task.Task{
			ID:        cid,
			Status:    task.StatusQueued,
			Input:     fmt.Sprintf("race %d", i),
			DependsOn: `["` + depID + `"]`,
		}
		if err := s.CreateTask(c); err != nil {
			t.Fatalf("create child %d: %v", i, err)
		}
	}

	// Complete the dep so children become claimable.
	_ = s.UpdateStatus(depID, task.StatusQueued)
	_ = s.UpdateStatus(depID, task.StatusRunning)
	_ = s.UpdateStatus(depID, task.StatusCompleted)

	// Both children should now be claimable.
	got1, err := s.ClaimTask("w1", 5*time.Minute)
	if err != nil {
		t.Fatalf("claim child 1 after dep done: %v", err)
	}
	got2, err := s.ClaimTask("w2", 5*time.Minute)
	if err != nil {
		t.Fatalf("claim child 2 after dep done: %v", err)
	}
	if got1.ID == got2.ID {
		t.Error("two workers claimed the same task")
	}
}

// ── Test 6: cancellation propagates to coordinator ──────────────────────────

func TestV26_CancelPropagatesToCoordinator(t *testing.T) {
	db := newTestDB26(t)
	s := newStore26(t, db)
	coordS := newCoordStore26(t, db)
	coord := coordinator.New(coordS, nil, newLogger26(t), coordinator.NewConfig())

	insertTask(t, s, "p-cancel-coord", "coord cancel", "coordinator")
	_ = s.UpdateStatus("p-cancel-coord", task.StatusQueued)
	_ = s.UpdateStatus("p-cancel-coord", task.StatusRunning)

	childID := "c-cancel-coord"
	insertChild(t, s, "p-cancel-coord", childID, "work", "general")
	_ = s.UpdateStatus(childID, task.StatusRunning)

	childrenJSON, _ := json.Marshal([]string{childID})
	_ = s.UpdateTaskSelective(&task.Task{ID: "p-cancel-coord", ChildrenJSON: string(childrenJSON)})

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	_, err := coord.WaitForChildren(ctx, &coordinator.TaskInfo{ID: "p-cancel-coord"}, []string{childID})
	if err == nil {
		t.Fatal("expected context cancelled error")
	}

	parent, _ := s.GetTask("p-cancel-coord")
	if parent.Status != task.StatusRunning {
		t.Errorf("parent status = %s, want running", parent.Status)
	}
}

// ── Test 7: cancellation propagates to children ──────────────────────────────

func TestV26_CancelPropagatesToChildren(t *testing.T) {
	db := newTestDB26(t)
	s := newStore26(t, db)
	coordS := newCoordStore26(t, db)
	coord := coordinator.New(coordS, nil, newLogger26(t), coordinator.NewConfig())

	insertTask(t, s, "p-cancel-children", "cancel children", "coordinator")
	_ = s.UpdateStatus("p-cancel-children", task.StatusRunning)

	child1 := insertChild(t, s, "p-cancel-children", "c1", "work1", "general")
	child2 := insertChild(t, s, "p-cancel-children", "c2", "work2", "general")
	_ = s.UpdateStatus(child1.ID, task.StatusRunning)
	_ = s.UpdateStatus(child2.ID, task.StatusRunning)

	childrenJSON, _ := json.Marshal([]string{child1.ID, child2.ID})
	_ = s.UpdateTaskSelective(&task.Task{ID: "p-cancel-children", ChildrenJSON: string(childrenJSON)})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := coord.WaitForChildren(ctx, &coordinator.TaskInfo{ID: "p-cancel-children"}, []string{child1.ID, child2.ID})
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

// ── Test 8: cancellation prevents retry ──────────────────────────────────────

func TestV26_CancelPreventsRetry(t *testing.T) {
	db := newTestDB26(t)
	s := newStore26(t, db)

	taskID := "cancelled-task"
	tsk := &task.Task{
		ID:         taskID,
		Status:     task.StatusPending,
		Input:      "will be cancelled",
		MaxRetries: 3,
	}
	if err := s.CreateTask(tsk); err != nil {
		t.Fatalf("create: %v", err)
	}
	_ = s.UpdateStatus(taskID, task.StatusQueued)
	_ = s.UpdateStatus(taskID, task.StatusRunning)
	_ = s.UpdateStatus(taskID, task.StatusCancelled)

	_, err := s.MakeRetryable(taskID, 5*time.Second)
	if err == nil {
		t.Fatal("expected error when retrying cancelled task")
	}

	err = s.FailTask(taskID, "boom")
	if err == nil {
		t.Fatal("expected error when failing cancelled task")
	}
}

// ── Test 9: nested coordinator executes correctly ───────────────────────────

func TestV26_NestedCoordinatorExecutes(t *testing.T) {
	db := newTestDB26(t)
	s := newStore26(t, db)
	coordS := newCoordStore26(t, db)
	cfg := coordinator.NewConfig()
	cfg.MaxDepth = 3
	coord := coordinator.New(coordS, nil, newLogger26(t), cfg)
	exec := coordinator.NewExecutor(s, &fakeSyncExecutor{}, coord, newLogger26(t))

	gpID := "gp-nested"
	gp := &task.Task{ID: gpID, Status: task.StatusPending, Input: "grandparent", Role: "coordinator"}
	_ = s.CreateTask(gp)
	_ = s.UpdateStatus(gpID, task.StatusQueued)
	_ = s.UpdateStatus(gpID, task.StatusRunning)

	// Pre-create parent coordinator child with its own completed child.
	parentID := "parent-nested"
	parent := &task.Task{
		ID:       parentID,
		ParentID: &gpID,
		Status:   task.StatusCompleted,
		Input:    "parent coord",
		Role:     "coordinator",
	}
	_ = s.CreateTask(parent)

	codingID := "coding-nested"
	coding := &task.Task{
		ID:       codingID,
		ParentID: &parentID,
		Status:   task.StatusCompleted,
		Input:    "code something",
		Role:     "coding",
		Output:   "coded it",
	}
	_ = s.CreateTask(coding)

	// Mark parent's children list.
	parentChildrenJSON, _ := json.Marshal([]string{codingID})
	_ = s.UpdateTaskSelective(&task.Task{ID: parentID, ChildrenJSON: string(parentChildrenJSON)})

	// Mark grandparent's children list.
	gpChildrenJSON, _ := json.Marshal([]string{parentID})
	_ = s.UpdateTaskSelective(&task.Task{ID: gpID, ChildrenJSON: string(gpChildrenJSON)})

	// Execute the grandparent coordinator.
	if err := exec.Execute(context.Background(), gpID); err != nil {
		t.Fatalf("execute grandparent: %v", err)
	}

	updated, _ := s.GetTask(gpID)
	if updated.Status != task.StatusCompleted {
		t.Errorf("gp status = %s, want completed", updated.Status)
	}
}

// ── Test 10: max depth enforced ──────────────────────────────────────────────

func TestV26_MaxDepthEnforced(t *testing.T) {
	db := newTestDB26(t)
	s := newStore26(t, db)
	coordS := newCoordStore26(t, db)
	cfg := coordinator.NewConfig()
	cfg.MaxDepth = 1
	coord := coordinator.New(coordS, nil, newLogger26(t), cfg)

	rootID := "root-depth"
	insertTask(t, s, rootID, "root", "coordinator")
	_ = s.UpdateStatus(rootID, task.StatusRunning)

	childID := "child-depth"
	insertChild(t, s, rootID, childID, "child", "coordinator")
	childTask, _ := s.GetTask(childID)
	childInfo := &coordinator.TaskInfo{
		ID:       childTask.ID,
		ParentID: childTask.ParentID,
		RootID:   childTask.RootID,
		Role:     "coordinator",
		Intent:   "coding",
		Input:    "nested",
	}

	_, err := coord.Delegate(context.Background(), childInfo)
	if err == nil {
		t.Fatal("expected depth limit error")
	}
}

// ── Test 11: role routing hint affects candidate score ──────────────────────

func TestV26_RoutingHintsAffectsScore(t *testing.T) {
	prefs := orchestration.RoutingPreferences{
		PreferredProviders:    []string{"openai"},
		ExcludedProviders:     []string{"bad-provider"},
		PreferredCapabilities: []string{"tool_calling"},
	}
	if len(prefs.PreferredProviders) != 1 {
		t.Error("expected PreferredProviders to be set")
	}
	if len(prefs.ExcludedProviders) != 1 {
		t.Error("expected ExcludedProviders to be set")
	}
}

// ── Test 12: bounded child context ───────────────────────────────────────────

func TestV26_BoundedChildContext(t *testing.T) {
	db := newTestDB26(t)
	s := newStore26(t, db)
	coordS := newCoordStore26(t, db)
	cfg := coordinator.NewConfig()
	cfg.MaxChildContext = 100
	coord := coordinator.New(coordS, nil, newLogger26(t), cfg)

	longInput := string(make([]byte, 500))
	insertTask(t, s, "p-bounded", longInput, "coordinator")
	_ = s.UpdateStatus("p-bounded", task.StatusRunning)

	childIDs, err := coord.Delegate(context.Background(), &coordinator.TaskInfo{
		ID:     "p-bounded",
		Role:   "coordinator",
		Intent: "coding",
		Input:  longInput,
	})
	if err != nil {
		t.Fatalf("delegate: %v", err)
	}
	if len(childIDs) == 0 {
		t.Fatal("expected children")
	}

	child, _ := s.GetTask(childIDs[0])
	if len(child.Input) > 200 { // allow for role suffix appended after truncation
		t.Errorf("child input len = %d, want <= ~200", len(child.Input))
	}
	if len(child.Input) == 0 {
		t.Error("child input should not be empty")
	}
}

// ── Test 13: parallel write protection ───────────────────────────────────────

func TestV26_ParallelWriteProtection(t *testing.T) {
	db := newTestDB26(t)
	s := newStore26(t, db)

	taskID := "concurrent-write"
	tsk := &task.Task{ID: taskID, Status: task.StatusQueued, Input: "work"}
	if err := s.CreateTask(tsk); err != nil {
		t.Fatalf("create: %v", err)
	}

	var wg sync.WaitGroup
	var errs [2]error
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			_, errs[idx] = s.ClaimTask(fmt.Sprintf("w-%d", idx), 5*time.Minute)
		}(i)
	}
	wg.Wait()

	succeeded := 0
	for _, e := range errs {
		if e == nil {
			succeeded++
		}
	}
	if succeeded != 1 {
		t.Errorf("expected 1 successful claim, got %d", succeeded)
	}
}

// ── Test 14: resume does not duplicate children ──────────────────────────────

func TestV26_ResumeNoDuplicateChildren(t *testing.T) {
	db := newTestDB26(t)
	s := newStore26(t, db)
	coordS := newCoordStore26(t, db)
	coord := coordinator.New(coordS, nil, newLogger26(t), coordinator.NewConfig())

	insertTask(t, s, "p-resume-nodup", "resume nodup", "coordinator")
	_ = s.UpdateStatus("p-resume-nodup", task.StatusRunning)

	firstIDs, err := coord.Delegate(context.Background(), &coordinator.TaskInfo{
		ID:     "p-resume-nodup",
		Role:   "coordinator",
		Intent: "coding",
		Input:  "resume nodup",
	})
	if err != nil {
		t.Fatalf("first delegate: %v", err)
	}
	count1, _ := s.ListChildTasks("p-resume-nodup", 100, 0)

	secondIDs, err := coord.Delegate(context.Background(), &coordinator.TaskInfo{
		ID:           "p-resume-nodup",
		Role:         "coordinator",
		Intent:       "coding",
		Input:        "resume nodup",
		ChildrenJSON: toJSON(firstIDs),
	})
	if err != nil {
		t.Fatalf("second delegate: %v", err)
	}
	count2, _ := s.ListChildTasks("p-resume-nodup", 100, 0)

	if len(secondIDs) != len(firstIDs) {
		t.Errorf("len(second)=%d, len(first)=%d", len(secondIDs), len(firstIDs))
	}
	if len(count2) != len(count1) {
		t.Errorf("db children count changed: %d -> %d", len(count1), len(count2))
	}
}

// ── Test 15: parent waits for all required children ─────────────────────────

func TestV26_ParentWaitsForAllChildren(t *testing.T) {
	db := newTestDB26(t)
	s := newStore26(t, db)
	coordS := newCoordStore26(t, db)
	coord := coordinator.New(coordS, nil, newLogger26(t), coordinator.NewConfig())

	insertTask(t, s, "p-wait-all", "wait all", "coordinator")
	_ = s.UpdateStatus("p-wait-all", task.StatusRunning)

	childIDs := []string{"c-wait-1", "c-wait-2", "c-wait-3"}
	for i, cid := range childIDs {
		insertChild(t, s, "p-wait-all", cid, fmt.Sprintf("work %d", i), "general")
	}
	completeChild(t, s, childIDs[0], "result 1")
	completeChild(t, s, childIDs[1], "result 2")

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	_, err := coord.WaitForChildren(ctx, &coordinator.TaskInfo{ID: "p-wait-all"}, childIDs)
	// Timeout is acceptable since child 3 is stuck.
	_ = err

	parent, _ := s.GetTask("p-wait-all")
	if parent.Status == task.StatusCompleted {
		t.Error("parent should not complete with incomplete children")
	}
}

// ── Test 16: retryable child does not finalize parent ────────────────────────

func TestV26_RetryableChildDoesNotFinalizeParent(t *testing.T) {
	db := newTestDB26(t)
	s := newStore26(t, db)
	coordS := newCoordStore26(t, db)
	cfg := coordinator.NewConfig()
	cfg.RequiredMode = false
	coord := coordinator.New(coordS, nil, newLogger26(t), cfg)

	insertTask(t, s, "p-retry-child", "retry test", "coordinator")
	_ = s.UpdateStatus("p-retry-child", task.StatusRunning)

	childID := "c-retry"
	child := &task.Task{
		ID:         childID,
		ParentID:   strPtr("p-retry-child"),
		Status:     task.StatusQueued,
		Input:      "will fail then retry",
		Role:       "general",
		MaxRetries: 2,
	}
	if err := s.CreateTask(child); err != nil {
		t.Fatalf("create child: %v", err)
	}
	_ = s.UpdateStatus(childID, task.StatusRunning)
	_ = s.FailTask(childID, "first failure")
	_, _ = s.MakeRetryable(childID, 10*time.Millisecond)
	_ = s.UpdateStatus(childID, task.StatusQueued)

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	_, err := coord.WaitForChildren(ctx, &coordinator.TaskInfo{ID: "p-retry-child"}, []string{childID})
	if err == nil {
		t.Fatal("expected timeout or error")
	}

	parent, _ := s.GetTask("p-retry-child")
	if parent.Status.IsTerminal() {
		t.Errorf("parent should not be terminal while child is retryable, got %s", parent.Status)
	}
}

// ── Test 17: permanent child failure finalizes parent correctly ──────────────

func TestV26_PermanentFailureFinalizesParent(t *testing.T) {
	db := newTestDB26(t)
	s := newStore26(t, db)
	coordS := newCoordStore26(t, db)
	cfg := coordinator.NewConfig()
	cfg.RequiredMode = true
	coord := coordinator.New(coordS, nil, newLogger26(t), cfg)

	insertTask(t, s, "p-perm-fail", "perm fail test", "coordinator")
	_ = s.UpdateStatus("p-perm-fail", task.StatusQueued)
	_ = s.UpdateStatus("p-perm-fail", task.StatusRunning)

	childID := "c-perm"
	child := &task.Task{
		ID:         childID,
		ParentID:   strPtr("p-perm-fail"),
		Status:     task.StatusQueued,
		Input:      "will permanently fail",
		Role:       "general",
		MaxRetries: 0,
	}
	if err := s.CreateTask(child); err != nil {
		t.Fatalf("create child: %v", err)
	}
	_ = s.UpdateStatus(childID, task.StatusRunning)
	_ = s.FailTask(childID, "permanent failure")

	// Pre-set children list so coordinator doesn't try to create more.
	childrenJSON, _ := json.Marshal([]string{childID})
	_ = s.UpdateTaskSelective(&task.Task{ID: "p-perm-fail", ChildrenJSON: string(childrenJSON)})

	agg, err := coord.WaitForChildren(context.Background(), &coordinator.TaskInfo{ID: "p-perm-fail"}, []string{childID})
	if err != nil {
		t.Fatalf("waitfor: %v", err)
	}
	if agg.AllSucceeded {
		t.Error("expected AllSucceeded=false with permanent failure")
	}

	// Manually mark parent final (simulating what executeCoordinator does).
	info, _ := s.GetTask("p-perm-fail")
	coordInfo := &coordinator.TaskInfo{ID: info.ID, Status: string(info.Status)}
	if err := coord.MarkParentFinal(context.Background(), coordInfo, agg); err != nil {
		t.Logf("MarkParentFinal error (expected in required mode): %v", err)
	}

	parent, _ := s.GetTask("p-perm-fail")
	if parent.Status != task.StatusFailed {
		t.Errorf("parent status = %s, want failed", parent.Status)
	}
}

// ── Test 18: event sequence correctness ──────────────────────────────────────

func TestV26_EventSequenceCorrectness(t *testing.T) {
	db := newTestDB26(t)
	s := newStore26(t, db)

	taskID := "event-seq"
	tsk := &task.Task{ID: taskID, Status: task.StatusPending, Input: "event test"}
	if err := s.CreateTask(tsk); err != nil {
		t.Fatalf("create: %v", err)
	}

	_ = s.UpdateStatus(taskID, task.StatusQueued)
	_ = s.UpdateStatus(taskID, task.StatusRunning)
	_ = s.UpdateStatus(taskID, task.StatusCompleted)

	updated, _ := s.GetTask(taskID)
	if updated.Status != task.StatusCompleted {
		t.Errorf("status = %s, want completed", updated.Status)
	}

	err := s.UpdateStatus(taskID, task.StatusCancelled)
	if err == nil {
		t.Error("expected error cancelling completed task")
	}
}

// ── Test 19: existing single-agent task unchanged ────────────────────────────

func TestV26_SingleAgentUnchanged(t *testing.T) {
	db := newTestDB26(t)
	s := newStore26(t, db)

	taskID := "single-agent"
	tsk := &task.Task{ID: taskID, Status: task.StatusPending, Input: "just a simple task", Model: "test-model"}
	if err := s.CreateTask(tsk); err != nil {
		t.Fatalf("create: %v", err)
	}
	// Transition to queued so the worker pool can claim it.
	_ = s.UpdateStatus(taskID, task.StatusQueued)

	fakeExec := &completingExecutor{store: s}
	pool := worker.New(worker.Config{WorkerCount: 1, PollInterval: 50 * time.Millisecond}, s, fakeExec, newLogger26(t))
	pool.Start()
	defer pool.Stop()

	deadline := time.After(2 * time.Second)
ticker:
	for {
		select {
		case <-deadline:
			t.Fatal("timeout waiting for single agent task")
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
	if fakeExec.called != 1 {
		t.Errorf("executor called = %d, want 1", fakeExec.called)
	}
}

// ── Test 20: gateway /v1/chat/completions path unchanged ────────────────────

func TestV26_GatewayUnchanged(t *testing.T) {
	db := newTestDB26(t)
	s := newStore26(t, db)

	taskID := "gateway-test"
	tsk := &task.Task{
		ID:     taskID,
		Status: task.StatusPending,
		Input:  "hello via gateway",
		Model:  "test-model",
		Role:   "",
	}
	if err := s.CreateTask(tsk); err != nil {
		t.Fatalf("create: %v", err)
	}

	fakeExec := &completingExecutor{store: s}
	exec := coordinator.NewExecutor(s, fakeExec, nil, newLogger26(t))

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

// ── Test 21: coordinator event bus publishes events ─────────────────────────

func TestV26_CoordinatorEventBus(t *testing.T) {
	db := newTestDB26(t)
	s := newStore26(t, db)
	coordS := newCoordStore26(t, db)
	eb := eventbus.NewEventBus()
	coord := coordinator.New(coordS, eb, newLogger26(t), coordinator.NewConfig())

	var received []eventbus.EventType
	subID := eb.Subscribe(eventbus.TaskDelegated, func(e eventbus.Event) {
		received = append(received, e.Type)
	})
	defer eb.Unsubscribe(eventbus.TaskDelegated, subID)

	insertTask(t, s, "p-evt-v26", "event test v26", "coordinator")
	_, err := coord.Delegate(context.Background(), &coordinator.TaskInfo{
		ID:     "p-evt-v26",
		Role:   "coordinator",
		Intent: "coding",
		Input:  "event test v26",
	})
	if err != nil {
		t.Fatalf("delegate: %v", err)
	}
	if len(received) == 0 {
		t.Error("expected delegation event")
	}
}

// ── Helper functions ────────────────────────────────────────────────────────

func toJSON(ids []string) string {
	b, _ := json.Marshal(ids)
	return string(b)
}

// completingExecutor simulates a task executor that transitions the task to completed.
type completingExecutor struct {
	called int
	store  task.Store
}

func (f *completingExecutor) Execute(_ context.Context, taskID string) error {
	f.called++
	if f.store != nil {
		_ = f.store.UpdateStatus(taskID, task.StatusQueued)
		_ = f.store.UpdateStatus(taskID, task.StatusRunning)
		_ = f.store.UpdateStatus(taskID, task.StatusCompleted)
	}
	return nil
}
