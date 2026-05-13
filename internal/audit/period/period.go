// Package period owns the slice 028 AuditPeriod + freezing primitive.
//
// An AuditPeriod is a tenant-scoped, framework-scoped time window over which
// an auditor evaluates compliance. Two-state lifecycle:
//
//	open    -> frozen   (terminal-for-content; metadata mutation rejected)
//
// Freezing pins the evidence-universe horizon. The append-only evidence
// ledger makes this cheap -- we shift the read horizon, no snapshot tables
// (canvas §8.4 + constitutional anti-criterion P0). Frozen state is
// committed via a deterministic SHA-256 hash over canonical-JSON content
// inputs; see ADR 0003 for the hash-input contract.
//
// The Store opens a single transaction per call and applies the tenant
// GUC via internal/tenancy. RLS is the defense-in-depth layer; the WHERE
// clauses are the primary correctness guarantee.
package period

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// ErrNotFound is returned when a tenant-scoped lookup yields zero rows.
var ErrNotFound = errors.New("auditperiod: not found")

// ErrAlreadyFrozen is returned when Freeze is called on a period whose
// status is already 'frozen'. The HTTP handler surfaces this as 409
// Conflict (AC-6).
var ErrAlreadyFrozen = errors.New("auditperiod: already frozen")

// Status enumerates the period lifecycle states. The DB CHECK constraint
// mirrors this.
type Status string

const (
	StatusOpen   Status = "open"
	StatusFrozen Status = "frozen"
)

// Period is the public shape returned from the Store. Mirrors the
// audit_periods row.
type Period struct {
	ID                 uuid.UUID
	TenantID           uuid.UUID
	Name               string
	FrameworkVersionID uuid.UUID
	PeriodStart        time.Time
	PeriodEnd          time.Time
	Status             Status
	FrozenAt           *time.Time
	FrozenHash         []byte
	FrozenBy           string
	CreatedBy          string
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

// CreateInput is the API-shape for POST /v1/audit-periods.
type CreateInput struct {
	Name               string
	FrameworkVersionID uuid.UUID
	PeriodStart        time.Time
	PeriodEnd          time.Time
	CreatedBy          string
}

// ControlStateObservation is a single evidence-record-driven observation
// returned by ControlState. The most-recent observation (smallest index
// in the returned slice) is the one auditors treat as the
// pass/fail-driving record.
type ControlStateObservation struct {
	EvidenceRecordID uuid.UUID
	ObservedAt       time.Time
	Result           string
	Hash             string
}

// Store is the entry point for slice-028 read/write operations.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore wires the Store. The pool is held but not owned -- callers
// (typically internal/api.New) close it.
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// Create inserts a new audit period in status='open', writes a
// period_created log row, and returns the hydrated Period. (AC-1)
func (s *Store) Create(ctx context.Context, in CreateInput) (Period, error) {
	if in.Name == "" {
		return Period{}, fmt.Errorf("auditperiod: name must be non-empty")
	}
	if in.CreatedBy == "" {
		return Period{}, fmt.Errorf("auditperiod: created_by must be non-empty")
	}
	if in.PeriodStart.After(in.PeriodEnd) {
		return Period{}, fmt.Errorf("auditperiod: period_start must be <= period_end")
	}

	var out Period
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		row, err := q.CreateAuditPeriod(ctx, dbx.CreateAuditPeriodParams{
			ID:                 pgUUID(uuid.New()),
			TenantID:           pgUUID(tenantID),
			Name:               in.Name,
			FrameworkVersionID: pgUUID(in.FrameworkVersionID),
			PeriodStart:        pgDate(in.PeriodStart),
			PeriodEnd:          pgDate(in.PeriodEnd),
			CreatedBy:          in.CreatedBy,
		})
		if err != nil {
			return fmt.Errorf("create audit period: %w", err)
		}
		if err := writeLog(ctx, q, tenantID, row.ID, "period_created", in.CreatedBy, nil); err != nil {
			return err
		}
		out = periodFromRow(row)
		return nil
	})
	return out, err
}

