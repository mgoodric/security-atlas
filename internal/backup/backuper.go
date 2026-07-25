package backup

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

// Backuper produces one backup: dump → hash → write through the Target →
// rotate old artifacts → record a backup_runs status row (D7/D5/D8).
//
// The pool MUST be the BYPASSRLS migrator pool — the dump reads all tenants'
// rows and the backup_runs table is granted to atlas_migrate ONLY (D8).
type Backuper struct {
	pool   *pgxpool.Pool
	target Target
	cfg    Config
	logger *slog.Logger
	now    func() time.Time
	// alert is invoked on a failure so the scheduler can compose with the
	// slice-445 notification/email path (D9). Optional.
	alert func(ctx context.Context, summary string)
}

// NewBackuper constructs a Backuper.
func NewBackuper(pool *pgxpool.Pool, target Target, cfg Config, logger *slog.Logger) *Backuper {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(discardWriter{}, nil))
	}
	return &Backuper{pool: pool, target: target, cfg: cfg, logger: logger, now: time.Now}
}

// SetAlertHook registers the failure-alert sink (D9). Wired by the scheduler.
func (b *Backuper) SetAlertHook(fn func(ctx context.Context, summary string)) { b.alert = fn }

// RunResult summarizes one backup for observability + tests.
type RunResult struct {
	RunID   string
	Name    string
	Size    int64
	Hash    string
	Rotated []string
	Outcome string
}

// BackupOnce performs a single backup. It always records a backup_runs row;
// on failure it records outcome=failed and fires the alert hook. Exposed for
// the scheduler's tick loop AND the integration test.
func (b *Backuper) BackupOnce(ctx context.Context) (RunResult, error) {
	tk := b.target.Kind()
	startRow, err := dbx.New(b.pool).StartBackupRun(ctx, dbx.StartBackupRunParams{
		Kind:       "backup",
		TargetKind: &tk,
	})
	if err != nil {
		return RunResult{}, fmt.Errorf("backup: start run: %w", err)
	}
	runID := uuidString(startRow.ID)
	res := RunResult{RunID: runID, Outcome: "running"}

	fail := func(reason string, cause error) (RunResult, error) {
		b.logger.Error("backup failed", "run", runID, "reason", reason, "err", errString(cause))
		_, _ = dbx.New(b.pool).FinishBackupRunFailed(ctx, dbx.FinishBackupRunFailedParams{
			ID:     startRow.ID,
			Detail: boundDetail(reason),
		})
		res.Outcome = "failed"
		if b.alert != nil {
			b.alert(ctx, "backup failed: "+reason)
		}
		return res, fmt.Errorf("backup: %s: %w", reason, cause)
	}

	dump, err := Dump(ctx, b.pool)
	if err != nil {
		return fail("dump", err)
	}
	name := ArtifactName(b.now())
	hash := HashBytes(dump)
	res.Name, res.Size, res.Hash = name, int64(len(dump)), hash

	if err := b.target.Put(ctx, name, dump); err != nil {
		return fail("write artifact", err)
	}

	// Rotation (P0-510-3). A rotation error does NOT fail the backup — the
	// artifact is already durably written; we log + continue so a transient
	// delete error never loses a good backup.
	if objs, lerr := b.target.List(ctx); lerr == nil {
		for _, dead := range SelectForDeletion(objs, b.cfg.KeepDaily, b.cfg.KeepWeekly) {
			if derr := b.target.Delete(ctx, dead); derr != nil {
				b.logger.Warn("backup rotation delete", "name", dead, "err", errString(derr))
				continue
			}
			res.Rotated = append(res.Rotated, dead)
		}
	} else {
		b.logger.Warn("backup rotation list", "err", errString(lerr))
	}

	if _, err := dbx.New(b.pool).FinishBackupRunSucceeded(ctx, dbx.FinishBackupRunSucceededParams{
		ID:           startRow.ID,
		ArtifactName: &name,
		SizeBytes:    res.Size,
		ContentHash:  hash,
		Detail:       boundDetail(fmt.Sprintf("rotated %d", len(res.Rotated))),
	}); err != nil {
		return RunResult{}, fmt.Errorf("backup: finish run: %w", err)
	}
	res.Outcome = "succeeded"
	b.logger.Info("backup succeeded", "run", runID, "name", name, "size", res.Size, "rotated", len(res.Rotated))
	return res, nil
}

func uuidString(u pgtype.UUID) string {
	if !u.Valid {
		return ""
	}
	return fmtUUID(u.Bytes)
}
