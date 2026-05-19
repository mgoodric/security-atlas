// Package adminvendors is the slice-139 HTTP surface for the vendor
// data-export endpoint:
//
//	GET /v1/admin/vendors/export?format=<csv|json|xlsx>
//
// Spillover from slice 135 (data-export library, merged at 6d4d2a0).
// Sibling of `internal/api/adminauditperiods/` and follows the same
// shape: reuse `internal/export/` verbatim, slice 145 concurrency cap,
// meta-audit on every outcome, streaming write, slice 135 filename
// sanitization.
//
// # Column set
//
// Mirrors `vendors.vendorWire` from the slice-024 read endpoint with
// two adjustments:
//
//   - `domain` renders unchanged (it is already the vendor's public
//     domain, not PII).
//   - `owner_user_masked` replaces `owner_user`. The slice-024 row's
//     `owner_user` field is a free-form identifier that is, in v1,
//     usually a user email. The slice 139 threat model masks any
//     email-shaped owner_user to `*@domain.tld` so a vendor export
//     handed to a third-party auditor leaks no operator local-parts.
//     Un-masked column deferred to v3 column selection with proper
//     RBAC gating.
//   - `notes` is INCLUDED at v1 — operators rely on the notes column
//     for the same workflow the read endpoint serves (e.g. "DPA
//     renegotiation in Q3"). Notes are NOT considered PII for v1;
//     callers needing redacted handoff can use jq / awk to strip the
//     column post-download.
//
// Final column set:
//
//	id, name, domain, criticality, contract_start, contract_end,
//	dpa_signed, dpa_signed_at, review_cadence, last_review_date,
//	overdue, owner_user_masked, linked_sow_uri, notes, scope_cell_ids,
//	created_at, updated_at
//
// `scope_cell_ids` renders as a `;`-separated UUID list (mirroring the
// CSV-friendly join the slice-135 reference uses for repeated-column
// fields).
//
// # Threat model
//
// Inherits slice 135. Per-entity addendums:
//
//   - Vendor `owner_user` is the only column where operator-identity
//     PII can leak. The export ALWAYS masks; v1 has no opt-out.
//   - `notes` is operator-authored free text; the export does not
//     parse / scrub. Operators are responsible for not pasting PII
//     into notes (same contract as the read endpoint).
//   - `domain` is the vendor's domain, not the operator's. Not
//     masked.
//
// # Constitutional posture
//
//   - Invariant #6 (RLS): every read goes through the slice-024
//     `vendor.Store.List` which applies `tenancy.ApplyTenant` under
//     `atlas_app`. NO `BYPASSRLS`. The cross-tenant isolation
//     integration test is the merge-blocking evidence (P0-A10).
//   - Slice 135 P0-A4 (meta-audit on every outcome).
//   - Slice 135 P0-A7 (streaming write).
//   - Slice 145 P0-HARDEN-3 (concurrency cap).
package adminvendors

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"iter"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/export"
	"github.com/mgoodric/security-atlas/internal/tenancy"
	"github.com/mgoodric/security-atlas/internal/vendor"
)

const (
	// defaultExportRowCap is the slice 139 P0-A7 vendor-export row
	// cap. 50K is the slice doc's stated ceiling — well above the
	// 30-80 vendors the slice-024 module is sized for, large enough
	// to absorb a tenant whose vendor program has grown 600x over
	// the v1 sizing.
	defaultExportRowCap = 50_000

	// metaAuditActionExport is the slice 139 vendor meta-audit
	// action value. Distinct from slice 135 (`audit_log_export`) and
	// from `audit_periods_export` so each entity's bulk-PII
	// extraction is independently enumerable in forensic queries.
	metaAuditActionExport = "vendors_export"

	// exportEntity is the slice 135 filename-builder entity string.
	exportEntity = "vendors"
)

// Handler owns the slice-139 vendor export endpoint. Constructed via
// [New]; tests inject a small-capacity limiter via [Handler.WithLimiter].
type Handler struct {
	pool    *pgxpool.Pool
	store   *vendor.Store
	now     func() time.Time
	limiter *export.Limiter
}

