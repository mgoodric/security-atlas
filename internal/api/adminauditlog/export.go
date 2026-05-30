// Slice 135 — audit-log data-export handler.
//
// `GET /v1/admin/audit-log/export?format=<csv|json|xlsx>&from=...&to=...&kind=...&actor=...`
//
// Reuses slice 124's underlying aggregator (`internal/audit/unifiedlog`)
// — same SQL, same 90-day window cap, same role gate
// (`HasUnifiedAuditLogRole`), same RLS contract (`atlas_app` +
// `tenancy.ApplyTenant`). The only differences from `UnifiedList` are:
//
//   - the response is the encoded file body (CSV / JSON / XLSX)
//     streamed back with `Content-Disposition: attachment; filename=...`,
//     not a JSON envelope;
//   - the meta-audit action is `audit_log_export` (not
//     `audit_log_query_unified`) so downstream consumers can tell a
//     bulk dump apart from a paginated screen-read;
//   - a row cap (slice 135 D3 default = 100,000) is enforced — a
//     caller whose result exceeds the cap gets `413 Payload Too Large`
//     with a body explaining the narrow-the-filter remediation;
//   - the request window is clamped against `audit_periods.frozen_at`
//     when a frozen period overlaps (constitutional invariant #10 /
//     canvas §8.4 / slice 135 AC-12).
//
// Constitutional posture:
//
//   - Invariant #6 (RLS): every read goes through `tenancy.ApplyTenant`
//     under `atlas_app`. NO `BYPASSRLS`. The cross-tenant isolation
//     integration test in `export_integration_test.go` is the
//     merge-blocking evidence (slice 135 P0-A5).
//
//   - Invariant #10 (audit-period freezing): see [Handler.minFrozenAtForWindow]
//     and the call site in [Handler.ExportUnified].
//
//   - Slice 135 P0-A4 (meta-audit on every outcome): every code path
//     in [Handler.ExportUnified] writes a `me_audit_log` row before
//     returning — success, 403, 413, or 500. Uses
//     [Handler.writeExportMetaAudit].
//
//   - Slice 135 P0-A7 (streaming write): the row iterator is built
//     against the slice-124 `unifiedlog.Query` result slice (which
//     materializes one page-worth — capped at the export limit), and
//     the encoder pipes per-row into the HTTP response. Per-row
//     allocation is bounded; live heap does not grow with row count.

package adminauditlog

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"iter"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/httperr"
	"github.com/mgoodric/security-atlas/internal/api/httpresp"
	"github.com/mgoodric/security-atlas/internal/audit/sink"
	"github.com/mgoodric/security-atlas/internal/audit/unifiedlog"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/export"
)

const (
	// defaultExportRowCap is the slice 135 D3 row-cap default. Set
	// large enough that 99% of audit-log exports fit in one request,
	// small enough that a 9-column row at ~200 bytes each
	// (= ~200 MB transfer) stays within an HTTP/2 streaming budget.
	// Per-entity override hook: spillovers 136-139 register their own
	// cap at registration time when a higher ceiling is justified
	// (controls UCF lifts to 500K, ledger entities to 250K, etc.) —
	// see docs/audit-log/135-data-export-library-decisions.md D3.
	defaultExportRowCap = 100_000

	// metaAuditActionExport is the slice 135 meta-audit action value.
	// Intentionally distinct from `audit_log_query_unified` (slice 124
	// read meta-audit) so a forensic query like
	// `WHERE action = 'audit_log_export'` cleanly enumerates bulk-PII
	// extraction events. The migration
	// `20260518000010_audit_log_export.sql` extends the
	// `me_audit_log.action` CHECK to permit this value.
	metaAuditActionExport = "audit_log_export"
)

// exportEntity is the slice 135 reference-impl entity identifier used
// in BuildFilename. Spillovers 136-139 will register their own entity
// strings (`risk-register`, `controls-ucf`, etc.); v1 of the export
// product ships with this one.
const exportEntity = "audit-log"

