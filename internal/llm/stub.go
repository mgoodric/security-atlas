package llm

import "context"

// StubClient is the CI / test seam: a deterministic Client that returns a
// fixed result without any network call. It is the documented pattern every
// downstream consumer (440/441/444/471) reuses so their integration + e2e
// tests run WITHOUT a live Ollama in CI (AC-4) -- the same way the
// oscal-bridge integration tests skip when Python/trestle is absent, except
// the stub lets the rest of the flow (audit write + enforcement) run for real.
//
// StubClient still runs the SHARED request validation (mandatory caps,
// surface, prompt) before returning, so a consumer's test that supplies an
// over-cap or malformed request gets the SAME rejection it would in
// production. Only the inference is stubbed; the contract is not.
type StubClient struct {
	// Result is returned verbatim on every successful Generate. Defaults to a
	// neutral, citation-free fixed draft so a consumer can assert against a
	// known string. Set explicitly to tailor a test.
	Result GenerateResult

	// Err, if non-nil, is returned instead of Result (after validation passes)
	// so a consumer can exercise its backend-failure handling.
	Err error
}

// NewStubClient returns a StubClient with a neutral fixed result. Consumers
// override Result/Err per test.
func NewStubClient() *StubClient {
	return &StubClient{
		Result: GenerateResult{
			Text:          "stub draft",
			ModelName:     "stub",
			ModelVersion:  "0",
			ModelProvider: "stub",
		},
	}
}

// Generate validates the request against the shared contract, then returns the
// configured Result (or Err). It performs no IO and holds no cross-call state.
func (c *StubClient) Generate(_ context.Context, req GenerateRequest) (GenerateResult, error) {
	if err := req.validate(); err != nil {
		return GenerateResult{}, err
	}
	if c.Err != nil {
		return GenerateResult{}, c.Err
	}
	return c.Result, nil
}

// compile-time assertion that StubClient satisfies Client.
var _ Client = (*StubClient)(nil)
