package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/spf13/cobra"

	"github.com/mgoodric/security-atlas/internal/demoseed"
)

// demoEnableEnvVar is the env-var gate that AC-14 requires. Without
// it, the CLI refuses to invoke `demo seed` / `demo teardown` — even
// when reachable from a developer's shell. P0-A1 enforcement.
const demoEnableEnvVar = "ATLAS_ENABLE_DEMO_SEED"

// demoDatabaseURLEnvVar is the BYPASSRLS pool URL — typically the
// same DATABASE_URL the docker-compose stack hands the
// atlas-bootstrap container.
const demoDatabaseURLEnvVar = "DATABASE_URL"

// demoAppDatabaseURLEnvVar is the RLS-enforced app-role pool URL
// (atlas_app). Slice 671 uses it to drive the post-seed evaluator under
// RLS so the seeded tenant shows real control state instead of "—".
// Matches the platform binary's DATABASE_URL_APP convention
// (cmd/atlas/main.go:95). When unset, the CLI prints a clear hint and
// skips evaluation rather than failing the seed.
const demoAppDatabaseURLEnvVar = "DATABASE_URL_APP"

// demoSeedFlags + demoTeardownFlags hold the per-invocation knobs.
// Held at package scope only because cobra reuses the same Command
// instance across invocations; the underlying tests close over a
// fresh cobra command per test case.
var (
	demoSeedTenantSlug     string
	demoSeedScale          float64
	demoSeedDatabaseURL    string
	demoSeedAppDatabaseURL string

	demoTeardownTenantSlug  string
	demoTeardownDatabaseURL string
)

// newDemoCmd wires `atlas-cli demo {seed,teardown}`.
//
// This is the slice-205 CLI surface. The command is intentionally
// minimal: two verbs, both env-var-gated, both connecting to the
// BYPASSRLS pool (atlas_migrate / DATABASE_URL). It is a maintainer-
// /operator-only surface — there is no HTTP layer for these verbs.
func newDemoCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "demo",
		Short: "demo dataset management (slice 205)",
		Long: `Manage the slice-205 comprehensive demo dataset.

OPT-IN. Requires the ATLAS_ENABLE_DEMO_SEED=true env var. See
docs/getting-started/demo-seed.md for the full operator workflow.`,
	}
	cmd.AddCommand(newDemoSeedCmd())
	cmd.AddCommand(newDemoTeardownCmd())
	return cmd
}

// newDemoSeedCmd wires `atlas-cli demo seed --tenant-slug=<slug>`.
func newDemoSeedCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "seed",
		Short: "create a new demo tenant + populate it with the slice-205 dataset",
		Long: `Create a NEW tenant identified by --tenant-slug and populate it with
the slice-205 demo dataset (~50 controls, ~20 risks, ~200 evidence records,
across 12 months + 3 frameworks + 3 audit periods).

REQUIRES the ATLAS_ENABLE_DEMO_SEED=true env var (AC-14 / P0-A1).

The CLI:

  - Generates a fresh strong password (>= 16 chars, mixed alphabet).
  - Prints the demo admin email + the password ONCE to stdout.
  - Never logs the password, never writes it to disk.
  - Is idempotent on --tenant-slug: re-running the same slug returns
    the existing tenant's metadata without re-seeding (the password
    is NOT re-printed; the operator must rotate via the standard
    /v1/admin/users surface).

Refuses to run if:

  - ATLAS_ENABLE_DEMO_SEED is unset.
  - The target tenant exists AND was not created by demo seed.
  - The target tenant exists AND has > 10 rows in any of
    (controls / risks / evidence_records).`,
		RunE: runDemoSeed,
	}
	cmd.Flags().StringVar(&demoSeedTenantSlug, "tenant-slug", "", "slug for the demo tenant (lower-case alnum + hyphen; suggest demo-*)")
	cmd.Flags().Float64Var(&demoSeedScale, "scale", demoseed.DefaultScale, "row-count multiplier (0.1 to 5.0)")
	cmd.Flags().StringVar(&demoSeedDatabaseURL, "database-url", "", "BYPASSRLS DSN (env: DATABASE_URL)")
	cmd.Flags().StringVar(&demoSeedAppDatabaseURL, "app-database-url", "", "RLS-enforced app-role DSN for post-seed evaluation (env: DATABASE_URL_APP)")
	_ = cmd.MarkFlagRequired("tenant-slug")
	return cmd
}

