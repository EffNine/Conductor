package tool_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/EffNine/conductor/internal/tool"
)

type fakeTool struct {
	name        string
	description string
	params      map[string]any
	result      tool.ToolResult
	err         error
	called      bool
}

func (f *fakeTool) Name() string { return f.name }
func (f *fakeTool) Description() string { return f.description }
func (f *fakeTool) Params() map[string]any { return f.params }
func (f *fakeTool) Execute(_ context.Context, _ json.RawMessage) (tool.ToolResult, error) {
	f.called = true
	return f.result, f.err
}

func TestRegister_Valid(t *testing.T) {
	reg := tool.NewRegistry()
	ft := &fakeTool{name: "test_tool", description: "A test tool"}
	if err := reg.Register(ft); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if !reg.Has("test_tool") {
		t.Error("Has('test_tool') = false, want true")
	}
	if reg.Count() != 1 {
		t.Errorf("Count = %d, want 1", reg.Count())
	}
}

func TestGet_Registered(t *testing.T) {
	reg := tool.NewRegistry()
	ft := &fakeTool{name: "get_me", description: "get"}
	reg.Register(ft)

	got, ok := reg.Get("get_me")
	if !ok {
		t.Fatal("Get returned ok=false")
	}
	if got.Name() != "get_me" {
		t.Errorf("Name = %q, want %q", got.Name(), "get_me")
	}
}

func TestGet_NotFound(t *testing.T) {
	reg := tool.NewRegistry()
	_, ok := reg.Get("missing")
	if ok {
		t.Error("Get returned ok=true for missing tool")
	}
}

func TestHas_NotFound(t *testing.T) {
	reg := tool.NewRegistry()
	if reg.Has("missing") {
		t.Error("Has('missing') = true, want false")
	}
}

func TestList_Deterministic(t *testing.T) {
	reg := tool.NewRegistry()
	for _, n := range []string{"beta", "alpha", "gamma"} {
		reg.Register(&fakeTool{name: n})
	}
	list := reg.List()
	if len(list) != 3 {
		t.Fatalf("len(list) = %d, want 3", len(list))
	}
	names := make([]string, len(list))
	for i, t := range list {
		names[i] = t.Name()
	}
	expected := []string{"beta", "alpha", "gamma"}
	for i, exp := range expected {
		if names[i] != exp {
			t.Errorf("list[%d] = %q, want %q", i, names[i], exp)
		}
	}
}

func TestSortByName(t *testing.T) {
	reg := tool.NewRegistry()
	for _, n := range []string{"gamma", "alpha", "beta"} {
		reg.Register(&fakeTool{name: n})
	}
	list := reg.SortByName()
	names := make([]string, len(list))
	for i, t := range list {
		names[i] = t.Name()
	}
	expected := []string{"alpha", "beta", "gamma"}
	for i, exp := range expected {
		if names[i] != exp {
			t.Errorf("sorted[%d] = %q, want %q", i, names[i], exp)
		}
	}
}

func TestRegister_Duplicate(t *testing.T) {
	reg := tool.NewRegistry()
	a := &fakeTool{name: "dup"}
	b := &fakeTool{name: "dup"}
	reg.Register(a)
	err := reg.Register(b)
	if err == nil {
		t.Fatal("expected error for duplicate registration")
	}
	if !errors.Is(err, tool.ErrDuplicateTool) {
		t.Errorf("error = %v, want ErrDuplicateTool", err)
	}
	if reg.Count() != 1 {
		t.Errorf("Count = %d, want 1", reg.Count())
	}
}

func TestRegister_EmptyName(t *testing.T) {
	reg := tool.NewRegistry()
	err := reg.Register(&fakeTool{name: ""})
	if err == nil {
		t.Fatal("expected error for empty name")
	}
	if !errors.Is(err, tool.ErrInvalidTool) {
		t.Errorf("error = %v, want ErrInvalidTool", err)
	}
}

