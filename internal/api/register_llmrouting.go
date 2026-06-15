package api

import (
	"log"
	"sync"

	"github.com/go-chi/chi/v5"

	llmroutingapi "github.com/mgoodric/security-atlas/internal/api/llmrouting"
	"github.com/mgoodric/security-atlas/internal/llm"
	"github.com/mgoodric/security-atlas/internal/llm/cloud"
)

// llmRoutingOnce guards one-time construction of the shared per-tenant routing
// substrate (slice 499): a single cloud.Store + a single cloud.Router shared by
// every AI-assist surface. Built lazily so a server with no DB pool (test
// fixtures) never constructs it.
var (
	llmRoutingOnce  sync.Once
	llmRoutingStore *cloud.Store
)

// routingStore lazily builds (once) and returns the shared cloud routing Store.
// The deployment cloud-key crypter is resolved once from the env and handed to
// the Store: nil when no cloud master key is configured — in that case the Store
// still serves reads/clears, but a cloud opt-in is rejected with a clear
// "cloud routing not enabled on this deployment" error (the off-by-default
// posture: a deployment must explicitly provision a key to enable egress).
func (s *Server) routingStore() *cloud.Store {
	llmRoutingOnce.Do(func() {
		crypter, err := cloud.NewCrypterFromEnv()
		if err != nil {
			// ErrCrypterUnconfigured is the expected, benign self-host default
			// (no cloud key => local-only). Any other error (malformed key) is
			// logged so the operator notices, but never logs the key material.
			if err != cloud.ErrCrypterUnconfigured {
				log.Printf("llm cloud crypter: %v (cloud routing disabled)", err)
			}
			crypter = nil
		}
		llmRoutingStore = cloud.NewStore(s.dbPool, crypter)
	})
	return llmRoutingStore
}

// inferenceClient returns the per-tenant inference llm.Client every AI-assist
// surface (440/441/444/471) is wired with (slice 499). It is a cloud.Router
// over the local Ollama client (the off-by-default backend) + the shared
// routing Store: at generation time the Router resolves the requesting tenant's
// config under app.current_tenant and dispatches local-or-cloud transparently,
// so the surface's call site (s.client.Generate) is UNCHANGED (P0-499-6).
//
// A fresh Router is cheap (it shares the local client + Store); each surface
// gets its own so a future surface-specific override stays isolated.
func (s *Server) inferenceClient() llm.Client {
	local := llm.NewOllamaClient(llm.ConfigFromEnv())
	return cloud.NewRouter(local, s.routingStore(), nil)
}

// registerLLMRouting mounts the tenant-admin routing-config endpoint. The
// surface-side wiring (injecting inferenceClient into each AI-assist service)
// lives in the respective domain registrars (register_questionnaire,
// register_board, register_controlstate).
func (s *Server) registerLLMRouting(root *chi.Mux) {
	h := llmroutingapi.New(s.routingStore())
	h.RegisterRoutes(root)
}
