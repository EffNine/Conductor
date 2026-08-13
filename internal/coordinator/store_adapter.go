package coordinator

import (
	"github.com/EffNine/conductor/internal/task"
)

// StoreAdapter wraps a task.Store to satisfy CoordinatorStore, breaking the
// import cycle between coordinator and task packages.
type StoreAdapter struct {
	store task.Store
}

// NewStoreAdapter creates a CoordinatorStore from a task.Store.
func NewStoreAdapter(s task.Store) *StoreAdapter {
	return &StoreAdapter{store: s}
}

func (a *StoreAdapter) GetTask(id string) (*TaskInfo, error) {
	t, err := a.store.GetTask(id)
	if err != nil {
		return nil, err
	}
	return taskToCoordInfo(t), nil
}

func (a *StoreAdapter) UpdateStatus(id string, newStatus string) error {
	return a.store.UpdateStatus(id, task.Status(newStatus))
}

func (a *StoreAdapter) FailTask(id string, errMsg string) error {
	return a.store.FailTask(id, errMsg)
}

func (a *StoreAdapter) CreateTask(info *TaskInfo) error {
	t := coordInfoToTask(info)
	return a.store.CreateTask(t)
}

func (a *StoreAdapter) UpdateTask(info *TaskInfo) error {
	t := coordInfoToTask(info)
	return a.store.UpdateTask(t)
}

// UpdateTaskSelective updates only the non-zero fields of a task.
func (a *StoreAdapter) UpdateTaskSelective(info *TaskInfo) error {
	t := coordInfoToTask(info)
	return a.store.UpdateTaskSelective(t)
}

func (a *StoreAdapter) ListChildTasks(parentID string) ([]*TaskInfo, error) {
	tasks, err := a.store.ListChildTasks(parentID, 100, 0)
	if err != nil {
		return nil, err
	}
	out := make([]*TaskInfo, len(tasks))
	for i := range tasks {
		out[i] = taskToCoordInfo(&tasks[i])
	}
	return out, nil
}

func (a *StoreAdapter) ListTasksByRootID(rootID string) ([]*TaskInfo, error) {
	tasks, err := a.store.ListTasksByRootID(rootID, 100, 0)
	if err != nil {
		return nil, err
	}
	out := make([]*TaskInfo, len(tasks))
	for i := range tasks {
		out[i] = taskToCoordInfo(&tasks[i])
	}
	return out, nil
}

func (a *StoreAdapter) SaveCheckpoint(id string, data []byte) error {
	return a.store.SaveCheckpoint(id, data)
}

func (a *StoreAdapter) GetCoordinatorState(id string) ([]byte, error) {
	return a.store.GetCoordinatorState(id)
}

func (a *StoreAdapter) UpdateCoordinatorState(id string, state []byte) error {
	return a.store.UpdateCoordinatorState(id, state)
}

func (a *StoreAdapter) CreateTaskEvent(evt *TaskCoordEvent) error {
	return a.store.CreateTaskEvent(&task.TaskEvent{
		ID:        evt.ID,
		TaskID:    evt.TaskID,
		EventType: evt.EventType,
		EventData: evt.EventData,
	})
}

// compile-time check
var _ CoordinatorStore = (*StoreAdapter)(nil)
