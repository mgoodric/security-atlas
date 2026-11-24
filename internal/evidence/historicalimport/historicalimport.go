// Package historicalimport loads prior-period evidence into the ledger with
// its real observed_at (#1742): last year's audit results, or history from
// a spreadsheet or another GRC tool.
//
// An import is one batch, admin only, and all or nothing. Each record goes
// through the ingest stage's normal pipeline (ingest.Service.ProcessInTx:
// schema validation, kind scope, redaction, hashing, audit logging) on a
// Service built ForHistoricalImport, which lifts the past observed_at skew
// limit and keeps the future one. ingested_at is the real write time, never
// backdated, so a replay or a frozen period that predates the import never
// counts the imported records (#1699).
//
// Audit periods: a record whose observed_at falls in an open period joins it
// like any other evidence. One that falls in a frozen period fails the whole
// import before anything is written, unless the request names that period in
// AmendFrozen; then the period is re-frozen as a new freeze revision holding
// the batch's records in its window (period.AmendInTx). The original freeze
// is never rewritten.
package historicalimport

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	evidencev1 "github.com/mgoodric/security-atlas/gen/proto/evidence/v1"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/audit/period"
	"github.com/mgoodric/security-atlas/internal/authz"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/evidence/ingest"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// MaxRecords caps one import batch.
const MaxRecords = 10000

// MaxSourceLen caps the batch's free-text source.
const MaxSourceLen = 500

var (
	// ErrNotAdmin refuses a credential that is not a tenant admin, or is a
	// platform system credential.
	ErrNotAdmin = errors.New("historicalimport: admin credential required")
	// ErrInvalid is a malformed request: no records, too many, no source.
	ErrInvalid = errors.New("historicalimport: invalid request")
)

// Request is one import batch.
type Request struct {
	// Source names where the batch came from, e.g. "FY25 SOC 2 Type II
	// report". Required.
	Source string
	// AmendFrozen names the frozen audit periods the import may re-freeze.
	AmendFrozen []uuid.UUID
	Records     []*evidencev1.EvidenceRecord
}

// FrozenHit is a frozen audit period the batch has records in.
type FrozenHit struct {
	PeriodID uuid.UUID
	Name     string
	Records  int
}

// FrozenPeriodError refuses a batch with records in frozen periods it did
// not name in AmendFrozen. Nothing was written.
type FrozenPeriodError struct {
	Hits []FrozenHit
}

func (e *FrozenPeriodError) Error() string {
	parts := make([]string, len(e.Hits))
	for i, h := range e.Hits {
		parts[i] = fmt.Sprintf("%s (%q, %d records)", h.PeriodID, h.Name, h.Records)
	}
	return "historicalimport: records fall in frozen audit periods not named in amend_frozen: " + strings.Join(parts, ", ")
}

// AmendTargetError refuses an AmendFrozen entry that is not a frozen period
// this batch has records in. Nothing was written.
type AmendTargetError struct {
	PeriodID uuid.UUID
	Reason   string
}

func (e *AmendTargetError) Error() string {
	return fmt.Sprintf("historicalimport: amend_frozen %s: %s", e.PeriodID, e.Reason)
}

// RecordError is the ingest refusal of one record; the whole batch rolled
// back. The record's rejection is in evidence_audit_log under the batch.
type RecordError struct {
	Index    int
	Decision ingest.Decision
	Err      error
}

func (e *RecordError) Error() string {
	return fmt.Sprintf("record[%d] %s: %v", e.Index, e.Decision, e.Err)
}

func (e *RecordError) Unwrap() error { return e.Err }

// Amendment is one frozen period the import re-froze.
type Amendment struct {
	PeriodID uuid.UUID
	Revision period.Revision
}

// Result is a committed import.
type Result struct {
	BatchID    uuid.UUID
	Receipts   []ingest.Receipt
	Amendments []Amendment
}

// Importer runs imports over the application-role pool.
type Importer struct {
	pool *pgxpool.Pool
	svc  *ingest.Service
}

// New wires an Importer. svc is the platform's ingest service; each import
// derives its own ForHistoricalImport copy from it.
func New(pool *pgxpool.Pool, svc *ingest.Service) *Importer {
	return &Importer{pool: pool, svc: svc}
}

