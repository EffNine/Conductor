// Package jev provides a minimal HTTP client for TypeSafe's System One
// evaluation API. It is used by the JEV shadow integration (Phase C1) as a
// pure observation aid: every call result is recorded as evidence and MUST
// never influence Conductor's production routing decision.
//
// The client is deliberately dependency-free (net/http only) and fails soft:
// callers treat any error, timeout, or malformed response as "advisory
// unavailable" and continue normally.
package jev

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DefaultEndpoint is the public TypeSafe System One evaluation endpoint.
const DefaultEndpoint = "https://api.typesafe.ai/v1/systemone"

// DefaultModel is the locked baseline JEV model alias (the flagship System
// One model). Phase C1 does not introduce a new model; it observes the
// existing baseline.
const DefaultModel = "jev-latest"

// Config controls a Client. All fields are optional; sensible defaults apply.
type Config struct {
	// Endpoint overrides the evaluation URL. Empty = DefaultEndpoint.
	Endpoint string
	// APIKey authenticates the request. Empty = unauthorized (the request
	// is attempted only when the caller has configured a key).
	APIKey string
	// Model overrides the model id. Empty = DefaultModel.
	Model string
	// Timeout bounds the HTTP call. <= 0 = no per-call deadline.
	Timeout time.Duration
	// HTTPClient is used when non-nil (tests inject fake transports).
	HTTPClient *http.Client
}

// Client evaluates state against typed questions via the System One API.
type Client struct {
	cfg    Config
	client *http.Client
}

// NewClient builds a Client from cfg.
func NewClient(cfg Config) *Client {
	if cfg.Endpoint == "" {
		cfg.Endpoint = DefaultEndpoint
	}
	if cfg.Model == "" {
		cfg.Model = DefaultModel
	}
	hc := cfg.HTTPClient
	if hc == nil {
		hc = &http.Client{}
		if cfg.Timeout > 0 {
			hc.Timeout = cfg.Timeout
		}
	}
	return &Client{cfg: cfg, client: hc}
}

// Model returns the configured model id.
func (c *Client) Model() string { return c.cfg.Model }

// Timeout returns the per-call deadline configured on the client.
func (c *Client) Timeout() time.Duration { return c.cfg.Timeout }

// Validate checks the client configuration (endpoint scheme). It is meant to
// be called at startup so misconfiguration fails fast, before any request.
func (c *Client) Validate() error {
	if c == nil {
		return errors.New("jev: nil client")
	}
	return validateEndpoint(c.cfg.Endpoint)
}

// RequestError classifies transport- or protocol-level failures so shadow
// consumers can distinguish a service problem from malformed output.
type RequestError struct {
	Kind   string // "http", "status", "timeout", "unauthorized", "rate_limited"
	Status int
	Reason string
}

func (e *RequestError) Error() string {
	return fmt.Sprintf("jev: %s: %s", e.Kind, e.Reason)
}

// IsRequestError reports whether err is a *RequestError.
func IsRequestError(err error) bool {
	var re *RequestError
	return errors.As(err, &re)
}

// question mirrors one entry of the API's questions map.
type question struct {
	Type         string `json:"type"`
	Instructions any    `json:"instructions"`
	Criteria     any    `json:"criteria,omitempty"`
}

// request is the System One request body.
type request struct {
	State     any                 `json:"state"`
	Model     string              `json:"model"`
	Questions map[string]question `json:"questions"`
}

// response mirrors the API response.
type response struct {
	Model   string                `json:"model"`
	Answers map[string]answerData `json:"answers"`
	Usage   *usage                `json:"usage,omitempty"`
}

type usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// answerData is the wire shape of a single answer.
type answerData struct {
	Type          string             `json:"type"`
	Choice        string             `json:"choice,omitempty"`
	Probabilities map[string]float64 `json:"probabilities,omitempty"`
	Confidence    *float64           `json:"confidence,omitempty"`
	Noul          *float64           `json:"noul,omitempty"`
	Score         *float64           `json:"score,omitempty"`
	Legend        map[string]string  `json:"legend,omitempty"`
}

// Question defines a typed question. Exactly one primitive must be set.
type Question struct {
	// Choice selects one option from a defined set (map of option ->
	// rubric; null entries allowed for self-explanatory options).
	Choice *ChoiceQuestion
	// Noul evaluates a yes/no condition.
	Noul *NoulQuestion
	// Score rates state along an ordered, descriptive rubric.
	Score *ScoreQuestion
}

// ChoiceQuestion is a "pick one of" primitive.
type ChoiceQuestion struct {
	Instructions any
	// Criteria maps option -> description (string or nil).
	Criteria map[string]any
}

// NoulQuestion is a yes/no primitive.
type NoulQuestion struct {
	Instructions any
	// Criteria optionally describes what yes (True) and no (False) mean.
	Criteria *NoulCriteria
}

// NoulCriteria refines yes/no semantics.
type NoulCriteria struct {
	True  string
	False string
}

