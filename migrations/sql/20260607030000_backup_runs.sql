-- security-atlas — slice 510: automated backup + restore-verification status.
--
-- backup_runs is the append-only deployment-scope status/audit ledger for the
-- automated backup feature (AC-6, threat-model R). A backup is a FULL
-- cross-tenant operation — it has no single tenant_id, so this is NOT a
-- tenant-scoped RLS table. It is a DEPLOYMENT-scope table.
--
-- SECURITY — P0-510-1 / AC-7 enforced at the GRANT level:
--   The table is granted to atlas_migrate (the BYPASSRLS deployment role the
--   in-process backup scheduler runs as) ONLY. It is deliberately NOT granted
--   to atlas_app — the RLS-enforced role every tenant-facing API runs through.
--   A tenant-facing handler therefore CANNOT read or write a backup record:
--   its role has zero privilege on this table. This is schema-level containment
--   of "no tenant can trigger or read a backup", not an application-code check.
--
-- Append-only (threat-model R): INSERT (a run begins) + a narrow UPDATE that
-- transitions outcome running -> succeeded|failed and stamps finished_at /
-- size_bytes / content_hash / detail. No DELETE grant — runs are never deleted
-- (the rows are small; rotation deletes ARTIFACTS, not status records).
--
-- Reversible via 20260607030000_backup_runs.down.sql.

CREATE TABLE backup_runs (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    -- kind distinguishes a backup run from a restore-verification run so the
    -- status surface can report both halves of the AC-4 cycle.
    kind          TEXT NOT NULL
                  CHECK (kind IN ('backup', 'verify')),
    -- target_kind records where a backup landed (local|s3) so the operator can
    -- tell on-host from off-host durability. NULL for verify runs.
    target_kind   TEXT
                  CHECK (target_kind IS NULL OR target_kind IN ('local', 's3')),
    -- artifact_name is the backup file name (a verify run references the backup
    -- it restored). NULL while a run is still selecting its artifact.
    artifact_name TEXT,
    outcome       TEXT NOT NULL DEFAULT 'running'
                  CHECK (outcome IN ('running', 'succeeded', 'failed')),
    -- size_bytes + content_hash describe the produced/verified artifact.
    size_bytes    BIGINT NOT NULL DEFAULT 0,
    content_hash  TEXT NOT NULL DEFAULT '',
    -- detail is a short human-readable reason (failure cause / smoke summary).
    -- Bounded by the writer; never carries credentials (threat-model S/I).
    detail        TEXT NOT NULL DEFAULT '',
    started_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at   TIMESTAMPTZ
);

-- Most reads are "latest runs, newest first" for the status surface.
CREATE INDEX backup_runs_kind_started
    ON backup_runs (kind, started_at DESC);

-- P0-510-1 / AC-7: deployment role ONLY. atlas_app is intentionally absent.
GRANT SELECT, INSERT, UPDATE ON backup_runs TO atlas_migrate;
