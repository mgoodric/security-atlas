-- security-atlas — AuditPeriod + freezing primitive (slice 028).
--
-- Two tables introduced + one column extension on slice-026 `populations`:
--   audit_periods             - tenant-scoped, framework-scoped time window over
--                               which an auditor evaluates compliance. Two-state
--                               lifecycle: `open` -> `frozen` (terminal-for-content;
--                               metadata mutation is rejected at the SQL guard).
--                               `frozen_hash` is the content commitment computed
--                               at freeze time. Inputs are pinned in ADR 0003:
--                               sha256(canonical_json({period_id, period_start,
--                               period_end, framework_version_id,
--                               evidence_record_ids_sorted, control_ids_sorted})).
--                               `frozen_at` is intentionally NOT in the hash
--                               inputs so AC-7 ("freezing the same content twice
--                               produces the same hash") holds under the natural
--                               reading (re-freeze of unchanged ledger universe).
--   audit_period_audit_log    - append-only lifecycle log. SELECT + INSERT
--                               policies only under FORCE ROW LEVEL SECURITY,
--                               matching slice 011/013/026/035/036/059 pattern.
--   populations.audit_period_id  - NULLABLE column. When set, the slice 026
--                                  query path already honors the population's
--                                  frozen_at horizon via the existing
--                                  `observed_at <= COALESCE(frozen_at, 'infinity')`
--                                  clause. Slice 028's Freeze method stamps the
--                                  population's frozen_at from the period's
--                                  frozen_at; the populations query already
--                                  enforces the horizon (forward-compat hook
--                                  written by slice 026 is now load-bearing).
--
-- Constitutional invariants honored:
--   #2  Ingestion and evaluation separated; freezing is a READ-side concept.
--       This migration does NOT touch evidence_records. The freeze hash is
--       computed in the application layer from the live ledger view at
--       freeze time. The ledger remains append-only.
--   #6  Tenant isolation at the database layer. Both new tables get FORCE
--       ROW LEVEL SECURITY. `audit_periods` uses the four-policy split
--       (tenant_read FOR SELECT, tenant_write FOR INSERT WITH CHECK,
--       tenant_update FOR UPDATE USING + WITH CHECK, tenant_delete FOR
--       DELETE) established by slices 011/014/017/018/021/026/035/036/059.
--       `audit_period_audit_log` uses SELECT + INSERT policies only --
--       the explicit absence of UPDATE/DELETE policies under FORCE makes
--       the table append-only.
--   #10 Audit-period freezing -- this is the slice that delivers the
--       invariant. The horizon shift is implemented entirely as a read-
--       side filter; no evidence snapshot table is introduced (anti-
--       criterion P0).
--
-- Anti-criteria honored at the schema layer (P0):
--   - No evidence snapshot table. Freeze persists (frozen_at, frozen_hash,
--     frozen_by) on the audit_periods row only. The "frozen state" is
--     computed by the read path joining audit_periods.frozen_at as a
--     filter against evidence_records.observed_at.
--   - No UPDATE path for frozen rows. The application's FreezeAuditPeriod
--     SQL is guarded by `WHERE status = 'open'`; once status flips to
--     'frozen', a re-freeze UPDATE matches zero rows and returns
--     ErrAlreadyFrozen (HTTP 409).
--   - No random salt in the hash inputs. The hash is computed over content-
--     derived fields (period id + bounds + framework_version + sorted
--     evidence ids + sorted control ids); see ADR 0003.
--
-- Migration is reversible via 20260511000020_audit_periods.down.sql which
-- drops both tables and the populations column in dependency order.

-- ===== 1. audit_periods =====

CREATE TABLE audit_periods (
    id                      UUID PRIMARY KEY,
    tenant_id               UUID NOT NULL,
    name                    TEXT NOT NULL,
    framework_version_id    UUID NOT NULL,
    period_start            DATE NOT NULL,
    period_end              DATE NOT NULL,
    status                  TEXT NOT NULL DEFAULT 'open',
    frozen_at               TIMESTAMPTZ NULL,
    frozen_hash             BYTEA NULL,
    frozen_by               TEXT NULL,
    created_by              TEXT NOT NULL,
    created_at              TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT audit_periods_status_chk
        CHECK (status IN ('open', 'frozen')),
    CONSTRAINT audit_periods_name_nonempty
        CHECK (length(name) > 0),
    CONSTRAINT audit_periods_created_by_nonempty
        CHECK (length(created_by) > 0),
    CONSTRAINT audit_periods_period_bounds_valid
        CHECK (period_start <= period_end),
    -- When status is frozen, the freeze metadata must all be set. When
    -- status is open, the freeze metadata must all be null. This is the
    -- second line of defense behind the application-level Freeze guard
    -- (status='open' WHERE clause).
    CONSTRAINT audit_periods_frozen_coherent
        CHECK (
            (status = 'open'
                AND frozen_at  IS NULL
                AND frozen_hash IS NULL
                AND frozen_by   IS NULL)
            OR
            (status = 'frozen'
                AND frozen_at  IS NOT NULL
                AND frozen_hash IS NOT NULL
                AND frozen_by   IS NOT NULL
                AND length(frozen_by) > 0)
        ),

    -- Composite FK to framework_versions blocks cross-tenant references
    -- and tolerates the global-catalog row (tenant_id NULL on
    -- framework_versions). The composite target is
    -- framework_versions(tenant_id, id); a future migration can elevate
    -- that to a UNIQUE constraint if needed. For now we rely on
    -- framework_versions.id being the PK (single-column unique) and add
    -- a single-column FK -- the tenancy match is enforced by RLS on
    -- audit_periods reads and by the application at write time.
    FOREIGN KEY (framework_version_id)
        REFERENCES framework_versions(id) ON DELETE RESTRICT
);

-- (tenant_id, name) is unique per tenant. NULLS DISTINCT is the Postgres-16
-- default; named here for clarity. Two tenants may use the same period
-- name ("SOC 2 2026 Q2") because the leading column is tenant_id.
ALTER TABLE audit_periods
    ADD CONSTRAINT audit_periods_tenant_name_uniq
    UNIQUE NULLS DISTINCT (tenant_id, name);

-- Composite uniqueness across (tenant_id, id) lets dependent tables
-- (populations.audit_period_id below) reference back with a cross-tenant-
-- safe FK.
ALTER TABLE audit_periods
    ADD CONSTRAINT audit_periods_tenant_id_unique UNIQUE (tenant_id, id);

CREATE INDEX idx_audit_periods_tenant_status
    ON audit_periods (tenant_id, status, created_at DESC);

CREATE INDEX idx_audit_periods_tenant_framework
    ON audit_periods (tenant_id, framework_version_id, period_start DESC);

-- ===== 2. audit_period_audit_log =====
--
-- Append-only lifecycle log. Every state transition writes one row:
--   period_created                    - on POST /v1/audit-periods
--   period_frozen                     - on POST /v1/audit-periods/:id:freeze success
--   freeze_rejected_already_frozen    - on POST :freeze when status='frozen'
--   population_attached               - on POST .../populations/:popID

CREATE TABLE audit_period_audit_log (
    id                  UUID PRIMARY KEY,
    tenant_id           UUID NOT NULL,
    audit_period_id     UUID NOT NULL,
    action              TEXT NOT NULL,
    actor               TEXT NOT NULL,
    detail              JSONB NOT NULL DEFAULT '{}'::jsonb,
    occurred_at         TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT audit_period_audit_log_action_chk
        CHECK (action IN (
            'period_created',
            'period_frozen',
            'freeze_rejected_already_frozen',
            'population_attached'
        )),
    CONSTRAINT audit_period_audit_log_actor_nonempty
        CHECK (length(actor) > 0)
);

CREATE INDEX idx_audit_period_audit_log_tenant_period
    ON audit_period_audit_log (tenant_id, audit_period_id, occurred_at DESC);

CREATE INDEX idx_audit_period_audit_log_tenant_occurred
    ON audit_period_audit_log (tenant_id, occurred_at DESC);

-- ===== 3. populations.audit_period_id (slice 026 forward-compat extension) =====
--
-- Slice 026 added populations.frozen_at as the read-horizon hook. Slice 028
-- adds the link back to the period that owns the freeze. Composite FK to
-- audit_periods(tenant_id, id) blocks cross-tenant linkage (slice 002 D3).
-- NULLABLE: existing populations have no period link and keep their
-- existing semantics (frozen_at = NULL = live horizon).

ALTER TABLE populations
    ADD COLUMN audit_period_id UUID NULL;

ALTER TABLE populations
    ADD CONSTRAINT populations_audit_period_fk
    FOREIGN KEY (tenant_id, audit_period_id)
        REFERENCES audit_periods(tenant_id, id) ON DELETE RESTRICT;

CREATE INDEX idx_populations_tenant_audit_period
    ON populations (tenant_id, audit_period_id)
    WHERE audit_period_id IS NOT NULL;

-- ===== 4. Row-Level Security =====
--
-- Four-policy split on audit_periods, mirroring slices 014/017/018/021/026/
-- 035/036/059. SELECT + INSERT only on audit_period_audit_log -- the
-- explicit absence of UPDATE/DELETE policies under FORCE makes the table
-- append-only.

ALTER TABLE audit_periods ENABLE ROW LEVEL SECURITY;
ALTER TABLE audit_periods FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_read ON audit_periods
    FOR SELECT USING (current_tenant_matches(tenant_id));
CREATE POLICY tenant_write ON audit_periods
    FOR INSERT WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_update ON audit_periods
    FOR UPDATE USING (current_tenant_matches(tenant_id))
    WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_delete ON audit_periods
    FOR DELETE USING (current_tenant_matches(tenant_id));

ALTER TABLE audit_period_audit_log ENABLE ROW LEVEL SECURITY;
ALTER TABLE audit_period_audit_log FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_read ON audit_period_audit_log
    FOR SELECT USING (current_tenant_matches(tenant_id));
CREATE POLICY tenant_write ON audit_period_audit_log
    FOR INSERT WITH CHECK (current_tenant_matches(tenant_id));

GRANT SELECT, INSERT, UPDATE, DELETE ON audit_periods TO atlas_app;
GRANT SELECT, INSERT ON audit_period_audit_log TO atlas_app;