// New constructs a Handler over the provided pgxpool. The internal
// [vendor.Store] is the slice-024 store; slice-139 export reuses it
// rather than touching `internal/vendor/`.
func New(pool *pgxpool.Pool) *Handler {
	return &Handler{
		pool:  pool,
		store: vendor.NewStore(pool),
		now:   time.Now,
	}
}

// NewWithClock is identical to New but lets tests pin "now" so the
// `overdue` column is deterministic. Used by integration tests.
func NewWithClock(pool *pgxpool.Pool, now func() time.Time) *Handler {
	return &Handler{
		pool:  pool,
		store: vendor.NewStore(pool),
		now:   now,
	}
}

// WithLimiter installs a Limiter for deterministic test caps.
// Production callers MUST NOT use this.
func (h *Handler) WithLimiter(l *export.Limiter) *Handler {
	h.limiter = l
	return h
}

// ExportVendors handles `GET /v1/admin/vendors/export?format=...`.
//
// Returns the encoded file body (CSV / JSON / XLSX) on success,
// streamed back with the appropriate Content-Type and a sanitized
// Content-Disposition filename. Writes a `me_audit_log` row on EVERY
// terminal outcome (slice 135 P0-A4).
func (h *Handler) ExportVendors(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	cred, ok := authctx.CredentialFromContext(ctx)
	if !ok {
		writeError(w, http.StatusUnauthorized, "missing credential")
		return
	}
	tenantID, err := uuid.Parse(cred.TenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "invalid tenant in credential")
		return
	}
	userIdentifier := cred.UserID
	if userIdentifier == "" {
		userIdentifier = cred.ID
	}

	format, parseErr := parseFormat(r)
	if parseErr != nil {
		h.writeExportMetaAudit(ctx, tenantID, userIdentifier, exportMetaAudit{
			Format: string(format),
			Result: "denied:bad_request",
			Reason: parseErr.Error(),
		})
		writeError(w, http.StatusBadRequest, parseErr.Error())
		return
	}

	// Role gate (defense-in-depth) — same admit set as
	// adminauditperiods.
	if !callerAllowedExport(cred) {
		h.writeExportMetaAudit(ctx, tenantID, userIdentifier, exportMetaAudit{
			Format: string(format),
			Result: "denied:forbidden",
			Reason: "caller lacks admin|auditor|grc_engineer role",
		})
		writeError(w, http.StatusForbidden, "admin, auditor, or grc_engineer role required")
		return
	}

	// Slice 145 concurrency cap.
	limiter := h.exportLimiter()
	release, capErr := limiter.Acquire(tenantID, userIdentifier)
	if capErr != nil {
		h.writeExportMetaAudit(ctx, tenantID, userIdentifier, exportMetaAudit{
			Format: string(format),
			Result: "denied:concurrency_cap_exceeded",
			Reason: capErr.Error(),
		})
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Retry-After", "30")
		w.WriteHeader(http.StatusTooManyRequests)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": fmt.Sprintf(
				"export concurrency cap (%d) reached for this (tenant, user); "+
					"retry in 30s or narrow concurrent forensics work",
				limiter.Cap()),
			"retry_after_seconds": 30,
			"cap":                 limiter.Cap(),
		})
		return
	}
	defer release()

	encoder, err := export.ResolveExporter(format)
	if err != nil {
		h.writeExportMetaAudit(ctx, tenantID, userIdentifier, exportMetaAudit{
			Format: string(format),
			Result: "denied:bad_format",
			Reason: err.Error(),
		})
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Row read. The slice-024 store's List opens its own tx + applies
	// the tenant GUC. No filter at v1 — the export surfaces the full
	// tenant vendor register. Future iterations can add a criticality
	// filter behind a separate decision.
	vendors, err := h.store.List(ctx, vendor.ListFilter{})
	if err != nil {
		h.writeExportMetaAudit(ctx, tenantID, userIdentifier, exportMetaAudit{
			Format: string(format),
			Result: "error:db",
			Reason: err.Error(),
		})
		writeError(w, http.StatusInternalServerError, "list vendors: "+err.Error())
		return
	}

	if len(vendors) > defaultExportRowCap {
		h.writeExportMetaAudit(ctx, tenantID, userIdentifier, exportMetaAudit{
			Format:   string(format),
			Result:   "denied:row_cap_exceeded",
			Reason:   fmt.Sprintf("rowCap=%d", defaultExportRowCap),
			RowCount: len(vendors),
		})
		writeError(w, http.StatusRequestEntityTooLarge,
			fmt.Sprintf("export would exceed row cap of %d vendors; "+
				"narrow the request scope and retry", defaultExportRowCap))
		return
	}

	header := vendorsExportHeader()
	filename := export.BuildFilename(exportEntity, encoder.FileExt(), nil)
	w.Header().Set("Content-Type", encoder.ContentType())
	w.Header().Set("Content-Disposition",
		`attachment; filename="`+filename+`"`)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)

	cw := &countingWriter{w: w}
	if err := encoder.WriteRows(cw, header, vendorsToRowIter(vendors, h.now())); err != nil {
		h.writeExportMetaAudit(ctx, tenantID, userIdentifier, exportMetaAudit{
			Format:    string(format),
			Result:    "error:encode",
			Reason:    err.Error(),
			RowCount:  len(vendors),
			ByteCount: cw.n,
		})
		return
	}
	h.writeExportMetaAudit(ctx, tenantID, userIdentifier, exportMetaAudit{
		Format:    string(format),
		Result:    "success",
		RowCount:  len(vendors),
		ByteCount: cw.n,
	})
}

