package tool_test

import (
	"encoding/json"
	"sync"
	"testing"

	"github.com/EffNine/conductor/internal/tool"
)

func TestConcurrentReads(t *testing.T) {
	reg := tool.NewRegistry()
	for i := 0; i < 10; i++ {
		reg.Register(&fakeTool{name: "tool-" + itoa(i)})
	}

	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				_ = reg.List()
				_ = reg.Count()
				_ = reg.Names()
				_ = reg.SortByName()
				for n := 0; n < 10; n++ {
					_, _ = reg.Get("tool-" + itoa(n))
					_ = reg.Has("tool-" + itoa(n))
				}
			}
		}()
	}
	wg.Wait()
}

func TestConcurrentRegisterUnregister(t *testing.T) {
	reg := tool.NewRegistry()
	var wg sync.WaitGroup

	// Register in parallel.
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			name := "parallel-" + itoa(idx)
			_ = reg.Register(&fakeTool{name: name})
		}(i)
	}
	wg.Wait()

	if reg.Count() != 10 {
		t.Errorf("Count = %d, want 10", reg.Count())
	}

	// Unregister in parallel.
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			_ = reg.Unregister("parallel-" + itoa(idx))
		}(i)
	}
	wg.Wait()

	if reg.Count() != 0 {
		t.Errorf("Count = %d, want 0", reg.Count())
	}
}

func itoa(i int) string {
	if i < 10 {
		return "0" + itoa2(i)
	}
	return itoa2(i)
}

func itoa2(i int) string {
	if i < 10 {
		return string(rune('0' + i))
	}
	return string(rune('0'+i/10)) + string(rune('0'+i%10))
}

func TestRegisterAndGetToolParams(t *testing.T) {
	reg := tool.NewRegistry()
	params := map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}}}
	ft := &fakeTool{name: "param_test", params: params}
	reg.Register(ft)

	t_, ok := reg.Get("param_test")
	if !ok {
		t.Fatal("tool not found")
	}
	got := t_.Params()
	if got["type"] != "object" {
		t.Errorf("Params type = %v, want object", got["type"])
	}
}

func TestToolResult_MarshalJSON_Success(t *testing.T) {
	r := tool.Success("hello")
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("MarshalJSON: %v", err)
	}
	var m map[string]any
	json.Unmarshal(b, &m)
	if m["error"] != false {
		t.Errorf("error = %v, want false", m["error"])
	}
	if m["content"] != "hello" {
		t.Errorf("content = %v, want 'hello'", m["content"])
	}
}

func TestToolResult_MarshalJSON_Error(t *testing.T) {
	r := tool.Failure("boom")
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("MarshalJSON: %v", err)
	}
	var m map[string]any
	json.Unmarshal(b, &m)
	if m["error"] != true {
		t.Errorf("error = %v, want true", m["error"])
	}
}
