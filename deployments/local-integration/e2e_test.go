//go:build integration

// End-to-end smoke test: boots the real Conductor binary against two
// in-process mock upstreams — one permanently broken, one healthy — and
// asserts that chat (non-streaming and streaming), embeddings, the merged
// catalog, and fallback observability all work through the live gateway.
//
// Run:  go test -tags=integration -v ./deployments/local-integration/

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("free port: %v", err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// brokenUpstream serves a valid catalog but fails every inference call.
func brokenUpstream(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/models", func(w http.ResponseWriter, r *http.Request) {
		writeJSONE2E(w, map[string]any{"object": "list", "data": []map[string]any{
			{"id": "gpt-4o", "object": "model", "owned_by": "mock"},
			{"id": "embed-model", "object": "model", "owned_by": "mock"},
		}})
	})
	fail := func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":{"message":"upstream is down","type":"server_error"}}`, http.StatusInternalServerError)
	}
	mux.HandleFunc("/v1/chat/completions", fail)
	mux.HandleFunc("/v1/embeddings", fail)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// healthyUpstream serves chat (streaming and not), embeddings, and a catalog.
func healthyUpstream(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/models", func(w http.ResponseWriter, r *http.Request) {
		writeJSONE2E(w, map[string]any{"object": "list", "data": []map[string]any{
			{"id": "llama-3.1-8b-instruct", "object": "model", "owned_by": "mock"},
			{"id": "embed-model", "object": "model", "owned_by": "mock"},
		}})
	})
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Stream bool `json:"stream"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		w.Header().Set("Content-Type", "application/json")
		if req.Stream {
			w.Header().Set("Content-Type", "text/event-stream")
			flusher := w.(http.Flusher)
			chunk := map[string]any{
				"id": "chatcmpl-e2e", "object": "chat.completion.chunk",
				"choices": []map[string]any{{"index": 0, "delta": map[string]any{"role": "assistant", "content": "hello from healthy"}}},
			}
			b, _ := json.Marshal(chunk)
			fmt.Fprintf(w, "data: %s\n\n", b)
			flusher.Flush()
			fmt.Fprint(w, "data: [DONE]\n\n")
			flusher.Flush()
			return
		}
		writeJSONE2E(w, map[string]any{
			"id": "chatcmpl-e2e", "object": "chat.completion", "model": "llama-3.1-8b-instruct",
			"choices": []map[string]any{{
				"index":         0,
				"message":       map[string]any{"role": "assistant", "content": "hello from healthy"},
				"finish_reason": "stop",
			}},
			"usage": map[string]any{"prompt_tokens": 3, "completion_tokens": 4, "total_tokens": 7},
		})
	})
	mux.HandleFunc("/v1/embeddings", func(w http.ResponseWriter, r *http.Request) {
		writeJSONE2E(w, map[string]any{
			"object": "list", "model": "embed-model",
			"data":  []map[string]any{{"object": "embedding", "embedding": []float64{0.1, 0.2}, "index": 0}},
			"usage": map[string]any{"prompt_tokens": 2, "total_tokens": 2},
		})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func writeJSONE2E(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func TestGatewayEndToEndFailover(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain unavailable")
	}

	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("repo root: %v", err)
	}

	broken := brokenUpstream(t)
	healthy := healthyUpstream(t)

	workDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workDir, "data"), 0o755); err != nil {
		t.Fatalf("data dir: %v", err)
	}

	gatewayPort := freePort(t)
	cfg := fmt.Sprintf(`
api_key: sk-e2e-test-key

server:
  host: "127.0.0.1"
  port: %d

providers:
  openai:
    enabled: true
    api_key: k-broken
    base_url: %s/v1
  groq:
    enabled: true
    api_key: k-healthy
    base_url: %s/v1

routes:
  gpt-4o:
    provider: openai
  embed-model:
    provider: openai

catalog:
  curated_only: false
