package v27_test

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/EffNine/conductor/internal/agent"
	"github.com/EffNine/conductor/internal/apitypes"
	"github.com/EffNine/conductor/internal/catalog"
	"github.com/EffNine/conductor/internal/config"
	"github.com/EffNine/conductor/internal/coordinator"
	"github.com/EffNine/conductor/internal/database"
	"github.com/EffNine/conductor/internal/eventbus"
	"github.com/EffNine/conductor/internal/health"
	"github.com/EffNine/conductor/internal/provider"
	"github.com/EffNine/conductor/internal/router"
	"github.com/EffNine/conductor/internal/task"
	workerpkg "github.com/EffNine/conductor/internal/worker"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// ─────────────────────────────────────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────────────────────────────────────

func openTestDB(t *testing.T, name string) *database.Database {
	t.Helper()
	dsn := filepath.Join(t.TempDir(), name+".db")
	db, err := database.Connect(&config.DatabaseConfig{
		Driver:       "sqlite",
		DSN:          dsn,
		MaxOpenConns: 1,
		MaxIdleConns: 1,
	})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if err := db.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if err := task.MigrateTasks(db.DB); err != nil {
		t.Fatalf("MigrateTasks: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

type noopProvider struct{ name string }

func (n *noopProvider) Name() string                 { return n.name }
func (n *noopProvider) SupportsModel(id string) bool { return true }
func (n *noopProvider) ChatCompletion(ctx context.Context, req *apitypes.ChatCompletionRequest) (*apitypes.ChatCompletionResponse, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	return &apitypes.ChatCompletionResponse{
		Choices: []apitypes.Choice{{Message: &apitypes.Message{Role: "assistant", Content: "ok"}}},
	}, nil
}
func (n *noopProvider) ChatCompletionStream(ctx context.Context, req *apitypes.ChatCompletionRequest) (<-chan apitypes.StreamChunk, error) {
	ch := make(chan apitypes.StreamChunk)
	go func() {
		defer close(ch)
		select {
		case <-ctx.Done():
			return
		case ch <- apitypes.StreamChunk{Done: true}:
		}
	}()
	return ch, nil
}
func (n *noopProvider) Embeddings(_ context.Context, _ *apitypes.EmbeddingRequest) (*apitypes.EmbeddingResponse, error) {
	return nil, nil
}
func (n *noopProvider) ListModels(_ context.Context) ([]provider.ModelInfo, error) {
	return []provider.ModelInfo{{ProviderModelID: n.name + "-model"}}, nil
}
func (n *noopProvider) GetPricing(_ context.Context) (map[string]provider.PricingInfo, error) {
	return nil, nil
}
func (n *noopProvider) HealthCheck(_ context.Context) (*provider.HealthStatus, error) {
	return &provider.HealthStatus{Provider: n.name, IsHealthy: true, LatencyMs: 1}, nil
}
func (n *noopProvider) GetMetadata() provider.Metadata { return provider.Metadata{Name: n.name} }

type quickExecutor struct{ store task.Store }

func (e *quickExecutor) Execute(ctx context.Context, taskID string) error {
	if e.store != nil {
		_ = e.store.UpdateStatus(taskID, task.StatusRunning)
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	if e.store != nil {
		_ = e.store.UpdateStatus(taskID, task.StatusCompleted)
	}
	return nil
}

type slowBlockingExecutor struct{ store task.Store }

func (e *slowBlockingExecutor) Execute(ctx context.Context, taskID string) error {
	if e.store != nil {
		_ = e.store.UpdateStatus(taskID, task.StatusRunning)
	}
	<-ctx.Done()
	return ctx.Err()
}

type failingExecutor struct {
	store task.Store
	err   error
}

func (e *failingExecutor) Execute(_ context.Context, taskID string) error {
	if e.store != nil {
		_ = e.store.UpdateStatus(taskID, task.StatusRunning)
	}
	return e.err
}

type mockAutoSelector struct{ model string }

func (m *mockAutoSelector) Select(_ context.Context, _ string) (string, error) {
	return m.model, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// 1. Startup Recovery
// ─────────────────────────────────────────────────────────────────────────────

func TestV27_StartupRecovery(t *testing.T) {
	db := openTestDB(t, "sr")
	store := task.NewSQLiteStore(db)
	logger := zap.NewNop()

	pendingID := uuid.New().String()
	_ = db.DB.Create(&task.Task{ID: pendingID, Status: task.StatusPending, Input: "p", MaxRetries: 3}).Error

	expiredID := uuid.New().String()
	expired := time.Now().UTC().Add(-2 * time.Hour)
	_ = db.DB.Create(&task.Task{ID: expiredID, Status: task.StatusRunning, Input: "e", ClaimedBy: "w1", ClaimedAt: &expired, LeaseUntil: &expired, MaxRetries: 3}).Error

	validID := uuid.New().String()
	valid := time.Now().UTC().Add(1 * time.Hour)
	_ = db.DB.Create(&task.Task{ID: validID, Status: task.StatusRunning, Input: "v", ClaimedBy: "w2", ClaimedAt: &expired, LeaseUntil: &valid, MaxRetries: 3}).Error

	completedID := uuid.New().String()
	_ = db.DB.Create(&task.Task{ID: completedID, Status: task.StatusCompleted, Input: "c"}).Error

	cancelledID := uuid.New().String()
	_ = db.DB.Create(&task.Task{ID: cancelledID, Status: task.StatusCancelled, Input: "x"}).Error

	pool := workerpkg.New(workerpkg.Config{WorkerCount: 1, PollInterval: 100 * time.Millisecond}, store, &quickExecutor{store: store}, logger)
	if err := pool.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}

	check := func(id string, want task.Status) {
		got, err := store.GetTask(id)
		if err != nil {
			t.Fatalf("GetTask %s: %v", id, err)
		}
		if got.Status != want {
			t.Errorf("task %s status = %q, want %q", id, got.Status, want)
		}
	}
	check(pendingID, task.StatusQueued)
	check(expiredID, task.StatusQueued)
	check(validID, task.StatusRunning)
	check(completedID, task.StatusCompleted)
	check(cancelledID, task.StatusCancelled)
}

// ─────────────────────────────────────────────────────────────────────────────
// 2. Task Timeout
// ─────────────────────────────────────────────────────────────────────────────

func TestV27_TaskTimeout(t *testing.T) {
	db := openTestDB(t, "tt")
	store := task.NewSQLiteStore(db)
	logger := zap.NewNop()

	id := uuid.New().String()
	_ = db.DB.Create(&task.Task{ID: id, Status: task.StatusQueued, Input: "slow", MaxRetries: 0}).Error

	pool := workerpkg.New(workerpkg.Config{
		WorkerCount:   1,
		PollInterval:  50 * time.Millisecond,
		LeaseDuration: 5 * time.Minute,
		TaskTimeout:   200 * time.Millisecond,
	}, store, &slowBlockingExecutor{store: store}, logger)
	pool.Start()
	defer pool.Stop()

	deadline := time.After(3 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatal("timeout waiting for cancellation")
		default:
		}
		got, _ := store.GetTask(id)
		if got != nil && got.Status == task.StatusCancelled {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	got, _ := store.GetTask(id)
	if got.Status != task.StatusCancelled {
		t.Errorf("status = %q, want cancelled", got.Status)
	}
	if got.RetryCount != 0 {
		t.Errorf("retry_count = %d, want 0", got.RetryCount)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// 3. Parent Cancellation → Child Cancellation
// ─────────────────────────────────────────────────────────────────────────────

func TestV27_ParentCancellationPropagates(t *testing.T) {
	db := openTestDB(t, "pc")
	store := task.NewSQLiteStore(db)
	coordStore := coordinator.NewStoreAdapter(store)
	coord := coordinator.New(coordStore, eventbus.NewEventBus(), zap.NewNop(), coordinator.NewConfig())

	parentID := uuid.New().String()
	_ = store.CreateTask(&task.Task{ID: parentID, Status: task.StatusRunning, Input: "p", Role: "coordinator"})

	childIDs := make([]string, 3)
	for i := range childIDs {
		cid := uuid.New().String()
		childIDs[i] = cid
		_ = store.CreateTask(&task.Task{ID: cid, ParentID: &parentID, Status: task.StatusRunning, Input: fmt.Sprintf("c%d", i)})
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := coord.WaitForChildren(ctx, &coordinator.TaskInfo{ID: parentID}, childIDs)
	if err == nil {
		t.Fatal("expected error")
	}

	for _, cid := range childIDs {
		got, err := store.GetTask(cid)
		if err != nil {
			t.Fatalf("get %s: %v", cid, err)
		}
		if got.Status != task.StatusCancelled {
			t.Errorf("child %s status = %q, want cancelled", cid, got.Status)
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// 4. Retry Backoff + MaxRetries
// ─────────────────────────────────────────────────────────────────────────────

func TestV27_RetryBackoffAndMaxRetries(t *testing.T) {
	db := openTestDB(t, "rb")
	store := task.NewSQLiteStore(db)
	logger := zap.NewNop()

	// max_retries=0: never retries
	id0 := uuid.New().String()
	_ = db.DB.Create(&task.Task{ID: id0, Status: task.StatusQueued, Input: "nr", MaxRetries: 0}).Error
	pool0 := workerpkg.New(workerpkg.Config{WorkerCount: 1, PollInterval: 50 * time.Millisecond}, store, &failingExecutor{store: store, err: fmt.Errorf("boom")}, logger)
	pool0.Start()
	defer pool0.Stop()

	deadline := time.After(2 * time.Second)
	for {
		select {
		case <-deadline:
			break
		default:
		}
		got, _ := store.GetTask(id0)
		if got != nil && got.Status == task.StatusFailed {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	got, _ := store.GetTask(id0)
	if got.RetryCount != 0 {
		t.Errorf("max_retries=0: retry_count = %d, want 0", got.RetryCount)
	}

	// max_retries=2: exactly 2 retries then permanent fail
	id2 := uuid.New().String()
	_ = db.DB.Create(&task.Task{ID: id2, Status: task.StatusQueued, Input: "lr", MaxRetries: 2}).Error
	pool2 := workerpkg.New(workerpkg.Config{WorkerCount: 1, PollInterval: 50 * time.Millisecond}, store, &failingExecutor{store: store, err: fmt.Errorf("boom")}, logger)
	sched2 := workerpkg.NewScheduler(store, logger)
	pool2.Start()
	sched2.Start()
	defer func() { sched2.Stop(); pool2.Stop() }()

	deadline = time.After(15 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatal("timeout waiting for permanent failure")
		default:
		}
		got, _ := store.GetTask(id2)
		if got != nil && got.Status == task.StatusFailed && got.RetryCount >= 2 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	got, _ = store.GetTask(id2)
	if got.RetryCount > 2 {
		t.Errorf("max_retries=2: retry_count = %d, want <= 2", got.RetryCount)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// 5. Bounded Checkpoint
// ─────────────────────────────────────────────────────────────────────────────

func TestV27_BoundedCheckpoint(t *testing.T) {
	db := openTestDB(t, "bc")
	store := task.NewStoreAdapter(task.NewSQLiteStore(db))
	taskID := uuid.New().String()

	_ = db.DB.Create(&task.Task{ID: taskID, Status: task.StatusRunning, Input: "test"}).Error

	messages := []apitypes.Message{
		{Role: "system", Content: "You are helpful."},
		{Role: "user", Content: "original instruction"},
	}
	for i := 0; i < 15; i++ {
		messages = append(messages, apitypes.Message{Role: "assistant", Content: fmt.Sprintf("resp %d", i)})
		messages = append(messages, apitypes.Message{Role: "tool", Content: strings.Repeat("x", 50000), ToolCallID: fmt.Sprintf("tc-%d", i)})
	}

	ctx := agent.NewContext(taskID, messages).WithMaxBytes(100_000)
	if err := agent.Save(context.Background(), store, ctx); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := store.GetTask(taskID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if len(loaded.Checkpoint) > 100_000 {
		t.Errorf("checkpoint = %d bytes, want <= 100000", len(loaded.Checkpoint))
	}

	var cp agent.Checkpoint
	if err := json.Unmarshal(loaded.Checkpoint, &cp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(cp.Messages) < 2 {
		t.Fatalf("expected >= 2 msgs, got %d", len(cp.Messages))
	}
	if cp.Messages[0].Content != "You are helpful." {
		t.Error("system msg not preserved")
	}
	if cp.Messages[1].Content != "original instruction" {
		t.Error("user msg not preserved")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// 6. Auto Routing Without NVIDIA
// ─────────────────────────────────────────────────────────────────────────────

func TestV27_AutoRoutingWithoutNVIDIA(t *testing.T) {
	reg := provider.NewRegistry()
	reg.Register(&noopProvider{name: "openai"})
	reg.Register(&noopProvider{name: "anthropic"})

	engine := router.NewEngine(&config.Config{}, reg)
	engine.SetAutoSelector(&mockAutoSelector{model: "gpt-4o"})

	route, err := engine.Resolve("auto")
	if err != nil {
		t.Fatalf("Resolve auto: %v", err)
	}
	if route.ModelID != "auto" {
		t.Errorf("ModelID = %q, want auto", route.ModelID)
	}
	if route.ProviderName != "openai" && route.ProviderName != "anthropic" {
		t.Errorf("ProviderName = %q, want openai or anthropic", route.ProviderName)
	}
	if route.ProviderModelID != "gpt-4o" {
		t.Errorf("ProviderModelID = %q, want gpt-4o", route.ProviderModelID)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// 7. Fallback Routing
// ─────────────────────────────────────────────────────────────────────────────

func TestV27_FallbackRouting(t *testing.T) {
	reg := provider.NewRegistry()
	reg.Register(&noopProvider{name: "primary"})
	reg.Register(&noopProvider{name: "fallback"})

	cfg := &config.Config{
		Routes: map[string]config.RouteConfig{
			"my-model": {Provider: "primary"},
		},
		Fallbacks: map[string][]config.FallbackConfig{
			"my-model": {{Provider: "fallback"}},
		},
	}
	engine := router.NewEngine(cfg, reg)

	primary, fallbacks, err := engine.ResolveWithFallback("my-model")
	if err != nil {
		t.Fatalf("ResolveWithFallback: %v", err)
	}
	if primary.ProviderName != "primary" {
		t.Errorf("primary = %q, want primary", primary.ProviderName)
	}
	if len(fallbacks) != 1 {
		t.Fatalf("fallbacks len = %d, want 1", len(fallbacks))
	}
	if fallbacks[0].ProviderName != "fallback" {
		t.Errorf("fallback = %q, want fallback", fallbacks[0].ProviderName)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// 8. Health Monitor Shutdown
// ─────────────────────────────────────────────────────────────────────────────

func TestV27_HealthMonitorShutdown(t *testing.T) {
	reg := provider.NewRegistry()
	reg.Register(&noopProvider{name: "p1"})
	reg.Register(&noopProvider{name: "p2"})

	monitor := health.NewMonitor(reg, zap.NewNop(), 50*time.Millisecond, 100*time.Millisecond)
	monitor.Start()
	time.Sleep(150 * time.Millisecond)

	done := make(chan struct{})
	go func() {
		monitor.Stop()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("HealthMonitor.Stop() leaked goroutines")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// 9. EventBus Shutdown
// ─────────────────────────────────────────────────────────────────────────────

func TestV27_EventBusShutdown(t *testing.T) {
	eb := eventbus.NewEventBus()

	subCh := make(chan eventbus.Event, 10)
	eb.Subscribe("test evt", func(e eventbus.Event) { subCh <- e })
	eb.Publish(context.Background(), eventbus.Event{Type: "test evt", Payload: "hi"})
	select {
	case <-subCh:
	case <-time.After(time.Second):
		t.Fatal("event not received")
	}

	done := make(chan struct{})
	go func() { eb.Stop(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("EventBus.Stop() leaked goroutines")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// 10. Model Prober Shutdown
// ─────────────────────────────────────────────────────────────────────────────

func TestV27_ModelProberShutdown(t *testing.T) {
	reg := provider.NewRegistry()
	reg.Register(&noopProvider{name: "np"})
	cat := catalog.New(reg, catalog.StaticFromConfig(&config.Config{}))
	status := health.NewModelStatusStore(3, true)

	prober := health.NewModelProber(cat, reg, status, zap.NewNop(), config.ModelHealthConfig{Enabled: true})
	prober.Start()
	time.Sleep(200 * time.Millisecond)

	done := make(chan struct{})
	go func() { prober.Stop(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("ModelProber.Stop() leaked goroutines")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// 11. Illegal Terminal Transitions
// ─────────────────────────────────────────────────────────────────────────────

func TestV27_IllegalTerminalTransitions(t *testing.T) {
	db := openTestDB(t, "itt")
	store := task.NewSQLiteStore(db)

	id1 := uuid.New().String()
	_ = db.DB.Create(&task.Task{ID: id1, Status: task.StatusCompleted, Input: "x"}).Error
	if err := store.FailTask(id1, "err"); err == nil {
		t.Error("completed→failed should reject")
	}

	id2 := uuid.New().String()
	_ = db.DB.Create(&task.Task{ID: id2, Status: task.StatusCancelled, Input: "x"}).Error
	if err := store.FailTask(id2, "err"); err == nil {
		t.Error("cancelled→failed should reject")
	}

	id3 := uuid.New().String()
	_ = db.DB.Create(&task.Task{ID: id3, Status: task.StatusCompleted, Input: "x", MaxRetries: 3}).Error
	if _, err := store.MakeRetryable(id3, time.Second); err == nil {
		t.Error("completed→retry should reject")
	}

	id4 := uuid.New().String()
	_ = db.DB.Create(&task.Task{ID: id4, Status: task.StatusCancelled, Input: "x", MaxRetries: 3}).Error
	if _, err := store.MakeRetryable(id4, time.Second); err == nil {
		t.Error("cancelled→retry should reject")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// 12. Normal Single-Agent Task
// ─────────────────────────────────────────────────────────────────────────────

func TestV27_NormalSingleAgentTask(t *testing.T) {
	db := openTestDB(t, "sa")
	store := task.NewSQLiteStore(db)
	logger := zap.NewNop()

	id := uuid.New().String()
	_ = db.DB.Create(&task.Task{ID: id, Status: task.StatusQueued, Input: "hi", MaxRetries: 0}).Error

	pool := workerpkg.New(workerpkg.Config{
		WorkerCount:   1,
		PollInterval:  50 * time.Millisecond,
		LeaseDuration: 5 * time.Minute,
		TaskTimeout:   30 * time.Second,
	}, store, &quickExecutor{store: store}, logger)
	pool.Start()
	defer pool.Stop()

	deadline := time.After(3 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatal("timeout waiting for completion")
		default:
		}
		got, _ := store.GetTask(id)
		if got != nil && got.Status == task.StatusCompleted {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	got, _ := store.GetTask(id)
	if got.Status != task.StatusCompleted {
		t.Errorf("status = %q, want completed", got.Status)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// 13. Normal Coordinator Task
// ─────────────────────────────────────────────────────────────────────────────

func TestV27_NormalCoordinatorTask(t *testing.T) {
	db := openTestDB(t, "nc")
	store := task.NewSQLiteStore(db)
	coordStore := coordinator.NewStoreAdapter(store)
	coord := coordinator.New(coordStore, eventbus.NewEventBus(), zap.NewNop(), coordinator.NewConfig())

	parentID := uuid.New().String()
	_ = store.CreateTask(&task.Task{ID: parentID, Status: task.StatusRunning, Input: "coord", Role: "coordinator"})

	children, err := coord.Delegate(context.Background(), &coordinator.TaskInfo{ID: parentID, Status: "running", Input: "coord"})
	if err != nil {
		t.Fatalf("Delegate: %v", err)
	}
	if len(children) == 0 {
		t.Fatal("expected children")
	}

	for _, cid := range children {
		// Children start as queued; transition through running to completed.
		_ = store.UpdateStatus(cid, task.StatusRunning)
		_ = store.UpdateStatus(cid, task.StatusCompleted)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	agg, err := coord.WaitForChildren(ctx, &coordinator.TaskInfo{ID: parentID}, children)
	if err != nil {
		t.Fatalf("WaitForChildren: %v", err)
	}
	if agg == nil || !agg.AllSucceeded {
		t.Error("expected all children to succeed")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// 14. Gateway /v1/chat/completions Backward Compatibility
// ─────────────────────────────────────────────────────────────────────────────

func TestV27_GatewayBackwardCompat(t *testing.T) {
	reg := provider.NewRegistry()
	reg.Register(&noopProvider{name: "openai"})

	cfg := &config.Config{
		Routes: map[string]config.RouteConfig{
			"gpt-4": {Provider: "openai", ModelID: "gpt-4-turbo"},
		},
	}
	engine := router.NewEngine(cfg, reg)

	route, err := engine.Resolve("gpt-4")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if route.ProviderName != "openai" || route.ProviderModelID != "gpt-4-turbo" {
		t.Errorf("route = %q/%q, want openai/gpt-4-turbo", route.ProviderName, route.ProviderModelID)
	}

	route2, err := engine.Resolve("openai/gpt-4o")
	if err != nil {
		t.Fatalf("Resolve prefixed: %v", err)
	}
	if route2.ProviderName != "openai" || route2.ProviderModelID != "gpt-4o" {
		t.Errorf("prefixed = %q/%q, want openai/gpt-4o", route2.ProviderName, route2.ProviderModelID)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Supplemental: EventBus Burst
// ─────────────────────────────────────────────────────────────────────────────

func TestV27_EventBusBurst(t *testing.T) {
	eb := eventbus.NewEventBus()
	defer eb.Stop()

	var mu sync.Mutex
	count := 0
	eb.Subscribe("burst", func(eventbus.Event) {
		mu.Lock()
		count++
		mu.Unlock()
		time.Sleep(10 * time.Millisecond)
	})

	for i := 0; i < 50; i++ {
		eb.Publish(context.Background(), eventbus.Event{Type: "burst"})
	}
	time.Sleep(500 * time.Millisecond)

	mu.Lock()
	c := count
	mu.Unlock()
	if c != 50 {
		t.Errorf("received %d events, want 50", c)
	}
}
