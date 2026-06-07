package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/spf13/cobra"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/evidence/ingest"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// Slice 464 — `atlas evidence verify`: ledger-wide integrity walk.
//
// Walks the append-only evidence ledger, recomputes each record's canonical
// hash from its persisted columns, and reports any record whose recomputed
// hash does not match the stored `hash` — a silently-corrupted or tampered
// record. READ-ONLY: it never writes to the ledger (invariant #2).
//
// Roles (AC-2):
//   - Tenant-scoped walk (`--tenant <uuid>`): connects as atlas_app and
//     sets the per-transaction tenant GUC so RLS bounds the walk to that
//     tenant. No SET ROLE — atlas_app is NOSUPERUSER NOBYPASSRLS.
//   - Cross-tenant walk (`--all-tenants`, super-admin): uses the documented
//     `SET LOCAL ROLE atlas_service_account` path (BYPASSRLS, granted to
//     atlas_app per migrations/bootstrap/01-roles.sql) inside the walk
//     transaction — never a superuser connection.
//
// Exit codes (AC-1): 0 = clean (zero mismatches), 1 = mismatches found,
// 2 = operational error (no DSN, connect failure, missing --tenant, etc.).

const (
	verifyExitClean      = 0
	verifyExitMismatch   = 1
	verifyExitOper0      = 2
	verifyDefaultPage    = 1000
	verifyConnectTimeout = 15 * time.Second
)

type evidenceVerifyFlags struct {
	tenant     string
	allTenants bool
	pageSize   int32
	dsn        string
}

