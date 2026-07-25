package llm

import (
	"os"
	"time"
)

// Config holds the local-inference configuration. v1 is LOCAL OLLAMA ONLY
// (P0-498-1): the only backend is a local Ollama endpoint, the only default
// model is Llama 3.1 8B (slice-182 D5). Cloud routing + per-tenant opt-in +
// the visible banner are slice 499 and add a SECOND Config/Client behind the
// same interface -- they do not change this one.
type Config struct {
	// OllamaEndpoint is the base URL of the local Ollama server. Read from
	// ATLAS_LLM_OLLAMA_ENDPOINT; defaults to the Ollama loopback default. The
	// client never egresses outside this endpoint (AC-2).
	OllamaEndpoint string

	// DefaultModel is the model id used when a request leaves ModelID empty.
	// Read from ATLAS_LLM_DEFAULT_MODEL; defaults to the slice-182 D5 baseline
	// (Llama 3.1 8B). The quality caveat for this local default is documented
	// in docs/ai-assist/llm-foundation.md (AC-13).
	DefaultModel string

	// RequestTimeout is the wall-clock cap the Ollama client applies when a
	// GenerateRequest does not set its own (Timeout is mandatory on the
	// request, so this is a backstop floor for the HTTP transport). Read from
	// ATLAS_LLM_REQUEST_TIMEOUT (Go duration string); defaults below.
	RequestTimeout time.Duration
}

const (
	// DefaultOllamaEndpoint is the Ollama loopback default. No tenant data
	// leaves the deployment with this endpoint (local-only posture).
	DefaultOllamaEndpoint = "http://127.0.0.1:11434"

	// DefaultModelID is the slice-182 D5 baseline: Llama 3.1 8B Instruct,
	// q5 quant -- runs on commodity 8-12GB GPU. The quality caveat is
	// explicitly documented for operators (AC-13).
	DefaultModelID = "llama3.1:8b-instruct-q5"

	// DefaultRequestTimeout is the transport-level backstop. The per-request
	// Timeout (mandatory) is the real cap; this bounds a request that somehow
	// reaches the transport without one (it cannot, given validate(), but the
	// HTTP client must carry a finite timeout regardless).
	DefaultRequestTimeout = 120 * time.Second
)

// Environment variable names. Prefixed ATLAS_LLM_ per the repo convention
// (ATLAS_* for platform config).
const (
	envOllamaEndpoint = "ATLAS_LLM_OLLAMA_ENDPOINT"
	envDefaultModel   = "ATLAS_LLM_DEFAULT_MODEL"
	envRequestTimeout = "ATLAS_LLM_REQUEST_TIMEOUT"
)

// ConfigFromEnv reads the local-inference config from the environment,
// applying defaults for any unset value. It never errors: an empty
// environment yields the all-defaults local-Ollama config, which is the
// intended self-host posture.
func ConfigFromEnv() Config {
	cfg := Config{
		OllamaEndpoint: DefaultOllamaEndpoint,
		DefaultModel:   DefaultModelID,
		RequestTimeout: DefaultRequestTimeout,
	}
	if v := os.Getenv(envOllamaEndpoint); v != "" {
		cfg.OllamaEndpoint = v
	}
	if v := os.Getenv(envDefaultModel); v != "" {
		cfg.DefaultModel = v
	}
	if v := os.Getenv(envRequestTimeout); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			cfg.RequestTimeout = d
		}
	}
	return cfg
}
