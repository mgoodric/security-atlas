// Package unifiedlog is the read-only aggregator for the slice-124 unified
// audit-log endpoint. It UNION-ALLs across the nine per-domain audit-log tables
// (`decision_audit_log`, `evidence_audit_log`, `exception_audit_log`,
// `sample_audit_log`, `audit_period_audit_log`, `aggregation_rule_audit_log`,
// `feature_flag_audit_log`, `me_audit_log`, `walkthrough_audit_log`) and
// projects each row to the canonical [Entry] shape.
//
// # Read-only contract
//
// The package exports exactly ONE function — [Query]. There is no exported
// writer, no exported inserter, no `Store` type that wraps a *pgx.Conn / Tx.
// The type system enforces read-only-ness: a caller cannot accidentally
// import this package and write to an audit-log table.
//
// # Tenant isolation
//
// The aggregator does NOT accept a tenant_id parameter (slice-124 anti-criterion
// P0-A5). The caller MUST establish the tenant context on the transaction via
// `tenancy.ApplyTenant` BEFORE calling Query. Postgres RLS on each underlying
// audit-log table then filters rows automatically; the query never sees rows
// from other tenants because the table's `tenant_read` policy denies them.
//
// The aggregator MUST run as `atlas_app` (the RLS-enforced role). Using
// `atlas_service_account` or any role with BYPASSRLS would defeat the
// entire tenant-isolation contract (slice-124 anti-criterion P0-A4).
//
// # Extension pattern
//
// A new domain audit-log table added in a future slice can be wired into
// this aggregator by:
//
//  1. Adding the table's SELECT branch to the UNION ALL in
//     `internal/db/queries/unified_audit_log.sql` (project to the canonical
//     8-column shape, NULL-ing absent columns into `payload_json`).
//  2. Adding the new kind enum to [Kind] and to [AllKinds].
//  3. Updating the integration test in `internal/api/adminauditlog/`
//     to seed + assert the new table's rows.
//
// No code change is required in this Go file beyond the [Kind] addition.
package unifiedlog

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

// Kind is the canonical kind enum. Each value maps 1:1 to one of the nine
// underlying audit-log tables.
type Kind string

const (
	KindDecision        Kind = "decision"
	KindEvidence        Kind = "evidence"
	KindException       Kind = "exception"
	KindSample          Kind = "sample"
	KindAuditPeriod     Kind = "audit_period"
	KindAggregationRule Kind = "aggregation_rule"
	KindFeatureFlag     Kind = "feature_flag"
	KindMe              Kind = "me"
	KindWalkthrough     Kind = "walkthrough"
)

// SubjectModule constants (slice 180). The module identifier tagged onto
// every audit-log write. `SubjectModuleCore` covers every primitive shipping
// today (Control / Risk / Evidence / Scope / Framework / Policy and their
// dependents). When the privacy sibling module lands (v2+, gated on canvas
// OQ #7 resolution), its writes will tag a `"privacy"` value defined in the
// privacy module's package — NOT here — to preserve module isolation.
//
// The DB column defaults to `'core'` so legacy rows and any future write
// path that forgets to set the field explicitly still land as core (safer
// default than `""` or `"unknown"`).
const (
	SubjectModuleCore = "core"
)

// AllKinds is the canonical list in declaration order. Used by tests + the
// handler's CSV parser to validate request-side kind filters.
var AllKinds = []Kind{
	KindDecision,
	KindEvidence,
	KindException,
	KindSample,
	KindAuditPeriod,
	KindAggregationRule,
	KindFeatureFlag,
	KindMe,
	KindWalkthrough,
}

// IsCanonical reports whether k is one of the nine canonical kinds.
func IsCanonical(k Kind) bool {
	for _, candidate := range AllKinds {
		if candidate == k {
			return true
		}
	}
	return false
}

