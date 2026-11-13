-- security-atlas — record of every all-zeros-tenant purge
-- (security-atlas#1876).
--
-- BACKGROUND:
--
--   `atlas-cli oauth unbound-rows --purge` deletes the rows that unbound
--   OAuth clients wrote into the all-zeros tenant before #1739. It runs on
--   the BYPASSRLS atlas_migrate connection, and ADR-0020 names privileged
--   BYPASSRLS SQL with no audit trail as the worst option for removing
--   data. This table is that trail: the purge writes one row in its own
--   transaction, so a purge that commits always leaves a row, and a purge
--   that rolls back leaves none.
--
-- WHAT IT DOES:
--
--   Creates tenant_data_purges, one row per committed purge: when, by which
--   database role and OS user, the per-table counts it deleted, the tables
--   it retained (append-only ledgers, ADR-0012) and the total.
--
--   The table is platform-global, not tenant-scoped: the purged rows belong
--   to no tenant. It has no tenant_id column, so scripts/audit-rls.sh does
--   not apply, and no grant to atlas_app: only the operator CLI on the
--   atlas_migrate connection writes or reads it. A trigger makes it
--   append-only, the same way change_audit_log is.
--
-- IDEMPOTENCY / REVERSIBILITY:
--
--   CREATE ... IF NOT EXISTS and CREATE OR REPLACE throughout. The down
--   migration drops the table and its function.

CREATE TABLE IF NOT EXISTS tenant_data_purges (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    purged_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    db_role     TEXT NOT NULL DEFAULT session_user,
    os_user     TEXT NOT NULL DEFAULT '',
    purged      JSONB NOT NULL,
    retained    JSONB NOT NULL DEFAULT '[]'::jsonb,
    total_rows  BIGINT NOT NULL CHECK (total_rows >= 0)
);

COMMENT ON TABLE tenant_data_purges IS
    'security-atlas#1876: one append-only row per committed `atlas-cli oauth unbound-rows --purge` (per-table counts purged from the all-zeros tenant, and the append-only ledger tables retained). Platform-global; written on the atlas_migrate connection only.';

CREATE OR REPLACE FUNCTION tenant_data_purges_append_only()
RETURNS TRIGGER AS $$
BEGIN
    RAISE EXCEPTION 'tenant_data_purges is append-only: % denied', TG_OP
        USING ERRCODE = 'restrict_violation';
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS tenant_data_purges_no_update_trg ON tenant_data_purges;
CREATE TRIGGER tenant_data_purges_no_update_trg
    BEFORE UPDATE OR DELETE ON tenant_data_purges
    FOR EACH ROW
    EXECUTE FUNCTION tenant_data_purges_append_only();
