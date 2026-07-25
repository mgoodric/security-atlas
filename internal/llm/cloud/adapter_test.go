package cloud

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mgoodric/security-atlas/internal/llm"
)

// fakeProviderKey is an obviously-fake provider key. It is deliberately NOT a
// real Anthropic/OpenAI/AWS shape (no sk-ant-/sk-/AKIA prefix) so GitGuardian
// does not flag it even though it appears in a test.
const fakeProviderKey = "fake-provider-key-for-tests-000"

func validReq() llm.GenerateRequest {
	return llm.GenerateRequest{
		Surface:       llm.SurfaceQuestionnaire,
		PromptVersion: "v1",
		SystemPrompt:  "you are a compliance assistant",
		Context:       map[string]any{"evidence_id": "ev-1"},
		MaxTokens:     256,
		Timeout:       5 * time.Second,
	}
}

// adapterPointedAt builds an Adapter whose injected doer rewrites the request to
// hit the test server (so we keep the real endpoint constant on the request the
// server records, but actually dial the local httptest server).
func adapterPointedAt(t *testing.T, provider Provider, srv *httptest.Server) *Adapter {
	t.Helper()
	a, err := NewAdapter(provider, Secret(fakeProviderKey), redirectDoer{base: srv.URL, inner: srv.Client()})
	if err != nil {
		t.Fatalf("NewAdapter(%q): %v", provider, err)
	}
	return a
}

// redirectDoer sends the request to the httptest server's host while preserving
// the original path + headers + body, so the test asserts the real request
// shape against a local server.
type redirectDoer struct {
	base  string
	inner *http.Client
}

func (d redirectDoer) Do(req *http.Request) (*http.Response, error) {
	// Repoint host to the test server, keep path/query.
	target := d.base + req.URL.Path
	if req.URL.RawQuery != "" {
		target += "?" + req.URL.RawQuery
	}
	out, err := http.NewRequestWithContext(req.Context(), req.Method, target, req.Body)
	if err != nil {
		return nil, err
	}
	out.Header = req.Header.Clone()
	return d.inner.Do(out)
}

func TestAdapter_Anthropic_RequestShapeAndAuthHeader(t *testing.T) {
	t.Parallel()
	var gotKey, gotVersion, gotPath, gotModel, gotSystem string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("x-api-key")
		gotVersion = r.Header.Get("anthropic-version")
		gotPath = r.URL.Path
		var body anthropicRequest
		_ = json.NewDecoder(r.Body).Decode(&body)
		gotModel = body.Model
		gotSystem = body.System
		_ = json.NewEncoder(w).Encode(anthropicResponse{
			Model:   "claude-3-5-sonnet-20241022",
			Content: []anthropicContentBlock{{Type: "text", Text: "drafted answer"}},
		})
	}))
	defer srv.Close()

	a := adapterPointedAt(t, ProviderAnthropic, srv)
	res, err := a.Generate(context.Background(), validReq())
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	// AC: auth header carries the key.
	if gotKey != fakeProviderKey {
		t.Errorf("x-api-key = %q, want the provider key", gotKey)
	}
	if gotVersion == "" {
		t.Error("anthropic-version header missing")
	}
	if !strings.HasSuffix(gotPath, "/v1/messages") {
		t.Errorf("path = %q, want .../v1/messages", gotPath)
	}
	if gotModel == "" {
		t.Error("model not sent")
	}
	if gotSystem != "you are a compliance assistant" {
		t.Errorf("system prompt = %q", gotSystem)
	}
	// AC-5: provider provenance recorded as the actual provider.
	if res.ModelProvider != string(ProviderAnthropic) {
		t.Errorf("ModelProvider = %q, want anthropic", res.ModelProvider)
	}
	if res.Text != "drafted answer" {
		t.Errorf("Text = %q", res.Text)
	}
	if res.ModelName == "" || res.ModelVersion == "" {
		t.Error("model provenance not populated")
	}
}