// Entry is the canonical row shape exposed to callers. Every underlying
// audit-log table is projected to this shape; columns the source table
// does not carry land in [Entry.PayloadJSON].
//
// RowID is the audit-row's own UUID primary key from the underlying table.
// It is GUARANTEED unique per row across the UNION (each base table's PK is
// independently unique and the kind discriminator separates branches), which
// is load-bearing for cursor pagination's strict-greater-than tiebreaker
// when many rows share the same occurred_at.
//
// ActorName (slice 129) is the human-readable display name resolved by
// LEFT JOIN against `users.display_name`. It is nil when no users row
// matches the actor_id — this is the normal case for bootstrap-key
// callers and credential-only callers (their actor_id is a credential id
// like "key_foo" rather than a UUID, or a system actor like "seeder").
// The wire shape exposes nil as JSON `null`; consumers must tolerate it.
//
// SubjectModule (slice 180) tags which module owns the writing code path.
// Every row shipped today tags `"core"`; when the privacy sibling module
// lands (v2+), its writes tag `"privacy"`. The column defaults to `"core"`
// at the DB layer so legacy rows + any future write that forgets to set
// the column explicitly still land as `"core"` (a safer default than
// `""` or `"unknown"`).
type Entry struct {
	OccurredAt    time.Time       `json:"occurred_at"`
	ActorID       string          `json:"actor_id"`
	ActorName     *string         `json:"actor_name"`
	TenantID      uuid.UUID       `json:"tenant_id"`
	Kind          Kind            `json:"kind"`
	TargetType    string          `json:"target_type"`
	TargetID      string          `json:"target_id"`
	Action        string          `json:"action"`
	RowID         uuid.UUID       `json:"row_id"`
	SubjectModule string          `json:"subject_module"`
	PayloadJSON   json.RawMessage `json:"payload_json"`
}

// Cursor is the opaque pagination token's deserialized form. The HTTP layer
// is responsible for base64-encoding it on the wire; callers passing the
// cursor back in [QueryParams] should treat it as opaque.
type Cursor struct {
	OccurredAt time.Time `json:"occurred_at"`
	Kind       Kind      `json:"kind"`
	RowID      uuid.UUID `json:"row_id"`
}

// QueryParams is the input to [Query]. From + To are required; the rest are
// optional. The maximum page size is enforced by the caller (the handler
// caps to 1000 rows + signals "more available" via NextCursor).
type QueryParams struct {
	From time.Time
	To   time.Time

	// ActorFilter, if non-empty, restricts the result to rows whose
	// actor_id equals this string. Cross-domain actor identifiers vary
	// (user_id, credential_id, "system") so the filter is a literal
	// exact match.
	ActorFilter string

	// KindFilter, if non-empty, restricts the result to rows whose kind
	// is one of the supplied values. Empty means "all nine kinds".
	KindFilter []Kind

	// Cursor, if non-nil, resumes from the given position. The cursor's
	// (OccurredAt, Kind, TargetID) tuple is the strict-greater-than
	// boundary in the WHERE clause.
	Cursor *Cursor

	// Limit caps the row count returned. The aggregator does NOT enforce
	// an upper bound on Limit — that's the caller's responsibility. The
	// slice-124 handler passes 1001 for a 1000-row page so it can detect
	// "more available" without an extra round-trip.
	Limit int

	// CallerIsPrivileged (slice 270) is the Go-side trust signal that
	// controls the row-visibility WHERE predicate. When true, the
	// predicate short-circuits and visibility is unchanged from slice
	// 124. When false, feature_flag rows are hidden and me-rows are
	// restricted to those whose actor_id equals CallerUserID — the
	// non-privileged shape consumed by the slice 270 `/v1/activity/unified`
	// endpoint.
	//
	// MUST be derived from the caller's credential + user_roles probe,
	// NEVER from a URL-controllable filter (slice 270 P0-A5).
	CallerIsPrivileged bool

	// CallerUserID (slice 270) is the caller's user_id (the UUID
	// `me_audit_log.user_id::text` value). Used only when
	// CallerIsPrivileged is false; ignored otherwise. Empty string is
	// acceptable when privileged.
	CallerUserID string

	// ExcludeReadTelemetry (slice 669) is the view-only deny flag. When
	// true, the SQL drops `decision`-kind rows whose `action = 'read'`
	// (the high-volume internal authz read-telemetry the app emits while
	// auditing its own GET reads) so the Activity feed defaults to
	// mutating/business events. When false, every row is returned —
	// the pre-slice-669 shape. This is a presentation concern only; the
	// underlying append-only ledger is unchanged (canvas invariant #2).
	// It never hides security-relevant mutations: auth/role/tenant/
	// exception writes are NOT `decision`/`read` rows.
	ExcludeReadTelemetry bool
}

