package llm

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
)

// These tests exercise OllamaClient against an httptest server -- no live
// Ollama (none in CI). They cover the success round-trip, the over-cap
// pre-IO rejection, the non-2xx backend error, the timeout path, and that
// the request body carries the mandatory num_predict bound.

func TestOllamaClient_Generate_Success(t *testing.T) {
	t.Parallel()
	var gotBody ollamaGenerateRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/generate" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &gotBody)
		_ = json.NewEncoder(w).Encode(ollamaGenerateResponse{
			Model:    "llama3.1:8b-instruct-q5",
			Response: "drafted answer",
		})
	}))
	defer srv.Close()

	c := NewOllamaClient(Config{OllamaEndpoint: srv.URL, DefaultModel: DefaultModelID, RequestTimeout: 5 * time.Second})
	res, err := c.Generate(context.Background(), validReq())
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if res.Text != "drafted answer" {
		t.Errorf("Text = %q", res.Text)
	}
	if res.ModelProvider != "ollama-local" {
		t.Errorf("ModelProvider = %q, want ollama-local", res.ModelProvider)
	}
	if res.ModelName != "llama3.1:8b-instruct-q5" {
		t.Errorf("ModelName = %q", res.ModelName)
	}
	// The mandatory token budget must be bound into the inference request
	// (num_predict), not just the wall clock.
	if gotBody.Options.NumPredict != validReq().MaxTokens {
		t.Errorf("num_predict = %d, want %d", gotBody.Options.NumPredict, validReq().MaxTokens)
	}
	if gotBody.Stream {
		t.Error("expected stream=false")
	}
}

func TestOllamaClient_Generate_OverCapRejectedBeforeIO(t *testing.T) {
	t.Parallel()
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	defer srv.Close()

	c := NewOllamaClient(Config{OllamaEndpoint: srv.URL, DefaultModel: DefaultModelID, RequestTimeout: 5 * time.Second})
	req := validReq()
	req.MaxTokens = MaxTokenBudget + 1
	_, err := c.Generate(context.Background(), req)
	if !errors.Is(err, ErrTokenBudgetExceeded) {
		t.Fatalf("Generate() = %v, want ErrTokenBudgetExceeded", err)
	}
	if called {
		t.Error("server was called despite over-cap request; must reject before IO")
	}
}

func TestOllamaClient_Generate_BackendError(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "model not found", http.StatusNotFound)
	}))
	defer srv.Close()

	c := NewOllamaClient(Config{OllamaEndpoint: srv.URL, DefaultModel: DefaultModelID, RequestTimeout: 5 * time.Second})
	_, err := c.Generate(context.Background(), validReq())
	if !errors.Is(err, ErrBackend) {
		t.Fatalf("Generate() = %v, want ErrBackend", err)
	}
	if !strings.Contains(err.Error(), "404") {
		t.Errorf("error should mention status: %v", err)
	}
}

func TestOllamaClient_Generate_Timeout(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Sleep past the request timeout so the context deadline fires.
		time.Sleep(200 * time.Millisecond)
		_ = json.NewEncoder(w).Encode(ollamaGenerateResponse{Response: "late"})
	}))
	defer srv.Close()

	c := NewOllamaClient(Config{OllamaEndpoint: srv.URL, DefaultModel: DefaultModelID, RequestTimeout: 5 * time.Second})
	req := validReq()
	req.Timeout = 20 * time.Millisecond
	_, err := c.Generate(context.Background(), req)
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("Generate() = %v, want ErrTimeout", err)
	}
}

func TestOllamaClient_Generate_DefaultModelWhenUnset(t *testing.T) {
	t.Parallel()
	var gotModel string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var body ollamaGenerateRequest
		_ = json.Unmarshal(b, &body)
		gotModel = body.Model
		// Empty model in response -> client falls back to request model.
		_ = json.NewEncoder(w).Encode(ollamaGenerateResponse{Response: "ok"})
	}))
	defer srv.Close()

	c := NewOllamaClient(Config{OllamaEndpoint: srv.URL, DefaultModel: "llama3.1:8b-instruct-q5", RequestTimeout: 5 * time.Second})
	req := validReq()
	req.ModelID = "" // force default
	res, err := c.Generate(context.Background(), req)
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if gotModel != "llama3.1:8b-instruct-q5" {
		t.Errorf("server saw model %q, want default", gotModel)
	}
	if res.ModelName != "llama3.1:8b-instruct-q5" {
		t.Errorf("resolved ModelName = %q, want default fallback", res.ModelName)
	}
}
