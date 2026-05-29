// Package ucfcoverage implements the UCF graph traversal HTTP API (slice
// 008). Three read-only endpoints query the requirement-anchor-control
// graph defined in canvas §3 + Plans/UCF_GRAPH_MODEL.md:
//
//   - GET /v1/requirements/{id}/coverage  — forward traversal: given a
//     framework requirement, list every SCF anchor it maps to plus the
//     tenant's controls anchored at each. (requirement_coverage.go)
//
//   - GET /v1/anchors/{id}/requirements   — reverse traversal: given an
//     SCF anchor, list every framework requirement it satisfies. This
//     supersedes the slice-006 in-memory placeholder route on the same
//     path; the response shape is compatible. (anchor_requirements.go)
//
//   - GET /v1/controls/{id}/coverage      — control-centric traversal:
//     given a control, return the framework requirements its SCF anchor
//     satisfies. (control_coverage.go)
//
// Per-endpoint handlers + their endpoint-specific helpers live in the
// three files named above (slice 381 F-UCF-2 — split out of a single
// 932-LOC handlers.go to keep each endpoint reviewable in isolation).
// This file retains the package-shared scaffolding: the Handler struct,
// constructors, route wiring, the cross-endpoint lookup/transaction
// helpers, the wire types, and the JSON-writing helpers.
//
// Constitutional invariants honored:
//
//   - Invariant 1 (canvas §3.1, CLAUDE.md): every traversal goes through
//     the SCF anchor spine. No requirement → requirement edge is ever
//     consulted; no such table exists (slice 007 enforces at DDL).
//
//   - Invariant 6 (canvas §5.4, CLAUDE.md): tenant-scoped reads on the
//     `controls` table run inside the request's `app.current_tenant`
//     GUC set by tenancy.Middleware; no app-level `WHERE tenant_id = ?`
//     clause is present anywhere in this package. Cross-tenant queries
//     return empty controls lists, not 403 — RLS makes the foreign rows
//     invisible at the database layer.
//
// Effectiveness scores (canvas §3.3) are deferred to slice 012 — the
// `controls` array omits the field entirely rather than emitting null,
// so slice 012 can add it without a breaking change.
//
// The `?as-of=<timestamp>` and `?scf_release=<version>` query parameters
// are accepted and documented but no-op in v1; slice 012 (point-in-time
// evidence filtering) and a future SCF-release-import feature will
// activate them.
package ucfcoverage

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/eval"
	"github.com/mgoodric/security-atlas/internal/frameworkscope"
	"github.com/mgoodric/security-atlas/internal/scope"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// Handler wires the three slice-008 routes to a pgx pool. Catalog reads
// go through a bare `*dbx.Queries`; tenant reads on `controls` go
// through `inTx` so the GUC is set before the query runs.
//
// Slice 256 — the optional `engine`, `scopeStore`, and `fwScopeStore`
// fields gate the per-row Coverage column on /v1/controls/{id}/coverage.
// When all three are wired, each requirement row carries a numeric
// `coverage` (strength × 30-day effectiveness when the requirement's
// framework_version is in scope; null otherwise — never client-
// computed, see slice 256 P0-1). When any is nil (unit servers built
// without these dependencies) the field is omitted entirely so the
// existing slice-008 wire shape stays backwards-compatible.
type Handler struct {
	pool         *pgxpool.Pool
	q            *dbx.Queries
	engine       *eval.Engine
	scopeStore   *scope.Store
	fwScopeStore *frameworkscope.Store
}

// New constructs a Handler from a pgx pool. pool must be non-nil.
//
// The returned Handler emits the slice-008 coverage response without the
// slice-256 per-row `coverage` field. Call AttachCoverage to wire the
// dependencies that promote that field to first-class.
func New(pool *pgxpool.Pool) *Handler {
	return &Handler{pool: pool, q: dbx.New(pool)}
}

// AttachCoverage wires the three stores the slice-256 per-row coverage
// computation needs: the eval engine (30-day pass rate), the scope
// store (control applicability), and the framework_scope store
// (per-framework activated predicate). All three must be non-nil; this
// is enforced at wire-time, not at request-time, so a partial wiring is
// caught by `cmd/atlas` startup rather than by a 500 on the first
// /coverage call.
//
// The two-stage constructor (New + AttachCoverage) preserves the
// existing zero-coverage-fields shape for unit tests that don't need
// eval/scope/framework_scope plumbing, and is the same pattern slice
// 013 used to graft an optional ingest pipeline onto the evidence
// handler without forcing every test to spin up NATS.
func (h *Handler) AttachCoverage(engine *eval.Engine, scopeStore *scope.Store, fwScopeStore *frameworkscope.Store) *Handler {
	h.engine = engine
	h.scopeStore = scopeStore
	h.fwScopeStore = fwScopeStore
	return h
}

