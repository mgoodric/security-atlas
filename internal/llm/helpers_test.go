// Pure-Go unit tests for the internal/llm substrate (slice 498, slice-353 Q-2
// helpers_test.go convention): no Postgres, no build tag, fast t.Parallel()
// table tests over the pure-Go branches -- request validation + cap rejection,
// the stub client, the enforcement guard (positive + negative), config
// defaults, and the deterministic context renderer. The integration tier
// (integration_test.go) is the safety net for the DB-layer guarantees.
package llm

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func validReq() GenerateRequest {
	return GenerateRequest{
		Surface:       SurfaceQuestionnaire,
		PromptVersion: "v1",
		SystemPrompt:  "you are a compliance assistant",
		MaxTokens:     256,
		Timeout:       5 * time.Second,
	}
}

func TestGenerateRequest_Validate(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		mutate  func(*GenerateRequest)
		wantErr error
	}{
		{"valid", func(*GenerateRequest) {}, nil},
		{"unknown surface", func(r *GenerateRequest) { r.Surface = "bogus" }, ErrInvalidRequest},
		{"empty surface", func(r *GenerateRequest) { r.Surface = "" }, ErrInvalidRequest},
		{"missing prompt_version", func(r *GenerateRequest) { r.PromptVersion = "" }, ErrInvalidRequest},
		{"missing system_prompt", func(r *GenerateRequest) { r.SystemPrompt = "" }, ErrInvalidRequest},
		{"zero timeout", func(r *GenerateRequest) { r.Timeout = 0 }, ErrInvalidRequest},
		{"negative timeout", func(r *GenerateRequest) { r.Timeout = -1 }, ErrInvalidRequest},
		{"zero max tokens", func(r *GenerateRequest) { r.MaxTokens = 0 }, ErrInvalidRequest},
		{"negative max tokens", func(r *GenerateRequest) { r.MaxTokens = -5 }, ErrInvalidRequest},
		{"over cap max tokens", func(r *GenerateRequest) { r.MaxTokens = MaxTokenBudget + 1 }, ErrTokenBudgetExceeded},
		{"at cap max tokens", func(r *GenerateRequest) { r.MaxTokens = MaxTokenBudget }, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			req := validReq()
			tt.mutate(&req)
			err := req.validate()
			if tt.wantErr == nil {
				if err != nil {
					t.Fatalf("validate() = %v, want nil", err)
				}
				return
			}
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("validate() = %v, want errors.Is %v", err, tt.wantErr)
			}
		})
	}
}

func TestStubClient_Generate(t *testing.T) {
	t.Parallel()

	t.Run("returns fixed result", func(t *testing.T) {
		t.Parallel()
		c := NewStubClient()
		res, err := c.Generate(context.Background(), validReq())
		if err != nil {
			t.Fatalf("Generate() error = %v", err)
		}
		if res.Text == "" || res.ModelProvider != "stub" {
			t.Fatalf("unexpected stub result: %+v", res)
		}
	})

	t.Run("validates before returning (over-cap rejected)", func(t *testing.T) {
		t.Parallel()
		c := NewStubClient()
		req := validReq()
		req.MaxTokens = MaxTokenBudget + 1
		_, err := c.Generate(context.Background(), req)
		if !errors.Is(err, ErrTokenBudgetExceeded) {
			t.Fatalf("Generate() = %v, want ErrTokenBudgetExceeded", err)
		}
	})

	t.Run("custom result respected", func(t *testing.T) {
		t.Parallel()
		c := &StubClient{Result: GenerateResult{Text: "hello", ModelName: "m", ModelVersion: "1", ModelProvider: "stub"}}
		res, err := c.Generate(context.Background(), validReq())
		if err != nil || res.Text != "hello" {
			t.Fatalf("Generate() = %+v, %v", res, err)
		}
	})

	t.Run("configured error returned after validation", func(t *testing.T) {
		t.Parallel()
		sentinel := errors.New("boom")
		c := &StubClient{Err: sentinel}
		_, err := c.Generate(context.Background(), validReq())
		if !errors.Is(err, sentinel) {
			t.Fatalf("Generate() = %v, want sentinel", err)
		}
	})
}

