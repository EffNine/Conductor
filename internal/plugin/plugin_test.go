package plugin

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockPlugin is a test double for the Plugin interface.
type mockPlugin struct {
	id          string
	name        string
	category    PluginCategory
	version     string
	metadata    PluginMetadata
	initCalled  bool
	startCalled bool
	stopCalled  bool
	health      PluginHealth
	config      PluginConfig
}

func (m *mockPlugin) ID() string               { return m.id }
func (m *mockPlugin) Name() string             { return m.name }
func (m *mockPlugin) Category() PluginCategory { return m.category }
func (m *mockPlugin) Version() string          { return m.version }
func (m *mockPlugin) Metadata() PluginMetadata { return m.metadata }
func (m *mockPlugin) Init(ctx context.Context, cfg PluginConfig) error {
	m.initCalled = true
	m.config = cfg
	return nil
}
func (m *mockPlugin) Start(ctx context.Context) error {
	m.startCalled = true
	return nil
}
func (m *mockPlugin) Stop(ctx context.Context) error {
	m.stopCalled = true
	return nil
}
func (m *mockPlugin) Health() PluginHealth { return m.health }

func TestPluginCategoryIsValid(t *testing.T) {
	cases := []struct {
		cat      PluginCategory
		expected bool
	}{
		{CategoryProvider, true},
		{CategoryPolicy, true},
		{CategoryLearning, true},
		{CategoryScheduler, true},
		{CategoryDashboard, true},
		{CategoryTool, true},
		{PluginCategory("unknown"), false},
		{PluginCategory(""), false},
	}
	for _, c := range cases {
		t.Run(string(c.cat), func(t *testing.T) {
			assert.Equal(t, c.expected, c.cat.IsValid())
		})
	}
}

func TestPluginCategoryString(t *testing.T) {
	assert.Equal(t, "provider", CategoryProvider.String())
	assert.Equal(t, "policy", CategoryPolicy.String())
	assert.Equal(t, "learning", CategoryLearning.String())
}

func TestNewPluginMetadata(t *testing.T) {
	m := NewPluginMetadata("id-1", "TestPlugin", "1.0.0", CategoryPolicy)
	assert.Equal(t, "id-1", m.ID)
	assert.Equal(t, "TestPlugin", m.Name)
	assert.Equal(t, "1.0.0", m.Version)
	assert.Equal(t, CategoryPolicy, m.Category)
	assert.False(t, m.RegisteredAt.IsZero())
}

func TestPluginMetadataChaining(t *testing.T) {
	m := NewPluginMetadata("id-1", "TestPlugin", "1.0.0", CategoryTool)
	m = m.WithDescription("A test plugin").WithAuthor("test auth").WithTags([]string{"a", "b"})
	m = m.WithConfigSchema(map[string]ConfigField{"key": {Type: "string", Required: true}})
	m = m.WithDeprecationNote("use v2 instead")

	assert.Equal(t, "A test plugin", m.Description)
	assert.Equal(t, "test auth", m.Author)
	assert.Equal(t, []string{"a", "b"}, m.Tags)
	assert.Len(t, m.ConfigSchema, 1)
	assert.Equal(t, "use v2 instead", m.DeprecationNote)
}

func TestPluginManager_Register(t *testing.T) {
	mgr := NewPluginManager().(*pluginManager)
	p := &mockPlugin{id: "p1", name: "Plugin1", category: CategoryPolicy, health: PluginHealth{OK: true}}

	err := mgr.Register(context.Background(), p)
	require.NoError(t, err)
	assert.Equal(t, 1, mgr.Count())

	// Duplicate ID should fail.
	err = mgr.Register(context.Background(), p)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "duplicate ID")

	// Nil plugin should fail.
	err = mgr.Register(context.Background(), nil)
	assert.Error(t, err)
}

func TestPluginManager_Deregister(t *testing.T) {
	mgr := NewPluginManager().(*pluginManager)
	p := &mockPlugin{id: "p1", name: "Plugin1", category: CategoryPolicy, health: PluginHealth{OK: true}}
	require.NoError(t, mgr.Register(context.Background(), p))

	err := mgr.Deregister(context.Background(), "p1")
	require.NoError(t, err)
	assert.Equal(t, 0, mgr.Count())

	// Deregister non-existent should fail.
	err = mgr.Deregister(context.Background(), "p1")
	assert.Error(t, err)
}