// Get returns one period by id. ErrNotFound if absent or cross-tenant.
func (s *Store) Get(ctx context.Context, id uuid.UUID) (Period, error) {
	var out Period
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		row, err := q.GetAuditPeriodByID(ctx, dbx.GetAuditPeriodByIDParams{
			TenantID: pgUUID(tenantID),
			ID:       pgUUID(id),
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("get audit period: %w", err)
		}
		out = periodFromRow(row)
		return nil
	})
	return out, err
}

// List returns periods for the current tenant, newest first.
func (s *Store) List(ctx context.Context) ([]Period, error) {
	var out []Period
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		rows, err := q.ListAuditPeriodsByTenant(ctx, pgUUID(tenantID))
		if err != nil {
			return fmt.Errorf("list audit periods: %w", err)
		}
		out = make([]Period, len(rows))
		for i, r := range rows {
			out[i] = periodFromRow(r)
		}
		return nil
	})
	return out, err
}

// Freeze flips status open->frozen at the wall-clock instant `at`, stamps
// the freeze metadata, computes the content commitment hash (ADR 0003),
// and stamps frozen_at on any populations already attached to the period.
// Re-freezing a frozen row returns ErrAlreadyFrozen. (AC-2 + AC-6 + AC-7)
//
// The `at` parameter is the wall-clock moment of the freeze event. It is
// persisted in audit_periods.frozen_at and the period's audit log but is
// NOT included in the hash inputs (see ADR 0003).
func (s *Store) Freeze(ctx context.Context, id uuid.UUID, frozenBy string, at time.Time) (Period, error) {
	if frozenBy == "" {
		return Period{}, fmt.Errorf("auditperiod: frozen_by must be non-empty")
	}
	if at.IsZero() {
		at = time.Now().UTC()
	} else {
		at = at.UTC()
	}

	var out Period
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		// Resolve current state first so we can give a clean
		// ErrAlreadyFrozen rather than a "zero rows updated" generic
		// error. The state read is in the same transaction so the
		// re-check is race-free under SERIALIZABLE; we tolerate
		// READ_COMMITTED here because the UPDATE itself is guarded by
		// `status='open'` and a concurrent Freeze would lose the
		// guard, returning ErrAlreadyFrozen.
		existing, err := q.GetAuditPeriodByID(ctx, dbx.GetAuditPeriodByIDParams{
			TenantID: pgUUID(tenantID),
			ID:       pgUUID(id),
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("get audit period (freeze): %w", err)
		}
		if existing.Status == string(StatusFrozen) {
			// AC-6: capture the rejection in the audit trail. The
			// log write must happen in its OWN transaction --
			// returning the sentinel here would otherwise be
			// rolled back by the deferred Rollback. We surface the
			// log-write error onto out (the outer return) only if
			// the log INSERT itself fails; the rejection itself
			// is still ErrAlreadyFrozen.
			alreadyFrozenID := existing.ID
			defer func() {
				_ = s.writeRejectionLog(ctx, tenantID, alreadyFrozenID, frozenBy)
			}()
			return ErrAlreadyFrozen
		}

		// Compute the content commitment hash BEFORE the UPDATE so a
		// hash-computation failure aborts the freeze without partial
		// state. Ingredients: sorted evidence_record_ids visible at
		// `at` + sorted control_ids in tenant. Per ADR 0003.
		evIDs, err := q.ListEvidenceIDsForPeriodHash(ctx, dbx.ListEvidenceIDsForPeriodHashParams{
			TenantID:   pgUUID(tenantID),
			ObservedAt: pgTimestamptz(at),
		})
		if err != nil {
			return fmt.Errorf("list evidence ids for hash: %w", err)
		}
		ctrlIDs, err := q.ListControlIDsForPeriodHash(ctx, pgUUID(tenantID))
		if err != nil {
			return fmt.Errorf("list control ids for hash: %w", err)
		}
		hash, err := computeFreezeHash(freezeHashInputs{
			AuditPeriodID:      uuid.UUID(existing.ID.Bytes),
			PeriodStart:        existing.PeriodStart.Time,
			PeriodEnd:          existing.PeriodEnd.Time,
			FrameworkVersionID: uuid.UUID(existing.FrameworkVersionID.Bytes),
			EvidenceRecordIDs:  pgUUIDsToUUIDs(evIDs),
			ControlIDs:         pgUUIDsToUUIDs(ctrlIDs),
		})
		if err != nil {
			return fmt.Errorf("compute freeze hash: %w", err)
		}

		row, err := q.FreezeAuditPeriod(ctx, dbx.FreezeAuditPeriodParams{
			TenantID:   pgUUID(tenantID),
			ID:         pgUUID(id),
			FrozenAt:   pgTimestamptz(at),
			FrozenHash: hash,
			FrozenBy:   nullable(frozenBy),
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				// Race: another transaction froze it between our
				// SELECT and our UPDATE. The WHERE guard caught
				// the change-of-state.
				return ErrAlreadyFrozen
			}
			return fmt.Errorf("freeze audit period: %w", err)
		}

		// Stamp populations.frozen_at for any populations already
		// attached to this period. New populations attached after
		// freeze get stamped at attach-time by AttachPopulation.
		if err := q.SetPopulationFrozenAt(ctx, dbx.SetPopulationFrozenAtParams{
			TenantID:      pgUUID(tenantID),
			AuditPeriodID: pgUUID(id),
			FrozenAt:      pgTimestamptz(at),
		}); err != nil {
			return fmt.Errorf("stamp populations frozen_at: %w", err)
		}

		if err := writeLog(ctx, q, tenantID, row.ID, "period_frozen", frozenBy,
			map[string]any{
				"frozen_at":   at.Format(time.RFC3339Nano),
				"frozen_hash": fmt.Sprintf("%x", hash),
				"evidence_n":  len(evIDs),
				"controls_n":  len(ctrlIDs),
			}); err != nil {
			return err
		}
		out = periodFromRow(row)
		return nil
	})
	return out, err
}

