package securityawareness

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// EvidenceKindHRISWorkerLifecycle is the evidence_kind the HRIS connectors
// (atlas-rippling / atlas-bamboohr) push for every roster worker. The roster
// sync reads these ledger records instead of calling the HRIS APIs directly:
// connectors are separate push-only processes (constitutional invariant #3),
// so the ledger is the platform's only view of the HRIS roster. Mirrors
// connectors/hris/workerrecord.EvidenceKind without importing the connector
// module.
const EvidenceKindHRISWorkerLifecycle = "hris.worker_lifecycle.v1"

// DefaultRosterSyncInterval is the cadence at which RosterSyncer.Run sweeps.
// Daily matches the connectors' recommended pull cadence; the platform binary
// overrides via ATLAS_ROSTER_SYNC_INTERVAL for dev loops.
const DefaultRosterSyncInterval = 24 * time.Hour

// hrisStatusTerminated is the only worker.EmploymentStatus value that maps to
// an inactive training person. on_leave / pending / unknown workers stay
// active: they remain in the training population, and an ambiguous status
// must never silently drop someone from overdue reminders.
const hrisStatusTerminated = "terminated"

// HRISRosterRecord is one worker's latest hris.worker_lifecycle.v1 ledger
// payload, reduced to the roster fields. The HRIS schema deliberately carries
// no display name (over-collection guard P0-491-3); work email is the only
// human-readable identity.
type HRISRosterRecord struct {
	SourceHRIS       string
	WorkerID         string
	EmploymentStatus string
	WorkEmail        string
}

// MapHRISPerson maps a worker-lifecycle record to an UpsertPersonInput with
// source "hris". The upsert key is "<source_hris>:<worker_id>" — the vendor
// prefix keeps worker ids from different HRIS systems from colliding.
// DisplayName falls back work email → key, since the HRIS payload has no name
// field. Only a terminated worker maps to active=false.
func MapHRISPerson(r HRISRosterRecord) (UpsertPersonInput, error) {
	vendor := strings.TrimSpace(r.SourceHRIS)
	workerID := strings.TrimSpace(r.WorkerID)
	if vendor == "" || workerID == "" {
		return UpsertPersonInput{}, ErrInvalidInput
	}
	key := vendor + ":" + workerID
	display := strings.TrimSpace(r.WorkEmail)
	if display == "" {
		display = key
	}
	var email *string
	if e := strings.TrimSpace(r.WorkEmail); e != "" {
		email = &e
	}
	active := !strings.EqualFold(strings.TrimSpace(r.EmploymentStatus), hrisStatusTerminated)
	return UpsertPersonInput{
		Source:         PersonSourceHRIS,
		SourcePersonID: &key,
		DisplayName:    display,
		WorkEmail:      email,
		Active:         &active,
	}, nil
}

// SCIMRosterRecord is one SCIM-managed platform user (users.scim_managed),
// reduced to the roster fields.
type SCIMRosterRecord struct {
	UserID      string
	DisplayName string
	Email       string
	Active      bool
}

// MapSCIMPerson maps a SCIM-managed user to an UpsertPersonInput with source
// "scim", keyed by the platform user id (always present and stable, unlike
// the optional scim_external_id). The user id is also the RecipientUserID:
// a SCIM person IS a platform user, so overdue reminders can reach them.
func MapSCIMPerson(r SCIMRosterRecord) (UpsertPersonInput, error) {
	id := strings.TrimSpace(r.UserID)
	if id == "" {
		return UpsertPersonInput{}, ErrInvalidInput
	}
	display := strings.TrimSpace(r.DisplayName)
	if display == "" {
		display = strings.TrimSpace(r.Email)
	}
	if display == "" {
		display = id
	}
	var email *string
	if e := strings.TrimSpace(r.Email); e != "" {
		email = &e
	}
	active := r.Active
	return UpsertPersonInput{
		Source:          PersonSourceSCIM,
		SourcePersonID:  &id,
		DisplayName:     display,
		WorkEmail:       email,
		RecipientUserID: &id,
		Active:          &active,
	}, nil
}

// RosterSyncReport summarizes one sweep for observability.
type RosterSyncReport struct {
	Tenants    int
	HRISPeople int
	SCIMPeople int
	Skipped    int
}

