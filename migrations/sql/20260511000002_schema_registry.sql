-- evidence_kind_schemas — the schema registry that backs `POST /v1/schemas`,
-- `GET /v1/schemas[/...]`, and the push-time payload validation hook (slice
-- 013). This is the contract-enforcement point for every `evidence_kind`
-- record that enters the ledger; the registry exists to prevent the
-- connector ecosystem from devolving into "every pusher invents its own
-- JSON shape" (canvas §4.1; EVIDENCE_SDK §4.5; analog: OpenTelemetry
-- semantic conventions).
--
-- Tenancy:
--   tenant_id NULL    → global / platform-bundled kind, visible to every tenant
--   tenant_id non-NULL → tenant-private kind, only visible inside that tenant
--
-- RLS mirrors the frameworks/framework_versions pattern from slice 002:
-- "tenant_id IS NULL OR current_tenant_matches(tenant_id)". The migration
-- role (atlas_migrate) has BYPASSRLS so DDL applies; the app role
-- (atlas_app) is bound by the policy.
--
-- UNIQUE constraints are split into two partial indexes because Postgres's
-- default UNIQUE treats NULLs as distinct (gotcha logged in
-- feedback_postgres_constraints.md). Global rows (tenant_id NULL) need a
-- partial unique index on (kind, semver); tenant-scoped rows need a
-- partial unique index on (tenant_id, kind, semver).

CREATE TABLE evidence_kind_schemas (
    id                          UUID PRIMARY KEY,
    tenant_id                   UUID NULL,
    kind                        TEXT NOT NULL,
    semver                      TEXT NOT NULL,
    major                       INTEGER NOT NULL,
    minor                       INTEGER NOT NULL,
    patch                       INTEGER NOT NULL,
    schema_json                 JSONB NOT NULL,
    owner                       TEXT NOT NULL,
    default_scf_anchors         TEXT[] NOT NULL DEFAULT ARRAY[]::TEXT[],
    deprecated_at               TIMESTAMPTZ NULL,
    deprecation_window_ends_at  TIMESTAMPTZ NULL,
    superseded_by_version       TEXT NULL,
    created_by                  TEXT NOT NULL,
    created_at                  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at                  TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT evidence_kind_schemas_semver_nonneg
        CHECK (major >= 0 AND minor >= 0 AND patch >= 0),
    CONSTRAINT evidence_kind_schemas_owner_nonempty
        CHECK (length(owner) > 0)
);

-- Partial unique indexes split global vs tenant rows so the NULL tenant_id
-- doesn't collide with the NULLs-distinct default on multi-column UNIQUE.
CREATE UNIQUE INDEX evidence_kind_schemas_global_uniq
    ON evidence_kind_schemas (kind, semver)
    WHERE tenant_id IS NULL;

CREATE UNIQUE INDEX evidence_kind_schemas_tenant_uniq
    ON evidence_kind_schemas (tenant_id, kind, semver)
    WHERE tenant_id IS NOT NULL;

-- Lookup index for the hot path: validate (kind, version) on push.
CREATE INDEX idx_evidence_kind_schemas_kind_version
    ON evidence_kind_schemas (kind, major DESC, minor DESC, patch DESC);

-- Row-Level Security. Same shape as frameworks/framework_versions for
-- reads (tenant_id NULL rows are visible to every tenant; tenant rows
-- are visible only when the GUC matches). Writes are stricter: a tenant
-- may only INSERT rows that carry its own tenant_id, never a NULL
-- (global) tenant_id. Global rows are inserted by the bundled-schema
-- importer running as atlas_migrate (BYPASSRLS) at boot. This split
-- prevents an admin credential from silently injecting into the global
-- namespace at runtime (anti-criterion: no cross-tenant private-kind
-- leak; no implicit global registration).
ALTER TABLE evidence_kind_schemas ENABLE ROW LEVEL SECURITY;
ALTER TABLE evidence_kind_schemas FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_or_catalog_read ON evidence_kind_schemas
    FOR SELECT
    USING (tenant_id IS NULL OR current_tenant_matches(tenant_id));
CREATE POLICY tenant_write ON evidence_kind_schemas
    FOR INSERT
    WITH CHECK (tenant_id IS NOT NULL AND current_tenant_matches(tenant_id));
CREATE POLICY tenant_update ON evidence_kind_schemas
    FOR UPDATE
    USING (tenant_id IS NOT NULL AND current_tenant_matches(tenant_id))
    WITH CHECK (tenant_id IS NOT NULL AND current_tenant_matches(tenant_id));
CREATE POLICY tenant_delete ON evidence_kind_schemas
    FOR DELETE
    USING (tenant_id IS NOT NULL AND current_tenant_matches(tenant_id));

GRANT SELECT, INSERT, UPDATE, DELETE ON evidence_kind_schemas TO atlas_app;
