package cloud

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/mgoodric/security-atlas/internal/llm"
)

// httpDoer is the injectable HTTP seam: *http.Client satisfies it, and tests
// inject an httptest-backed doer so the cloud request shape (URL, auth header,
// body, timeout, error mapping) is unit-tested WITHOUT a live cloud call (CI has
// no cloud keys). Mirrors the connector raw-HTTP-behind-an-interface pattern.
type httpDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// Adapter is a cloud llm.Client for one provider. The endpoint is fixed per
// provider (P0-499-3 — no operator URL); the API key is supplied per-Generate
// (decrypted from the tenant's config by the Router) so the adapter holds no
// tenant secret across calls and no cross-tenant state.
type Adapter struct {
	provider Provider
	endpoint string
	doer     httpDoer
	apiKey   Secret
	// model overrides the per-provider default model when a request leaves
	// ModelID empty. Empty => the provider's documented default.
	defaultModel string
}

var _ llm.Client = (*Adapter)(nil)

// Default cloud endpoints (the providers' official APIs — hard-coded, never
// operator-supplied). Exported as consts so the unit tests assert the adapter
// targets the real endpoint shape.
const (
	AnthropicEndpoint = "https://api.anthropic.com/v1/messages"
	OpenAIEndpoint    = "https://api.openai.com/v1/chat/completions"
	// BedrockEndpoint is a template; {region}/{model} are filled per request.
	// The Adapter scaffolds Bedrock (see decisions log D6) — the SigV4 signer
	// is the follow-on; the request shape + error mapping are exercised here.
	BedrockEndpointTemplate = "https://bedrock-runtime.us-east-1.amazonaws.com/model/%s/invoke"
)

// Default cloud models per provider when a request leaves ModelID empty.
const (
	defaultAnthropicModel = "claude-3-5-sonnet-latest"
	defaultOpenAIModel    = "gpt-4o-mini"
	defaultBedrockModel   = "anthropic.claude-3-5-sonnet-20240620-v1:0"
)

// NewAdapter builds a cloud Adapter for provider with the given API key and
// HTTP doer. The doer is mandatory (tests inject httptest; production passes an
// *http.Client whose Timeout is a backstop — the real cap is the per-request
// context deadline derived from GenerateRequest.Timeout). Returns an error for
// a non-cloud or unknown provider.
func NewAdapter(provider Provider, apiKey Secret, doer httpDoer) (*Adapter, error) {
	if !provider.IsCloud() {
		return nil, fmt.Errorf("cloud: NewAdapter requires a cloud provider, got %q", provider)
	}
	if apiKey.IsZero() {
		return nil, fmt.Errorf("cloud: NewAdapter requires a non-empty api key for %q", provider)
	}
	if doer == nil {
		return nil, errors.New("cloud: NewAdapter requires an http doer")
	}
	a := &Adapter{provider: provider, doer: doer, apiKey: apiKey}
	switch provider {
	case ProviderAnthropic:
		a.endpoint = AnthropicEndpoint
		a.defaultModel = defaultAnthropicModel
	case ProviderOpenAI:
		a.endpoint = OpenAIEndpoint
		a.defaultModel = defaultOpenAIModel
	case ProviderBedrock:
		a.endpoint = fmt.Sprintf(BedrockEndpointTemplate, defaultBedrockModel)
		a.defaultModel = defaultBedrockModel
	default:
		return nil, fmt.Errorf("cloud: no adapter for provider %q", provider)
	}
	return a, nil
}

// Generate runs one bounded cloud inference. Order of operations mirrors the
// slice-498 OllamaClient (the contract is identical across backends):
//
//  1. req.validate() via the shared contract — rejects over-cap / malformed
//     requests BEFORE any IO (the mandatory token + timeout caps, P0-499 D-mitigation).
//     We re-run the public-equivalent checks here because validate() is
//     unexported; the Router has already validated, but a direct Adapter caller
//     must be bounded too.
//  2. derive a context deadline from req.Timeout (mandatory cap, AC-6).
//  3. POST to the FIXED provider endpoint (never a data-derived URL).
//  4. carry the API key in the provider's auth header (Reveal()ed only here).
//  5. return the raw response text as OPAQUE GenerateResult.Text with
//     ModelProvider = this provider (AC-5 audit provenance).
func (a *Adapter) Generate(ctx context.Context, req llm.GenerateRequest) (llm.GenerateResult, error) {
	if err := req.Validate(); err != nil {
		return llm.GenerateResult{}, err
	}

	model := req.ModelID
	if model == "" {
		model = a.defaultModel
	}

	// Mandatory per-request deadline (AC-6): a cloud outage/slow response never
	// hangs unbounded.
	ctx, cancel := context.WithTimeout(ctx, req.Timeout)
	defer cancel()

	body, err := a.encodeBody(model, req)
	if err != nil {
		return llm.GenerateResult{}, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, a.endpoint, bytes.NewReader(body))
	if err != nil {
		return llm.GenerateResult{}, fmt.Errorf("%w: build http request: %v", llm.ErrBackend, err)
	}
	a.setAuthHeaders(httpReq)

	resp, err := a.doer.Do(httpReq)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return llm.GenerateResult{}, fmt.Errorf("%w after %s", llm.ErrTimeout, req.Timeout)
		}
		return llm.GenerateResult{}, fmt.Errorf("%w: POST %s (%s): %v", llm.ErrBackend, a.endpoint, a.provider, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		// Bound the error-body read so a misbehaving backend cannot make the
		// error path itself unbounded.
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return llm.GenerateResult{}, fmt.Errorf("%w: %s returned %d: %s", llm.ErrBackend, a.provider, resp.StatusCode, strings.TrimSpace(string(snippet)))
	}

	text, resolvedModel, err := a.decodeBody(resp.Body, model)
	if err != nil {
		return llm.GenerateResult{}, err
	}

	return llm.GenerateResult{
		Text:          text,
		ModelName:     resolvedModel,
		ModelVersion:  llm.ModelVersionTag(resolvedModel),
		ModelProvider: string(a.provider),
	}, nil
}