// Import runs one batch for cred, which must be a tenant admin.
func (im *Importer) Import(ctx context.Context, cred credstore.Credential, req Request) (Result, error) {
	if cred.System || !authz.IsTenantAdmin(cred) {
		return Result{}, ErrNotAdmin
	}
	if err := validate(req); err != nil {
		return Result{}, err
	}
	batch := ingest.ImportBatch{ID: uuid.New(), Source: strings.TrimSpace(req.Source), Importer: cred.ID}
	svc := im.svc.ForHistoricalImport(batch)

	tenantCtx, err := tenancy.WithTenant(ctx, cred.TenantID)
	if err != nil {
		return Result{}, fmt.Errorf("historicalimport: tenant context: %w", err)
	}
	tenantCtx, outbox := ingest.WithAuditOutbox(tenantCtx)

	res := Result{BatchID: batch.ID}
	var refused error
	err = pgx.BeginTxFunc(tenantCtx, im.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := tenancy.ApplyTenant(tenantCtx, tx); err != nil {
			return fmt.Errorf("apply tenant: %w", err)
		}
		q := dbx.New(tx)
		first, last := dayRange(req.Records)
		periods, err := q.LockAuditPeriodsOverlapping(tenantCtx, dbx.LockAuditPeriodsOverlappingParams{
			TenantID: pgtype.UUID{Bytes: uuid.MustParse(cred.TenantID), Valid: true},
			FirstDay: pgtype.Date{Time: first, Valid: true},
			LastDay:  pgtype.Date{Time: last, Valid: true},
		})
		if err != nil {
			return fmt.Errorf("lock audit periods: %w", err)
		}
		amend, err := planAmendments(periods, req)
		if err != nil {
			refused = err
			return err
		}

		accepted := make(map[uuid.UUID]time.Time, len(req.Records))
		for i, rec := range req.Records {
			receipt, decision, err := svc.ProcessInTx(tenantCtx, tx, rec, cred, ingest.LedgerOptions{})
			if err != nil {
				refused = &RecordError{Index: i, Decision: decision, Err: err}
				return refused
			}
			res.Receipts = append(res.Receipts, receipt)
			if decision == ingest.DecisionAccepted {
				id, perr := uuid.Parse(receipt.RecordID)
				if perr != nil {
					return fmt.Errorf("receipt record id: %w", perr)
				}
				accepted[id] = rec.GetObservedAt().AsTime()
			}
		}

		for _, p := range amend {
			var ids []uuid.UUID
			for id, observed := range accepted {
				if inWindow(p, observed) {
					ids = append(ids, id)
				}
			}
			if len(ids) == 0 {
				// Every record in the window was already in the ledger
				// (deduplicated); the period's population is unchanged.
				continue
			}
			rev, err := period.AmendInTx(tenantCtx, q, p, period.AmendInput{
				ImportBatchID:     batch.ID,
				Actor:             cred.ID,
				EvidenceRecordIDs: ids,
			})
			if err != nil {
				return fmt.Errorf("amend period %s: %w", uuid.UUID(p.ID.Bytes), err)
			}
			res.Amendments = append(res.Amendments, Amendment{PeriodID: uuid.UUID(p.ID.Bytes), Revision: rev})
		}
		return nil
	})
	if err != nil {
		if refused != nil {
			var fpe *FrozenPeriodError
			var ate *AmendTargetError
			if errors.As(refused, &fpe) || errors.As(refused, &ate) {
				svc.AuditRefusedBatch(ctx, req.Records, cred, refused.Error())
			}
			return Result{}, refused
		}
		return Result{}, err
	}
	outbox.Flush(tenantCtx)
	return res, nil
}

func validate(req Request) error {
	if strings.TrimSpace(req.Source) == "" {
		return fmt.Errorf("%w: source is required", ErrInvalid)
	}
	if len(req.Source) > MaxSourceLen {
		return fmt.Errorf("%w: source exceeds %d characters", ErrInvalid, MaxSourceLen)
	}
	if len(req.Records) == 0 {
		return fmt.Errorf("%w: no records", ErrInvalid)
	}
	if len(req.Records) > MaxRecords {
		return fmt.Errorf("%w: batch exceeds %d records", ErrInvalid, MaxRecords)
	}
	for i, rec := range req.Records {
		if rec == nil || rec.GetObservedAt() == nil {
			return fmt.Errorf("%w: record[%d] has no observed_at", ErrInvalid, i)
		}
	}
	return nil
}

// dayRange is the UTC calendar days of the earliest and latest observed_at.
func dayRange(recs []*evidencev1.EvidenceRecord) (time.Time, time.Time) {
	var first, last time.Time
	for i, rec := range recs {
		d := utcDay(rec.GetObservedAt().AsTime())
		if i == 0 || d.Before(first) {
			first = d
		}
		if i == 0 || d.After(last) {
			last = d
		}
	}
	return first, last
}

func utcDay(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// inWindow reports whether observed falls on a calendar day (UTC) inside
// p's [period_start, period_end].
func inWindow(p dbx.AuditPeriod, observed time.Time) bool {
	d := utcDay(observed)
	return !d.Before(utcDay(p.PeriodStart.Time)) && !d.After(utcDay(p.PeriodEnd.Time))
}

// planAmendments checks the batch against the locked periods. It returns
// the frozen periods to amend, a *FrozenPeriodError for frozen periods hit
// but not named, or an *AmendTargetError for a named period that is not a
// frozen period the batch hits.
func planAmendments(periods []dbx.AuditPeriod, req Request) ([]dbx.AuditPeriod, error) {
	named := make(map[uuid.UUID]bool, len(req.AmendFrozen))
	for _, id := range req.AmendFrozen {
		named[id] = true
	}
	hit := map[uuid.UUID]bool{}
	var amend []dbx.AuditPeriod
	var unnamed []FrozenHit
	for _, p := range periods {
		if p.Status != string(period.StatusFrozen) {
			continue
		}
		n := 0
		for _, rec := range req.Records {
			if inWindow(p, rec.GetObservedAt().AsTime()) {
				n++
			}
		}
		if n == 0 {
			continue
		}
		id := uuid.UUID(p.ID.Bytes)
		hit[id] = true
		if named[id] {
			amend = append(amend, p)
			continue
		}
		unnamed = append(unnamed, FrozenHit{PeriodID: id, Name: p.Name, Records: n})
	}
	if len(unnamed) > 0 {
		sort.Slice(unnamed, func(i, j int) bool { return unnamed[i].PeriodID.String() < unnamed[j].PeriodID.String() })
		return nil, &FrozenPeriodError{Hits: unnamed}
	}
	for _, id := range req.AmendFrozen {
		if !hit[id] {
			return nil, &AmendTargetError{PeriodID: id, Reason: "not a frozen audit period with records in this batch"}
		}
	}
	return amend, nil
}