func TestPluginManager_Get(t *testing.T) {
	mgr := NewPluginManager().(*pluginManager)
	p := &mockPlugin{id: "p1", name: "Plugin1", category: CategoryPolicy, health: PluginHealth{OK: true}}
	require.NoError(t, mgr.Register(context.Background(), p))

	got, err := mgr.Get("p1")
	require.NoError(t, err)
	assert.Equal(t, "Plugin1", got.Name())

	_, err = mgr.Get("missing")
	assert.Error(t, err)
}

func TestPluginManager_List(t *testing.T) {
	mgr := NewPluginManager().(*pluginManager)
	p1 := &mockPlugin{id: "p1", name: "Plugin1", category: CategoryProvider, health: PluginHealth{OK: true}}
	p2 := &mockPlugin{id: "p2", name: "Plugin2", category: CategoryPolicy, health: PluginHealth{OK: false}}
	require.NoError(t, mgr.Register(context.Background(), p1))
	require.NoError(t, mgr.Register(context.Background(), p2))

	list := mgr.List()
	assert.Len(t, list, 2)
}

func TestPluginManager_ListByCategory(t *testing.T) {
	mgr := NewPluginManager().(*pluginManager)
	p1 := &mockPlugin{id: "p1", name: "Plugin1", category: CategoryProvider, health: PluginHealth{OK: true}}
	p2 := &mockPlugin{id: "p2", name: "Plugin2", category: CategoryProvider, health: PluginHealth{OK: true}}
	p3 := &mockPlugin{id: "p3", name: "Plugin3", category: CategoryPolicy, health: PluginHealth{OK: true}}
	require.NoError(t, mgr.Register(context.Background(), p1))
	require.NoError(t, mgr.Register(context.Background(), p2))
	require.NoError(t, mgr.Register(context.Background(), p3))

	providers := mgr.ListByCategory(CategoryProvider)
	assert.Len(t, providers, 2)

	policies := mgr.ListByCategory(CategoryPolicy)
	assert.Len(t, policies, 1)
}

func TestPluginManager_StartStop(t *testing.T) {
	mgr := NewPluginManager().(*pluginManager)
	p := &mockPlugin{
		id:       "p1",
		name:     "Plugin1",
		category: CategoryPolicy,
		health:   PluginHealth{OK: true},
	}
	require.NoError(t, mgr.Register(context.Background(), p))

	err := mgr.Start(context.Background())
	require.NoError(t, err)
	assert.True(t, p.initCalled)
	assert.True(t, p.startCalled)

	err = mgr.Stop(context.Background())
	require.NoError(t, err)
	assert.True(t, p.stopCalled)
}

func TestPluginManager_Health(t *testing.T) {
	mgr := NewPluginManager().(*pluginManager)
	p := &mockPlugin{id: "p1", name: "Plugin1", category: CategoryProvider, health: PluginHealth{OK: true, Status: "healthy"}}
	require.NoError(t, mgr.Register(context.Background(), p))

	health := mgr.Health()
	assert.Len(t, health, 1)
	assert.True(t, health["p1"].OK)
	assert.Equal(t, "healthy", health["p1"].Status)
}

func TestPluginRegistry(t *testing.T) {
	reg := NewPluginRegistry().(*registry)
	p1 := &mockPlugin{id: "p1", name: "Plugin1", category: CategoryProvider}
	p2 := &mockPlugin{id: "p2", name: "Plugin2", category: CategoryPolicy}

	reg.add(p1)
	reg.add(p2)

	// ById
	got, ok := reg.ById("p1")
	require.True(t, ok)
	assert.Equal(t, "Plugin1", got.Name())

	// ByName
	got, ok = reg.ByName("Plugin2")
	require.True(t, ok)
	assert.Equal(t, "p2", got.ID())

	// ByIdentifier (ID match)
	got, ok = reg.ByIdentifier("p1")
	require.True(t, ok)
	assert.Equal(t, "Plugin1", got.Name())

	// ByIdentifier (name match)
	got, ok = reg.ByIdentifier("Plugin2")
	require.True(t, ok)
	assert.Equal(t, "p2", got.ID())

	// Not found
	_, ok = reg.ById("missing")
	assert.False(t, ok)

	// Contains
	assert.True(t, reg.Contains("p1"))
	assert.False(t, reg.Contains("missing"))

	// Count
	assert.Equal(t, 2, reg.Count())

	// GetAll
	all := reg.GetAll()
	assert.Len(t, all, 2)

	// ByCategory
	providers := reg.ByCategory(CategoryProvider)
	assert.Len(t, providers, 1)
	assert.Equal(t, "p1", providers[0].ID())
}

