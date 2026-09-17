package jev_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	jev "github.com/EffNine/conductor/internal/jev"
)

func TestEvaluateChoiceHappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/systemone" {
			t.Errorf("path = %q, want /v1/systemone", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("auth header = %q, want Bearer test-key", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"model": "jev-latest",
			"answers": {
				"best_provider": {
					"type": "choice",
					"choice": "openai",
					"probabilities": {"openai": 0.7, "groq": 0.3},
					"confidence": 0.7
				}
			},
			"usage": {"input_tokens": 10, "output_tokens": 2}
		}`))
	}))
	defer srv.Close()

	c := jev.NewClient(jev.Config{
		APIKey:     "test-key",
		Endpoint:   srv.URL + "/v1/systemone",
		Model:      "jev-latest",
		Timeout:    5 * time.Second,
		HTTPClient: srv.Client(),
	})
	state := map[string]any{"requested_model": "gpt-4o", "selected_provider": "openai"}
	questions := map[string]jev.Question{
		"best_provider": {Choice: &jev.ChoiceQuestion{
			Instructions: "Which provider?",
			Criteria:     map[string]any{"openai": nil, "groq": nil},
		}},
	}
	answers, usage, err := c.Evaluate(context.Background(), state, questions)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if got := answers["best_provider"].Choice; got != "openai" {
		t.Fatalf("choice = %q, want openai", got)
	}
	if usage == nil || usage.InputTokens != 10 {
		t.Fatalf("usage = %+v, want 10 input tokens", usage)
	}
}

func TestEvaluateStatusError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":"rate limited"}`))
	}))
	defer srv.Close()

	c := jev.NewClient(jev.Config{
		APIKey:     "k",
		Endpoint:   srv.URL + "/v1/systemone",
		Timeout:    5 * time.Second,
		HTTPClient: srv.Client(),
	})
	questions := map[string]jev.Question{
		"q": {Noul: &jev.NoulQuestion{Instructions: "yes?"}},
	}
	_, _, err := c.Evaluate(context.Background(), "state", questions)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestEvaluateUnauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	c := jev.NewClient(jev.Config{
		Endpoint:   srv.URL + "/v1/systemone",
		Timeout:    5 * time.Second,
		HTTPClient: srv.Client(),
	})
	questions := map[string]jev.Question{
		"q": {Noul: &jev.NoulQuestion{Instructions: "yes?"}},
	}
	_, _, err := c.Evaluate(context.Background(), "state", questions)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestEvaluateMalformedResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`not json`))
	}))
	defer srv.Close()

	c := jev.NewClient(jev.Config{
		Endpoint:   srv.URL + "/v1/systemone",
		Timeout:    5 * time.Second,
		HTTPClient: srv.Client(),
	})
	questions := map[string]jev.Question{
		"q": {Noul: &jev.NoulQuestion{Instructions: "yes?"}},
	}
	_, _, err := c.Evaluate(context.Background(), "state", questions)
	if err == nil {
		t.Fatal("expected malformed error")
	}
}

func TestEvaluateNoQuestions(t *testing.T) {
	c := jev.NewClient(jev.Config{Endpoint: "https://example.com"})
	_, _, err := c.Evaluate(context.Background(), "state", nil)
	if err == nil {
		t.Fatal("expected error for empty questions")
	}
}

func TestValidateEndpointScheme(t *testing.T) {
	c := jev.NewClient(jev.Config{Endpoint: "ftp://bad"})
	if err := c.Validate(); err == nil {
		t.Fatal("expected scheme error")
	}
	c2 := jev.NewClient(jev.Config{Endpoint: "https://ok.example"})
	if err := c2.Validate(); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
}