func newEvidenceVerifyCmd() *cobra.Command {
	var f evidenceVerifyFlags

	cmd := &cobra.Command{
		Use:   "verify",
		Short: "walk the evidence ledger and verify each record's stored hash",
		Long: `Recompute each evidence record's canonical hash from its persisted
content and compare it to the stored hash. Reports any record whose hash no
longer matches — a silently-corrupted or tampered record. Read-only.

Run tenant-scoped (--tenant <uuid>, as atlas_app under RLS) or, as a
super-admin, across all tenants (--all-tenants, via the documented
atlas_service_account role). Exits non-zero if any mismatch is found.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runEvidenceVerify(cmd, &f)
		},
	}

	cmd.Flags().StringVar(&f.tenant, "tenant", "", "tenant UUID to verify (RLS-scoped walk as atlas_app)")
	cmd.Flags().BoolVar(&f.allTenants, "all-tenants", false, "verify every tenant (super-admin; uses atlas_service_account)")
	cmd.Flags().Int32Var(&f.pageSize, "page-size", verifyDefaultPage, "rows per keyset page (streaming walk)")
	cmd.Flags().StringVar(&f.dsn, "dsn", "", "Postgres DSN (env: DATABASE_URL_APP, then DATABASE_URL)")
	return cmd
}

func runEvidenceVerify(cmd *cobra.Command, f *evidenceVerifyFlags) error {
	out := cmd.OutOrStdout()
	errOut := cmd.ErrOrStderr()

	// operError prints the message to stderr and exits with the
	// operational-error code (2). It returns nil so cobra/main does not
	// ALSO print + exit 1 — but exitVerify normally terminates the process
	// first; the nil return is the no-op-exit (test seam) fall-through.
	operError := func(format string, a ...any) error {
		_, _ = fmt.Fprintf(errOut, "error: "+format+"\n", a...)
		exitVerify(verifyExitOper0)
		return nil
	}

	if f.tenant == "" && !f.allTenants {
		return operError("one of --tenant <uuid> or --all-tenants is required")
	}
	if f.tenant != "" && f.allTenants {
		return operError("--tenant and --all-tenants are mutually exclusive")
	}
	if f.tenant != "" {
		if _, err := uuid.Parse(f.tenant); err != nil {
			return operError("--tenant %q: %v", f.tenant, err)
		}
	}
	if f.pageSize <= 0 {
		f.pageSize = verifyDefaultPage
	}

	dsn := f.dsn
	if dsn == "" {
		dsn = os.Getenv("DATABASE_URL_APP")
	}
	if dsn == "" {
		dsn = os.Getenv("DATABASE_URL")
	}
	if dsn == "" {
		return operError("--dsn or DATABASE_URL_APP / DATABASE_URL is required")
	}

	ctx, cancel := context.WithTimeout(context.Background(), verifyConnectTimeout)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return operError("connect: %v", err)
	}
	defer pool.Close()

	res, err := verifyLedger(context.Background(), pool, f)
	if err != nil {
		return operError("%v", err)
	}

	for _, m := range res.mismatches {
		_, _ = fmt.Fprintf(out, "MISMATCH tenant=%s record=%s stored=%s recomputed=%s%s\n",
			m.tenant, m.recordID, short(m.stored), short(m.recomputed), m.note)
	}
	_, _ = fmt.Fprintf(out, "verify: scanned=%d mismatches=%d tenants=%d\n",
		res.scanned, len(res.mismatches), res.tenants)

	if len(res.mismatches) > 0 {
		exitVerify(verifyExitMismatch)
	}
	return nil
}

type verifyMismatch struct {
	tenant     string
	recordID   string
	stored     string
	recomputed string
	note       string
}

type verifyResult struct {
	scanned    int
	tenants    int
	mismatches []verifyMismatch
}

// verifyLedger drives the walk. For --all-tenants it enumerates tenants then
// walks each as atlas_service_account; for a single tenant it walks under
// atlas_app + RLS GUC.
func verifyLedger(ctx context.Context, pool *pgxpool.Pool, f *evidenceVerifyFlags) (verifyResult, error) {
	var res verifyResult

	tenants := []string{f.tenant}
	if f.allTenants {
		ids, err := listTenantIDsForVerify(ctx, pool)
		if err != nil {
			return res, err
		}
		tenants = ids
	}

	for _, tid := range tenants {
		scanned, ms, err := walkTenant(ctx, pool, tid, f.pageSize, f.allTenants)
		if err != nil {
			return res, fmt.Errorf("tenant %s: %w", tid, err)
		}
		res.scanned += scanned
		res.tenants++
		res.mismatches = append(res.mismatches, ms...)
	}
	return res, nil
}

// listTenantIDsForVerify enumerates tenant ids for the cross-tenant walk.
// Runs under atlas_service_account so the BYPASSRLS read sees every tenant.
func listTenantIDsForVerify(ctx context.Context, pool *pgxpool.Pool) ([]string, error) {
	var ids []string
	err := pgx.BeginTxFunc(ctx, pool, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "SET LOCAL ROLE atlas_service_account"); err != nil {
			return fmt.Errorf("set local role atlas_service_account: %w", err)
		}
		rows, err := tx.Query(ctx, "SELECT id FROM tenants ORDER BY id")
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id pgtype.UUID
			if err := rows.Scan(&id); err != nil {
				return err
			}
			ids = append(ids, uuid.UUID(id.Bytes).String())
		}
		return rows.Err()
	})
	return ids, err
}

// walkTenant keyset-pages the ledger for one tenant and verifies each row.
// crossTenant selects the role path: atlas_service_account (BYPASSRLS) for
// the all-tenants walk, or atlas_app + RLS GUC for the tenant-scoped walk.
func walkTenant(ctx context.Context, pool *pgxpool.Pool, tenantID string, pageSize int32, crossTenant bool) (int, []verifyMismatch, error) {
	var scanned int
	var mismatches []verifyMismatch

	cursor := pgtype.UUID{Bytes: [16]byte{}, Valid: true} // zero-UUID seed
	tenantUUID, err := uuid.Parse(tenantID)
	if err != nil {
		return 0, nil, fmt.Errorf("tenant id: %w", err)
	}
	tpg := pgtype.UUID{Bytes: tenantUUID, Valid: true}

	for {
		var page []dbx.EvidenceRecord
		err := pgx.BeginTxFunc(ctx, pool, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(tx pgx.Tx) error {
			if crossTenant {
				if _, err := tx.Exec(ctx, "SET LOCAL ROLE atlas_service_account"); err != nil {
					return fmt.Errorf("set local role atlas_service_account: %w", err)
				}
			} else {
				tctx, terr := tenancy.WithTenant(ctx, tenantID)
				if terr != nil {
					return terr
				}
				if err := tenancy.ApplyTenant(tctx, tx); err != nil {
					return err
				}
			}
			q := dbx.New(tx)
			rows, err := q.WalkEvidenceRecordsForVerify(ctx, dbx.WalkEvidenceRecordsForVerifyParams{
				TenantID: tpg,
				AfterID:  cursor,
				PageSize: pageSize,
			})
			if err != nil {
				return err
			}
			page = rows
			return nil
		})
		if err != nil {
			return scanned, mismatches, err
		}
		if len(page) == 0 {
			break
		}

		for _, row := range page {
			scanned++
			ok, recomputed, verr := ingest.VerifyLedgerRow(row)
			if verr != nil {
				mismatches = append(mismatches, verifyMismatch{
					tenant: tenantID, recordID: ingest.RowID(row),
					stored: row.Hash, recomputed: "", note: " (" + verr.Error() + ")",
				})
				continue
			}
			if !ok {
				mismatches = append(mismatches, verifyMismatch{
					tenant: tenantID, recordID: ingest.RowID(row),
					stored: row.Hash, recomputed: recomputed,
				})
			}
		}
		cursor = page[len(page)-1].ID
	}
	return scanned, mismatches, nil
}

func short(h string) string {
	if len(h) > 12 {
		return h[:12]
	}
	return h
}

// exitVerify is a seam so unit tests can override process exit. Production
// calls os.Exit; tests swap it for a recorder.
var exitVerify = os.Exit