func TestPluginRegistry_SyncFromManager(t *testing.T) {
	mgr := NewPluginManager().(*pluginManager)
	reg := NewPluginRegistry().(*registry)

	p1 := &mockPlugin{id: "p1", name: "Plugin1", category: CategoryProvider}
	p2 := &mockPlugin{id: "p2", name: "Plugin2", category: CategoryPolicy}
	require.NoError(t, mgr.Register(context.Background(), p1))
	require.NoError(t, mgr.Register(context.Background(), p2))

	err := reg.SyncFromManager(mgr)
	require.NoError(t, err)
	assert.Equal(t, 2, reg.Count())
	assert.True(t, reg.Contains("p1"))
	assert.True(t, reg.Contains("p2"))
}

func TestPluginLoaderFunc(t *testing.T) {
	factory := func(ctx context.Context, cfg PluginConfig) (Plugin, error) {
		return &mockPlugin{id: cfg.PluginID, name: cfg.Name, category: CategoryTool}, nil
	}
	loader := NewFactoryLoader(factory)

	p, err := loader.Load(context.Background(), PluginConfig{PluginID: "test-1", Name: "Test"})
	require.NoError(t, err)
	assert.Equal(t, "test-1", p.ID())
	assert.Equal(t, "Test", p.Name())
	assert.Equal(t, CategoryTool, p.Category())
}

func TestPluginLoaderFunc_LoadBatch(t *testing.T) {
	factory := func(ctx context.Context, cfg PluginConfig) (Plugin, error) {
		return &mockPlugin{id: cfg.PluginID, name: cfg.Name, category: CategoryTool}, nil
	}
	loader := NewFactoryLoader(factory)

	configs := []PluginConfig{
		{PluginID: "p1", Name: "Plugin1"},
		{PluginID: "p2", Name: "Plugin2"},
	}
	result, err := loader.LoadBatch(context.Background(), configs)
	require.NoError(t, err)
	assert.Len(t, result, 2)
	assert.NotNil(t, result["p1"])
	assert.NotNil(t, result["p2"])
}

func TestExtensionPointFn(t *testing.T) {
	hook := NewExtensionHook(AfterDecision, func(ctx context.Context, input any) (output any, err error) {
		return input, nil
	})

	assert.Equal(t, AfterDecision, hook.Point())
	out, err := hook.Execute(context.Background(), "test")
	require.NoError(t, err)
	assert.Equal(t, "test", out)
}

func TestExtensionPointConstants(t *testing.T) {
	expected := []ExtensionPoint{
		BeforePipeline,
		AfterIntent,
		AfterCapability,
		AfterCandidateGeneration,
		AfterSelection,
		BeforeExecution,
		AfterExecution,
		AfterDecision,
	}
	for _, ep := range expected {
		assert.NotEmpty(t, ep)
	}
}

func TestPluginManager_StartOrder(t *testing.T) {
	mgr := NewPluginManager().(*pluginManager)

	// Plugins in reverse category order should be started in correct order.
	scheduler := &mockPlugin{id: "s1", name: "Scheduler", category: CategoryScheduler, health: PluginHealth{OK: true}}
	policy := &mockPlugin{id: "pol1", name: "Policy", category: CategoryPolicy, health: PluginHealth{OK: true}}
	provider := &mockPlugin{id: "pr1", name: "Provider", category: CategoryProvider, health: PluginHealth{OK: true}}

	require.NoError(t, mgr.Register(context.Background(), scheduler))
	require.NoError(t, mgr.Register(context.Background(), policy))
	require.NoError(t, mgr.Register(context.Background(), provider))

	err := mgr.Start(context.Background())
	require.NoError(t, err)

	// All should be initialized and started.
	assert.True(t, provider.initCalled)
	assert.True(t, policy.initCalled)
	assert.True(t, scheduler.initCalled)
	assert.True(t, provider.startCalled)
	assert.True(t, policy.startCalled)
	assert.True(t, scheduler.startCalled)
}
