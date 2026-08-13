package coordinator_test

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/EffNine/conductor/internal/config"
	"github.com/EffNine/conductor/internal/coordinator"
	"github.com/EffNine/conductor/internal/database"
	"github.com/EffNine/conductor/internal/task"
	"go.uber.org/zap"
)

func newTestDBForCoord(t *testing.T) *database.Database {
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

func insertCoordChild(t *testing.T, s task.Store, parentID, id, input string) {
	t.Helper()
	tsk := &task.Task{
		ID:       id,
		ParentID: &parentID,
		Status:   task.StatusQueued,
		Input:    input,
	}
	if err := s.CreateTask(tsk); err != nil {
		t.Fatalf("create child: %v", err)
	}
}

// TestParentCancellationPropagatesToChildren verifies that when a parent task's
// context is cancelled, running children are also cancelled.
func TestParentCancellationPropagatesToChildren(t *testing.T) {
	db := newTestDBForCoord(t)
	s := task.NewSQLiteStore(db)
	coordS := coordinator.NewStoreAdapter(s)
	coord := coordinator.New(coordS, nil, zap.NewNop(), coordinator.NewConfig())

	// Create parent task.
	parentID := "parent-cancel-prop"
	parent := &task.Task{
		ID:     parentID,
		Status: task.StatusRunning,
		Input:  "coordinate work",
		Role:   "coordinator",
	}
	if err := s.CreateTask(parent); err != nil {
		t.Fatalf("create parent: %v", err)
	}

	// Create child tasks.
	childIDs := make([]string, 3)
	for i := 0; i < 3; i++ {
		cid := fmt.Sprintf("child-cancel-%d", i)
		childIDs[i] = cid
		insertCoordChild(t, s, parentID, cid, fmt.Sprintf("work %d", i))
		// Transition to running so they appear active.
		_ = s.UpdateStatus(cid, task.StatusRunning)
	}

	// Cancel the parent context and verify children get cancelled.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := coord.WaitForChildren(ctx, &coordinator.TaskInfo{ID: parentID}, childIDs)
	if err == nil {
		t.Fatal("expected context cancelled error")
	}

	// Verify all children are cancelled.
	for _, cid := range childIDs {
		child, err := s.GetTask(cid)
		if err != nil {
			t.Fatalf("get child %s: %v", cid, err)
		}
		if child.Status != task.StatusCancelled {
			t.Errorf("child %s status = %q, want cancelled", cid, child.Status)
		}
	}
}