// ===== Parsing =====

func parseFormat(r *http.Request) (export.Format, error) {
	raw := r.URL.Query().Get("format")
	if raw == "" {
		return export.FormatCSV, nil
	}
	f := export.Format(strings.ToLower(raw))
	if !export.IsValid(f) {
		return f, fmt.Errorf("unsupported format %q (want csv|json|xlsx)", raw)
	}
	return f, nil
}

// ===== Meta-audit =====

type exportMetaAudit struct {
	Format    string `json:"format"`
	Result    string `json:"result"`
	Reason    string `json:"reason,omitempty"`
	RowCount  int    `json:"row_count"`
	ByteCount int64  `json:"byte_count"`
}

func (h *Handler) writeExportMetaAudit(ctx context.Context, tenantID uuid.UUID, userIdentifier string, meta exportMetaAudit) {
	beforeBlob, _ := json.Marshal(map[string]string{"format": meta.Format})
	afterBlob, _ := json.Marshal(meta)

	uID, parseErr := uuid.Parse(userIdentifier)
	if parseErr != nil {
		uID = uuid.Nil
	}

	_ = h.inTx(ctx, func(ctx context.Context, q *dbx.Queries) error {
		return q.InsertMeAuditLog(ctx, dbx.InsertMeAuditLogParams{
			TenantID: pgtype.UUID{Bytes: tenantID, Valid: true},
			UserID:   pgtype.UUID{Bytes: uID, Valid: true},
			Action:   metaAuditActionExport,
			Before:   beforeBlob,
			After:    afterBlob,
		})
	})
}

// ===== Column set + row projection =====

// vendorsExportHeader is the canonical column list for slice 139's
// vendor export. Includes `owner_user_masked` (slice 139 D1 — vendor
// email masking at v1). Excludes the raw `owner_user` column —
// un-masked access deferred to v3 column selection with proper RBAC.
// Stable: changing this list is a breaking change.
func vendorsExportHeader() []string {
	return []string{
		"id",
		"name",
		"domain",
		"criticality",
		"contract_start",
		"contract_end",
		"dpa_signed",
		"dpa_signed_at",
		"review_cadence",
		"last_review_date",
		"overdue",
		"owner_user_masked",
		"linked_sow_uri",
		"notes",
		"scope_cell_ids",
		"created_at",
		"updated_at",
	}
}