// RosterSyncer is the scheduled roster sync (OPENENGINE-658). Tenant
// enumeration runs on the migrator pool (BYPASSRLS) because "all tenants with
// roster sources" has no single tenant context; each tenant's reads and
// upserts then run under tenancy.WithTenant on the app pool, so every write
// into security_training_people is RLS-scoped. UpsertPerson is idempotent on
// (tenant_id, source, source_person_id), so re-running a sweep is a no-op
// diff. A person deactivated at the source (terminated worker, disabled SCIM
// user) is upserted with active=false, which drops them from
// OverdueReminders.
type RosterSyncer struct {
	enum   *pgxpool.Pool
	store  *Store
	logger *slog.Logger
}

// NewRosterSyncer constructs a RosterSyncer. migratorPool MUST be connected
// as the migrator role (BYPASSRLS) — it is used only to enumerate tenant ids.
// appPool is the RLS-enforcing application pool all per-tenant work runs on.
// logger may be nil; a discard logger is substituted.
func NewRosterSyncer(migratorPool, appPool *pgxpool.Pool, logger *slog.Logger) *RosterSyncer {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &RosterSyncer{enum: migratorPool, store: NewStore(appPool), logger: logger}
}

// Run executes the tick loop until ctx is cancelled. The first sweep runs
// immediately so a fresh deploy doesn't sit silent for a full interval.
func (r *RosterSyncer) Run(ctx context.Context, interval time.Duration) error {
	if interval <= 0 {
		interval = DefaultRosterSyncInterval
	}
	r.logger.Info("security awareness roster sync starting", "interval", interval.String())
	if _, err := r.SweepOnce(ctx); err != nil && !errors.Is(err, context.Canceled) {
		r.logger.Error("security awareness roster sync initial sweep", "err", err.Error())
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			r.logger.Info("security awareness roster sync stopping")
			return nil
		case <-ticker.C:
			if _, err := r.SweepOnce(ctx); err != nil && !errors.Is(err, context.Canceled) {
				r.logger.Error("security awareness roster sync sweep", "err", err.Error())
			}
		}
	}
}

// SweepOnce runs a single sync across all tenants with roster sources.
// Per-tenant failures are logged and skipped — one tenant's bad data never
// blocks the rest of the sweep. Exposed for integration tests and Run.
func (r *RosterSyncer) SweepOnce(ctx context.Context) (RosterSyncReport, error) {
	var report RosterSyncReport
	tenantIDs, err := r.listTenants(ctx)
	if err != nil {
		return report, fmt.Errorf("securityawareness: list roster tenants: %w", err)
	}
	for _, tenantID := range tenantIDs {
		select {
		case <-ctx.Done():
			return report, ctx.Err()
		default:
		}
		hris, scim, skipped, err := r.syncTenant(ctx, tenantID)
		if err != nil {
			r.logger.Error("roster sync tenant", "tenant_id", tenantID.String(), "err", err.Error())
			continue
		}
		report.Tenants++
		report.HRISPeople += hris
		report.SCIMPeople += scim
		report.Skipped += skipped
		if hris+scim > 0 {
			r.logger.Info("roster sync tenant complete",
				"tenant_id", tenantID.String(), "hris", hris, "scim", scim, "skipped", skipped)
		}
	}
	return report, nil
}

