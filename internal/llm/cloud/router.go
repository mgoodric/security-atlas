package cloud

import (
	"context"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mgoodric/security-atlas/internal/llm"
)

// pgxNoRows aliases pgx.ErrNoRows so the Store's "no row => default" branches
// read cleanly without importing pgx at every call site.
var pgxNoRows = pgx.ErrNoRows

// AdapterFactory builds a cloud llm.Client for a provider + decrypted key. The
// Router injects this so tests can substitute a fake cloud client (the default
// factory builds a real HTTP Adapter). It is the seam that keeps the Router's
// dispatch logic unit-testable without a live cloud call.
type AdapterFactory func(provider Provider, apiKey Secret) (llm.Client, error)

// Router is the per-tenant inference dispatcher. It IS an llm.Client, so the
// four AI-assist surfaces (440/441/444/471) keep calling Generate unchanged
// (P0-499-6) — swapping llm.NewOllamaClient(...) for this Router at the
// registration site is the ONLY wiring change.
//
// At generation time Generate:
//  1. resolves the requesting tenant's routing config under app.current_tenant
//     (the Store's tenant-scoped tx — cross-tenant bleed is impossible,
//     P0-499-5 / AC-10);
//  2. dispatches to the LOCAL Ollama client when the tenant is on local-ollama
//     (the default / no row), or to a per-provider cloud Adapter built with the
//     tenant's DECRYPTED key when the tenant has opted in.
//
// The resulting GenerateResult.ModelProvider carries the actual provider, which
// the AuditWriter records on ai_generations.model_provider (AC-5) and the
// surfaces use to drive the visible banner (AC-7). The approval gate is
// untouched — the Router only decides WHERE a draft is generated (P0-499-7).
type Router struct {
	local   llm.Client
	store   *Store
	factory AdapterFactory
}

var _ llm.Client = (*Router)(nil)

// NewRouter builds a Router over the local client, the routing-config Store, and
// an AdapterFactory. When factory is nil, DefaultAdapterFactory is used (real
// HTTP adapters). local must be non-nil (the off-by-default backend).
func NewRouter(local llm.Client, store *Store, factory AdapterFactory) *Router {
	if factory == nil {
		factory = DefaultAdapterFactory()
	}
	return &Router{local: local, store: store, factory: factory}
}

// Generate resolves the tenant's backend and dispatches. The request contract
// (mandatory caps, surface, prompt) is validated by the chosen backend's
// Generate — identical across local and cloud.
func (r *Router) Generate(ctx context.Context, req llm.GenerateRequest) (llm.GenerateResult, error) {
	provider, key, err := r.store.Resolve(ctx)
	if err != nil {
		return llm.GenerateResult{}, err
	}
	if !provider.IsCloud() {
		// Default / no-opt-in path: local Ollama. No tenant data leaves the
		// deployment.
		return r.local.Generate(ctx, req)
	}
	client, err := r.factory(provider, key)
	if err != nil {
		return llm.GenerateResult{}, err
	}
	return client.Generate(ctx, req)
}

// DefaultAdapterFactory returns an AdapterFactory that builds real HTTP cloud
// adapters with a backstop-timeout *http.Client. The per-request context
// deadline (from GenerateRequest.Timeout) is the real cap; this client timeout
// is a floor so a hung dial cannot outlive the process (AC-6).
func DefaultAdapterFactory() AdapterFactory {
	httpClient := &http.Client{Timeout: defaultCloudHTTPTimeout}
	return func(provider Provider, apiKey Secret) (llm.Client, error) {
		return NewAdapter(provider, apiKey, httpClient)
	}
}

// defaultCloudHTTPTimeout is the transport backstop for cloud calls. The real
// per-request cap is GenerateRequest.Timeout (mandatory); this bounds a request
// that somehow reaches the transport without one.
const defaultCloudHTTPTimeout = 120 * time.Second
