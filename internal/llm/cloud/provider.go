// Package cloud is the slice-499 per-tenant cloud-LLM opt-in layer that sits
// BEHIND the unchanged slice-498 llm.Client interface. It ships four things and
// deliberately nothing more:
//
//  1. The closed provider enum (provider.go) — local-ollama (default) /
//     anthropic / openai / bedrock. There is NO free-text endpoint: a cloud
//     provider's API URL is hard-coded in its adapter, never operator-supplied
//     (P0-499-3 — an operator URL would be an SSRF / exfiltration primitive).
//
//  2. The provider-key crypter (crypter.go) — AES-256-GCM encrypt/decrypt of
//     the provider API key, keyed by a deployment master key read from a 0600
//     file or env (the keystore-style "key material never in the DB" pattern).
//     The Secret type masks the plaintext key in every log / format / JSON path
//     (P0-499-4).
//
//  3. The tenant-scoped routing-config Store (store.go) — set / replace / clear
//     the tenant's provider + (encrypted) key, under four-policy RLS on
//     app.current_tenant. The key is write-only: it is encrypted on the way in
//     and NEVER returned (the read path returns a masked view) (AC-3 / AC-11).
//
//  4. The per-tenant Router (router.go) — itself an llm.Client. At generation
//     time it resolves the requesting tenant's routing config (under
//     app.current_tenant), and dispatches to the local Ollama client
//     (default / no row) or the configured cloud adapter (with the tenant's
//     decrypted key). The four AI-assist surfaces (440/441/444/471) keep
//     calling llm.Client.Generate — they are not modified (P0-499-6); swapping
//     llm.NewOllamaClient(...) for this Router at the registration site is the
//     only wiring change.
//
// SCOPE DISCIPLINE (anti-criteria, block merge):
//   - OFF BY DEFAULT: no routing row => local-ollama (P0-499-1).
//   - CLOSED ENUM, no operator URL (P0-499-3).
//   - KEY write-only / masked / never logged (P0-499-4).
//   - TENANT-ISOLATED: config + key resolved under app.current_tenant; no
//     cross-tenant bleed (P0-499-5).
//   - APPROVAL GATE UNCHANGED: this layer changes WHERE a draft is generated,
//     never WHETHER it can publish without human approval (P0-499-7).
package cloud

import "strings"

// Provider is the closed set of inference backends a tenant may route to.
// There is intentionally no "custom" / "other" member and no URL field: a new
// provider requires a new adapter + a migration extending the DB CHECK
// (P0-499-3).
type Provider string

const (
	// ProviderLocalOllama is the DEFAULT for every tenant. No tenant data
	// leaves the deployment. This is the value the router assumes when a
	// tenant has no routing row at all (off-by-default, P0-499-1 / AC-2).
	ProviderLocalOllama Provider = "local-ollama"

	// ProviderAnthropic routes to the Anthropic Messages API.
	ProviderAnthropic Provider = "anthropic"

	// ProviderOpenAI routes to the OpenAI Chat Completions API.
	ProviderOpenAI Provider = "openai"

	// ProviderBedrock routes to AWS Bedrock (Anthropic-on-Bedrock invoke).
	ProviderBedrock Provider = "bedrock"
)

// validProviders is the canonical set. It MUST mirror the
// tenant_llm_routing_provider_chk CHECK in
// migrations/sql/20260612100000_tenant_llm_routing.sql — adding a provider
// requires extending BOTH this set and the migration CHECK (and shipping an
// adapter).
var validProviders = map[Provider]bool{
	ProviderLocalOllama: true,
	ProviderAnthropic:   true,
	ProviderOpenAI:      true,
	ProviderBedrock:     true,
}

// ParseProvider normalizes and validates a provider string. It is the single
// gate the config Store + API handler run untrusted input through, so an
// unknown provider is rejected with a clean error rather than reaching the DB
// CHECK as a raw 23514.
func ParseProvider(s string) (Provider, bool) {
	p := Provider(strings.ToLower(strings.TrimSpace(s)))
	if validProviders[p] {
		return p, true
	}
	return "", false
}

// IsCloud reports whether the provider routes tenant data to a third party
// (i.e. anything other than the local Ollama default). The visible banner and
// the "key required" invariant both key off this.
func (p Provider) IsCloud() bool {
	return p != ProviderLocalOllama && validProviders[p]
}

// String returns the wire/DB value of the provider.
func (p Provider) String() string { return string(p) }

// IsCloudProvider reports whether a resolved model-provider STRING (as recorded
// on ai_generations.model_provider and returned in GenerateResult.ModelProvider)
// names a cloud backend. It is the shared predicate the AI-assist surfaces use
// to drive the visible banner. It accepts the local-ollama variants the
// slice-498 OllamaClient emits ("ollama-local") as well as the routing-config
// provider value ("local-ollama"), and the test "stub" sentinel — all of which
// are local / non-egress.
//
// Centralizing it here means a new surface does not re-author the banner
// predicate (the slice narrative's "banner driven by config, not hardcoded per
// surface" requirement applies to the predicate too).
func IsCloudProvider(provider string) bool {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "", "ollama", "ollama-local", "local", "local-ollama", "stub":
		return false
	default:
		return true
	}
}
