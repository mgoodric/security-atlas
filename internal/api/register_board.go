package api

import (
	"github.com/go-chi/chi/v5"

	boardapi "github.com/mgoodric/security-atlas/internal/api/board"
	boardnarrativeapi "github.com/mgoodric/security-atlas/internal/api/boardnarrative"
	"github.com/mgoodric/security-atlas/internal/board"
	"github.com/mgoodric/security-atlas/internal/boardnarrative"
	"github.com/mgoodric/security-atlas/internal/drift"
	"github.com/mgoodric/security-atlas/internal/featureflag"
	"github.com/mgoodric/security-atlas/internal/freshness"
	"github.com/mgoodric/security-atlas/internal/llm"
	"github.com/mgoodric/security-atlas/internal/vendor"
)

// registerBoard registers its domain's routes onto the shared root
// router. It is one of the slice-436 per-domain registrars extracted from
// the former monolithic httpHandler(); the root it receives already carries
// the full shared middleware chain (security headers, request ID, CORS,
// JWT, credential gate, tenancy, authz, feature-flag cache), so every route
// registered here is gated identically to how it was inline (slice 436
// AC-6). Behavior is unchanged: routes, handlers, and declaration order are
// preserved verbatim.
func (s *Server) registerBoard(root *chi.Mux, featureFlagStore *featureflag.Store, freshnessStore *freshness.Store, driftStore *drift.Store, vendorStore *vendor.Store) {
	// Slice 031: monthly board brief. Generates a single-page, board-ready
	// posture snapshot (per-framework posture + 30-day drift + top-3 risks
	// aging) and persists it as a PINNED, IMMUTABLE snapshot (canvas §7.5).
	// The Generator is a pure reader of the slice-016 freshness + drift read
	// models (reused via the freshnessStore + driftStore constructed above)
	// plus the frameworks + risks tables; its only write target is the
	// append-only board_briefs table. The narrative is TEMPLATED — no LLM
	// (AC-6, P0 anti-criterion). Routes appended per the parallel-batch
	// convention (chi rejects two Mounts at "/"); the literal-suffix routes
	// (/{id}.md, /{id}/pdf) are declared before the bare /{id} so chi's
	// declaration-order match keeps them ahead of the generic id route.
	boardStore := board.NewStore(s.dbPool)
	boardGen := board.NewGenerator(boardStore, freshnessStore, driftStore)
	boardH := boardapi.New(boardGen, boardStore)
	// Slice 660: the board-reporting module (briefs + packs) gates on the
	// `board.reporting` feature flag. Both RegisterRoutes calls receive a
	// featureflag.Gate-wrapped router so a flag-off tenant gets a clean 404
	// + {"error":"feature disabled"} on EVERY board route (briefs + packs),
	// matching the OSCAL gate above (consistent 404 shape; no internal
	// leak). `board.reporting` is OFF by default pending GA. The Gate reads
	// the caller's tenant flag under RLS (invariant #6).
	root.Group(func(r chi.Router) {
		r.Use(featureflag.Gate(featureFlagStore, "board.reporting"))
		boardH.RegisterRoutes(r)
		// Slice 032: quarterly board pack. Extends the slice-031 monthly brief
		// into the full board-meeting artifact — a multi-section report
		// (posture, top risks, coverage trend, open findings, operational
		// metrics, investment-vs-coverage, asks of the board) with a
		// draft -> publish lifecycle. The PackGenerator reuses the same
		// slice-016 freshness + drift read models plus the board-pack-owned
		// failing-evaluations read (control_evaluations as of period_end —
		// decision D4). The narrative is TEMPLATED — no LLM (P0 anti-criterion).
		// Publish is gated on every section being human-approved (decision D6).
		// Routes appended per the parallel-batch convention; the literal-suffix
		// and deeper /sections/... routes are declared before the bare /{id}.
		boardPackStore := board.NewPackStore(s.dbPool)
		// Slice 273: the board-pack `vendor_burndown` section reads through
		// the existing slice-122 high-criticality vendor burndown surface
		// (vendor.Store.Burndown) via a tiny in-process adapter. The adapter
		// lives at this wiring layer so internal/board stays free of an
		// internal/vendor import (board.VendorBurndownReader is the contract).
		// Pinned to criticality=high per slice 273 D2 — the board concern is
		// overdue reviews on the vendors that matter.
		boardPackGen := board.NewPackGenerator(
			boardPackStore, freshnessStore, driftStore,
			vendorBurndownAdapter{store: vendorStore},
		)
		boardPackH := boardapi.NewPack(boardPackGen, boardPackStore)
		boardPackH.RegisterRoutes(r)
		// Slice 440: board-narrative AI v0 (cited, numeric-verified,
		// per-section approval). The HIGHEST-RISK AI-assist surface: an
		// AI-drafted board-report SECTION (the control-coverage-summary
		// section) an operator approves before it ships into the board pack.
		// The deterministic rollup REUSES the slice-031 brief data path
		// (boardGen.Assemble — a pure read, no board_briefs write); the seven
		// guardrails (hybrid input, mandatory citations, numeric verification,
		// section-shape, per-section approval, full audit, tone) are enforced
		// server-side BEFORE the operator sees a draft. Constitutional
		// invariants (no fabricated coverage/numbers, no cross-tenant bleed,
		// one-click approval, local-Ollama-only) enforced by the service + the
		// DB CHECK on board_narrative_sections (the slice-498 shared ai_assist
		// guard). Gated with the board module under board.reporting; routes
		// append per the parallel-batch convention.
		boardNarrativeStore := boardnarrative.NewStore(s.dbPool, boardGen)
		boardNarrativeSvc := boardnarrative.NewService(
			boardNarrativeStore,
			llm.NewOllamaClient(llm.ConfigFromEnv()),
			boardNarrativeStore,
			boardnarrative.NewAuditSink(llm.NewAuditWriter(s.dbPool)),
			boardNarrativeStore,
		)
		boardNarrativeH := boardnarrativeapi.New(boardNarrativeSvc)
		boardNarrativeH.RegisterRoutes(r)
	})
}