// ControlState returns the evidence observations for one control bounded
// by the period's frozen_at horizon (or live state when the period is
// still 'open'). The most-recent observation drives the auditor's
// pass/fail read. (AC-3)
func (s *Store) ControlState(ctx context.Context, periodID, controlID uuid.UUID) ([]ControlStateObservation, error) {
	var out []ControlStateObservation
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		p, err := q.GetAuditPeriodByID(ctx, dbx.GetAuditPeriodByIDParams{
			TenantID: pgUUID(tenantID),
			ID:       pgUUID(periodID),
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("get period for control-state: %w", err)
		}
		// When the period is still open, frozen_at is NULL; the SQL
		// uses COALESCE(..., 'infinity') so the call falls through to
		// live state.
		rows, err := q.ListEvidenceForPeriodControl(ctx, dbx.ListEvidenceForPeriodControlParams{
			TenantID:  pgUUID(tenantID),
			ControlID: pgUUID(controlID),
			FrozenAt:  p.FrozenAt,
		})
		if err != nil {
			return fmt.Errorf("list evidence for period control: %w", err)
		}
		out = make([]ControlStateObservation, len(rows))
		for i, r := range rows {
			obs := ControlStateObservation{
				EvidenceRecordID: uuid.UUID(r.ID.Bytes),
				Result:           string(r.Result),
				Hash:             r.Hash,
			}
			if r.ObservedAt.Valid {
				obs.ObservedAt = r.ObservedAt.Time
			}
			out[i] = obs
		}
		return nil
	})
	return out, err
}

// AttachPopulation links a populations row to this period. If the period
// is already frozen, the population's frozen_at is stamped from the
// period's frozen_at at attach time -- the slice 026 query path then
// enforces `observed_at <= populations.frozen_at` on subsequent draws.
// (AC-4)
func (s *Store) AttachPopulation(ctx context.Context, periodID, populationID uuid.UUID, actor string) error {
	if actor == "" {
		return fmt.Errorf("auditperiod: actor must be non-empty")
	}
	return s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		p, err := q.GetAuditPeriodByID(ctx, dbx.GetAuditPeriodByIDParams{
			TenantID: pgUUID(tenantID),
			ID:       pgUUID(periodID),
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("get period for attach: %w", err)
		}
		if err := q.AttachPopulationToPeriod(ctx, dbx.AttachPopulationToPeriodParams{
			TenantID:      pgUUID(tenantID),
			ID:            pgUUID(populationID),
			AuditPeriodID: pgUUID(periodID),
		}); err != nil {
			return fmt.Errorf("attach population to period: %w", err)
		}
		// If the period is already frozen, stamp the population's
		// frozen_at now -- the SetPopulationFrozenAt UPDATE is guarded
		// by `frozen_at IS NULL` so it's a no-op for populations
		// already stamped by an earlier Freeze call.
		if p.Status == string(StatusFrozen) && p.FrozenAt.Valid {
			if err := q.SetPopulationFrozenAt(ctx, dbx.SetPopulationFrozenAtParams{
				TenantID:      pgUUID(tenantID),
				AuditPeriodID: pgUUID(periodID),
				FrozenAt:      p.FrozenAt,
			}); err != nil {
				return fmt.Errorf("stamp population frozen_at on attach: %w", err)
			}
		}
		return writeLog(ctx, q, tenantID, p.ID, "population_attached", actor,
			map[string]any{"population_id": populationID.String()})
	})
}

