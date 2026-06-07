-- backup_runs — slice 510 automated-backup status/audit ledger.
--
-- DEPLOYMENT-scope (no tenant_id): a backup is a full cross-tenant operation.
-- Granted to atlas_migrate ONLY (P0-510-1 / AC-7) — the in-process backup
-- scheduler runs as the BYPASSRLS migrator role; no tenant-facing handler
-- (atlas_app) can reach this table. Append-only: INSERT begins a run, the
-- narrow Finish* updates transition outcome once; no DELETE query exists.

-- name: StartBackupRun :one
-- Insert a run in the `running` state. kind is 'backup' or 'verify'.
INSERT INTO backup_runs (kind, target_kind, artifact_name)
VALUES ($1, $2, $3)
RETURNING *;

-- name: FinishBackupRunSucceeded :one
-- Transition a run to succeeded with the produced/verified artifact details.
UPDATE backup_runs
SET outcome      = 'succeeded',
    artifact_name = COALESCE($2, artifact_name),
    size_bytes   = $3,
    content_hash = $4,
    detail       = $5,
    finished_at  = now()
WHERE id = $1
RETURNING *;

-- name: FinishBackupRunFailed :one
-- Transition a run to failed with a short reason. Never carries credentials.
UPDATE backup_runs
SET outcome     = 'failed',
    detail      = $2,
    finished_at = now()
WHERE id = $1
RETURNING *;

-- name: ListRecentBackupRuns :many
-- Latest runs of a kind, newest first — backs the deployment status surface.
SELECT *
FROM backup_runs
WHERE kind = $1
ORDER BY started_at DESC
LIMIT $2;

-- name: LatestSucceededBackup :one
-- The most recent successful backup run — what restore-verification restores.
SELECT *
FROM backup_runs
WHERE kind = 'backup' AND outcome = 'succeeded'
ORDER BY started_at DESC
LIMIT 1;