// ===== route wiring =====

// RegisterRoutes attaches the three slice-008 read endpoints to the
// supplied chi.Router. Use the Mount-append pattern in
// internal/api/httpserver.go: call this method on the root router so
// the routes live alongside slice-014/017/etc. Never wrap with a second
// chi.NewRouter().Mount("/", ...) — chi panics on duplicate Mount.
func (h *Handler) RegisterRoutes(r chi.Router) {
	r.Get("/v1/requirements/{id}/coverage", h.RequirementCoverage)
	r.Get("/v1/anchors/{id}/requirements", h.AnchorRequirements)
	r.Get("/v1/controls/{id}/coverage", h.ControlCoverage)
}

// ===== shared helpers =====

// inTenantTx opens a tx on the pool, sets the `app.current_tenant` GUC
// from the request context's tenant binding (slice-033 middleware
// wired this), runs fn, and commits. The pattern mirrors
// internal/risk/store.go's inTx. Required for any read against a
// tenant-scoped table — RLS denies on unset GUC.
func (h *Handler) inTenantTx(ctx context.Context, fn func(context.Context, *dbx.Queries) error) error {
	if _, err := tenancy.TenantFromContext(ctx); err != nil {
		return err
	}
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("ucfcoverage: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		return err
	}
	if err := fn(ctx, dbx.New(tx)); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("ucfcoverage: commit tx: %w", err)
	}
	return nil
}

// pgUUIDFromTenantCtx converts the context-bound tenant id string into
// a pgtype.UUID for sqlc-generated query params that still take a
// tenant_id parameter. Slice 008's traversal queries don't, but the
// reused slice-009 `GetControlByID` does.
func pgUUIDFromTenantCtx(ctx context.Context) pgtype.UUID {
	t, _ := tenancy.TenantFromContext(ctx)
	u, _ := uuid.Parse(t)
	return pgtype.UUID{Bytes: u, Valid: true}
}