// vendorsToRowIter projects a slice of [vendor.Vendor] into an
// iter.Seq[[]string] in the canonical column order. The `overdue`
// column is computed against `asOf` so test runs are deterministic;
// production passes `time.Now()`.
//
// `owner_user_masked` runs the raw `OwnerUser` field through
// [MaskEmail]. Non-email values pass through unchanged; email-shaped
// values render as `*@domain.tld` (slice 139 D1).
func vendorsToRowIter(vendors []vendor.Vendor, asOf time.Time) iter.Seq[[]string] {
	return func(yield func([]string) bool) {
		for _, v := range vendors {
			row := []string{
				v.ID.String(),
				v.Name,
				ptrToStr(v.Domain),
				string(v.Criticality),
				dateStr(v.ContractStart),
				dateStr(v.ContractEnd),
				boolStr(v.DPASigned),
				dateStr(v.DPASignedAt),
				string(v.ReviewCadence),
				dateStr(v.LastReviewDate),
				boolStr(v.IsOverdueAsOf(asOf)),
				MaskEmail(v.OwnerUser),
				ptrToStr(v.LinkedSOWURI),
				v.Notes,
				joinUUIDs(v.ScopeCellIDs),
				v.CreatedAt.UTC().Format(time.RFC3339Nano),
				v.UpdatedAt.UTC().Format(time.RFC3339Nano),
			}
			if !yield(row) {
				return
			}
		}
	}
}

// MaskEmail returns a masked rendering of an email-shaped string for
// the slice-139 vendor export. The mask drops the local-part and the
// `@`-internal whitespace so the result is always of the shape
// `*@domain.tld`.
//
// Slice 139 D1 — vendor email masking at v1:
//
//   - Empty input -> empty output. No emit means no leak.
//   - Input without any `@` -> empty output. We treat a no-`@` value
//     as a non-email identifier we still don't want to leak by
//     accident; the audit trail is the meta-audit row, not the export
//     body. (Slice 139 P0-A11 — must not panic; this branch covers
//     it.)
//   - Input with exactly one `@` -> `*@domain` (the substring after
//     the last `@`).
//   - Input with multiple `@` -> `*@<final-segment>` (the substring
//     after the LAST `@`). RFC 5322 permits quoted `@` in the
//     local-part, but the practical universe of values that reach
//     this function is `email@domain` shapes typed by operators; the
//     last-@ rule produces the right answer for both pathological
//     quoted local-parts and the realistic case. We never panic.
//   - Input where the last `@` is the LAST character (e.g. `foo@`) ->
//     empty output. A trailing-`@` value has no domain to surface;
//     emitting `*@` would still leak the existence of the local-part
//     without anything useful, so we drop the cell entirely.
//
// The function is total (panic-free for every string input) and pure
// (no I/O, no allocation beyond the result string).
func MaskEmail(in string) string {
	if in == "" {
		return ""
	}
	idx := strings.LastIndex(in, "@")
	if idx < 0 {
		return ""
	}
	domain := in[idx+1:]
	if domain == "" {
		return ""
	}
	return "*@" + domain
}

// ===== Helpers =====

func (h *Handler) exportLimiter() *export.Limiter {
	if h.limiter != nil {
		return h.limiter
	}
	return export.DefaultLimiter()
}

func callerAllowedExport(cred credstore.Credential) bool {
	if cred.IsAdmin {
		return true
	}
	for _, role := range cred.OwnerRoles {
		if role == "auditor" || role == "grc_engineer" {
			return true
		}
	}
	return false
}

func (h *Handler) inTx(ctx context.Context, fn func(context.Context, *dbx.Queries) error) error {
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		return err
	}
	q := dbx.New(tx)
	if err := fn(ctx, q); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ===== Column projection helpers =====

func ptrToStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func dateStr(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.UTC().Format("2006-01-02")
}

func joinUUIDs(ids []uuid.UUID) string {
	if len(ids) == 0 {
		return ""
	}
	parts := make([]string, 0, len(ids))
	for _, id := range ids {
		parts = append(parts, id.String())
	}
	return strings.Join(parts, ";")
}

// ===== HTTP helpers =====

func writeError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

type countingWriter struct {
	w io.Writer
	n int64
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}
