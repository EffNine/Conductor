// mock-upstream is a deterministic OpenAI-compatible upstream for local
// Conductor integration testing. It serves:
//
//	GET  /v1/models
//	POST /v1/chat/completions   (streaming and non-streaming)
//
// Run:  go run mock-upstream.go -addr 127.0.0.1:9119
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"
)

type chatRequest struct {
	Model    string `json:"model"`
	Stream   bool   `json:"stream"`
	Messages []struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"messages"`
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func main() {
	addr := flag.String("addr", "127.0.0.1:9119", "listen address")
	flag.Parse()

	mux := http.NewServeMux()

	mux.HandleFunc("/v1/models", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{
			"object": "list",
			"data": []map[string]any{
				{"id": "mock-large", "object": "model", "owned_by": "mock"},
				{"id": "mock-small", "object": "model", "owned_by": "mock"},
			},
		})
	})

	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		var req chatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, `{"error":{"message":"bad json","type":"invalid_request_error"}}`, http.StatusBadRequest)
			return
		}
		log.Printf("[mock] chat model=%s stream=%v messages=%d", req.Model, req.Stream, len(req.Messages))

		now := time.Now().Unix()
		id := fmt.Sprintf("chatcmpl-mock-%d", now)

		if req.Stream {
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			flusher := w.(http.Flusher)
			frame := func(content string) {
				fmt.Fprintf(w, "data: {\"id\":%q,\"object\":\"chat.completion.chunk\",\"created\":%d,\"model\":%q,\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":%q}}]}\n\n",
					id, now, req.Model, content)
				flusher.Flush()
			}
			frame("mock ")
			time.Sleep(30 * time.Millisecond)
			frame("response ")
			time.Sleep(30 * time.Millisecond)
			frame("from upstream")
			fmt.Fprint(w, "data: [DONE]\n\n")
			flusher.Flush()
			return
		}

		writeJSON(w, map[string]any{
			"id": id, "object": "chat.completion", "created": now, "model": req.Model,
			"choices": []map[string]any{{
				"index":         0,
				"message":       map[string]any{"role": "assistant", "content": "mock response from upstream"},
				"finish_reason": "stop",
			}},
			"usage": map[string]any{"prompt_tokens": 12, "completion_tokens": 5, "total_tokens": 17},
		})
	})

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/v1/") {
			http.NotFound(w, r)
			return
		}
		http.NotFound(w, r)
	})

	log.Printf("[mock] OpenAI-compatible upstream listening on %s", *addr)
	srv := &http.Server{
		Addr:              *addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      120 * time.Second, // generous: SSE streams
	}
	if err := srv.ListenAndServe(); err != nil {
		log.Fatal(err)
	}
}