func TestAdapter_OpenAI_RequestShapeAndAuthHeader(t *testing.T) {
	t.Parallel()
	var gotAuth, gotPath string
	var gotMessages int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		var body openAIRequest
		_ = json.NewDecoder(r.Body).Decode(&body)
		gotMessages = len(body.Messages)
		resp := openAIResponse{Model: "gpt-4o-mini", Choices: make([]openAIChoice, 1)}
		resp.Choices[0].Message.Content = "openai draft"
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	a := adapterPointedAt(t, ProviderOpenAI, srv)
	res, err := a.Generate(context.Background(), validReq())
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if gotAuth != "Bearer "+fakeProviderKey {
		t.Errorf("Authorization = %q, want Bearer <key>", gotAuth)
	}
	if !strings.HasSuffix(gotPath, "/chat/completions") {
		t.Errorf("path = %q", gotPath)
	}
	if gotMessages != 2 {
		t.Errorf("messages = %d, want 2 (system+user)", gotMessages)
	}
	if res.ModelProvider != string(ProviderOpenAI) {
		t.Errorf("ModelProvider = %q", res.ModelProvider)
	}
	if res.Text != "openai draft" {
		t.Errorf("Text = %q", res.Text)
	}
}

func TestAdapter_Bedrock_Scaffold(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(anthropicResponse{
			Model:   "anthropic.claude-3-5-sonnet",
			Content: []anthropicContentBlock{{Type: "text", Text: "bedrock draft"}},
		})
	}))
	defer srv.Close()
	a := adapterPointedAt(t, ProviderBedrock, srv)
	res, err := a.Generate(context.Background(), validReq())
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if res.ModelProvider != string(ProviderBedrock) {
		t.Errorf("ModelProvider = %q", res.ModelProvider)
	}
	if res.Text != "bedrock draft" {
		t.Errorf("Text = %q", res.Text)
	}
}

func TestAdapter_Non200MapsToErrBackend(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":"bad key"}`)
	}))
	defer srv.Close()
	a := adapterPointedAt(t, ProviderAnthropic, srv)
	_, err := a.Generate(context.Background(), validReq())
	if !errors.Is(err, llm.ErrBackend) {
		t.Fatalf("err = %v, want ErrBackend", err)
	}
}

func TestAdapter_TimeoutMapsToErrTimeout(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		_, _ = io.WriteString(w, `{}`)
	}))
	defer srv.Close()
	a := adapterPointedAt(t, ProviderAnthropic, srv)
	req := validReq()
	req.Timeout = 20 * time.Millisecond // shorter than the server sleep
	_, err := a.Generate(context.Background(), req)
	if !errors.Is(err, llm.ErrTimeout) {
		t.Fatalf("err = %v, want ErrTimeout", err)
	}
}

func TestAdapter_RejectsOverCapRequestBeforeIO(t *testing.T) {
	t.Parallel()
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	defer srv.Close()
	a := adapterPointedAt(t, ProviderAnthropic, srv)
	req := validReq()
	req.MaxTokens = llm.MaxTokenBudget + 1 // over cap
	_, err := a.Generate(context.Background(), req)
	if !errors.Is(err, llm.ErrTokenBudgetExceeded) {
		t.Fatalf("err = %v, want ErrTokenBudgetExceeded", err)
	}
	if called {
		t.Fatal("over-cap request reached the network (no pre-IO rejection)")
	}
}

func TestNewAdapter_RejectsLocalAndKeyless(t *testing.T) {
	t.Parallel()
	if _, err := NewAdapter(ProviderLocalOllama, Secret(fakeProviderKey), http.DefaultClient); err == nil {
		t.Error("NewAdapter accepted local-ollama")
	}
	if _, err := NewAdapter(ProviderAnthropic, Secret(""), http.DefaultClient); err == nil {
		t.Error("NewAdapter accepted an empty key")
	}
	if _, err := NewAdapter(ProviderAnthropic, Secret(fakeProviderKey), nil); err == nil {
		t.Error("NewAdapter accepted a nil doer")
	}
}