// lookupRequirement resolves the {id} path segment to a
// framework_requirement row, supporting UUID, slug:version:code, and
// slug::code forms. Identical pattern to slice-007's
// anchors.lookupRequirement; duplicated here per the 2-call duplication
// allowance — a third user would justify hoisting to a shared package.
func (h *Handler) lookupRequirement(ctx context.Context, idOrCode string) (dbx.FrameworkRequirement, bool, error) {
	if uid, err := uuid.Parse(idOrCode); err == nil {
		row, err := h.q.GetFrameworkRequirementByID(ctx, pgtype.UUID{Bytes: uid, Valid: true})
		if errors.Is(err, pgx.ErrNoRows) {
			return dbx.FrameworkRequirement{}, false, nil
		}
		if err != nil {
			return dbx.FrameworkRequirement{}, false, err
		}
		return row, true, nil
	}
	parts := strings.SplitN(idOrCode, ":", 3)
	if len(parts) != 3 {
		return dbx.FrameworkRequirement{}, false, nil
	}
	slug, version, code := parts[0], parts[1], parts[2]
	if version == "" {
		row, err := h.q.GetFrameworkRequirementByCurrentVersion(ctx, dbx.GetFrameworkRequirementByCurrentVersionParams{
			Slug: slug,
			Code: code,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return dbx.FrameworkRequirement{}, false, nil
		}
		return row, err == nil, err
	}
	row, err := h.q.GetFrameworkRequirementByFrameworkSlugVersionCode(ctx, dbx.GetFrameworkRequirementByFrameworkSlugVersionCodeParams{
		Slug:    slug,
		Version: version,
		Code:    code,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return dbx.FrameworkRequirement{}, false, nil
	}
	return row, err == nil, err
}

// resolveFrameworkVersion parses ?framework_version=slug:version. Returns
// (row, true) on success; (zero, false) if the param shape is invalid
// or the slug:version pair doesn't exist in the catalog. Handlers
// interpret false as "no rows match" (200 + empty list), not 404 —
// the underlying anchor/requirement still resolved, just not the pin.
func (h *Handler) resolveFrameworkVersion(ctx context.Context, param string) (dbx.FrameworkVersion, bool) {
	parts := strings.SplitN(param, ":", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return dbx.FrameworkVersion{}, false
	}
	row, err := h.q.GetFrameworkVersionBySlugAndVersion(ctx, dbx.GetFrameworkVersionBySlugAndVersionParams{
		Slug:    parts[0],
		Version: parts[1],
	})
	if err != nil {
		return dbx.FrameworkVersion{}, false
	}
	return row, true
}

// ===== wire types =====

// requirementWire is the shape of a framework_requirement in slice-008
// responses. Matches slice-007's requirementWire shape — when both
// slices ship the same wire type, callers don't need per-route
// deserializers.
type requirementWire struct {
	ID    string `json:"id"`
	Code  string `json:"code"`
	Title string `json:"title"`
	Body  string `json:"body,omitempty"`
}

// anchorWire is the shape of an SCF anchor in slice-008 responses.
// Includes the STRM edge metadata when present (in RequirementCoverage)
// and is omitted when not relevant (in ControlCoverage's `anchor` field
// which is the bare anchor, no edge).
type anchorWire struct {
	ID                string  `json:"id"`
	SCFID             string  `json:"scf_id"`
	Family            string  `json:"family"`
	Name              string  `json:"name"`
	Description       string  `json:"description,omitempty"`
	EdgeID            string  `json:"edge_id,omitempty"`
	RelationshipType  string  `json:"relationship_type,omitempty"`
	Strength          float64 `json:"strength,omitempty"`
	SourceAttribution string  `json:"source_attribution,omitempty"`
	Rationale         string  `json:"rationale,omitempty"`
}

// controlWire is the shape of a control in slice-008 responses.
// Effectiveness is intentionally absent (slice 012's territory) — the
// field is omitted entirely rather than emitted as null so slice 012
// can add it without a breaking-change semver bump.
type controlWire struct {
	ID                 string `json:"id"`
	BundleID           string `json:"bundle_id"`
	Version            int32  `json:"version"`
	SCFID              string `json:"scf_id,omitempty"`
	SCFAnchorID        string `json:"scf_anchor_id,omitempty"`
	Title              string `json:"title"`
	ControlFamily      string `json:"control_family"`
	ImplementationType string `json:"implementation_type"`
	OwnerRole          string `json:"owner_role"`
	LifecycleState     string `json:"lifecycle_state"`
	FreshnessClass     string `json:"freshness_class,omitempty"`
}

// requirementForAnchorWire is the shape of one row in
// AnchorRequirements + ControlCoverage. Carries enough framework
// metadata that callers don't need a second round-trip per row.
//
// Slice 256 — `Coverage` is the per-row weighted score
// (strength × 30-day effectiveness, intersected with the framework's
// scope predicate). `*float64` so we can JSON-encode `null` when the
// requirement's framework_version is out of scope OR the control has
// no effectiveness data yet (TotalCount == 0). Distinguishing null from
// 0 is the AC-2 contract — "no data" must NOT degrade to "perfectly
// failing". The field is always emitted (no `omitempty`) so the wire
// shape is a stable contract: callers always see `coverage: <number>`
// or `coverage: null`, never an absent key. On
// /v1/anchors/{id}/requirements (which does not compute coverage) the
// field emits as `null` — the honest "not computed for this surface"
// shape rather than a silent omission.
type requirementForAnchorWire struct {
	EdgeID                 string   `json:"edge_id"`
	RequirementID          string   `json:"requirement_id"`
	Code                   string   `json:"code"`
	Title                  string   `json:"title"`
	Body                   string   `json:"body,omitempty"`
	FrameworkSlug          string   `json:"framework_slug"`
	FrameworkName          string   `json:"framework_name"`
	FrameworkVersion       string   `json:"framework_version"`
	FrameworkVersionID     string   `json:"framework_version_id"`
	FrameworkVersionStatus string   `json:"framework_version_status"`
	RelationshipType       string   `json:"relationship_type"`
	Strength               float64  `json:"strength"`
	Coverage               *float64 `json:"coverage"`
	SourceAttribution      string   `json:"source_attribution"`
	Rationale              string   `json:"rationale,omitempty"`
}

// ===== shared write helpers =====

func uuidStr(u pgtype.UUID) string {
	if !u.Valid {
		return ""
	}
	return uuid.UUID(u.Bytes).String()
}