// setAuthHeaders carries the API key in the provider's expected auth header.
// The key is Reveal()ed ONLY here, at the transport boundary, and never logged.
func (a *Adapter) setAuthHeaders(r *http.Request) {
	r.Header.Set("Content-Type", "application/json")
	switch a.provider {
	case ProviderAnthropic:
		r.Header.Set("x-api-key", a.apiKey.Reveal())
		r.Header.Set("anthropic-version", "2023-06-01")
	case ProviderOpenAI:
		r.Header.Set("Authorization", "Bearer "+a.apiKey.Reveal())
	case ProviderBedrock:
		// Bedrock uses SigV4 (follow-on, decisions log D6). The key is carried
		// as a bearer for the request-shape test; the SigV4 signer replaces
		// this when Bedrock graduates from scaffold to fully-built.
		r.Header.Set("Authorization", "Bearer "+a.apiKey.Reveal())
	}
}

// --- request/response codecs (per provider) ---

func (a *Adapter) encodeBody(model string, req llm.GenerateRequest) ([]byte, error) {
	prompt := llm.RenderContextPrompt(req.Context)
	switch a.provider {
	case ProviderAnthropic, ProviderBedrock:
		return json.Marshal(anthropicRequest{
			Model:     model,
			System:    req.SystemPrompt,
			MaxTokens: req.MaxTokens,
			Messages:  []anthropicMessage{{Role: "user", Content: prompt}},
		})
	case ProviderOpenAI:
		return json.Marshal(openAIRequest{
			Model:     model,
			MaxTokens: req.MaxTokens,
			Messages: []openAIMessage{
				{Role: "system", Content: req.SystemPrompt},
				{Role: "user", Content: prompt},
			},
		})
	default:
		return nil, fmt.Errorf("%w: no encoder for provider %q", llm.ErrBackend, a.provider)
	}
}

func (a *Adapter) decodeBody(r io.Reader, fallbackModel string) (text, model string, err error) {
	switch a.provider {
	case ProviderAnthropic, ProviderBedrock:
		var decoded anthropicResponse
		if derr := json.NewDecoder(r).Decode(&decoded); derr != nil {
			return "", "", fmt.Errorf("%w: decode %s response: %v", llm.ErrBackend, a.provider, derr)
		}
		var sb strings.Builder
		for _, blk := range decoded.Content {
			if blk.Type == "text" {
				sb.WriteString(blk.Text)
			}
		}
		model = decoded.Model
		if model == "" {
			model = fallbackModel
		}
		return sb.String(), model, nil
	case ProviderOpenAI:
		var decoded openAIResponse
		if derr := json.NewDecoder(r).Decode(&decoded); derr != nil {
			return "", "", fmt.Errorf("%w: decode openai response: %v", llm.ErrBackend, derr)
		}
		model = decoded.Model
		if model == "" {
			model = fallbackModel
		}
		if len(decoded.Choices) == 0 {
			return "", model, nil
		}
		return decoded.Choices[0].Message.Content, model, nil
	default:
		return "", "", fmt.Errorf("%w: no decoder for provider %q", llm.ErrBackend, a.provider)
	}
}

// --- wire shapes (the subset we use) ---

type anthropicMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}
type anthropicRequest struct {
	Model     string             `json:"model"`
	System    string             `json:"system"`
	MaxTokens int                `json:"max_tokens"`
	Messages  []anthropicMessage `json:"messages"`
}
type anthropicContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}
type anthropicResponse struct {
	Model   string                  `json:"model"`
	Content []anthropicContentBlock `json:"content"`
}

type openAIMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}
type openAIRequest struct {
	Model     string          `json:"model"`
	MaxTokens int             `json:"max_tokens"`
	Messages  []openAIMessage `json:"messages"`
}
type openAIChoice struct {
	Message struct {
		Content string `json:"content"`
	} `json:"message"`
}
type openAIResponse struct {
	Model   string         `json:"model"`
	Choices []openAIChoice `json:"choices"`
}