// listTenants enumerates every tenant with at least one HRIS worker-lifecycle
// evidence record or SCIM-managed user. Runs as the migrator role (BYPASSRLS).
func (r *RosterSyncer) listTenants(ctx context.Context) ([]uuid.UUID, error) {
	rows, err := r.enum.Query(ctx, `
SELECT DISTINCT tenant_id FROM evidence_records WHERE evidence_kind = $1
UNION
SELECT DISTINCT tenant_id FROM users WHERE scim_managed = true`,
		EvidenceKindHRISWorkerLifecycle)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// syncTenant reads the tenant's roster sources under RLS and upserts each
// person. Per-person failures (e.g. a work-email collision with an existing
// person from another source — security_training_people_email_unique is
// source-agnostic) are logged and counted as skipped, never fatal.
func (r *RosterSyncer) syncTenant(ctx context.Context, tenantID uuid.UUID) (hris, scim, skipped int, err error) {
	tenantCtx, err := tenancy.WithTenant(ctx, tenantID.String())
	if err != nil {
		return 0, 0, 0, err
	}
	hrisRecords, recipients, scimRecords, err := r.collect(tenantCtx)
	if err != nil {
		return 0, 0, 0, err
	}
	for _, rec := range hrisRecords {
		in, mapErr := MapHRISPerson(rec)
		if mapErr != nil {
			r.logger.Warn("roster sync: unmappable HRIS record",
				"tenant_id", tenantID.String(), "worker_id", rec.WorkerID)
			skipped++
			continue
		}
		// Preserve an operator-linked recipient: UpsertPerson overwrites
		// recipient_user_id wholesale, and the HRIS payload never carries one.
		if in.RecipientUserID == nil && in.SourcePersonID != nil {
			if linked, ok := recipients[*in.SourcePersonID]; ok {
				in.RecipientUserID = &linked
			}
		}
		if _, upErr := r.store.UpsertPerson(tenantCtx, in); upErr != nil {
			r.logger.Warn("roster sync: HRIS upsert skipped",
				"tenant_id", tenantID.String(), "source_person_id", *in.SourcePersonID, "err", upErr.Error())
			skipped++
			continue
		}
		hris++
	}
	for _, rec := range scimRecords {
		in, mapErr := MapSCIMPerson(rec)
		if mapErr != nil {
			r.logger.Warn("roster sync: unmappable SCIM user",
				"tenant_id", tenantID.String(), "user_id", rec.UserID)
			skipped++
			continue
		}
		if _, upErr := r.store.UpsertPerson(tenantCtx, in); upErr != nil {
			r.logger.Warn("roster sync: SCIM upsert skipped",
				"tenant_id", tenantID.String(), "source_person_id", *in.SourcePersonID, "err", upErr.Error())
			skipped++
			continue
		}
		scim++
	}
	return hris, scim, skipped, nil
}

// collect reads, inside one RLS-scoped transaction: the latest
// worker-lifecycle ledger record per (source_hris, worker_id), the existing
// hris-person recipient links to carry forward, and the SCIM-managed users.
func (r *RosterSyncer) collect(tenantCtx context.Context) ([]HRISRosterRecord, map[string]string, []SCIMRosterRecord, error) {
	var hrisRecords []HRISRosterRecord
	recipients := map[string]string{}
	var scimRecords []SCIMRosterRecord
	err := r.store.inTx(tenantCtx, func(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID) error {
		rows, err := tx.Query(ctx, `
SELECT DISTINCT ON (payload->>'source_hris', payload->>'worker_id')
       COALESCE(payload->>'source_hris', ''),
       COALESCE(payload->>'worker_id', ''),
       COALESCE(payload->>'employment_status', ''),
       COALESCE(payload->>'work_email', '')
FROM evidence_records
WHERE tenant_id = $1 AND evidence_kind = $2
ORDER BY payload->>'source_hris', payload->>'worker_id', observed_at DESC, ingested_at DESC`,
			tenantID, EvidenceKindHRISWorkerLifecycle)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var rec HRISRosterRecord
			if err := rows.Scan(&rec.SourceHRIS, &rec.WorkerID, &rec.EmploymentStatus, &rec.WorkEmail); err != nil {
				return err
			}
			hrisRecords = append(hrisRecords, rec)
		}
		if err := rows.Err(); err != nil {
			return err
		}

		linkRows, err := tx.Query(ctx, `
SELECT source_person_id, recipient_user_id
FROM security_training_people
WHERE tenant_id = $1 AND source = $2
  AND source_person_id IS NOT NULL AND recipient_user_id IS NOT NULL`,
			tenantID, PersonSourceHRIS)
		if err != nil {
			return err
		}
		defer linkRows.Close()
		for linkRows.Next() {
			var key, recipient string
			if err := linkRows.Scan(&key, &recipient); err != nil {
				return err
			}
			recipients[key] = recipient
		}
		if err := linkRows.Err(); err != nil {
			return err
		}

		userRows, err := tx.Query(ctx, `
SELECT id::text, display_name, email, active
FROM users
WHERE tenant_id = $1 AND scim_managed = true`,
			tenantID)
		if err != nil {
			return err
		}
		defer userRows.Close()
		for userRows.Next() {
			var rec SCIMRosterRecord
			if err := userRows.Scan(&rec.UserID, &rec.DisplayName, &rec.Email, &rec.Active); err != nil {
				return err
			}
			scimRecords = append(scimRecords, rec)
		}
		return userRows.Err()
	})
	if err != nil {
		return nil, nil, nil, err
	}
	return hrisRecords, recipients, scimRecords, nil
}
