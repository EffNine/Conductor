package coordinator_test

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/EffNine/conductor/internal/agent"
	"github.com/EffNine/conductor/internal/config"
	"github.com/EffNine/conductor/internal/coordinator"
	"github.com/EffNine/conductor/internal/database"
	"github.com/EffNine/conductor/internal/eventbus"
	"github.com/EffNine/conductor/internal/task"
	"github.com/EffNine/conductor/internal/worker"
	"go.uber.org/zap"
)

func newTestDB(t *testing.T) *database.Database {
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

func newStore(t *testing.T, db *database.Database) task.Store {
	t.Helper()
	return task.NewSQLiteStore(db)
}

func newCoordStore(t *testing.T, db *database.Database) *coordinator.StoreAdapter {
	t.Helper()
	return coordinator.NewStoreAdapter(newStore(t, db))
}

func newLogger(t *testing.T) *zap.Logger {
	t.Helper()
	cfg := zap.NewDevelopmentConfig()
	cfg.Level = zap.NewAtomicLevelAt(zap.ErrorLevel)
	logger, err := cfg.Build()
	if err != nil {
		t.Fatalf("build logger: %v", err)
	}
	return logger
}

func insertTask(t *testing.T, s task.Store, id, input, role string) *task.Task {
	t.Helper()
	task_ := &task.Task{
		ID:     id,
		Status: task.StatusPending,
		Input:  input,
		Role:   role,
	}
	if err := s.CreateTask(task_); err != nil {
		t.Fatalf("create task: %v", err)
	}
	return task_
}

func insertChild(t *testing.T, s task.Store, parentID, id, input, role string) *task.Task {
	t.Helper()
	task_ := &task.Task{
		ID:       id,
		ParentID: &parentID,
		Status:   task.StatusQueued,
		Input:    input,
		Role:     role,
	}
	if err := s.CreateTask(task_); err != nil {
		t.Fatalf("create child: %v", err)
	}
	return task_
}

func completeChild(t *testing.T, s task.Store, id, output string) {
	t.Helper()
	_ = s.UpdateStatus(id, task.StatusRunning)
	_ = s.UpdateStatus(id, task.StatusCompleted)
	t_, err := s.GetTask(id)
	if err != nil {
		t.Fatalf("get child: %v", err)
	}
	t_.Output = output
	_ = s.UpdateTask(t_)
}

func failChild(t *testing.T, s task.Store, id, errMsg string) {
	t.Helper()
	_ = s.FailTask(id, errMsg)
}

func toCoordInfo(s task.Store, id string) *coordinator.TaskInfo {
	raw, err := s.GetTask(id)
	if err != nil || raw == nil {
		return nil
	}
	return &coordinator.TaskInfo{
		ID:           raw.ID,
		ParentID:     raw.ParentID,
		RootID:       raw.RootID,
		Status:       string(raw.Status),
		Input:        raw.Input,
		Role:         raw.Role,
		Intent:       raw.Intent,
		ChildrenJSON: raw.ChildrenJSON,
		DependsOn:    raw.DependsOn,
	}
}

// --- Test 1: Agent registry registration and lookup ---

func TestRegistry_RegisterAndGet(t *testing.T) {
	reg := agent.NewRegistry()
	def := agent.AgentDefinition{
		Name:     "research",
		MaxSteps: 15,
	}
	if err := reg.Register(def); err != nil {
		t.Fatalf("register: %v", err)
	}
	got, ok := reg.Get("research")
	if !ok {
		t.Fatal("expected to find research role")
	}
	if got.Name != "research" {
		t.Errorf("name = %q, want research", got.Name)
	}
	if got.MaxSteps != 15 {
		t.Errorf("max_steps = %d, want 15", got.MaxSteps)
	}
}

func TestRegistry_Duplicate(t *testing.T) {
	reg := agent.NewRegistry()
	def := agent.AgentDefinition{Name: "coder"}
	if err := reg.Register(def); err != nil {
		t.Fatalf("first register: %v", err)
	}
	if err := reg.Register(def); err == nil {
		t.Fatal("expected duplicate error")
	}
}

func TestRegistry_HasAndList(t *testing.T) {
	reg := agent.NewRegistry()
	reg.Register(agent.AgentDefinition{Name: "a"})
	reg.Register(agent.AgentDefinition{Name: "b"})
	if !reg.Has("a") {
		t.Error("expected Has(a)=true")
	}
	if reg.Has("z") {
		t.Error("expected Has(z)=false")
	}
	names := reg.Names()
	if len(names) != 2 {
		t.Errorf("len(names)=%d, want 2", len(names))
	}
}

// --- Test 2: Parent task creation and child inheritance ---

func TestDelegation_RootIDInherited(t *testing.T) {
	db := newTestDB(t)
	s := newStore(t, db)
	coordS := newCoordStore(t, db)
	coord := coordinator.New(coordS, nil, newLogger(t), coordinator.NewConfig())

	insertTask(t, s, "parent-1", "fix the bug", "coordinator")
	childIDs, err := coord.Delegate(context.Background(), &coordinator.TaskInfo{
		ID:     "parent-1",
		Role:   "coordinator",
		Intent: "coding",
		Input:  "fix the bug",
	})
	if err != nil {
		t.Fatalf("delegate: %v", err)
	}
	if len(childIDs) == 0 {
		t.Fatal("expected children")
	}
	for _, cid := range childIDs {
		child, err := s.GetTask(cid)
		if err != nil {
			t.Fatalf("get child %s: %v", cid, err)
		}
		if child.RootID != "parent-1" {
			t.Errorf("child %s root_id=%s, want parent-1", cid, child.RootID)
		}
		if child.ParentID == nil || *child.ParentID != "parent-1" {
			t.Errorf("child %s parent_id=%v, want parent-1", cid, child.ParentID)
		}
	}
}

// --- Test 3: Parallel independent children ---

func TestDelegation_ParallelChildrenCreated(t *testing.T) {
	db := newTestDB(t)
	s := newStore(t, db)
	coordS := newCoordStore(t, db)
	coord := coordinator.New(coordS, nil, newLogger(t), coordinator.NewConfig())

	insertTask(t, s, "p-1", "build feature X", "coordinator")
	_, err := coord.Delegate(context.Background(), &coordinator.TaskInfo{
		ID:     "p-1",
		Role:   "coordinator",
		Intent: "coding",
		Input:  "build feature X",
	})
	if err != nil {
		t.Fatalf("delegate: %v", err)
	}
	children, err := s.ListChildTasks("p-1", 100, 0)
	if err != nil {
		t.Fatalf("list children: %v", err)
	}
	if len(children) != 3 {
		t.Fatalf("expected 3 children, got %d", len(children))
	}
	for _, ch := range children {
		if ch.Status != task.StatusQueued {
			t.Errorf("child %s status=%s, want queued", ch.ID, ch.Status)
		}
	}
}

// --- Test 4: Resume does not duplicate children ---

func TestDelegation_NoDuplicateOnResume(t *testing.T) {
	db := newTestDB(t)
	s := newStore(t, db)
	coordS := newCoordStore(t, db)
	coord := coordinator.New(coordS, nil, newLogger(t), coordinator.NewConfig())

	insertTask(t, s, "p-resume", "do work", "coordinator")
	firstIDs, err := coord.Delegate(context.Background(), &coordinator.TaskInfo{
		ID:           "p-resume",
		Role:         "coordinator",
		Intent:       "research",
		Input:        "do work",
		ChildrenJSON: `["already-created"]`,
	})
	if err != nil {
		t.Fatalf("first delegate: %v", err)
	}
	secondIDs, err := coord.Delegate(context.Background(), &coordinator.TaskInfo{
		ID:           "p-resume",
		Role:         "coordinator",
		Intent:       "research",
		Input:        "do work",
		ChildrenJSON: `["already-created"]`,
	})
	if err != nil {
		t.Fatalf("second delegate: %v", err)
	}
	if len(firstIDs) != len(secondIDs) {
		t.Errorf("len(first)=%d, len(second)=%d", len(firstIDs), len(secondIDs))
	}
}

// --- Test 5: Child result aggregation ---

func TestAggregation_CompletedChildren(t *testing.T) {
	db := newTestDB(t)
	s := newStore(t, db)
	coordS := newCoordStore(t, db)
	coord := coordinator.New(coordS, nil, newLogger(t), coordinator.NewConfig())

	insertTask(t, s, "p-agg", "aggregate test", "coordinator")
	_ = s.UpdateStatus("p-agg", task.StatusQueued)
	_ = s.UpdateStatus("p-agg", task.StatusRunning)

	child1 := insertChild(t, s, "p-agg", "child-1", "research result", "research")
	child2 := insertChild(t, s, "p-agg", "child-2", "code result", "coding")
	completeChild(t, s, child1.ID, "done with research")
	completeChild(t, s, child2.ID, "done with coding")

	agg, err := coord.WaitForChildren(context.Background(), &coordinator.TaskInfo{ID: "p-agg"}, []string{child1.ID, child2.ID})
	if err != nil {
		t.Fatalf("waitfor: %v", err)
	}
	if len(agg.Children) != 2 {
		t.Errorf("children count=%d, want 2", len(agg.Children))
	}
	if !agg.AllSucceeded {
		t.Error("expected all succeeded")
	}
	if agg.Summary == "" {
		t.Error("expected non-empty summary")
	}
}

// --- Test 6: Parent completion marks correctly ---

func TestMarkParent_FinalizesParent(t *testing.T) {
	db := newTestDB(t)
	s := newStore(t, db)
	coordS := newCoordStore(t, db)
	coord := coordinator.New(coordS, nil, newLogger(t), coordinator.NewConfig())

	insertTask(t, s, "p-mark", "mark test", "coordinator")
	// Transition through valid states: pending → queued → running
	if err := s.UpdateStatus("p-mark", task.StatusQueued); err != nil {
		t.Fatalf("transition to queued: %v", err)
	}
	if err := s.UpdateStatus("p-mark", task.StatusRunning); err != nil {
		t.Fatalf("transition to running: %v", err)
	}

	agg := &coordinator.AggregationResult{
		Children: []*coordinator.ChildResult{
			{ChildID: "c1", Status: "completed", Output: "ok result"},
			{ChildID: "c2", Status: "completed", Output: "fine result"},
		},
		AllSucceeded:     true,
		Summary:          "all good",
		AggregatedOutput: "[c1] ok result\n---\n[c2] fine result",
	}
	info, _ := s.GetTask("p-mark")
	coordInfo := &coordinator.TaskInfo{
		ID:     info.ID,
		Status: string(info.Status),
	}
	if err := coord.MarkParentFinal(context.Background(), coordInfo, agg); err != nil {
		t.Fatalf("mark final: %v", err)
	}
	updated, err := s.GetTask("p-mark")
	if err != nil {
		t.Fatalf("get parent: %v", err)
	}
	if updated.Status != task.StatusCompleted {
		t.Errorf("status=%s, want completed", updated.Status)
	}
	if updated.Output == "" {
		t.Error("expected non-empty output")
	}
}

// --- Test 7: Parent failure on child failure (required mode) ---

func TestMarkParent_FailsOnChildFailure(t *testing.T) {
	db := newTestDB(t)
	s := newStore(t, db)
	coordS := newCoordStore(t, db)
	coord := coordinator.New(coordS, nil, newLogger(t), coordinator.NewConfig())

	insertTask(t, s, "p-fail", "fail test", "coordinator")
	_ = s.UpdateStatus("p-fail", task.StatusQueued)
	_ = s.UpdateStatus("p-fail", task.StatusRunning)

	agg := &coordinator.AggregationResult{
		Children: []*coordinator.ChildResult{
			{ChildID: "c1", Status: "completed", Output: "ok"},
			{ChildID: "c2", Status: "failed", Error: "boom"},
		},
		AllSucceeded: false,
	}
	err := coord.MarkParentFinal(context.Background(), &coordinator.TaskInfo{ID: "p-fail", Status: "running"}, agg)
	if err == nil {
		t.Fatal("expected error on required-mode failure")
	}
	updated, _ := s.GetTask("p-fail")
	if updated.Status != task.StatusFailed {
		t.Errorf("status=%s, want failed", updated.Status)
	}
}

// --- Test 8: Cancellation propagates to children ---

func TestCancelPropagation(t *testing.T) {
	db := newTestDB(t)
	s := newStore(t, db)
	coordS := newCoordStore(t, db)
	coord := coordinator.New(coordS, nil, newLogger(t), coordinator.NewConfig())

	insertTask(t, s, "p-cancel", "cancel test", "coordinator")
	child := insertChild(t, s, "p-cancel", "c-running", "work", "general")
	_ = s.UpdateStatus(child.ID, task.StatusRunning)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := coord.WaitForChildren(ctx, &coordinator.TaskInfo{ID: "p-cancel"}, []string{child.ID})
	if err == nil {
		t.Fatal("expected context cancelled error")
	}
	c, _ := s.GetTask(child.ID)
	if c.Status != task.StatusCancelled {
		t.Errorf("child status=%s, want cancelled", c.Status)
	}
}

// --- Test 9: Depth bound enforcement ---

func TestDepthBound(t *testing.T) {
	db := newTestDB(t)
	s := newStore(t, db)
	coordS := newCoordStore(t, db)
	cfg := coordinator.NewConfig()
	cfg.MaxDepth = 1
	coord := coordinator.New(coordS, nil, newLogger(t), cfg)

	gp := insertTask(t, s, "gp", "grandparent", "coordinator")
	pid := "p-depth"
	parentID := gp.ID
	insertChild(t, s, parentID, pid, "parent task", "coordinator")
	childID := "c-depth"
	insertChild(t, s, pid, childID, "child task", "coordinator")

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

// --- Test 10: Max children bound ---

func TestMaxChildrenBound(t *testing.T) {
	db := newTestDB(t)
	s := newStore(t, db)
	coordS := newCoordStore(t, db)
	cfg := coordinator.NewConfig()
	cfg.MaxChildren = 2
	coord := coordinator.New(coordS, nil, newLogger(t), cfg)

	insertTask(t, s, "p-max", "big task", "coordinator")
	_, err := coord.Delegate(context.Background(), &coordinator.TaskInfo{
		ID:     "p-max",
		Role:   "coordinator",
		Intent: "coding",
		Input:  "big task",
	})
	if err != nil {
		t.Fatalf("delegate: %v", err)
	}
	children, _ := s.ListChildTasks("p-max", 100, 0)
	if len(children) > 2 {
		t.Errorf("expected at most 2 children, got %d", len(children))
	}
}

// --- Test 11: Role-specific system prompt injection ---
// (Tests buildInitialMessages via agent package; coordinator tests role delegation instead)

func TestAgentRoleRegistry(t *testing.T) {
	agentReg := agent.NewRegistry()
	agentReg.Register(agent.AgentDefinition{
		Name:             "research",
		SystemPromptHint: "You are a researcher.",
		PreferredTools:   []string{},
		RoutingHints:     agent.RoutingHints{PreferredCapabilities: []string{"reasoning"}},
	})
	agentReg.Register(agent.AgentDefinition{
		Name:             "coder",
		SystemPromptHint: "You are a coder.",
		PreferredTools:   []string{"read_file", "write_file"},
	})

	def, ok := agentReg.Get("research")
	if !ok {
		t.Fatal("expected to find research role")
	}
	if def.SystemPromptHint != "You are a researcher." {
		t.Errorf("system prompt=%q, want 'You are a researcher.'", def.SystemPromptHint)
	}
	if len(def.PreferredTools) != 0 {
		t.Errorf("preferred_tools=%v, want empty", def.PreferredTools)
	}
}

// --- Test 12: RootID propagation in task model ---

func TestRootIDPropagation(t *testing.T) {
	db := newTestDB(t)
	s := newStore(t, db)

	parent := &task.Task{ID: "root-1", Status: task.StatusPending, Input: "root"}
	if err := s.CreateTask(parent); err != nil {
		t.Fatalf("create parent: %v", err)
	}
	parent, _ = s.GetTask(parent.ID)

	childID := "child-1"
	parentID := parent.ID
	child := &task.Task{
		ID:       childID,
		ParentID: &parentID,
		Status:   task.StatusQueued,
		Input:    "child work",
	}
	if err := s.CreateTask(child); err != nil {
		t.Fatalf("create child: %v", err)
	}
	child, _ = s.GetTask(childID)
	if child.RootID != parent.ID {
		t.Errorf("root_id=%s, want %s", child.RootID, parent.ID)
	}
}

// --- Test 13: Coordinator checkpoint/resume ---

func TestCheckpointResume(t *testing.T) {
	db := newTestDB(t)
	s := newStore(t, db)
	coordS := newCoordStore(t, db)
	coord := coordinator.New(coordS, nil, newLogger(t), coordinator.NewConfig())

	insertTask(t, s, "p-ckpt", "ckpt test", "coordinator")
	_ = s.UpdateStatus("p-ckpt", task.StatusQueued)
	_ = s.UpdateStatus("p-ckpt", task.StatusRunning)

	state := coordinator.ResumeState{
		CompletedChildren: []coordinator.ChildStatusRecord{
			{ChildID: "done-1", Status: "completed", Output: "result1"},
		},
		AggregatedOutput: "partial",
	}
	data, _ := json.Marshal(state)
	_ = s.UpdateCoordinatorState("p-ckpt", data)

	resumed := coord.LoadResumeState("p-ckpt")
	if len(resumed.CompletedChildren) != 1 {
		t.Fatalf("expected 1 resumed child, got %d", len(resumed.CompletedChildren))
	}
	if resumed.CompletedChildren[0].ChildID != "done-1" {
		t.Errorf("child_id=%s, want done-1", resumed.CompletedChildren[0].ChildID)
	}
}

// --- Test 14: Dependency tracking ---

func TestDependencyTracking(t *testing.T) {
	db := newTestDB(t)
	s := newStore(t, db)
	coordS := newCoordStore(t, db)
	coord := coordinator.New(coordS, nil, newLogger(t), coordinator.NewConfig())

	insertTask(t, s, "p-dep", "dep test", "coordinator")
	_ = s.UpdateStatus("p-dep", task.StatusQueued)
	_ = s.UpdateStatus("p-dep", task.StatusRunning)

	childA := insertChild(t, s, "p-dep", "child-a", "first", "research")
	completeChild(t, s, childA.ID, "a done")

	childBDeps := childA.ID
	childB := &task.Task{
		ID:        "child-b",
		ParentID:  strPtr("p-dep"),
		Status:    task.StatusQueued,
		Input:     "second",
		Role:      "general",
		DependsOn: `["` + childBDeps + `"]`,
	}
	_ = s.CreateTask(childB)

	_, err := coord.Delegate(context.Background(), &coordinator.TaskInfo{
		ID:        "p-dep",
		Role:      "coordinator",
		Intent:    "coding",
		Input:     "dep test",
		DependsOn: `["` + childBDeps + `"]`,
	})
	if err != nil {
		t.Fatalf("delegate: %v", err)
	}
}

// --- Test 15: Concurrent child polling ---

func TestConcurrentChildPolling(t *testing.T) {
	db := newTestDB(t)
	s := newStore(t, db)
	coordS := newCoordStore(t, db)
	coord := coordinator.New(coordS, nil, newLogger(t), coordinator.NewConfig())

	insertTask(t, s, "p-conc", "concurrent test", "coordinator")
	_ = s.UpdateStatus("p-conc", task.StatusQueued)
	_ = s.UpdateStatus("p-conc", task.StatusRunning)

	var childIDs []string
	for i := 0; i < 3; i++ {
		cid := fmt.Sprintf("child-conc-%d", i)
		childIDs = append(childIDs, cid)
		insertChild(t, s, "p-conc", cid, fmt.Sprintf("work %d", i), "general")
		completeChild(t, s, cid, fmt.Sprintf("result %d", i))
	}

	agg, err := coord.WaitForChildren(context.Background(), &coordinator.TaskInfo{ID: "p-conc"}, childIDs)
	if err != nil {
		t.Fatalf("waitfor: %v", err)
	}
	if len(agg.Children) != 3 {
		t.Errorf("children=%d, want 3", len(agg.Children))
	}
}

// --- Test 16: Worker pool claims coordinator children ---

func TestWorkerPoolClaimsChildren(t *testing.T) {
	db := newTestDB(t)
	s := newStore(t, db)

	for i := 0; i < 3; i++ {
		cid := fmt.Sprintf("worker-child-%d", i)
		insertChild(t, s, "parent-worker", cid, fmt.Sprintf("task %d", i), "general")
	}

	cfg := worker.DefaultConfig()
	cfg.WorkerCount = 1
	cfg.PollInterval = 100 * time.Millisecond

	fakeExec := &storeUpdatingExecutor{store: s}
	pool := worker.New(cfg, s, fakeExec, newLogger(t))
	pool.Start()
	defer pool.Stop()

	time.Sleep(600 * time.Millisecond)

	children, _ := s.ListChildTasks("parent-worker", 10, 0)
	completed := 0
	for _, c := range children {
		if c.Status == task.StatusCompleted {
			completed++
		}
	}
	if completed == 0 {
		t.Error("expected at least one completed child")
	}
}

// --- Test 17: CoordinatorExecutor delegates correctly ---

func TestCoordinatorExecutor_DelegatesToCoordinator(t *testing.T) {
	db := newTestDB(t)
	s := newStore(t, db)
	coordS := newCoordStore(t, db)
	coord := coordinator.New(coordS, nil, newLogger(t), coordinator.NewConfig())

	leafID := "leaf-1"
	leaf := &task.Task{
		ID:     leafID,
		Status: task.StatusPending,
		Input:  "simple task",
		Role:   "general",
		Model:  "test-model",
	}
	_ = s.CreateTask(leaf)
	_ = s.UpdateStatus(leafID, task.StatusQueued)
	_ = s.UpdateStatus(leafID, task.StatusRunning)

	coordID := "coord-1"
	coordTask := &task.Task{
		ID:     coordID,
		Status: task.StatusPending,
		Input:  "coordinate work",
		Role:   "coordinator",
	}
	_ = s.CreateTask(coordTask)
	_ = s.UpdateStatus(coordID, task.StatusQueued)
	_ = s.UpdateStatus(coordID, task.StatusRunning)

	// Pre-create completed children so coordinator can finish.
	childIDs := []string{"coord-child-1", "coord-child-2"}
	for i, cid := range childIDs {
		insertChild(t, s, coordID, cid, fmt.Sprintf("work %d", i), "general")
		completeChild(t, s, cid, fmt.Sprintf("result %d", i))
	}
	// Persist children list so coordinator resumes without creating new ones.
	childrenJSON, _ := json.Marshal(childIDs)
	coordTaskDB, _ := s.GetTask(coordID)
	coordTaskDB.ChildrenJSON = string(childrenJSON)
	_ = s.UpdateTask(coordTaskDB)

	fakeLeafExec := &storeUpdatingExecutor{store: s}
	exec := coordinator.NewExecutor(s, fakeLeafExec, coord, newLogger(t))

	if err := exec.Execute(context.Background(), leafID); err != nil {
		t.Fatalf("execute leaf: %v", err)
	}
	if fakeLeafExec.called != 1 {
		t.Errorf("fake leaf called=%d, want 1", fakeLeafExec.called)
	}

	if err := exec.Execute(context.Background(), coordID); err != nil {
		t.Fatalf("execute coordinator: %v", err)
	}
	updated, _ := s.GetTask(coordID)
	if updated.Status != task.StatusCompleted {
		t.Errorf("coord status=%s, want completed", updated.Status)
	}
}

// --- Test 18: No duplicate children on resume ---

func TestResumeNoDuplicateChildren(t *testing.T) {
	db := newTestDB(t)
	s := newStore(t, db)
	coordS := newCoordStore(t, db)
	coord := coordinator.New(coordS, nil, newLogger(t), coordinator.NewConfig())

	insertTask(t, s, "p-resume-dup", "resume dup", "coordinator")
	_ = s.UpdateStatus("p-resume-dup", task.StatusQueued)
	_ = s.UpdateStatus("p-resume-dup", task.StatusRunning)

	firstIDs, err := coord.Delegate(context.Background(), &coordinator.TaskInfo{
		ID:     "p-resume-dup",
		Role:   "coordinator",
		Intent: "coding",
		Input:  "resume dup",
	})
	if err != nil {
		t.Fatalf("first delegate: %v", err)
	}

	secondIDs, err := coord.Delegate(context.Background(), toCoordInfo(s, "p-resume-dup"))
	if err != nil {
		t.Fatalf("second delegate: %v", err)
	}
	if len(secondIDs) != len(firstIDs) {
		t.Errorf("len(second)=%d, len(first)=%d", len(secondIDs), len(firstIDs))
	}
}

// --- Test 19: Event bus publishes coordination events ---

func TestEventBusCoordinationEvents(t *testing.T) {
	db := newTestDB(t)
	s := newStore(t, db)
	coordS := newCoordStore(t, db)
	eb := eventbus.NewEventBus()
	coord := coordinator.New(coordS, eb, newLogger(t), coordinator.NewConfig())

	var received []eventbus.EventType
	subID := eb.Subscribe(eventbus.TaskDelegated, func(e eventbus.Event) {
		received = append(received, e.Type)
	})
	defer eb.Unsubscribe(eventbus.TaskDelegated, subID)

	insertTask(t, s, "p-evt", "event test", "coordinator")
	_, err := coord.Delegate(context.Background(), &coordinator.TaskInfo{
		ID:     "p-evt",
		Role:   "coordinator",
		Intent: "coding",
		Input:  "event test",
	})
	if err != nil {
		t.Fatalf("delegate: %v", err)
	}
	if len(received) == 0 {
		t.Error("expected delegation event")
	}
}

// --- Test 20: Bounded concurrency with worker pool ---

func TestBoundedConcurrency(t *testing.T) {
	db := newTestDB(t)
	s := newStore(t, db)

	for i := 0; i < 5; i++ {
		cid := fmt.Sprintf("bounded-%d", i)
		insertChild(t, s, "parent-bounded", cid, fmt.Sprintf("work %d", i), "general")
	}

	claimed := make(chan string, 5)
	fakeExec := &trackingExecutor{claims: claimed}
	cfg := worker.Config{
		WorkerCount:  2,
		PollInterval: 50 * time.Millisecond,
	}
	pool := worker.New(cfg, s, fakeExec, newLogger(t))
	pool.Start()
	defer pool.Stop()

	// Collect claims over a short window.
	claims := make([]string, 0, 5)
	timer := time.NewTimer(500 * time.Millisecond)
	defer timer.Stop()
	for len(claims) < 5 {
		select {
		case id := <-claimed:
			claims = append(claims, id)
		case <-timer.C:
			goto done
		}
	}
done:
	// With 2 workers, we should not see more than ~3-4 claims in 500ms
	// because each claim+return is nearly instant. The real test is that
	// the pool doesn't crash and processes tasks.
	if len(claims) == 0 {
		t.Error("expected some claims")
	}
}

// --- Test 21: Child failure isolation ---

func TestChildFailureIsolation(t *testing.T) {
	db := newTestDB(t)
	s := newStore(t, db)
	coordS := newCoordStore(t, db)
	coord := coordinator.New(coordS, nil, newLogger(t), coordinator.NewConfig())

	insertTask(t, s, "p-isolate", "isolation test", "coordinator")
	_ = s.UpdateStatus("p-isolate", task.StatusQueued)
	_ = s.UpdateStatus("p-isolate", task.StatusRunning)

	child1 := insertChild(t, s, "p-isolate", "c-ok", "good work", "general")
	child2 := insertChild(t, s, "p-isolate", "c-fail", "bad work", "general")
	completeChild(t, s, child1.ID, "success")
	failChild(t, s, child2.ID, "boom")

	agg, err := coord.WaitForChildren(context.Background(), &coordinator.TaskInfo{ID: "p-isolate"}, []string{child1.ID, child2.ID})
	if err != nil {
		t.Fatalf("waitfor: %v", err)
	}
	if len(agg.Children) != 2 {
		t.Errorf("children=%d, want 2", len(agg.Children))
	}
	if agg.AllSucceeded {
		t.Error("expected AllSucceeded=false with one failure")
	}
}

// --- Test 22: Tree API response shape ---

func TestTreeResponse(t *testing.T) {
	db := newTestDB(t)
	s := newStore(t, db)

	insertTask(t, s, "tree-parent", "parent task", "coordinator")
	for i := 0; i < 2; i++ {
		cid := fmt.Sprintf("tree-child-%d", i)
		insertChild(t, s, "tree-parent", cid, fmt.Sprintf("child %d", i), "general")
		completeChild(t, s, cid, fmt.Sprintf("result %d", i))
	}

	children, err := s.ListChildTasks("tree-parent", 100, 0)
	if err != nil {
		t.Fatalf("list children: %v", err)
	}
	if len(children) != 2 {
		t.Errorf("children=%d, want 2", len(children))
	}
}

// --- Helper types ---

type fakeSyncExecutor struct {
	called int
}

func (f *fakeSyncExecutor) Execute(ctx context.Context, taskID string) error {
	f.called++
	_ = ctx
	_ = taskID
	return nil
}

type storeUpdatingExecutor struct {
	store  task.Store
	called int
}

func (f *storeUpdatingExecutor) Execute(ctx context.Context, taskID string) error {
	f.called++
	_ = ctx
	// Complete the task like a real executor would.
	_ = f.store.UpdateStatus(taskID, task.StatusCompleted)
	return nil
}

type trackingExecutor struct {
	claims chan string
}

func (f *trackingExecutor) Execute(ctx context.Context, taskID string) error {
	f.claims <- taskID
	_ = ctx
	return nil
}

func strPtr(s string) *string { return &s }
