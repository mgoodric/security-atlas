// Package demoseed assembles + applies a comprehensive demo dataset
// for a single tenant (slice 205).
//
// The seeder is invoked by the `atlas-cli demo seed` subcommand. It
// is opt-in (env-var-gated at the CLI layer), idempotent on
// `--tenant-slug`, and creates ONE polished tenant with every primitive
// the product surfaces populated:
//
//   - controls (~50 spread across security families)
//   - risks (~20)
//   - evidence_records (~200, spread across the prior 12 months)
//   - policies (~5, linked to controls via the slice-019 path)
//   - audit_periods (~3 — 1 frozen, 2 open)
//   - populations + samples + sample_evidence (~5 sampled per period)
//   - walkthroughs (~5, finalized)
//   - exceptions (~10, mixed lifecycle states)
//   - board_briefs + board_packs (~3 total)
//   - vendors (~10)
//   - audit-log rows (~50 across the primitives)
//
// LOAD-BEARING DESIGN — write path uses the BYPASSRLS auth pool
// (atlas_migrate). Demo data is written cross-tenant from the platform
// operator's session tenant into a NEW tenant that does not yet have an
// RLS context. Mirrors the slice-143 admintenants pattern and the
// slice-037 bootstrap pattern exactly.
//
// LOAD-BEARING DESIGN — append-only ledger invariant honored. The
// seeder synthesizes evidence_records rows by INSERTing them directly
// (the standard ingestion path); the schema's RLS + composite-FK
// guards refuse a tenant mismatch (P0-A6). The seeder does NOT touch
// evaluation tables (control_evaluations, evidence_freshness) — those
// are computed downstream by the slice-016 freshness/drift evaluator
// when it next runs over the tenant's evidence.
//
// LOAD-BEARING DESIGN — 10-row safety guard (AC-3). Before writing any
// row, the seeder counts controls + risks + evidence_records in the
// target tenant. If any exceeds 10, the seeder refuses to run — the
// CLI exits with a clear error pointing at the slug-uniqueness guard.
// The intent is to ensure that a slug typo or accidental re-use cannot
// pollute a real tenant with synthetic data.
package demoseed

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/auth/password"
)

// DemoSeedVersion is the slice-205 version stamp written into every
// synthesized audit-log row's payload_json under the `demo_seed_v` key.
// Forensic queries filter via `WHERE payload_json ? 'demo_seed_v'` to
// exclude demo noise from real activity (threat-model R + AC-9).
//
// Bump this constant in any future slice that re-shapes the demo
// dataset so re-applying the seeder with the new shape leaves a
// distinguishable forensic mark.
const DemoSeedVersion = "205"

// DefaultScale is the row-count multiplier applied to every per-primitive
// floor when no --scale flag is passed to the CLI. The floors in this
// package are sized for scale=1.0; scale=2.0 doubles every count;
// scale=0.5 halves them. AC-5 makes this knob a required surface.
const DefaultScale = 1.0

// MinScale + MaxScale clamp operator input. Below 0.1 produces
// rows-rounded-to-zero floors that violate the per-primitive AC-6
// "every primitive has at least one row" invariant. Above 5.0 starts
// to stress the >10-row guard's intent — past that, the operator
// should be loading real data not demo data.
const (
	MinScale = 0.1
	MaxScale = 5.0
)

// PopulatedRowCap is the threshold above which the seeder refuses to
// write into the target tenant (AC-3). Set to 10 — chosen as
// generously above the slice-037 bootstrap seed's row count (it writes
// 1 row each into scope_dimensions / scope_cells / users /
// local_credentials = 4 rows) while still being well below any
// realistic populated-tenant footprint. The intent: distinguish "fresh
// new tenant" from "real tenant that already has data".
const PopulatedRowCap = 10