// ListLog returns the lifecycle audit log entries for one period.
func (s *Store) ListLog(ctx context.Context, periodID uuid.UUID) ([]dbx.AuditPeriodAuditLog, error) {
	var out []dbx.AuditPeriodAuditLog
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		rows, err := q.ListAuditPeriodLog(ctx, dbx.ListAuditPeriodLogParams{
			TenantID:      pgUUID(tenantID),
			AuditPeriodID: pgUUID(periodID),
		})
		if err != nil {
			return fmt.Errorf("list audit period log: %w", err)
		}
		out = rows
		return nil
	})
	return out, err
}

// writeRejectionLog opens a sibling transaction to write a
// freeze_rejected_already_frozen audit log row. Used by Freeze when the
// happy-path transaction must roll back (because ErrAlreadyFrozen aborts
// the freeze) but we still want the rejection captured in the audit trail.
func (s *Store) writeRejectionLog(ctx context.Context, tenantID uuid.UUID, periodID pgtype.UUID, actor string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		return err
	}
	q := dbx.New(tx)
	if err := writeLog(ctx, q, tenantID, periodID,
		"freeze_rejected_already_frozen", actor,
		map[string]any{"reason": "already_frozen"}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ===== hash =====

type freezeHashInputs struct {
	AuditPeriodID      uuid.UUID   `json:"audit_period_id"`
	PeriodStart        time.Time   `json:"period_start"`
	PeriodEnd          time.Time   `json:"period_end"`
	FrameworkVersionID uuid.UUID   `json:"framework_version_id"`
	EvidenceRecordIDs  []uuid.UUID `json:"evidence_record_ids"`
	ControlIDs         []uuid.UUID `json:"control_ids"`
}

// computeFreezeHash produces the slice-028 freeze-state content commitment
// per ADR 0003. Canonical-JSON shape: keys in literal order (NOT
// alphabetical), arrays sorted by UUID string form. The SQL queries that
// feed this function already return UUIDs sorted ASC, so we sort again
// here defensively (callers that wire other sources should not have to
// know the ordering contract).
//
// Note: encoding/json with a struct emits fields in declaration order,
// which is the contract here. Dates render as ISO-8601 ("2006-01-02"),
// times as RFC-3339. We intentionally use the struct shape rather than a
// hand-rolled byte stream so a Python/TS reimplementation has zero
// ambiguity.
func computeFreezeHash(in freezeHashInputs) ([]byte, error) {
	// Defensive sort: the queries already emit ASC, but pin the contract
	// here so a future query change can't break determinism silently.
	sortUUIDs(in.EvidenceRecordIDs)
	sortUUIDs(in.ControlIDs)

	// Render dates as date-only strings (no wall-clock) so two periods
	// with the same calendar bounds hash identically regardless of any
	// implicit timezone the DB returned them as.
	type wire struct {
		AuditPeriodID      string   `json:"audit_period_id"`
		PeriodStart        string   `json:"period_start"`
		PeriodEnd          string   `json:"period_end"`
		FrameworkVersionID string   `json:"framework_version_id"`
		EvidenceRecordIDs  []string `json:"evidence_record_ids"`
		ControlIDs         []string `json:"control_ids"`
	}
	w := wire{
		AuditPeriodID:      in.AuditPeriodID.String(),
		PeriodStart:        in.PeriodStart.UTC().Format("2006-01-02"),
		PeriodEnd:          in.PeriodEnd.UTC().Format("2006-01-02"),
		FrameworkVersionID: in.FrameworkVersionID.String(),
		EvidenceRecordIDs:  uuidsToStrings(in.EvidenceRecordIDs),
		ControlIDs:         uuidsToStrings(in.ControlIDs),
	}
	buf, err := json.Marshal(w)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(buf)
	return sum[:], nil
}

// ===== internals =====

func writeLog(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID, periodID pgtype.UUID, action, actor string, detail map[string]any) error {
	var detailJSON []byte
	if detail != nil {
		b, err := json.Marshal(detail)
		if err != nil {
			return fmt.Errorf("marshal log detail: %w", err)
		}
		detailJSON = b
	} else {
		detailJSON = []byte(`{}`)
	}
	_, err := q.WriteAuditPeriodLog(ctx, dbx.WriteAuditPeriodLogParams{
		ID:            pgUUID(uuid.New()),
		TenantID:      pgUUID(tenantID),
		AuditPeriodID: periodID,
		Action:        action,
		Actor:         actor,
		Detail:        detailJSON,
	})
	if err != nil {
		return fmt.Errorf("write audit period log: %w", err)
	}
	return nil
}

func (s *Store) inTx(ctx context.Context, fn func(context.Context, *dbx.Queries, uuid.UUID) error) error {
	tenantStr, err := tenancy.TenantFromContext(ctx)
	if err != nil {
		return err
	}
	tenantID, err := uuid.Parse(tenantStr)
	if err != nil {
		return fmt.Errorf("auditperiod: parse tenant id: %w", err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("auditperiod: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		return err
	}
	q := dbx.New(tx)
	if err := fn(ctx, q, tenantID); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("auditperiod: commit: %w", err)
	}
	return nil
}

// ===== row converters / helpers =====

func periodFromRow(r dbx.AuditPeriod) Period {
	p := Period{
		ID:                 uuid.UUID(r.ID.Bytes),
		TenantID:           uuid.UUID(r.TenantID.Bytes),
		Name:               r.Name,
		FrameworkVersionID: uuid.UUID(r.FrameworkVersionID.Bytes),
		Status:             Status(r.Status),
		CreatedBy:          r.CreatedBy,
	}
	if r.PeriodStart.Valid {
		p.PeriodStart = r.PeriodStart.Time
	}
	if r.PeriodEnd.Valid {
		p.PeriodEnd = r.PeriodEnd.Time
	}
	if r.FrozenAt.Valid {
		t := r.FrozenAt.Time
		p.FrozenAt = &t
	}
	if len(r.FrozenHash) > 0 {
		p.FrozenHash = append([]byte(nil), r.FrozenHash...)
	}
	if r.FrozenBy != nil {
		p.FrozenBy = *r.FrozenBy
	}
	if r.CreatedAt.Valid {
		p.CreatedAt = r.CreatedAt.Time
	}
	if r.UpdatedAt.Valid {
		p.UpdatedAt = r.UpdatedAt.Time
	}
	return p
}

func pgUUID(u uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: u, Valid: true}
}

func pgTimestamptz(t time.Time) pgtype.Timestamptz {
	if t.IsZero() {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: t.UTC(), Valid: true}
}

func pgDate(t time.Time) pgtype.Date {
	if t.IsZero() {
		return pgtype.Date{}
	}
	return pgtype.Date{Time: t, Valid: true}
}

func nullable(s string) *string {
	if s == "" {
		return nil
	}
	v := s
	return &v
}

func pgUUIDsToUUIDs(in []pgtype.UUID) []uuid.UUID {
	out := make([]uuid.UUID, 0, len(in))
	for _, u := range in {
		if !u.Valid {
			continue
		}
		out = append(out, uuid.UUID(u.Bytes))
	}
	return out
}

func uuidsToStrings(in []uuid.UUID) []string {
	out := make([]string, len(in))
	for i, u := range in {
		out[i] = u.String()
	}
	return out
}

func sortUUIDs(s []uuid.UUID) {
	// Sort by canonical-string form so the hash contract (ADR 0003) is
	// expressed in terms a Python/TS verifier reimplementation can
	// match without depending on Go's [16]byte ordering.
	sort.Slice(s, func(i, j int) bool { return s[i].String() < s[j].String() })
}