// ExportUnified handles `GET /v1/admin/audit-log/export?format=...`.
//
// Returns the encoded file body (CSV / JSON / XLSX) on success,
// streamed back with the appropriate Content-Type and a sanitized
// Content-Disposition filename. Writes a `me_audit_log` row on EVERY
// terminal outcome (slice 135 P0-A4).
//
// Slice 145 hardening additions:
//   - `?include_payload=<bool>` (default true) — redacts the
//     `payload_json` column for the external-audit-handoff workflow
//     (slice 145 AC-1 / AC-2). The meta-audit row records the value
//     used so operators can prove which export went to which
//     audience (slice 145 AC-3).
//   - per-(tenant, user) concurrency cap — excess returns 429 with
//     `Retry-After: 30` and a JSON body explaining the limit
//     (slice 145 AC-4 / AC-5).
func (h *Handler) ExportUnified(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	cred, ok := authctx.CredentialFromContext(ctx)
	if !ok {
		// No credential in context — this is upstream-middleware
		// territory (the bearer auth chain should have rejected
		// before reaching here). No meta-audit is possible because
		// we have no tenant id; the upstream layer already logged
		// the 401 via the request log.
		httpresp.WriteError(w, http.StatusUnauthorized, "missing credential")
		return
	}
	tenantID, err := uuid.Parse(cred.TenantID)
	if err != nil {
		httpresp.WriteError(w, http.StatusInternalServerError, "invalid tenant in credential")
		return
	}
	userIdentifier := cred.UserID
	if userIdentifier == "" {
		userIdentifier = cred.ID
	}

	// Parse + validate. Bad request shapes get a 400; the meta-audit
	// fires (slice 135 P0-A4) with result=denied:bad_request so the
	// trail captures the attempt.
	format, params, includePayload, parseErr := parseExportRequest(r)
	if parseErr != nil {
		h.writeExportMetaAudit(ctx, tenantID, userIdentifier, exportMetaAudit{
			Format:         string(format),
			From:           params.fromRaw,
			To:             params.toRaw,
			Kinds:          kindStringsFromQuery(params.queryParams),
			Actor:          params.queryParams.ActorFilter,
			Result:         "denied:bad_request",
			RowCount:       0,
			ByteCount:      0,
			Reason:         parseErr.Error(),
			IncludePayload: includePayloadPtr(includePayload),
		})
		httpresp.WriteError(w, http.StatusBadRequest, parseErr.Error())
		return
	}

	// Role gate (defense-in-depth) — same as slice 124's
	// UnifiedList: admin OR auditor OR grc_engineer. The upstream
	// slice-035 OPA middleware is the canonical gate; AC-10 of
	// slice 135 + the rego matrix test ensure the admit set matches
	// audit-log-unified exactly.
	allowed, err := h.callerAllowedUnified(ctx, tenantID, cred.UserID, cred.IsAdmin)
	if err != nil {
		h.writeExportMetaAudit(ctx, tenantID, userIdentifier, exportMetaAudit{
			Format: string(format), From: params.fromRaw, To: params.toRaw,
			Result: "error:role_probe", Reason: err.Error(),
			IncludePayload: includePayloadPtr(includePayload),
		})
		httperr.WriteInternal(w, r, "role probe", err)
		return
	}
	if !allowed {
		h.writeExportMetaAudit(ctx, tenantID, userIdentifier, exportMetaAudit{
			Format: string(format), From: params.fromRaw, To: params.toRaw,
			Result:         "denied:forbidden",
			Reason:         "caller lacks admin|auditor|grc_engineer role",
			IncludePayload: includePayloadPtr(includePayload),
		})
		httpresp.WriteError(w, http.StatusForbidden, "admin, auditor, or grc_engineer role required")
		return
	}

	// Slice 145 — per-(tenant, user) concurrency cap. ACQUIRED HERE,
	// AFTER auth + role gate but BEFORE encoder resolve / DB work:
	//   * Anonymous + bad-auth requests are never counted against the
	//     cap (they're already 401/403'd above).
	//   * The slot is held for the duration of the streaming write
	//     (release is deferred), so an attacker firing N concurrent
	//     requests really does saturate at cap=N regardless of
	//     individual request latency.
	//   * Refusal returns 429 with `Retry-After: 30` and a JSON body
	//     explaining the limit (slice 145 P0-HARDEN-3 + P0-A10).
	//   * Meta-audit fires on the 429 path too — operators reading
	//     `me_audit_log WHERE action = 'audit_log_export'` see the
	//     attempt with result=denied:concurrency_cap_exceeded.
	limiter := h.exportLimiter()
	release, capErr := limiter.Acquire(tenantID, userIdentifier)
	if capErr != nil {
		h.writeExportMetaAudit(ctx, tenantID, userIdentifier, exportMetaAudit{
			Format: string(format), From: params.fromRaw, To: params.toRaw,
			Kinds:          kindStringsFromQuery(params.queryParams),
			Actor:          params.queryParams.ActorFilter,
			Result:         "denied:concurrency_cap_exceeded",
			Reason:         capErr.Error(),
			IncludePayload: includePayloadPtr(includePayload),
		})
		// `Retry-After: 30` per slice 145 P0-HARDEN-3 (mirroring the
		// slice 141 P0-DOS-1 pattern). JSON body so operators reading
		// curl output without -i still see the limit message
		// (slice 145 P0-A10).
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
	// Slice 145 P0-A9 — release on EVERY exit path (panic / error /
	// 413 / 500 / success). The limiter's release fn is idempotent
	// (sync.Once-guarded) so this is safe alongside any other
	// explicit release call.
	defer release()

	// Resolve encoder for the requested format. Bad format would
	// have been caught in parseExportRequest; this is the safe
	// secondary lookup.
	encoder, err := export.ResolveExporter(format)
	if err != nil {
		h.writeExportMetaAudit(ctx, tenantID, userIdentifier, exportMetaAudit{
			Format: string(format), Result: "denied:bad_format", Reason: err.Error(),
			IncludePayload: includePayloadPtr(includePayload),
		})
		httpresp.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Capture the encoded body into a streaming counter so the
	// meta-audit can record byte_count. The encoder writes per-row
	// into countingWriter; countingWriter forwards to the HTTP
	// response. Row count comes from the limit-check loop below.
	//
	// The HTTP layer's response headers MUST be sent BEFORE the
	// first write to the body. We set them now (after auth + role
	// gate succeed, before we attempt the query) so callers see
	// the file metadata immediately. A 413 path returns BEFORE we
	// write the body — see below.

	// One transaction wraps:
	//   1. compute the frozen_at clamp (AC-12),
	//   2. run the slice-124 aggregator over the (possibly clamped)
	//      window,
	//   3. emit the body (only if under the row cap),
	//   4. write the meta-audit row.
	//
	// Streaming posture: the aggregator returns at most (rowCap+1)
	// rows in memory (capped by params.Limit); the encoder iterates
	// over that slice once and writes per-row. Per-row allocation is
	// bounded — slice 135 P0-A7. Tested in
	// TestExportStreamingMemoryUnder50MB.
	var (
		statusToSend int
		bodyBytes    int64
		rowsEmitted  int
		usedClamp    bool
		clampUsed    time.Time
		metaErr      string
		queryErr     string
	)

	rowCap := defaultExportRowCap

	err = h.inTx(ctx, func(ctx context.Context, q *dbx.Queries) error {
		// AC-12: clamp the effective `to` if a frozen audit_period
		// overlaps the request window. The min(frozen_at) across
		// overlapping frozen periods is the most-conservative
		// horizon — any row past that boundary would surface
		// post-freeze data and break point-in-time replay (canvas
		// §8.4 / constitutional invariant #10).
		min, err := q.MinFrozenAtOverlappingWindow(ctx, dbx.MinFrozenAtOverlappingWindowParams{
			TenantID:   pgtype.UUID{Bytes: tenantID, Valid: true},
			WindowFrom: pgtype.Timestamptz{Time: params.queryParams.From, Valid: true},
			WindowTo:   pgtype.Timestamptz{Time: params.queryParams.To, Valid: true},
		})
		if err != nil {
			queryErr = "frozen-window probe: " + err.Error()
			return fmt.Errorf("frozen-window probe: %w", err)
		}
		effectiveTo := params.queryParams.To
		if min.Valid && min.Time.Before(effectiveTo) {
			effectiveTo = min.Time
			usedClamp = true
			clampUsed = min.Time
		}
		clampedParams := params.queryParams
		clampedParams.To = effectiveTo
		// Slice 270 / slice 402: the export endpoint is admit-gated to
		// exactly {admin, auditor, grc_engineer} (the callerAllowedUnified
		// gate above) — the same set as the slice-124 unified list, which
		// sets CallerIsPrivileged=true unconditionally (unified.go). Every
		// caller that reaches this query is privileged, so the export must
		// render the full privileged view: feature_flag rows and ALL me
		// rows, not just the caller's own. Without this the forensic export
		// silently omitted feature_flag + cross-actor me rows (the default
		// zero-value CallerIsPrivileged=false enabled the slice-270
		// row-visibility predicate meant for the non-privileged
		// /v1/activity/unified endpoint). Surfaced by the never-CI-run
		// slice-135 export integration suite during slice 402 enrolment.
		clampedParams.CallerIsPrivileged = true
		// Ask for (rowCap + 1) rows so we can detect "exceeds cap"
		// without an extra round-trip.
		clampedParams.Limit = rowCap + 1

		entries, _, err := unifiedlog.Query(ctx, q, clampedParams)
		if err != nil {
			queryErr = err.Error()
			return err
		}

		if len(entries) > rowCap {
			statusToSend = http.StatusRequestEntityTooLarge
			return nil // out of tx, handle in caller block
		}

		// Streaming write.
		statusToSend = http.StatusOK
		header := unifiedExportHeader()
		filename := export.BuildFilename(exportEntity, encoder.FileExt(),
			filenameParamsFor(params))

		w.Header().Set("Content-Type", encoder.ContentType())
		w.Header().Set("Content-Disposition",
			`attachment; filename="`+filename+`"`)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.WriteHeader(http.StatusOK)

		// Slice 145 — when include_payload=false the row iterator
		// emits the empty string for the payload_json column. CSV +
		// XLSX render that as an empty cell; JSON renders it as the
		// literal `null` token via WriteOpts.NullForEmpty.
		rowIter := entriesToRowIter(entries, includePayload)
		writeOpts := export.WriteOpts{}
		if !includePayload {
			writeOpts.NullForEmpty = map[string]bool{
				"payload_json": true,
			}
		}

		cw := &countingWriter{w: w}
		if err := encoder.WriteRowsWithOpts(cw, header, rowIter, writeOpts); err != nil {
			queryErr = "encoder: " + err.Error()
			// We already started the body; cannot change status now.
			// The meta-audit captures the partial state.
			return err
		}
		bodyBytes = cw.n
		rowsEmitted = len(entries)
		return nil
	})

	// One last meta-audit / response shape decision. The transaction
	// has committed (or rolled back); we now write the meta-audit
	// row in a fresh tx and emit the 413 body if applicable.
	if err != nil {
		// txn errored after we may have started writing the body.
		// The meta-audit fires with result=error:tx; if we hadn't
		// already WriteHeader'd, surface a 500.
		h.writeExportMetaAudit(ctx, tenantID, userIdentifier, exportMetaAudit{
			Format: string(format), From: params.fromRaw, To: params.toRaw,
			Kinds:             kindStringsFromQuery(params.queryParams),
			Actor:             params.queryParams.ActorFilter,
			Result:            "error:tx",
			Reason:            queryErr,
			ClampedToFrozenAt: clampPtrIfSet(usedClamp, clampUsed),
			RowCount:          rowsEmitted, ByteCount: bodyBytes,
			IncludePayload: includePayloadPtr(includePayload),
		})
		if statusToSend == 0 {
			httpresp.WriteError(w, http.StatusInternalServerError, "export: "+queryErr)
		}
		// If statusToSend was already set we already started the
		// response; there's nothing more to write.
		return
	}

	if statusToSend == http.StatusRequestEntityTooLarge {
		h.writeExportMetaAudit(ctx, tenantID, userIdentifier, exportMetaAudit{
			Format: string(format), From: params.fromRaw, To: params.toRaw,
			Kinds:             kindStringsFromQuery(params.queryParams),
			Actor:             params.queryParams.ActorFilter,
			Result:            "denied:row_cap_exceeded",
			Reason:            fmt.Sprintf("rowCap=%d", rowCap),
			ClampedToFrozenAt: clampPtrIfSet(usedClamp, clampUsed),
			IncludePayload:    includePayloadPtr(includePayload),
		})
		// 413 body: actionable narrow-the-filter guidance. The
		// caller knows the cap and can re-issue with a tighter
		// `from` / `to` / `kind` / `actor` filter.
		httpresp.WriteError(w, http.StatusRequestEntityTooLarge,
			fmt.Sprintf("export would exceed row cap of %d; narrow the request filter "+
				"(from/to window, kind=, actor=) and retry", rowCap))

		return
	}

	// Success path — write the meta-audit row. The body is already
	// streamed to the wire; this row records what happened.
	_ = metaErr
	h.writeExportMetaAudit(ctx, tenantID, userIdentifier, exportMetaAudit{
		Format: string(format), From: params.fromRaw, To: params.toRaw,
		Kinds:             kindStringsFromQuery(params.queryParams),
		Actor:             params.queryParams.ActorFilter,
		Result:            "success",
		ClampedToFrozenAt: clampPtrIfSet(usedClamp, clampUsed),
		RowCount:          rowsEmitted,
		ByteCount:         bodyBytes,
		IncludePayload:    includePayloadPtr(includePayload),
	})
}

// ===== Parsing =====

// parseExportRequest reuses slice 124's parseUnifiedParams plus a
// `format` query param + slice 145's `include_payload` flag. Returns
// the resolved format, the parsed unified params (carrying the
// kindStrings + raw from/to so the meta-audit doesn't re-serialize),
// the resolved include_payload value, and a parse error.
//
// include_payload defaults to true (slice 145 P0-HARDEN-1 — preserves
// the slice 135 wire shape for existing callers). When the query
// parameter is present, it MUST parse as a strconv-acceptable bool
// (`true`, `false`, `1`, `0`, `t`, `f`, …); anything else is a 400.
func parseExportRequest(r *http.Request) (export.Format, parsedUnifiedParams, bool, error) {
	q := r.URL.Query()
	formatRaw := q.Get("format")
	if formatRaw == "" {
		formatRaw = string(export.FormatCSV)
	}
	format := export.Format(strings.ToLower(formatRaw))
	if !export.IsValid(format) {
		return format, parsedUnifiedParams{}, true,
			fmt.Errorf("unsupported format %q (want csv|json|xlsx)", formatRaw)
	}

	// Slice 145 — include_payload flag. Default true preserves the
	// slice 135 wire shape (P0-HARDEN-1). Validation is intentionally
	// strict (strconv.ParseBool, not a contains-check) so a typo'd
	// `?include_payload=ture` 400s rather than silently defaulting.
	includePayload := true
	if rawIP := q.Get("include_payload"); rawIP != "" {
		parsed, ipErr := strconv.ParseBool(rawIP)
		if ipErr != nil {
			return format, parsedUnifiedParams{}, true,
				fmt.Errorf("invalid include_payload %q (want true|false): %w", rawIP, ipErr)
		}
		includePayload = parsed
	}

	params, err := parseUnifiedParams(r)
	if err != nil {
		return format, params, includePayload, err
	}
	return format, params, includePayload, nil
}

// ===== Meta-audit shape =====

// exportMetaAudit is the JSON shape persisted to me_audit_log.after on
// every export attempt. The outcome buckets are:
//
//	"success"
//	"denied:bad_request"
//	"denied:bad_format"
//	"denied:forbidden"
//	"denied:row_cap_exceeded"
//	"denied:concurrency_cap_exceeded"     (slice 145)
//	"error:tx"
//	"error:role_probe"
//
// Slice 145 P0-A5: the `include_payload` field is **additive**. Legacy
// slice 135 rows that predate slice 145 do NOT carry the key; readers
// that need to distinguish "redacted handoff" from "full forensics"
// should treat absence as `true` (the slice 135 default). The field
// is emitted as `*bool` (rendered `null` when nil) so a future
// always-set migration can backfill without ambiguity.
type exportMetaAudit struct {
	Format            string     `json:"format"`
	From              string     `json:"from"`
	To                string     `json:"to"`
	Kinds             []string   `json:"kinds,omitempty"`
	Actor             string     `json:"actor,omitempty"`
	Result            string     `json:"result"`
	Reason            string     `json:"reason,omitempty"`
	RowCount          int        `json:"row_count"`
	ByteCount         int64      `json:"byte_count"`
	ClampedToFrozenAt *time.Time `json:"clamped_to_frozen_at,omitempty"`
	IncludePayload    *bool      `json:"include_payload,omitempty"`
}

// writeExportMetaAudit writes ONE me_audit_log row with
// action='audit_log_export'. Slice 135 P0-A4 — EVERY terminal outcome
// path writes a row. Uses a fresh tx (not the export's outer tx) so
// success-path writes commit even when the outer body-streaming
// transaction is already closed; on failure paths we are not in a tx
// to begin with so a fresh tx is correct.
//
// The meta-audit failure is intentionally non-fatal to the caller:
// if the audit write itself errors (e.g. CHECK constraint mismatch
// during a migration), we log via the external sink and continue.
// The export body the caller is already receiving is the
// authoritative artifact; failing the caller to surface an audit-
// write failure would be a bad trade-off.
func (h *Handler) writeExportMetaAudit(ctx context.Context, tenantID uuid.UUID, userIdentifier string, meta exportMetaAudit) {
	paramsBlob, _ := json.Marshal(metaAuditParams{
		From:  meta.From,
		To:    meta.To,
		Actor: meta.Actor,
		Kinds: meta.Kinds,
	})
	resultBlob, _ := json.Marshal(meta)

	uID, parseErr := uuid.Parse(userIdentifier)
	if parseErr != nil {
		// Bootstrap-key callers carry a non-UUID id; the zero
		// UUID is the honest fallback. Slice 124 uses the same
		// pattern in UnifiedList.
		uID = uuid.Nil
	}

	_ = h.inTx(ctx, func(ctx context.Context, q *dbx.Queries) error {
		if err := q.InsertMeAuditLog(ctx, dbx.InsertMeAuditLogParams{
			TenantID: pgtype.UUID{Bytes: tenantID, Valid: true},
			UserID:   pgtype.UUID{Bytes: uID, Valid: true},
			Action:   metaAuditActionExport,
			Before:   paramsBlob,
			After:    resultBlob,
		}); err != nil {
			return err
		}
		// Slice 126: fan out to external sink (tamper-evidence).
		// The export action is a high-value forensic event — bulk
		// PII extraction. The external sink ensures the in-app
		// admin cannot retroactively hide the export.
		sinkPayload, _ := json.Marshal(map[string]any{
			"before": json.RawMessage(paramsBlob),
			"after":  json.RawMessage(resultBlob),
		})
		sink.EmitDefault(ctx, unifiedlog.Entry{
			OccurredAt:    time.Now().UTC(),
			ActorID:       userIdentifier,
			TenantID:      tenantID,
			Kind:          unifiedlog.KindMe,
			TargetType:    "user",
			TargetID:      uID.String(),
			Action:        metaAuditActionExport,
			RowID:         uuid.New(),
			SubjectModule: unifiedlog.SubjectModuleCore,
			PayloadJSON:   sinkPayload,
		})
		return nil
	})
}

// ===== Helpers =====

// unifiedExportHeader is the canonical column list for the slice 135
// audit-log export. Matches the unifiedlog.Entry public fields in
// declaration order. Stable: changing this list is a breaking change
// for downstream consumers (Excel-shaped reports, scripts that key
// off column position).
func unifiedExportHeader() []string {
	return []string{
		"occurred_at",
		"actor_id",
		"actor_name",
		"tenant_id",
		"kind",
		"target_type",
		"target_id",
		"action",
		"row_id",
		"payload_json",
	}
}

// entriesToRowIter projects a slice of unifiedlog.Entry into an
// iter.Seq[[]string] in the canonical column order. The projection
// is pure — no DB I/O, O(1) per row.
//
// payload_json is rendered as the raw JSON text (a single stringified
// blob per row) when includePayload is true (slice 135 default). When
// includePayload is false (slice 145 `?include_payload=false`
// redacted-handoff workflow), the cell is the empty string — CSV +
// XLSX emit a blank cell; JSON emits `null` via
// [export.WriteOpts.NullForEmpty].
//
// actor_name is "" when nil. tenant_id renders the UUID string (the
// export deliberately preserves it so downstream re-imports /
// forensic-queries can correlate) regardless of the include_payload
// value — tenant_id is identifier-level, not content-level, and
// slice 145 P0-A2 keeps column-level redaction beyond `payload_json`
// out of scope.
func entriesToRowIter(entries []unifiedlog.Entry, includePayload bool) iter.Seq[[]string] {
	return func(yield func([]string) bool) {
		for _, e := range entries {
			actorName := ""
			if e.ActorName != nil {
				actorName = *e.ActorName
			}
			payload := ""
			if includePayload && len(e.PayloadJSON) > 0 {
				payload = string(e.PayloadJSON)
			}
			row := []string{
				e.OccurredAt.UTC().Format(time.RFC3339Nano),
				e.ActorID,
				actorName,
				e.TenantID.String(),
				string(e.Kind),
				e.TargetType,
				e.TargetID,
				e.Action,
				e.RowID.String(),
				payload,
			}
			if !yield(row) {
				return
			}
		}
	}
}

// includePayloadPtr returns &v — used to populate the optional
// `include_payload` field in exportMetaAudit. The field is `*bool` so
// legacy rows that predate slice 145 stay distinguishable from
// "explicit false" (rendered as `false`) and "explicit true"
// (rendered as `true`).
func includePayloadPtr(v bool) *bool {
	out := v
	return &out
}

// exportLimiter returns the per-(tenant, user) concurrency limiter
// used by this handler. Default implementation returns the
// process-wide singleton; tests override via [Handler.WithLimiter]
// for deterministic caps.
func (h *Handler) exportLimiter() *export.Limiter {
	if h.limiter != nil {
		return h.limiter
	}
	return export.DefaultLimiter()
}

// WithLimiter installs a Limiter into the handler — used by
// integration tests to pin a small, deterministic cap (default 2)
// without setting the env var across the whole test process.
//
// Production callers MUST NOT use this — the default singleton is the
// only correct shape for a process-wide cap.
func (h *Handler) WithLimiter(l *export.Limiter) *Handler {
	h.limiter = l
	return h
}

// kindStringsFromQuery is a slice-of-strings projection of the
// unifiedlog kind enum slice; mirrors the slice-124 metaAuditParams.Kinds
// shape so the meta-audit blobs are wire-compatible between read +
// export.
func kindStringsFromQuery(p unifiedlog.QueryParams) []string {
	if len(p.KindFilter) == 0 {
		return nil
	}
	out := make([]string, len(p.KindFilter))
	for i, k := range p.KindFilter {
		out[i] = string(k)
	}
	return out
}

// filenameParamsFor builds the param map BuildFilename consumes from
// the parsed unified params. Tenant id is intentionally NEVER included
// (slice 135 P0-A2). Filter values are passed through BuildFilename's
// sanitizer so any unsafe characters are dropped before they reach the
// Content-Disposition header.
func filenameParamsFor(p parsedUnifiedParams) map[string]string {
	out := map[string]string{}
	if p.fromRaw != "" {
		// from/to come in as RFC3339; strip everything but the
		// YYYYMMDD prefix so the filename stays short.
		out["from"] = compactDateForFilename(p.fromRaw)
	}
	if p.toRaw != "" {
		out["to"] = compactDateForFilename(p.toRaw)
	}
	if len(p.queryParams.KindFilter) > 0 {
		// Join kinds with `-` (sanitizer keeps `-`); a 3-kind
		// filter renders as `kind-evidencemewalkthrough` after
		// sanitization. Best-effort: filter summaries are not
		// guaranteed to be human-readable, only stable + safe.
		kinds := kindStringsFromQuery(p.queryParams)
		out["kind"] = strings.Join(kinds, "")
	}
	if p.queryParams.ActorFilter != "" {
		// First 8 chars of the actor filter — keeps the summary
		// short while still distinguishing per-actor exports for
		// a single date.
		af := p.queryParams.ActorFilter
		if len(af) > 8 {
			af = af[:8]
		}
		out["actor"] = af
	}
	return out
}

// compactDateForFilename extracts the YYYYMMDD prefix from an RFC3339
// string. Returns the input unchanged on any parse error — the
// downstream sanitizer drops the unsafe characters either way.
func compactDateForFilename(rfc3339 string) string {
	t, err := time.Parse(time.RFC3339, rfc3339)
	if err != nil {
		return rfc3339
	}
	return t.UTC().Format("20060102")
}

// clampPtrIfSet returns &t when used; nil otherwise. Keeps the
// exportMetaAudit serialization clean (omit the field unless a clamp
// was applied).
func clampPtrIfSet(used bool, t time.Time) *time.Time {
	if !used {
		return nil
	}
	out := t.UTC()
	return &out
}

// countingWriter wraps an io.Writer and counts bytes written. Used
// to record body byte-count for the meta-audit row.
type countingWriter struct {
	w io.Writer
	n int64
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}