// Refusal sentinels — Apply returns these (wrapped) when it declines to
// seed an existing tenant. They are NOT internal errors: the caller (the
// admindemo HTTP handler) maps them to a 409 Conflict with an operator-
// facing "already loaded" message rather than a 500. errors.Is-matchable.
var (
	// ErrTenantPopulated — the target tenant already carries the
	// slice-205 forensic mark AND has more than PopulatedRowCap rows in
	// controls/risks/evidence_records (i.e. the demo dataset is already
	// loaded). Re-seeding is refused; tear down first.
	ErrTenantPopulated = errors.New("demoseed: tenant already populated with demo data")
	// ErrTenantUnmarked — a tenant with the target slug exists but does
	// NOT carry the slice-205 forensic mark (likely operator-created).
	// Refused to avoid mixing synthetic data into a real tenant.
	ErrTenantUnmarked = errors.New("demoseed: tenant exists without the demo forensic mark")
)

// Seeder writes the slice-205 demo dataset into a target tenant.
//
// Construction is intentionally narrow: callers pass exactly the
// BYPASSRLS pool (the auth pool, atlas_migrate). The seeder never
// reads from the RLS-bound atlas_app pool — every read is cross-tenant
// or is checking a row the seeder itself wrote within the same
// transaction.
type Seeder struct {
	authPool *pgxpool.Pool

	// clock is the time source for synthesized timestamps. Defaults to
	// time.Now but test-overridable so the integration test asserts the
	// 12-month temporal spread without sleeping.
	clock func() time.Time

	// scale is the row-count multiplier. >= MinScale, <= MaxScale.
	scale float64
}

// NewSeeder constructs a Seeder over the BYPASSRLS auth pool. authPool
// must NOT be nil; the CLI surfaces a 503-style error if construction
// fails because of a missing pool.
func NewSeeder(authPool *pgxpool.Pool, scale float64) (*Seeder, error) {
	if authPool == nil {
		return nil, fmt.Errorf("demoseed: NewSeeder: nil auth pool")
	}
	if scale < MinScale || scale > MaxScale {
		return nil, fmt.Errorf("demoseed: scale %g out of range [%g, %g]", scale, MinScale, MaxScale)
	}
	return &Seeder{
		authPool: authPool,
		clock:    time.Now,
		scale:    scale,
	}, nil
}

// WithClock overrides the clock. Test-only.
func (s *Seeder) WithClock(fn func() time.Time) *Seeder {
	s.clock = fn
	return s
}

// Result summarizes a successful seeder Apply. The CLI prints these
// counts so the operator sees the dataset shape it just created. Each
// count is the actual number of rows INSERTed (post-scale).
type Result struct {
	TenantID          uuid.UUID
	TenantSlug        string
	UserID            uuid.UUID
	UserEmail         string
	PlaintextPasswd   string // one-time only; never persisted; never logged
	Controls          int
	Risks             int
	Evidence          int
	Policies          int
	Vendors           int
	AuditPeriods      int
	Populations       int
	Samples           int
	SampleEvidence    int
	Walkthroughs      int
	Exceptions        int
	BoardBriefs       int
	BoardPacks        int
	FrameworkScopes   int
	AuditLogRows      int
	OrgUnits          int      // slice 678 / ATLAS-028
	Decisions         int      // slice 678 / ATLAS-028
	RoleUsers         int      // slice 678 / ATLAS-037
	QuestionnaireQns  int      // slice 678 / ATLAS-037 (questions in the seeded questionnaire)
	FrameworkReqs     int      // slice 682 / ATLAS-037 (posture spine: framework_requirements)
	STRMEdges         int      // slice 682 / ATLAS-037 (posture spine: fw_to_scf_edges)
	ControlsAnchored  int      // slice 682 / ATLAS-037 (controls carrying scf_anchor_id)
	EvidenceKindsUsed []string // for D3 surface — which kinds got rows
	Idempotent        bool     // true if no rows were INSERTed (already-seeded path)
}