// newDemoTeardownCmd wires `atlas-cli demo teardown --tenant-slug=<slug>`.
//
// D6: the teardown verb ships in this slice. AC-21 documents the
// "drop the tenant" workflow but a single command is safer + cheaper
// than walking the operator through ~30 DELETE statements.
//
// Teardown refuses to operate on a tenant that does not carry the
// slice-205 forensic mark (the `demo_seed_apply` audit-log row). The
// safety check is a primary guard against typos.
func newDemoTeardownCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "teardown",
		Short: "delete the named demo tenant + every row anchored to it",
		Long: `Delete the named demo tenant + every row anchored to it.

REQUIRES the ATLAS_ENABLE_DEMO_SEED=true env var (AC-14 / P0-A1).

Refuses to operate on a tenant that does not carry the slice-205
forensic mark (the demo_seed_apply audit-log entry). This guard
ensures a typo'd --tenant-slug cannot accidentally erase a real
tenant. Writes one demo_seed_teardown audit-log row before
deleting, so the operator's forensic trail includes the teardown.`,
		RunE: runDemoTeardown,
	}
	cmd.Flags().StringVar(&demoTeardownTenantSlug, "tenant-slug", "", "slug of the demo tenant to delete")
	cmd.Flags().StringVar(&demoTeardownDatabaseURL, "database-url", "", "BYPASSRLS DSN (env: DATABASE_URL)")
	_ = cmd.MarkFlagRequired("tenant-slug")
	return cmd
}

// runDemoSeed executes `atlas-cli demo seed`.
//
// Wire shape:
//
//  1. Env-var gate.
//  2. Resolve BYPASSRLS DSN from --database-url or DATABASE_URL.
//  3. Open one pgxpool (closed on return).
//  4. Construct seeder + Apply.
//  5. On success, print the status line + row counts to stdout.
//
// Exit codes:
//   - 0: success (or idempotent re-run that found existing state)
//   - 1: any error (env gate / DSN missing / DB error / refusal)
func runDemoSeed(cmd *cobra.Command, _ []string) error {
	if !demoEnvEnabled() {
		return fmt.Errorf(
			"%s=true is required to run demo seed (P0-A1 — opt-in only). See docs/getting-started/demo-seed.md",
			demoEnableEnvVar,
		)
	}
	dsn, err := resolveDemoDSN(demoSeedDatabaseURL)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(cmd.Context(), 5*time.Minute)
	defer cancel()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return fmt.Errorf("demo seed: open pool: %w", err)
	}
	defer pool.Close()

	seeder, err := demoseed.NewSeeder(pool, demoSeedScale)
	if err != nil {
		return fmt.Errorf("demo seed: %w", err)
	}
	res, err := seeder.Apply(ctx, demoseed.ApplyInput{
		Slug:          demoSeedTenantSlug,
		ActorUserID:   uuid.Nil, // CLI is invoked with no JWT context
		ActorTenantID: uuid.Nil,
	})
	if err != nil {
		return fmt.Errorf("demo seed: %w", err)
	}

	// Slice 671: drive the REAL evaluator for the seeded tenant so its
	// controls show computed STATE / FRESHNESS / LAST OBSERVED instead of "—".
	// This is a SEPARATE stage AFTER the seed write committed: it reads the
	// ledger and writes only the evaluation read models (invariant #2), scoped
	// to the seeded tenant via the app-role pool's RLS (invariant #6). Runs on
	// the idempotent re-seed path too so a re-run ensures evaluation has run.
	// Non-fatal on a missing app DSN: the seed itself succeeded.
	runDemoEvaluation(cmd, ctx, res.TenantID)

	if res.Idempotent {
		_, _ = fmt.Fprintf(cmd.OutOrStdout(),
			"demo seed: tenant %q already seeded (id=%s); no changes made. Rotate the password via /v1/admin/users if needed.\n",
			res.TenantSlug, res.TenantID,
		)
		return nil
	}

	// Status line: AC-15 — tenant slug + user email + one-time password.
	_, _ = fmt.Fprintf(cmd.OutOrStdout(),
		"\n=== Slice 205 demo seed complete ===\n"+
			"  tenant_slug : %s\n"+
			"  tenant_id   : %s\n"+
			"  admin email : %s\n"+
			"  admin pass  : %s   <-- printed ONCE; capture now\n"+
			"\n"+
			"Row counts:\n"+
			"  controls         : %d\n"+
			"  risks            : %d\n"+
			"  evidence_records : %d\n"+
			"  policies         : %d\n"+
			"  vendors          : %d\n"+
			"  audit_periods    : %d (1 frozen)\n"+
			"  populations      : %d\n"+
			"  samples          : %d\n"+
			"  walkthroughs     : %d\n"+
			"  exceptions       : %d\n"+
			"  board_briefs     : %d\n"+
			"  board_packs      : %d\n"+
			"  framework_scopes : %d\n"+
			"  framework_reqs   : %d (posture spine)\n"+
			"  strm_edges       : %d (posture spine)\n"+
			"  controls_anchored: %d (scf_anchor_id set)\n"+
			"  audit_log rows   : %d\n"+
			"  evidence_kinds   : %d distinct kinds\n",
		res.TenantSlug, res.TenantID, res.UserEmail, res.PlaintextPasswd,
		res.Controls, res.Risks, res.Evidence, res.Policies, res.Vendors,
		res.AuditPeriods, res.Populations, res.Samples,
		res.Walkthroughs, res.Exceptions, res.BoardBriefs, res.BoardPacks,
		res.FrameworkScopes, res.FrameworkReqs, res.STRMEdges, res.ControlsAnchored,
		res.AuditLogRows, len(res.EvidenceKindsUsed),
	)

	// Slice 476 — reach-it hint. The demo data lives in a SEPARATE tenant,
	// so the operator who seeded it is not yet a member and the tenant does
	// not appear in their switcher. Without this hint the seed is a dead end
	// (the gap slice 476 was filed for). Point at the slice-478/479 self-
	// assign journey; the membership-bounded switcher (P0-192-5) is the
	// reason a grant is required, not a bug to work around.
	_, _ = fmt.Fprintf(cmd.OutOrStdout(),
		"\nReach the demo data (it lives in a SEPARATE tenant):\n"+
			"  1. As a super admin, open /admin/users and click \"Add me to a tenant\".\n"+
			"  2. Self-assign to tenant_id %s with the role(s) you want.\n"+
			"  3. Sign out and sign back in to mint a fresh token, then switch to\n"+
			"     the demo tenant in the tenant switcher (top of the app shell).\n",
		res.TenantID,
	)
	return nil
}

