package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// OllamaClient is the default Client implementation: it talks to a LOCAL
// Ollama server over its /api/generate HTTP endpoint. It is the only backend
// in v1 (P0-498-1). It holds no cross-call state -- each Generate is an
// independent, stateless round-trip, so no tenant data is retained between
// calls (I-mitigation).
//
// The client NEVER egresses outside cfg.OllamaEndpoint (AC-2): the request URL
// is built from the configured base only, never from request data.
type OllamaClient struct {
	cfg  Config
	http *http.Client
}

// NewOllamaClient builds an OllamaClient from cfg. The underlying http.Client
// carries cfg.RequestTimeout as a backstop; the per-request context deadline
// (derived from GenerateRequest.Timeout, which is mandatory) is the real cap.
func NewOllamaClient(cfg Config) *OllamaClient {
	return &OllamaClient{
		cfg: cfg,
		http: &http.Client{
			Timeout: cfg.RequestTimeout,
		},
	}
}

var _ Client = (*OllamaClient)(nil)

// ollamaGenerateRequest is the subset of Ollama's /api/generate body we use.
// stream=false yields a single JSON object. options.num_predict is Ollama's
// max-output-tokens knob -- we bind the mandatory MaxTokens here so the
// inference itself is bounded, not just the wall clock (P0-498-6).
type ollamaGenerateRequest struct {
	Model   string             `json:"model"`
	System  string             `json:"system"`
	Prompt  string             `json:"prompt"`
	Stream  bool               `json:"stream"`
	Options ollamaGenerateOpts `json:"options"`
}

type ollamaGenerateOpts struct {
	NumPredict int `json:"num_predict"`
}

// ollamaGenerateResponse is the subset of Ollama's response we read.
type ollamaGenerateResponse struct {
	Model    string `json:"model"`
	Response string `json:"response"`
}

// Generate runs one bounded local inference. Order of operations is
// load-bearing for the threat model:
//
//  1. validate() -- rejects over-cap / malformed requests BEFORE any IO
//     (P0-498-6: never launch an unbounded job).
//  2. derive a context deadline from req.Timeout (mandatory cap).
//  3. POST to the configured local endpoint only (no data-derived URL).
//  4. return the raw response text as OPAQUE GenerateResult.Text -- the
//     substrate never executes/interpolates it (P0-498-7).
func (c *OllamaClient) Generate(ctx context.Context, req GenerateRequest) (GenerateResult, error) {
	if err := req.validate(); err != nil {
		return GenerateResult{}, err
	}

	model := req.ModelID
	if model == "" {
		model = c.cfg.DefaultModel
	}

	// Mandatory per-request deadline (P0-498-6). Derived from the validated,
	// positive req.Timeout.
	ctx, cancel := context.WithTimeout(ctx, req.Timeout)
	defer cancel()

	// The assembled context is recorded on the audit row by the caller; for
	// the prompt we append a compact, deterministic rendering so the model
	// sees it. It is opaque text either way.
	prompt := renderContextPrompt(req.Context)

	body, err := json.Marshal(ollamaGenerateRequest{
		Model:   model,
		System:  req.SystemPrompt,
		Prompt:  prompt,
		Stream:  false,
		Options: ollamaGenerateOpts{NumPredict: req.MaxTokens},
	})
	if err != nil {
		return GenerateResult{}, fmt.Errorf("%w: marshal request: %v", ErrBackend, err)
	}

	url := strings.TrimRight(c.cfg.OllamaEndpoint, "/") + "/api/generate"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return GenerateResult{}, fmt.Errorf("%w: build http request: %v", ErrBackend, err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(httpReq)
	if err != nil {
		// A deadline-exceeded error becomes ErrTimeout so callers can
		// distinguish a slow/runaway generation from a transport fault.
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return GenerateResult{}, fmt.Errorf("%w after %s", ErrTimeout, req.Timeout)
		}
		return GenerateResult{}, fmt.Errorf("%w: POST %s: %v", ErrBackend, url, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		// Bound the error body read so a misbehaving backend can't make the
		// error path itself unbounded.
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return GenerateResult{}, fmt.Errorf("%w: ollama returned %d: %s", ErrBackend, resp.StatusCode, strings.TrimSpace(string(snippet)))
	}

	var decoded ollamaGenerateResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return GenerateResult{}, fmt.Errorf("%w: decode response: %v", ErrBackend, err)
	}

	resolvedModel := decoded.Model
	if resolvedModel == "" {
		resolvedModel = model
	}

	return GenerateResult{
		Text:          decoded.Response,
		ModelName:     resolvedModel,
		ModelVersion:  modelVersionTag(resolvedModel),
		ModelProvider: "ollama-local",
	}, nil
}

// renderContextPrompt deterministically renders the assembled context into a
// single opaque prompt string. Keys are emitted in a stable (sorted) order so
// the same context produces the same prompt -- important for the forensic
// audit record to be reproducible. The values are never interpreted.
func renderContextPrompt(ctxInputs map[string]any) string {
	if len(ctxInputs) == 0 {
		return ""
	}
	// json.Marshal of a map sorts keys, giving a deterministic rendering.
	b, err := json.Marshal(ctxInputs)
	if err != nil {
		return ""
	}
	return string(b)
}

// modelVersionTag extracts the version/quant tag from an Ollama model id
// ("llama3.1:8b-instruct-q5" -> "8b-instruct-q5"). When no tag is present the
// whole id is used so the version column is never empty (the DB CHECK
// ai_generations_provenance_nonempty requires non-empty).
func modelVersionTag(modelID string) string {
	if i := strings.Index(modelID, ":"); i >= 0 && i+1 < len(modelID) {
		return modelID[i+1:]
	}
	if modelID == "" {
		return "unknown"
	}
	return modelID
}