// ApplyInput carries the per-invocation knobs for Apply.
type ApplyInput struct {
	// Slug is the tenant slug — passed to atlas-cli demo seed --tenant-slug.
	// The seeder both uses this for the tenant's row + as the canonical
	// idempotency key.
	Slug string

	// ActorUserID is the super_admin user_id that invoked the seeder.
	// Written into super_admin_audit_log.actor_user_id +
	// me_audit_log.user_id. Defaults to the all-zero UUID when the CLI
	// is invoked outside a super_admin context (the CLI rejects this
	// before reaching here, but defense-in-depth).
	ActorUserID uuid.UUID

	// ActorTenantID is the actor's session tenant_id — written into
	// super_admin_audit_log.actor_tenant_id + me_audit_log.tenant_id.
	// May be uuid.Nil when called from a non-tenant-bound context (the
	// audit-log row then anchors to the new demo tenant; see comment
	// inside writeAuditLog).
	ActorTenantID uuid.UUID
}

// Apply writes the demo dataset to the named tenant slug. The function
// is idempotent on Slug:
//
//   - If a tenant with that slug already exists AND has been seeded
//     (demo_seed_v rows present in its audit log), Apply returns the
//     existing tenant's metadata + Result.Idempotent=true with zero
//     new rows. The plaintext password is NOT re-generated; the caller
//     must rotate it via the standard /v1/admin/users surface.
//
//   - If a tenant with that slug already exists AND has > PopulatedRowCap
//     rows in any of (controls / risks / evidence_records) AND was NOT
//     previously seeded, Apply refuses with an error pointing at the
//     guard rationale (AC-3).
//
//   - Otherwise Apply creates the tenant + every demo row in one
//     BYPASSRLS transaction (P0-A6: write-path bypasses RLS but every
//     row carries the correct tenant_id, so subsequent atlas_app reads
//     respect RLS).
func (s *Seeder) Apply(ctx context.Context, in ApplyInput) (Result, error) {
	if err := validateSlug(in.Slug); err != nil {
		return Result{}, err
	}

	// 1. Idempotency probe — read tenant by slug under the auth pool.
	existing, err := s.lookupTenantBySlug(ctx, in.Slug)
	if err != nil {
		return Result{}, fmt.Errorf("demoseed: lookup tenant: %w", err)
	}
	if existing != nil {
		// Tenant exists. Check whether it was seeded by us.
		seeded, err := s.tenantWasSeeded(ctx, *existing)
		if err != nil {
			return Result{}, fmt.Errorf("demoseed: probe seeded state: %w", err)
		}
		if seeded {
			// Idempotent re-run: report the existing state, do nothing.
			return Result{
				TenantID:   *existing,
				TenantSlug: in.Slug,
				Idempotent: true,
			}, nil
		}
		// Existing tenant but NOT seeded by us — refuse (AC-3).
		populated, err := s.tenantIsPopulated(ctx, *existing)
		if err != nil {
			return Result{}, fmt.Errorf("demoseed: populated probe: %w", err)
		}
		if populated {
			return Result{}, fmt.Errorf(
				"refusing to seed: tenant %q already has > %d rows in controls/risks/evidence_records; pick a fresh --tenant-slug: %w",
				in.Slug, PopulatedRowCap, ErrTenantPopulated,
			)
		}
		// Existing tenant with < 10 rows that we did NOT create. Still
		// refuse — the operator likely created this manually and the
		// seeder would silently mix synthetic into manual.
		return Result{}, fmt.Errorf(
			"refusing to seed: tenant %q already exists but does not carry the slice-205 forensic mark; pick a fresh --tenant-slug: %w",
			in.Slug, ErrTenantUnmarked,
		)
	}

	// 2. Generate the one-time password.
	plaintext, err := GenerateDemoPassword()
	if err != nil {
		return Result{}, fmt.Errorf("demoseed: generate password: %w", err)
	}
	passwordHash, err := password.Hash(plaintext)
	if err != nil {
		return Result{}, fmt.Errorf("demoseed: hash password: %w", err)
	}

	// 3. Build the in-memory fixture set. Pure function — no DB writes.
	fixtures := s.buildFixtures(in.Slug)

	// 4. One BYPASSRLS transaction for every write.
	tx, err := s.authPool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Result{}, fmt.Errorf("demoseed: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Stash the tenant id on ctx so the writer helpers can pull it
	// without threading it through every signature.
	ctx = withTenant(ctx, fixtures.tenant.ID)

	if err := writeTenantRow(ctx, tx, fixtures.tenant, in.ActorUserID); err != nil {
		return Result{}, err
	}
	if err := writeScopeDimensionAndCell(ctx, tx, fixtures); err != nil {
		return Result{}, err
	}
	if err := writeUserAndCreds(ctx, tx, fixtures.user, passwordHash); err != nil {
		return Result{}, err
	}
	if err := writeAuditLogRow(ctx, tx, "demo_seed_apply",
		in.ActorUserID, in.ActorTenantID, fixtures.tenant.ID,
		map[string]any{"slug": fixtures.tenant.Slug, "demo_seed_v": DemoSeedVersion},
	); err != nil {
		return Result{}, err
	}

	// Domain rows — each helper returns the number of rows it inserted
	// so the Result is honest about scale.
	cCount, err := writeControls(ctx, tx, fixtures)
	if err != nil {
		return Result{}, err
	}
	// org_units BEFORE risks: risks.org_unit_id FKs into org_units
	// (slice 678 / ATLAS-028).
	ouCount, err := writeOrgUnits(ctx, tx, fixtures)
	if err != nil {
		return Result{}, err
	}
	rCount, err := writeRisks(ctx, tx, fixtures)
	if err != nil {
		return Result{}, err
	}
	// decisions AFTER risks: decision_risks links FK into risks
	// (slice 678 / ATLAS-028).
	dCount, err := writeDecisions(ctx, tx, fixtures)
	if err != nil {
		return Result{}, err
	}
	pCount, err := writePolicies(ctx, tx, fixtures)
	if err != nil {
		return Result{}, err
	}
	// role-holder users + api_keys + acks AFTER policies: the ack roster
	// matches users' api_keys.owner_roles against policies'
	// acknowledgment_required_roles (slice 678 / ATLAS-037). Reuses the
	// admin user's password hash (these users never log in interactively).
	ruCount, err := writeRoleUsers(ctx, tx, fixtures, passwordHash)
	if err != nil {
		return Result{}, err
	}
	// one questionnaire so /questionnaires is demonstrable (ATLAS-037).
	qCount, err := writeQuestionnaire(ctx, tx, fixtures)
	if err != nil {
		return Result{}, err
	}
	vCount, err := writeVendors(ctx, tx, fixtures)
	if err != nil {
		return Result{}, err
	}
	apCount, popCount, sCount, seCount, err := writeAuditPeriodsAndSamples(ctx, tx, fixtures)
	if err != nil {
		return Result{}, err
	}
	// Framework-posture spine (slice 682 / ATLAS-037): anchor the demo
	// controls to SCF anchors + seed framework_requirements + STRM edges so
	// the dashboard "Framework posture" tiles render real coverage. Runs
	// AFTER writeControls (the controls exist to be anchored) and AFTER
	// writeAuditPeriodsAndSamples (the framework-version creation order is
	// settled) but BEFORE writeFrameworkScopes (which reuses the demo
	// framework_version the spine guarantees is current).
	reqCount, edgeCount, ctrlAnchored, err := writeFrameworkPostureSpine(ctx, tx, fixtures)
	if err != nil {
		return Result{}, err
	}
	eCount, kindsUsed, err := writeEvidence(ctx, tx, fixtures)
	if err != nil {
		return Result{}, err
	}
	wCount, err := writeWalkthroughs(ctx, tx, fixtures)
	if err != nil {
		return Result{}, err
	}
	xCount, err := writeExceptions(ctx, tx, fixtures)
	if err != nil {
		return Result{}, err
	}
	bbCount, bpCount, err := writeBoardReports(ctx, tx, fixtures)
	if err != nil {
		return Result{}, err
	}
	fsCount, err := writeFrameworkScopes(ctx, tx, fixtures)
	if err != nil {
		return Result{}, err
	}
	auditCount, err := writeDemoAuditTrail(ctx, tx, fixtures)
	if err != nil {
		return Result{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return Result{}, fmt.Errorf("demoseed: commit: %w", err)
	}

	return Result{
		TenantID:          fixtures.tenant.ID,
		TenantSlug:        fixtures.tenant.Slug,
		UserID:            fixtures.user.ID,
		UserEmail:         fixtures.user.Email,
		PlaintextPasswd:   plaintext,
		Controls:          cCount,
		Risks:             rCount,
		Evidence:          eCount,
		Policies:          pCount,
		Vendors:           vCount,
		AuditPeriods:      apCount,
		Populations:       popCount,
		Samples:           sCount,
		SampleEvidence:    seCount,
		Walkthroughs:      wCount,
		Exceptions:        xCount,
		BoardBriefs:       bbCount,
		BoardPacks:        bpCount,
		FrameworkScopes:   fsCount,
		AuditLogRows:      auditCount + 1, // +1 for the demo_seed_apply meta row
		OrgUnits:          ouCount,
		Decisions:         dCount,
		RoleUsers:         ruCount,
		QuestionnaireQns:  qCount,
		FrameworkReqs:     reqCount,
		STRMEdges:         edgeCount,
		ControlsAnchored:  ctrlAnchored,
		EvidenceKindsUsed: kindsUsed,
	}, nil
}

// Teardown removes the named demo tenant + every row anchored to it.
// Symmetric to Apply — only operates against a tenant that carries the
// slice-205 forensic mark; refuses otherwise. AC documented in spec D6.
//
// Writes ONE super_admin_audit_log + one me_audit_log row with action
// 'demo_seed_teardown' before deleting (so the teardown is recorded
// even though the demo audit-log rows themselves are about to vanish).
//
// Note: this does NOT verify the operator's authority — that gate is
// the CLI's `ATLAS_ENABLE_DEMO_SEED=true` env-var check (AC-14) +
// any super_admin credentials the operator brings. The seeder itself
// is a library; the gate lives in the caller.
func (s *Seeder) Teardown(ctx context.Context, slug string, actorUserID uuid.UUID, actorTenantID uuid.UUID) error {
	if err := validateSlug(slug); err != nil {
		return err
	}
	tenantID, err := s.lookupTenantBySlug(ctx, slug)
	if err != nil {
		return fmt.Errorf("demoseed: teardown lookup: %w", err)
	}
	if tenantID == nil {
		return fmt.Errorf("demoseed: teardown: tenant %q not found", slug)
	}
	seeded, err := s.tenantWasSeeded(ctx, *tenantID)
	if err != nil {
		return fmt.Errorf("demoseed: teardown probe: %w", err)
	}
	if !seeded {
		return fmt.Errorf("demoseed: teardown: tenant %q does not carry the slice-205 forensic mark; refusing to delete", slug)
	}

	tx, err := s.authPool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("demoseed: teardown begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Write the teardown meta-audit row FIRST (before deleting).
	if err := writeAuditLogRow(ctx, tx, "demo_seed_teardown",
		actorUserID, actorTenantID, *tenantID,
		map[string]any{"slug": slug, "demo_seed_v": DemoSeedVersion},
	); err != nil {
		return err
	}

	// Now cascade-delete. The dependency order is bottom-up.
	stmts := []string{
		`DELETE FROM sample_annotations WHERE tenant_id = $1`,
		`DELETE FROM sample_evidence    WHERE tenant_id = $1`,
		`DELETE FROM samples            WHERE tenant_id = $1`,
		`DELETE FROM populations        WHERE tenant_id = $1`,
		`DELETE FROM walkthrough_audit_log WHERE tenant_id = $1`,
		`DELETE FROM walkthrough_attachments WHERE tenant_id = $1`,
		`DELETE FROM walkthroughs       WHERE tenant_id = $1`,
		`DELETE FROM exception_audit_log WHERE tenant_id = $1`,
		`DELETE FROM exceptions         WHERE tenant_id = $1`,
		`DELETE FROM audit_period_audit_log WHERE tenant_id = $1`,
		`DELETE FROM audit_periods      WHERE tenant_id = $1`,
		`DELETE FROM evidence_audit_log WHERE tenant_id = $1`,
		`DELETE FROM evidence_records   WHERE tenant_id = $1`,
		// Slice 678 demo-breadth rows. decision link tables BEFORE
		// decisions (FK CASCADE handles it, but explicit keeps the sweep
		// deterministic); decisions BEFORE risks is NOT required
		// (decision_risks → risks is ON DELETE CASCADE), but risks'
		// org_unit_id → org_units is ON DELETE SET NULL, so org_units may
		// be swept any time after risks. questionnaire children CASCADE
		// from the parent, swept explicitly for determinism.
		`DELETE FROM decision_risks      WHERE tenant_id = $1`,
		`DELETE FROM decision_controls   WHERE tenant_id = $1`,
		`DELETE FROM decision_exceptions WHERE tenant_id = $1`,
		`DELETE FROM decision_scope_predicates WHERE tenant_id = $1`,
		`DELETE FROM decisions           WHERE tenant_id = $1`,
		`DELETE FROM questionnaire_answers   WHERE tenant_id = $1`,
		`DELETE FROM questionnaire_questions  WHERE tenant_id = $1`,
		`DELETE FROM questionnaires           WHERE tenant_id = $1`,
		`DELETE FROM risk_control_links WHERE tenant_id = $1`,
		`DELETE FROM risks              WHERE tenant_id = $1`,
		`DELETE FROM org_units          WHERE tenant_id = $1`,
		`DELETE FROM policy_acknowledgments WHERE tenant_id = $1`,
		`DELETE FROM policies           WHERE tenant_id = $1`,
		`DELETE FROM controls           WHERE tenant_id = $1`,
		`DELETE FROM framework_scopes   WHERE tenant_id = $1`,
		// Tenant-scoped demo framework rows: Apply writes a fallback
		// `frameworks` + `framework_versions` pair ONLY when the global
		// SCF catalog (tenant_id IS NULL) is absent (writers.go
		// writeAuditPeriodsAndSamples). When the catalog IS present Apply
		// adopts it and writes neither — so these deletes are 0-row no-ops
		// in that case. The `WHERE tenant_id = $1` predicate can never
		// match the global catalog rows (their tenant_id IS NULL), so the
		// catalog is safe by construction (AC-4 / canvas invariant #6).
		// framework_versions deletes BEFORE frameworks: framework_scopes +
		// audit_periods (both ON DELETE RESTRICT into framework_versions)
		// are already swept above; frameworks.latest_version_id is
		// ON DELETE SET NULL so the version delete does not block on the
		// parent (AC-3).
		`DELETE FROM framework_versions WHERE tenant_id = $1`,
		`DELETE FROM frameworks         WHERE tenant_id = $1`,
		`DELETE FROM vendor_scope_cells WHERE tenant_id = $1`,
		`DELETE FROM vendors            WHERE tenant_id = $1`,
		`DELETE FROM board_briefs       WHERE tenant_id = $1`,
		`DELETE FROM board_packs        WHERE tenant_id = $1`,
		`DELETE FROM me_audit_log       WHERE tenant_id = $1`,
		`DELETE FROM user_roles         WHERE tenant_id = $1`,
		// api_keys carry the role-holder users' roster roles (slice 678).
		// No FK ordering constraint vs users (issued_by is FK-less), but
		// sweep before users for a clean dependency order.
		`DELETE FROM api_keys           WHERE tenant_id = $1`,
		`DELETE FROM local_credentials  WHERE tenant_id = $1`,
		`DELETE FROM sessions           WHERE tenant_id = $1`,
		`DELETE FROM users              WHERE tenant_id = $1`,
		`DELETE FROM scope_cells        WHERE tenant_id = $1`,
		`DELETE FROM scope_dimensions   WHERE tenant_id = $1`,
		`DELETE FROM tenants            WHERE id = $1`,
	}
	for _, stmt := range stmts {
		if _, err := tx.Exec(ctx, stmt, *tenantID); err != nil {
			// Ignore unknown-table errors — older / missing tables
			// won't have any rows for this tenant anyway; the
			// teardown is meant to be best-effort. Fail loudly on
			// other errors.
			if pgIsUndefinedTable(err) {
				continue
			}
			return fmt.Errorf("demoseed: teardown exec %s: %w", strings.SplitN(stmt, " ", 4)[2], err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("demoseed: teardown commit: %w", err)
	}
	return nil
}

// ----- internals -----

// lookupTenantBySlug returns the tenant id if one with the given slug
// exists, or nil if not. Reads via the auth pool (BYPASSRLS) because
// this seeder runs outside the actor's session tenant context.
func (s *Seeder) lookupTenantBySlug(ctx context.Context, slug string) (*uuid.UUID, error) {
	var id uuid.UUID
	err := s.authPool.QueryRow(ctx,
		`SELECT id FROM tenants WHERE slug = $1`, slug,
	).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &id, nil
}

// tenantWasSeeded returns true if at least one me_audit_log row on the
// target tenant carries a `demo_seed_v` key in its payload. Reads via
// the auth pool.
func (s *Seeder) tenantWasSeeded(ctx context.Context, tenantID uuid.UUID) (bool, error) {
	var has bool
	err := s.authPool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM me_audit_log
			WHERE tenant_id = $1
			  AND action IN ('demo_seed_apply', 'demo_seed_teardown')
		)
	`, tenantID).Scan(&has)
	if err != nil {
		return false, err
	}
	return has, nil
}

// tenantIsPopulated returns true if any of (controls, risks,
// evidence_records) has > PopulatedRowCap rows for the target tenant.
// The seeder refuses to write into such a tenant (AC-3). Reads via the
// auth pool.
func (s *Seeder) tenantIsPopulated(ctx context.Context, tenantID uuid.UUID) (bool, error) {
	for _, table := range []string{"controls", "risks", "evidence_records"} {
		var n int
		err := s.authPool.QueryRow(ctx,
			fmt.Sprintf(`SELECT count(*) FROM %s WHERE tenant_id = $1`, table),
			tenantID,
		).Scan(&n)
		if err != nil {
			return false, fmt.Errorf("count %s: %w", table, err)
		}
		if n > PopulatedRowCap {
			return true, nil
		}
	}
	return false, nil
}

// pgIsUndefinedTable returns true when err is a Postgres SQLSTATE
// 42P01 (undefined_table). Used by Teardown so a future schema delta
// that removes a table the seeder used to write into doesn't break
// teardown of older demo data.
func pgIsUndefinedTable(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "SQLSTATE 42P01") ||
		strings.Contains(err.Error(), "does not exist")
}

// validateSlug enforces the same shape as slice 143 — lower-case ASCII
// + digits + hyphen, 1-63 chars, must start with alnum. The seeder
// re-validates because it is the FIRST gate at the CLI level (the
// admintenants handler does the same check; defense-in-depth).
func validateSlug(slug string) error {
	if slug == "" {
		return fmt.Errorf("demoseed: --tenant-slug is required")
	}
	if len(slug) > 63 {
		return fmt.Errorf("demoseed: --tenant-slug exceeds 63-char cap")
	}
	// Inline matcher to avoid an import cycle.
	first := slug[0]
	if (first < 'a' || first > 'z') && (first < '0' || first > '9') {
		return fmt.Errorf("demoseed: --tenant-slug must start with [a-z0-9]")
	}
	for i := 1; i < len(slug); i++ {
		c := slug[i]
		switch {
		case c >= 'a' && c <= 'z':
		case c >= '0' && c <= '9':
		case c == '-':
		default:
			return fmt.Errorf("demoseed: --tenant-slug has invalid char %q", string(c))
		}
	}
	// Note: "demo-" prefix is recommended for UI clarity but not enforced
	// — operators may need a custom slug for screenshots.
	return nil
}

// hashCanonicalJSON serializes v as canonical JSON (key-sorted) and
// returns the sha256 hex digest. Used by the seeder to compute
// walkthroughs.canonical_hash + audit-period frozen_hash.
func hashCanonicalJSON(v any) ([]byte, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("demoseed: canonical hash marshal: %w", err)
	}
	h := sha256.Sum256(b)
	return h[:], nil
}

// hexString returns hex.EncodeToString as a convenience for callers
// that need the scope-cell dimensions_hash shape (TEXT, not BYTEA).
func hexString(b []byte) string { return hex.EncodeToString(b) }