// Query executes the unified UNION ALL against the nine audit-log tables.
// The caller MUST have applied the tenant context on q's underlying
// transaction (via `tenancy.ApplyTenant`) before calling Query; this
// function does NOT thread the tenant id explicitly (slice-124 anti-criterion
// P0-A5).
//
// Returns the matched entries, the cursor to resume from (nil when no further
// pages are available, i.e. when len(entries) < params.Limit), and any DB
// error. A zero-result query returns (nil, nil, nil) — not an error.
//
// The function NEVER writes to any audit-log table (slice-124 anti-criterion
// P0-A1). The handler's meta-audit (AC-10) is the caller's responsibility,
// written via dbx.InsertMeAuditLog AFTER a successful Query.
func Query(ctx context.Context, q *dbx.Queries, params QueryParams) ([]Entry, *Cursor, error) {
	if q == nil {
		return nil, nil, fmt.Errorf("unifiedlog: nil dbx queries")
	}
	if params.From.IsZero() || params.To.IsZero() {
		return nil, nil, fmt.Errorf("unifiedlog: from and to are required")
	}
	if !params.To.After(params.From) {
		return nil, nil, fmt.Errorf("unifiedlog: to must be after from")
	}
	if params.Limit <= 0 {
		return nil, nil, fmt.Errorf("unifiedlog: limit must be positive")
	}

	arg := dbx.ListUnifiedAuditLogParams{
		FromTs:               pgtype.Timestamptz{Time: params.From, Valid: true},
		ToTs:                 pgtype.Timestamptz{Time: params.To, Valid: true},
		ActorFilter:          params.ActorFilter,
		KindFilterCsv:        joinKinds(params.KindFilter),
		CallerIsPrivileged:   params.CallerIsPrivileged,
		CallerUserID:         params.CallerUserID,
		ExcludeReadTelemetry: params.ExcludeReadTelemetry,
		LimitN:               int32(params.Limit),
	}
	if params.Cursor != nil {
		arg.CursorTs = pgtype.Timestamptz{Time: params.Cursor.OccurredAt, Valid: true}
		arg.CursorKind = string(params.Cursor.Kind)
		arg.CursorRowID = params.Cursor.RowID.String()
	}

	rows, err := q.ListUnifiedAuditLog(ctx, arg)
	if err != nil {
		return nil, nil, fmt.Errorf("unifiedlog: list: %w", err)
	}

	entries := make([]Entry, 0, len(rows))
	for _, r := range rows {
		entries = append(entries, Entry{
			OccurredAt:    r.OccurredAt.Time,
			ActorID:       r.ActorID,
			ActorName:     r.ActorName,
			TenantID:      r.TenantID.Bytes,
			Kind:          Kind(r.Kind),
			TargetType:    r.TargetType,
			TargetID:      r.TargetID,
			Action:        r.Action,
			RowID:         r.RowID.Bytes,
			SubjectModule: r.SubjectModule,
			PayloadJSON:   json.RawMessage(r.PayloadJson),
		})
	}

	// The aggregator returns up to Limit rows verbatim. Trimming + cursor
	// derivation is the caller's responsibility: it knows the page size
	// (Limit - 1 when the caller used the "peek extra row" convention)
	// and can stitch the next-cursor off the last in-page row.
	//
	// We return (entries, nil, nil) here regardless of count. The handler
	// can derive its own Cursor by inspecting entries[pageSize-1] when
	// len(entries) > pageSize.
	return entries, nil, nil
}

// joinKinds is the CSV serializer for the KindFilter. Empty slice -> empty
// string -> "match all kinds" branch in the SQL.
func joinKinds(kinds []Kind) string {
	if len(kinds) == 0 {
		return ""
	}
	out := ""
	for i, k := range kinds {
		if i > 0 {
			out += ","
		}
		out += string(k)
	}
	return out
}
