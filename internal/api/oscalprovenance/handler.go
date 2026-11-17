package oscalprovenance

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/api/httperr"
	"github.com/mgoodric/security-atlas/internal/api/httpresp"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// provenanceReader is the read seam the handler reads through. The
// production *Store satisfies it; tests inject a stub to drive the wire
// shape + error branches with no Postgres pool.
type provenanceReader interface {
	ProvenanceForBaseline(ctx context.Context, baselineID uuid.UUID) (provenanceRow, error)
}

// Handler wires the OSCAL resolved-chain provenance read route over a Store.
// It holds no write surface — the single route is a pure read.
type Handler struct {
	reader provenanceReader
}

// New constructs a Handler over the application pgx pool's Store. The
// production call site (httpserver.go) passes NewStore(pool).
func New(store *Store) *Handler { return &Handler{reader: storeReader{store}} }

// newHandlerWithReader constructs a Handler over an arbitrary read seam.
// Unexported — it exists only for the unit tests, which inject a fixed-row
// stub so the wire shape records without a Postgres pool.
func newHandlerWithReader(r provenanceReader) *Handler { return &Handler{reader: r} }

// ===== wire shapes =====

// chainLinkWire is one supplied document in the resolved chain: its role
// ("entry-profile" | "profile" | "catalog"), the sha256 content hash of the
// supplied bytes, and the byte length (for quick diagnostics). It mirrors
// the profileimport.chainLink shape slice 578 persisted into the audit
// detail JSON — this is the read counterpart of that write.
type chainLinkWire struct {
	Role   string `json:"role"`
	Sha256 string `json:"sha256"`
	Bytes  int    `json:"bytes"`
}

// provenanceWire is the response body for the provenance read: the baseline
// identity + the ordered resolved chain.
type provenanceWire struct {
	BaselineID   string          `json:"baseline_id"`
	ProfileTitle string          `json:"profile_title"`
	SourceLabel  string          `json:"source_label"`
	OSCALVersion string          `json:"oscal_version"`
	SourceSHA256 string          `json:"source_sha256"`
	ImportedAt   string          `json:"imported_at"`
	ChainDepth   int             `json:"chain_depth"`
	Chain        []chainLinkWire `json:"chain"`
}

// auditDetail is the slice-578 success-audit detail JSON shape this read
// decodes. Only the chain-provenance members are read here; the other
// members (mapped/unmapped/kind) are ignored.
type auditDetail struct {
	Chain      []chainLinkWire `json:"chain"`
	ChainDepth int             `json:"chain_depth"`
}

// Provenance handles GET /v1/oscal/imported-profiles/{id}/provenance — the
// resolved-chain provenance for one imported profile baseline (AC-1/AC-2).
// The chain is returned in document order: entry-profile first, then any
// intermediate profiles, then the resolving catalog(s). A single-level
// slice-511 import returns its two-element [entry-profile, catalog] chain
// without special-casing (AC-2). The read is tenant-scoped via RLS plus the
// query's explicit tenant_id predicate (AC-3 / canvas invariant #6); a
// cross-tenant or unknown id returns 404.
func (h *Handler) Provenance(w http.ResponseWriter, r *http.Request) {
	if !requireOscalRead(w, r) {
		return
	}
	ctx, ok := tenantContext(r)
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}

	baselineID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "imported-profile id must be a uuid")
		return
	}

	row, err := h.reader.ProvenanceForBaseline(ctx, baselineID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			httpresp.WriteError(w, http.StatusNotFound, "imported profile baseline not found")
			return
		}
		httperr.WriteInternal(w, r, "oscalprovenance", err)
		return
	}

	var detail auditDetail
	if len(row.Detail) > 0 {
		if err := json.Unmarshal(row.Detail, &detail); err != nil {
			httperr.WriteInternal(w, r, "oscalprovenance", err)
			return
		}
	}
	// Normalize a nil chain to an empty slice so the JSON renders [] not null.
	chain := detail.Chain
	if chain == nil {
		chain = []chainLinkWire{}
	}

	httpresp.WriteJSON(w, http.StatusOK, provenanceWire{
		BaselineID:   uuidString(row.BaselineID),
		ProfileTitle: row.ProfileTitle,
		SourceLabel:  row.SourceLabel,
		OSCALVersion: row.OscalVersion,
		SourceSHA256: row.SourceSha256,
		ImportedAt:   tsString(row.ImportedAt),
		ChainDepth:   detail.ChainDepth,
		Chain:        chain,
	})
}

// ===== authz (defense-in-depth) =====

// requireOscalRead is the handler-level role guard, the defense-in-depth
// twin of the slice-035 OPA middleware (the OPA engine is not wired in
// unit/integration test servers, so a handler-level check is the testable
// enforcement point — the controldetail.requireControlRead precedent). The
// OSCAL imported-profile provenance read is an operator/auditor surface:
// the read set is admin (wildcard) + grc_engineer (IsApprover) +
// control_owner (OwnerRoles), the same set controldetail.hasControlRead
// admits. A bare push credential (no flags) has no business reading it.
func requireOscalRead(w http.ResponseWriter, r *http.Request) bool {
	cred, ok := authctx.CredentialFromContext(r.Context())
	if !ok || !hasOscalRead(cred) {
		httpresp.WriteError(w, http.StatusForbidden, "role does not grant oscal-read access")
		return false
	}
	return true
}

// hasOscalRead reports whether the credential carries an explicit
// OSCAL-read role signal. Mirrors controldetail.hasControlRead.
func hasOscalRead(c credstore.Credential) bool {
	return c.IsAdmin || c.IsApprover || len(c.OwnerRoles) > 0
}

// ===== helpers =====

// tenantContext confirms the upstream slice-033 tenancy middleware lifted a
// tenant id onto the request context. Absent it, the request is
// unauthenticated.
func tenantContext(r *http.Request) (context.Context, bool) {
	if _, err := tenancy.TenantFromContext(r.Context()); err != nil {
		return nil, false
	}
	return r.Context(), true
}

// uuidString renders a pgtype.UUID as its canonical string, or "" when the
// value is NULL/invalid.
func uuidString(u pgtype.UUID) string {
	if !u.Valid {
		return ""
	}
	return uuid.UUID(u.Bytes).String()
}

// tsString renders a pgtype.Timestamptz as RFC3339 UTC, or "" when NULL.
func tsString(t pgtype.Timestamptz) string {
	if !t.Valid {
		return ""
	}
	return t.Time.UTC().Format(time.RFC3339)
}
