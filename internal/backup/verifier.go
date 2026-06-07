package backup

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

// Verifier restores the latest backup into an EPHEMERAL throwaway database,
// recomputes + checks the artifact's sha256 BEFORE replay (AC-5 / D7), runs a
// smoke check, then DROPs the ephemeral database — even on failure (P0-510-2 /
// D6: no standing second copy). It records a backup_runs (kind=verify) row.
//
// migratorDSN MUST be the BYPASSRLS migrator DSN; the role needs CREATE
// DATABASE on the cluster. maintenanceDB is the cluster database the verifier
// connects to in order to CREATE/DROP the ephemeral DB (it cannot do that from
// within the DB being created).
type Verifier struct {
	pool          *pgxpool.Pool
	target        Target
	migratorDSN   string
	maintenanceDB string
	logger        *slog.Logger
	now           func() time.Time
	alert         func(ctx context.Context, summary string)
}

// NewVerifier constructs a Verifier.
func NewVerifier(pool *pgxpool.Pool, target Target, migratorDSN, maintenanceDB string, logger *slog.Logger) *Verifier {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(discardWriter{}, nil))
	}
	if maintenanceDB == "" {
		maintenanceDB = DefaultMaintenanceDB
	}
	return &Verifier{
		pool:          pool,
		target:        target,
		migratorDSN:   migratorDSN,
		maintenanceDB: maintenanceDB,
		logger:        logger,
		now:           time.Now,
	}
}

// SetAlertHook registers the failure-alert sink (D9).
func (v *Verifier) SetAlertHook(fn func(ctx context.Context, summary string)) { v.alert = fn }

// VerifyResult summarizes one verification for observability + tests.
type VerifyResult struct {
	RunID   string
	Name    string
	Tables  int
	Outcome string
}

// VerifyOnce restores + smoke-checks the latest successful backup. It always
// records a backup_runs (verify) row; on failure records outcome=failed and
// fires the alert hook. Exposed for the scheduler tick AND the integration
// test.
func (v *Verifier) VerifyOnce(ctx context.Context) (VerifyResult, error) {
	startRow, err := dbx.New(v.pool).StartBackupRun(ctx, dbx.StartBackupRunParams{Kind: "verify"})
	if err != nil {
		return VerifyResult{}, fmt.Errorf("verify: start run: %w", err)
	}
	runID := uuidString(startRow.ID)
	res := VerifyResult{RunID: runID, Outcome: "running"}

	fail := func(reason string, cause error) (VerifyResult, error) {
		v.logger.Error("restore-verification failed", "run", runID, "reason", reason, "err", errString(cause))
		_, _ = dbx.New(v.pool).FinishBackupRunFailed(ctx, dbx.FinishBackupRunFailedParams{
			ID:     startRow.ID,
			Detail: boundDetail(reason),
		})
		res.Outcome = "failed"
		if v.alert != nil {
			v.alert(ctx, "restore-verification failed: "+reason)
		}
		return res, fmt.Errorf("verify: %s: %w", reason, cause)
	}

	latest, err := dbx.New(v.pool).LatestSucceededBackup(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		return fail("no backup to verify", errors.New("no successful backup exists"))
	}
	if err != nil {
		return fail("lookup latest backup", err)
	}
	if latest.ArtifactName == nil {
		return fail("latest backup has no artifact", errors.New("nil artifact name"))
	}
	name := *latest.ArtifactName
	res.Name = name

	// Fetch the artifact and verify its sha256 BEFORE replay (AC-5 / D7).
	body, err := v.target.Get(ctx, name)
	if err != nil {
		return fail("fetch artifact", err)
	}
	if got := HashBytes(body); got != latest.ContentHash {
		return fail("hash mismatch", fmt.Errorf("recorded=%s computed=%s", latest.ContentHash, got))
	}

	// Restore into an ephemeral DB, smoke-check, then DROP — even on failure.
	tables, err := v.restoreAndSmoke(ctx, body)
	if err != nil {
		return fail("restore+smoke", err)
	}
	res.Tables = tables

	if _, err := dbx.New(v.pool).FinishBackupRunSucceeded(ctx, dbx.FinishBackupRunSucceededParams{
		ID:           startRow.ID,
		ArtifactName: &name,
		SizeBytes:    int64(len(body)),
		ContentHash:  latest.ContentHash,
		Detail:       boundDetail(fmt.Sprintf("smoke ok: %d tables", tables)),
	}); err != nil {
		return VerifyResult{}, fmt.Errorf("verify: finish run: %w", err)
	}
	res.Outcome = "succeeded"
	v.logger.Info("restore-verification succeeded", "run", runID, "name", name, "tables", tables)
	return res, nil
}

// restoreAndSmoke creates an ephemeral DB, replays the dump, runs the smoke
// check, and DROPs the DB in a defer (P0-510-2: torn down on every path).
// Returns the number of tables that materialized.
func (v *Verifier) restoreAndSmoke(ctx context.Context, dump []byte) (int, error) {
	ephemeralName := fmt.Sprintf("atlas_restore_verify_%d", v.now().UnixNano())

	admin, err := pgx.Connect(ctx, replaceDBName(v.migratorDSN, v.maintenanceDB))
	if err != nil {
		return 0, fmt.Errorf("connect maintenance db: %w", err)
	}
	defer func() { _ = admin.Close(ctx) }()

	if _, err := admin.Exec(ctx, fmt.Sprintf("CREATE DATABASE %s", quoteIdent(ephemeralName))); err != nil {
		return 0, fmt.Errorf("create ephemeral db: %w", err)
	}
	// P0-510-2: ALWAYS drop the ephemeral DB, even if the smoke check fails
	// or panics. WITH (FORCE) terminates any lingering connection so the drop
	// never blocks (no standing second copy).
	defer func() {
		dropCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if _, derr := admin.Exec(dropCtx, fmt.Sprintf("DROP DATABASE IF EXISTS %s WITH (FORCE)", quoteIdent(ephemeralName))); derr != nil {
			v.logger.Error("ephemeral db teardown", "db", ephemeralName, "err", errString(derr))
		}
	}()

	eph, err := pgx.Connect(ctx, replaceDBName(v.migratorDSN, ephemeralName))
	if err != nil {
		return 0, fmt.Errorf("connect ephemeral db: %w", err)
	}
	defer func() { _ = eph.Close(ctx) }()

	// Replay the self-contained dump (schema + data).
	if _, err := eph.Exec(ctx, string(dump)); err != nil {
		return 0, fmt.Errorf("replay dump: %w", err)
	}

	return smokeCheck(ctx, eph)
}

// smokeCheck proves the restore is faithful: at least one table materialized,
// and a sentinel query returns. Returns the table count.
func smokeCheck(ctx context.Context, conn *pgx.Conn) (int, error) {
	var tableCount int
	if err := conn.QueryRow(ctx, `
		SELECT count(*) FROM pg_tables WHERE schemaname = 'public'`).Scan(&tableCount); err != nil {
		return 0, fmt.Errorf("smoke table count: %w", err)
	}
	if tableCount == 0 {
		return 0, errors.New("smoke check: no tables restored")
	}
	// Sentinel query: the controls table is a load-bearing primitive; a
	// restored DB must be able to answer a SELECT against it without error
	// (proving the schema + a representative table round-tripped).
	var sentinel int
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM controls`).Scan(&sentinel); err != nil {
		return 0, fmt.Errorf("smoke sentinel query: %w", err)
	}
	return tableCount, nil
}