// runDemoEvaluation drives the slice-671 post-seed evaluator for the seeded
// tenant. It resolves the RLS-enforced app-role DSN from --app-database-url or
// DATABASE_URL_APP, opens a short-lived pool, and runs
// demoseed.EvaluateSeededTenant. It is NON-FATAL: a missing app DSN or an
// evaluation error prints a hint to stderr and returns — the seed already
// committed, so the demo tenant exists regardless; the operator can re-trigger
// evaluation (re-running `demo seed` on the same slug re-evaluates).
func runDemoEvaluation(cmd *cobra.Command, ctx context.Context, tenantID uuid.UUID) {
	appDSN := demoSeedAppDatabaseURL
	if appDSN == "" {
		appDSN = os.Getenv(demoAppDatabaseURLEnvVar)
	}
	if appDSN == "" {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
			"demo seed: %s (or --app-database-url) not set; skipping post-seed evaluation.\n"+
				"  The demo tenant's controls will show \"—\" until the evaluator runs.\n"+
				"  Set the app-role DSN and re-run `demo seed` on the same slug to evaluate.\n",
			demoAppDatabaseURLEnvVar,
		)
		return
	}

	appPool, err := pgxpool.New(ctx, appDSN)
	if err != nil {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
			"demo seed: post-seed evaluation: open app pool: %v (seed committed; re-run to evaluate)\n", err,
		)
		return
	}
	defer appPool.Close()

	summary, err := demoseed.EvaluateSeededTenant(ctx, appPool, tenantID)
	if err != nil {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
			"demo seed: post-seed evaluation failed: %v (seed committed; re-run to evaluate)\n", err,
		)
		return
	}
	_, _ = fmt.Fprintf(cmd.OutOrStdout(),
		"\nPost-seed evaluation complete (controls now show real state):\n"+
			"  control_evaluations : %d\n"+
			"  evidence_freshness  : %d\n",
		summary.ControlEvaluations, summary.FreshnessRows,
	)
}

// runDemoTeardown executes `atlas-cli demo teardown`.
func runDemoTeardown(cmd *cobra.Command, _ []string) error {
	if !demoEnvEnabled() {
		return fmt.Errorf(
			"%s=true is required to run demo teardown (P0-A1 — opt-in only)",
			demoEnableEnvVar,
		)
	}
	dsn, err := resolveDemoDSN(demoTeardownDatabaseURL)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(cmd.Context(), 5*time.Minute)
	defer cancel()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return fmt.Errorf("demo teardown: open pool: %w", err)
	}
	defer pool.Close()

	seeder, err := demoseed.NewSeeder(pool, demoseed.DefaultScale)
	if err != nil {
		return fmt.Errorf("demo teardown: %w", err)
	}
	if err := seeder.Teardown(ctx, demoTeardownTenantSlug, uuid.Nil, uuid.Nil); err != nil {
		return fmt.Errorf("demo teardown: %w", err)
	}
	_, _ = fmt.Fprintf(cmd.OutOrStdout(),
		"demo teardown: tenant %q deleted.\n", demoTeardownTenantSlug,
	)
	return nil
}

// demoEnvEnabled returns true if ATLAS_ENABLE_DEMO_SEED is the
// canonical truthy value. Anything other than the exact string
// "true" (lower-case) is treated as disabled.
//
// Conservative on purpose — operators who actively flip the env var
// will use the documented value; the "loose interpretation of truthy"
// of e.g. "1" / "yes" / "TRUE" risks accidental enablement.
func demoEnvEnabled() bool {
	return os.Getenv(demoEnableEnvVar) == "true"
}

// resolveDemoDSN returns the BYPASSRLS DSN from the flag or env var.
func resolveDemoDSN(fromFlag string) (string, error) {
	if fromFlag != "" {
		return fromFlag, nil
	}
	if v := os.Getenv(demoDatabaseURLEnvVar); v != "" {
		return v, nil
	}
	return "", fmt.Errorf(
		"--database-url or %s is required (BYPASSRLS DSN for the demo seed)",
		demoDatabaseURLEnvVar,
	)
}