func TestEnforceApproval(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		state   ApprovalState
		wantErr bool
	}{
		// The single forbidden shape.
		{"ai+approved+no approver", ApprovalState{AIAssisted: true, HumanApproved: true, HumanApprover: ""}, true},
		{"ai+approved+blank approver", ApprovalState{AIAssisted: true, HumanApproved: true, HumanApprover: "   "}, true},
		// Allowed shapes.
		{"ai+approved+approver", ApprovalState{AIAssisted: true, HumanApproved: true, HumanApprover: "user-7"}, false},
		{"ai+not approved", ApprovalState{AIAssisted: true, HumanApproved: false, HumanApprover: ""}, false},
		{"not ai+approved+no approver", ApprovalState{AIAssisted: false, HumanApproved: true, HumanApprover: ""}, false},
		{"not ai+not approved", ApprovalState{AIAssisted: false, HumanApproved: false, HumanApprover: ""}, false},
		{"zero value", ApprovalState{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := EnforceApproval(tt.state)
			if tt.wantErr {
				if !errors.Is(err, ErrApproverRequired) {
					t.Fatalf("EnforceApproval(%+v) = %v, want ErrApproverRequired", tt.state, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("EnforceApproval(%+v) = %v, want nil", tt.state, err)
			}
		})
	}
}

func TestValidSurface(t *testing.T) {
	t.Parallel()
	for _, s := range []Surface{
		SurfaceQuestionnaire, SurfaceBoardNarrative, SurfaceGapExplanation, SurfaceChecklist, SurfaceSummary,
	} {
		if !ValidSurface(s) {
			t.Errorf("ValidSurface(%q) = false, want true", s)
		}
	}
	for _, s := range []Surface{"", "bogus", "Questionnaire"} {
		if ValidSurface(s) {
			t.Errorf("ValidSurface(%q) = true, want false", s)
		}
	}
}

func TestConfigFromEnv_Defaults(t *testing.T) {
	// Not parallel: mutates process env.
	t.Setenv("ATLAS_LLM_OLLAMA_ENDPOINT", "")
	t.Setenv("ATLAS_LLM_DEFAULT_MODEL", "")
	t.Setenv("ATLAS_LLM_REQUEST_TIMEOUT", "")
	cfg := ConfigFromEnv()
	if cfg.OllamaEndpoint != DefaultOllamaEndpoint {
		t.Errorf("OllamaEndpoint = %q, want default", cfg.OllamaEndpoint)
	}
	if cfg.DefaultModel != DefaultModelID {
		t.Errorf("DefaultModel = %q, want %q", cfg.DefaultModel, DefaultModelID)
	}
	if cfg.RequestTimeout != DefaultRequestTimeout {
		t.Errorf("RequestTimeout = %v, want %v", cfg.RequestTimeout, DefaultRequestTimeout)
	}
}

func TestConfigFromEnv_Overrides(t *testing.T) {
	t.Setenv("ATLAS_LLM_OLLAMA_ENDPOINT", "http://127.0.0.1:9999")
	t.Setenv("ATLAS_LLM_DEFAULT_MODEL", "llama3.1:70b")
	t.Setenv("ATLAS_LLM_REQUEST_TIMEOUT", "30s")
	cfg := ConfigFromEnv()
	if cfg.OllamaEndpoint != "http://127.0.0.1:9999" {
		t.Errorf("OllamaEndpoint = %q", cfg.OllamaEndpoint)
	}
	if cfg.DefaultModel != "llama3.1:70b" {
		t.Errorf("DefaultModel = %q", cfg.DefaultModel)
	}
	if cfg.RequestTimeout != 30*time.Second {
		t.Errorf("RequestTimeout = %v", cfg.RequestTimeout)
	}
}

func TestConfigFromEnv_BadTimeoutFallsBackToDefault(t *testing.T) {
	t.Setenv("ATLAS_LLM_REQUEST_TIMEOUT", "not-a-duration")
	cfg := ConfigFromEnv()
	if cfg.RequestTimeout != DefaultRequestTimeout {
		t.Errorf("bad timeout: RequestTimeout = %v, want default", cfg.RequestTimeout)
	}
}

func TestModelVersionTag(t *testing.T) {
	t.Parallel()
	tests := map[string]string{
		"llama3.1:8b-instruct-q5": "8b-instruct-q5",
		"llama3.1":                "llama3.1",
		"":                        "unknown",
		"model:":                  "model:", // trailing colon, no tag -> whole id
	}
	for in, want := range tests {
		if got := modelVersionTag(in); got != want {
			t.Errorf("modelVersionTag(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRenderContextPrompt_Deterministic(t *testing.T) {
	t.Parallel()
	if got := renderContextPrompt(nil); got != "" {
		t.Errorf("nil context = %q, want empty", got)
	}
	if got := renderContextPrompt(map[string]any{}); got != "" {
		t.Errorf("empty context = %q, want empty", got)
	}
	ctxInputs := map[string]any{"b": "two", "a": "one"}
	first := renderContextPrompt(ctxInputs)
	second := renderContextPrompt(ctxInputs)
	if first != second {
		t.Errorf("render not deterministic: %q vs %q", first, second)
	}
	// Keys must be sorted (json.Marshal of a map sorts keys).
	if !strings.HasPrefix(first, `{"a":`) {
		t.Errorf("expected sorted keys, got %q", first)
	}
}

func TestGeneration_Validate(t *testing.T) {
	t.Parallel()
	base := Generation{
		Surface:       SurfaceSummary,
		PromptVersion: "v1",
		ModelName:     "llama3.1",
		ModelVersion:  "8b",
		ModelProvider: "ollama-local",
		SystemPrompt:  "sys",
	}
	if err := base.validate(); err != nil {
		t.Fatalf("valid generation rejected: %v", err)
	}
	mutators := map[string]func(*Generation){
		"bad surface":       func(g *Generation) { g.Surface = "x" },
		"no prompt version": func(g *Generation) { g.PromptVersion = "" },
		"no model name":     func(g *Generation) { g.ModelName = "" },
		"no model version":  func(g *Generation) { g.ModelVersion = "" },
		"no model provider": func(g *Generation) { g.ModelProvider = "" },
		"no system prompt":  func(g *Generation) { g.SystemPrompt = "" },
	}
	for name, m := range mutators {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			g := base
			m(&g)
			if err := g.validate(); !errors.Is(err, ErrInvalidGeneration) {
				t.Fatalf("validate() = %v, want ErrInvalidGeneration", err)
			}
		})
	}
}