// ScoreQuestion is an ordered-rubric primitive.
type ScoreQuestion struct {
	Instructions any
	// Levels is the ordered rubric (at least two entries).
	Levels []string
}

// Answer is the normalized result of one question.
type Answer struct {
	Type          string
	Choice        string
	Probabilities map[string]float64
	Confidence    *float64
	Noul          *float64
	Score         *float64
	Legend        map[string]string
}

// Usage reports token accounting for one evaluation call.
type Usage struct {
	InputTokens  int
	OutputTokens int
}

// Evaluate evaluates state against the given questions in a single API call
// (all questions run in parallel server-side). Returns the answers keyed by
// question id, plus token usage.
func (c *Client) Evaluate(ctx context.Context, state any, questions map[string]Question) (map[string]Answer, *Usage, error) {
	if c == nil {
		return nil, nil, errors.New("jev: nil client")
	}
	if len(questions) == 0 {
		return nil, nil, errors.New("jev: no questions")
	}
	qs := make(map[string]question, len(questions))
	for id, q := range questions {
		wire, err := q.toWire()
		if err != nil {
			return nil, nil, fmt.Errorf("jev: question %q: %w", id, err)
		}
		qs[id] = wire
	}

	body, err := json.Marshal(request{State: state, Model: c.cfg.Model, Questions: qs})
	if err != nil {
		return nil, nil, fmt.Errorf("jev: encode request: %w", err)
	}

	reqCtx := ctx
	if c.cfg.Timeout > 0 {
		var cancel context.CancelFunc
		reqCtx, cancel = context.WithTimeout(ctx, c.cfg.Timeout)
		defer cancel()
	}
	httpReq, err := http.NewRequestWithContext(reqCtx, http.MethodPost, c.cfg.Endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, nil, &RequestError{Kind: "http", Reason: err.Error()}
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if c.cfg.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
	}

	resp, err := c.client.Do(httpReq)
	if err != nil {
		var kind = "http"
		if errors.Is(err, context.DeadlineExceeded) {
			kind = "timeout"
		}
		return nil, nil, &RequestError{Kind: kind, Reason: err.Error()}
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return nil, nil, &RequestError{Kind: "unauthorized", Status: resp.StatusCode, Reason: "missing or invalid API key"}
	}
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == 529 {
		return nil, nil, &RequestError{Kind: "rate_limited", Status: resp.StatusCode, Reason: "rate limit or overload"}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		reason := truncate(readErrorBody(resp), 256)
		return nil, nil, &RequestError{Kind: "status", Status: resp.StatusCode, Reason: reason}
	}

	var parsed response
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, nil, &RequestError{Kind: "malformed", Reason: "decode response: " + err.Error()}
	}

	answers := make(map[string]Answer, len(parsed.Answers))
	for id, a := range parsed.Answers {
		answers[id] = a.toAnswer()
	}
	var usage *Usage
	if parsed.Usage != nil {
		usage = &Usage{InputTokens: parsed.Usage.InputTokens, OutputTokens: parsed.Usage.OutputTokens}
	}
	return answers, usage, nil
}

func (q Question) toWire() (question, error) {
	switch {
	case q.Choice != nil:
		if len(q.Choice.Criteria) == 0 {
			return question{}, errors.New("choice requires at least one criterion")
		}
		return question{
			Type:         "choice",
			Instructions: q.Choice.Instructions,
			Criteria:     q.Choice.Criteria,
		}, nil
	case q.Noul != nil:
		w := question{Type: "noul", Instructions: q.Noul.Instructions}
		if q.Noul.Criteria != nil {
			crit := map[string]string{}
			if q.Noul.Criteria.True != "" {
				crit["true"] = q.Noul.Criteria.True
			}
			if q.Noul.Criteria.False != "" {
				crit["false"] = q.Noul.Criteria.False
			}
			if len(crit) > 0 {
				w.Criteria = crit
			}
		}
		return w, nil
	case q.Score != nil:
		if len(q.Score.Levels) < 2 {
			return question{}, errors.New("score requires at least two levels")
		}
		levels := make([]string, 0, len(q.Score.Levels))
		levels = append(levels, q.Score.Levels...)
		return question{
			Type:         "score",
			Instructions: q.Score.Instructions,
			Criteria:     levels,
		}, nil
	default:
		return question{}, errors.New("no primitive set (choice|noul|score)")
	}
}

func (a answerData) toAnswer() Answer {
	// answerData and Answer share identical field names and types, so a
	// type conversion maps the values 1:1.
	return Answer(a)
}

func readErrorBody(resp *http.Response) string {
	if resp.Body == nil {
		return ""
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// validateEndpoint normalizes/validates the endpoint URL early so
// misconfiguration surfaces at startup, not per request.
func validateEndpoint(endpoint string) error {
	if endpoint == "" {
		return nil
	}
	u, err := url.Parse(endpoint)
	if err != nil {
		return fmt.Errorf("jev: invalid endpoint %q: %w", endpoint, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("jev: endpoint must use http(s) scheme: %q", endpoint)
	}
	return nil
}