func TestRegister_Nil(t *testing.T) {
	reg := tool.NewRegistry()
	err := reg.Register(nil)
	if err == nil {
		t.Fatal("expected error for nil tool")
	}
	if !errors.Is(err, tool.ErrInvalidTool) {
		t.Errorf("error = %v, want ErrInvalidTool", err)
	}
}

func TestUnregister_Existing(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Register(&fakeTool{name: "remove_me"})
	if err := reg.Unregister("remove_me"); err != nil {
		t.Fatalf("Unregister: %v", err)
	}
	if reg.Has("remove_me") {
		t.Error("tool still present after unregister")
	}
	if reg.Count() != 0 {
		t.Errorf("Count = %d, want 0", reg.Count())
	}
}

func TestUnregister_NotFound(t *testing.T) {
	reg := tool.NewRegistry()
	err := reg.Unregister("missing")
	if err == nil {
		t.Fatal("expected error for unregistering missing tool")
	}
	if !errors.Is(err, tool.ErrToolNotFound) {
		t.Errorf("error = %v, want ErrToolNotFound", err)
	}
}

func TestUnregister_EmptyName(t *testing.T) {
	reg := tool.NewRegistry()
	err := reg.Unregister("")
	if err == nil {
		t.Fatal("expected error for empty name")
	}
}

func TestExecute_Success(t *testing.T) {
	reg := tool.NewRegistry()
	ft := &fakeTool{name: "exec_test", result: tool.Success("hello")}
	reg.Register(ft)

	t_, ok := reg.Get("exec_test")
	if !ok {
		t.Fatal("tool not found")
	}
	result, err := t_.Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Content != "hello" {
		t.Errorf("Content = %q, want %q", result.Content, "hello")
	}
	if result.IsError {
		t.Error("IsError = true, want false")
	}
	if !ft.called {
		t.Error("fake tool Execute was not called")
	}
}

func TestExecute_Error(t *testing.T) {
	reg := tool.NewRegistry()
	ft := &fakeTool{name: "err_test", err: assertAnError("boom")}
	reg.Register(ft)

	t_, _ := reg.Get("err_test")
	_, err := t_.Execute(context.Background(), json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected error from Execute")
	}
}

func TestNames_Deterministic(t *testing.T) {
	reg := tool.NewRegistry()
	for _, n := range []string{"z", "a", "m"} {
		reg.Register(&fakeTool{name: n})
	}
	names := reg.Names()
	expected := []string{"z", "a", "m"}
	for i, exp := range expected {
		if names[i] != exp {
			t.Errorf("Names[%d] = %q, want %q", i, names[i], exp)
		}
	}
}

// ── ToolResult ───────────────────────────────────────────────────────────────

func TestToolResult_Success(t *testing.T) {
	r := tool.Success("output")
	if r.Content != "output" {
		t.Errorf("Content = %q, want %q", r.Content, "output")
	}
	if r.IsError {
		t.Error("IsError = true, want false")
	}
}

func TestToolResult_Failure(t *testing.T) {
	r := tool.Failure("bad stuff")
	if r.Content != "bad stuff" {
		t.Errorf("Content = %q, want %q", r.Content, "bad stuff")
	}
	if !r.IsError {
		t.Error("IsError = false, want true")
	}
}

func TestToolResult_String_Success(t *testing.T) {
	r := tool.Success("ok")
	if r.String() != "ok" {
		t.Errorf("String() = %q, want %q", r.String(), "ok")
	}
}

func TestToolResult_String_Error(t *testing.T) {
	r := tool.Failure("fail")
	s := r.String()
	if s != "tool error: fail" {
		t.Errorf("String() = %q, want %q", s, "tool error: fail")
	}
}

func assertAnError(s string) error {
	return &testErr{msg: s}
}

type testErr struct{ msg string }

func (e *testErr) Error() string { return e.msg }