`, gatewayPort, broken.URL, healthy.URL)

	if err := os.WriteFile(filepath.Join(workDir, "config.yaml"), []byte(cfg), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	bin := buildConductor(t, repoRoot, workDir)
	cmd, logs := startConductor(ctx, t, bin, workDir)
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})

	base := fmt.Sprintf("http://127.0.0.1:%d", gatewayPort)
	waitHealthy(t, ctx, base, logs)

	authz := "Bearer sk-e2e-test-key"

	// 1. Merged dashboard catalog advertises the healthy provider's models.
	catalogJSON := getE2E(t, ctx, base+"/api/models", authz)
	if !strings.Contains(catalogJSON, "groq/llama-3.1-8b-instruct") {
		t.Fatalf("/api/models missing healthy provider model:\n%s", catalogJSON)
	}

	// 2. Non-streaming chat fails over to the healthy provider.
	resp := postE2E(t, ctx, base+"/v1/chat/completions", authz,
		`{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`)
	body := readAllE2E(t, resp)
	if resp.StatusCode != 200 || !strings.Contains(body, "hello from healthy") {
		t.Fatalf("non-streaming failover failed: status=%d body=%s", resp.StatusCode, body)
	}

	// 3. Streaming chat fails over before first byte.
	resp = postE2E(t, ctx, base+"/v1/chat/completions", authz,
		`{"model":"gpt-4o","stream":true,"messages":[{"role":"user","content":"hi"}]}`)
	body = readAllE2E(t, resp)
	if resp.StatusCode != 200 || !strings.Contains(body, "hello from healthy") || !strings.Contains(body, "[DONE]") {
		t.Fatalf("streaming failover failed: status=%d body=%s", resp.StatusCode, body)
	}

	// 4. Embeddings fail over to the healthy provider.
	resp = postE2E(t, ctx, base+"/v1/embeddings", authz,
		`{"model":"embed-model","input":"hello"}`)
	body = readAllE2E(t, resp)
	if resp.StatusCode != 200 || !strings.Contains(body, `"embedding"`) {
		t.Fatalf("embeddings failover failed: status=%d body=%s", resp.StatusCode, body)
	}

	// 5. Fallback engagement is observable.
	metrics := getE2E(t, ctx, base+"/api/metrics", authz)
	if !strings.Contains(metrics, `conductor_fallback_total{kind="dynamic",provider="groq"}`) {
		t.Fatalf("metrics missing dynamic fallback counter:\n%s", metrics)
	}
}

func waitHealthy(t *testing.T, ctx context.Context, base string, logs *bytes.Buffer) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, base+"/health", nil)
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == 200 {
				return
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("gateway did not become healthy; logs:\n%s", logs.String())
}

func getE2E(t *testing.T, ctx context.Context, url, authz string) string {
	t.Helper()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	req.Header.Set("Authorization", authz)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("GET %s: status %d body=%s", url, resp.StatusCode, string(b))
	}
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	return string(b)
}

func postE2E(t *testing.T, ctx context.Context, url, authz, body string) *http.Response {
	t.Helper()
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(body))
	req.Header.Set("Authorization", authz)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	return resp
}

func readAllE2E(t *testing.T, resp *http.Response) string {
	t.Helper()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	_ = resp.Body.Close()
	return string(b)
}

func buildConductor(t *testing.T, repoRoot, workDir string) string {
	t.Helper()
	bin := filepath.Join(workDir, "conductor")
	build := exec.Command("go", "build", "-o", bin, "./cmd/conductor")
	build.Dir = repoRoot
	build.Env = append(os.Environ(), "CGO_ENABLED=1")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build conductor: %v\n%s", err, out)
	}
	return bin
}

func startConductor(ctx context.Context, t *testing.T, bin, workDir string) (*exec.Cmd, *bytes.Buffer) {
	t.Helper()
	cmd := exec.CommandContext(ctx, bin)
	cmd.Dir = workDir
	// Hermetic env: no inherited provider keys, proxies, or other host state.
	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + os.Getenv("HOME"),
		"CGO_ENABLED=1",
		"CONDUCTOR_API_KEY=sk-e2e-test-key",
	}
	var logs bytes.Buffer
	cmd.Stdout = &logs
	cmd.Stderr = &logs
	if err := cmd.Start(); err != nil {
		t.Fatalf("start conductor: %v", err)
	}
	return cmd, &logs
}

// TestGatewayMinimalConfigNoRoutes encodes the out-of-box deployment journey:
// a single provider configured, NO routes section. Bare model IDs,
// provider-prefixed IDs, and virtual categories all serve; dynamic failover
// covers the rest of the catalog.
func TestGatewayMinimalConfigNoRoutes(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain unavailable")
	}

	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("repo root: %v", err)
	}
	healthy := healthyUpstream(t)

	workDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workDir, "data"), 0o755); err != nil {
		t.Fatalf("data dir: %v", err)
	}

	gatewayPort := freePort(t)
	cfg := fmt.Sprintf(`
api_key: sk-e2e-test-key

server:
  host: "127.0.0.1"
  port: %d

providers:
  openai:
    enabled: true
    api_key: k-healthy
    base_url: %s/v1
`, gatewayPort, healthy.URL)
	if err := os.WriteFile(filepath.Join(workDir, "config.yaml"), []byte(cfg), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	bin := buildConductor(t, repoRoot, workDir)
	cmd, logs := startConductor(ctx, t, bin, workDir)
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})

	base := fmt.Sprintf("http://127.0.0.1:%d", gatewayPort)
	waitHealthy(t, ctx, base, logs)
	authz := "Bearer sk-e2e-test-key"

	// Provider-prefixed IDs route with zero route config.
	resp := postE2E(t, ctx, base+"/v1/chat/completions", authz,
		`{"model":"openai/gpt-4o","messages":[{"role":"user","content":"hi"}]}`)
	body := readAllE2E(t, resp)
	if resp.StatusCode != 200 || !strings.Contains(body, "hello from healthy") {
		t.Fatalf("prefixed-ID request failed: status=%d body=%s", resp.StatusCode, body)
	}

	// Bare model ID auto-resolves when exactly one provider is configured
	// (routing.auto_resolve_bare_models defaults to true via config defaults).
	resp = postE2E(t, ctx, base+"/v1/chat/completions", authz,
		`{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`)
	body = readAllE2E(t, resp)
	if resp.StatusCode != 200 || !strings.Contains(body, "hello from healthy") {
		t.Fatalf("bare single-provider model should serve: status=%d body=%s", resp.StatusCode, body)
	}

	// Virtual auto model selects from the catalog and serves.
	resp = postE2E(t, ctx, base+"/v1/chat/completions", authz,
		`{"model":"auto","messages":[{"role":"user","content":"hi"}]}`)
	body = readAllE2E(t, resp)
	if resp.StatusCode != 200 {
		t.Fatalf("auto request failed: status=%d body=%s", resp.StatusCode, body)
	}
}
