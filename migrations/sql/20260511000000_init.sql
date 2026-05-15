-- security-atlas — initial schema (slice 002)
--
-- Defines the six domain primitives (Control, Risk, Evidence, Scope, Framework, Policy)
-- plus FrameworkScope. Every tenant-scoped table carries `tenant_id`, has RLS enabled
-- with FORCE ROW LEVEL SECURITY, and isolates via `app.current_tenant` GUC.
--
-- Source of truth: this file. sqlc reads it for codegen; Atlas applies it via versioned
-- migrations. See ARCHITECTURE_CANVAS.md sections 02-primitives.md and 05-scopes.md.

-- ===== Enum types (prefixed per table to avoid global-namespace collisions) =====
--
-- Every CREATE TYPE is wrapped in a DO block that swallows `duplicate_object`
-- (slice 065 bug #3). Postgres has no `CREATE TYPE IF NOT EXISTS`, so the
-- exception-catch is the canonical idempotency idiom. Without it, re-running
-- this migration against a partially-migrated database — which the
-- self-host bootstrap does on every `docker compose up` — aborts on the
-- first `type "..." already exists` error and strands the deployment.

DO $$ BEGIN
    CREATE TYPE control_implementation_type AS ENUM (
        'automated',
        'semi_automated',
        'manual_attested',
        'manual_periodic'
    );
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

DO $$ BEGIN
    CREATE TYPE control_lifecycle_state AS ENUM (
        'draft',
        'proposed',
        'active',
        'deprecated',
        'retired'
    );
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

DO $$ BEGIN
    CREATE TYPE risk_category AS ENUM (
        'confidentiality',
        'integrity',
        'availability',
        'privacy',
        'regulatory',
        'operational',
        'financial'
    );
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

DO $$ BEGIN
    CREATE TYPE risk_methodology AS ENUM (
        'nist_800_30',
        'fair',
        'cis_ram',
        'iso_27005',
        'qualitative_5x5'
    );
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

DO $$ BEGIN
    CREATE TYPE risk_treatment AS ENUM (
        'accept',
        'mitigate',
        'transfer',
        'avoid'
    );
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

DO $$ BEGIN
    CREATE TYPE scope_environment AS ENUM (
        'prod',
        'staging',
        'dev',
        'sandbox'
    );
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

DO $$ BEGIN
    CREATE TYPE scope_data_classification AS ENUM (
        'restricted',
        'confidential',
        'internal',
        'public'
    );
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

DO $$ BEGIN
    CREATE TYPE evidence_result AS ENUM (
        'pass',
        'fail',
        'na',
        'inconclusive'
    );
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

DO $$ BEGIN
    CREATE TYPE evidence_freshness_class AS ENUM (
        'realtime',
        'daily',
        'weekly',
        'monthly',
        'quarterly',
        'annual'
    );
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

DO $$ BEGIN
    CREATE TYPE framework_version_status AS ENUM (
        'current',
        'legacy',
        'withdrawn'
    );
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

DO $$ BEGIN
    CREATE TYPE policy_status AS ENUM (
        'draft',
        'under_review',
        'approved',
        'published',
        'superseded'
    );
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

DO $$ BEGIN
    CREATE TYPE framework_scope_status AS ENUM (
        'draft',
        'approved',
        'active',
        'retired'
    );
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

-- ===== Tables =====
--
-- All `updated_at` columns are application-owned: the schema defaults the
-- value on INSERT but does NOT trigger on UPDATE. Every DML path that mutates
-- a row is responsible for setting `updated_at = now()`. A trigger-based
-- enforcement can land in a later slice if drift becomes a real issue.

-- frameworks: global catalog by default; tenants may register custom frameworks
-- (tenant_id NOT NULL = tenant-private; tenant_id NULL = global catalog).
CREATE TABLE frameworks (
    id          UUID PRIMARY KEY,
    tenant_id   UUID NULL,
    name        TEXT NOT NULL,
    slug        TEXT NOT NULL,
    issuer      TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    latest_version_id UUID NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, slug)
);

-- framework_versions: same tenancy semantics as frameworks.
CREATE TABLE framework_versions (
    id                  UUID PRIMARY KEY,
    tenant_id           UUID NULL,
    framework_id        UUID NOT NULL REFERENCES frameworks(id) ON DELETE CASCADE,
    version             TEXT NOT NULL,
    effective_from      DATE NULL,
    effective_to        DATE NULL,
    status              framework_version_status NOT NULL DEFAULT 'current',
    requirement_count   INTEGER NOT NULL DEFAULT 0,
    oscal_catalog_uri   TEXT NULL,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (framework_id, version)
);

ALTER TABLE frameworks
    ADD CONSTRAINT frameworks_latest_version_fk
    FOREIGN KEY (latest_version_id) REFERENCES framework_versions(id) ON DELETE SET NULL
    DEFERRABLE INITIALLY DEFERRED;

-- controls
CREATE TABLE controls (
    id                  UUID PRIMARY KEY,
    tenant_id           UUID NOT NULL,
    scf_id              TEXT NULL,
    title               TEXT NOT NULL,
    description         TEXT NOT NULL DEFAULT '',
    control_family      TEXT NOT NULL,
    implementation_type control_implementation_type NOT NULL,
    owner_role          TEXT NOT NULL DEFAULT '',
    lifecycle_state     control_lifecycle_state NOT NULL DEFAULT 'draft',
    applicability_expr  TEXT NOT NULL DEFAULT 'true',
    version             INTEGER NOT NULL DEFAULT 1,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- Composite uniqueness supports cross-tenant-safe FK targets (D3).
    UNIQUE (tenant_id, id)
);

-- risks
CREATE TABLE risks (
    id                 UUID PRIMARY KEY,
    tenant_id          UUID NOT NULL,
    title              TEXT NOT NULL,
    description        TEXT NOT NULL DEFAULT '',
    category           risk_category NOT NULL,
    methodology        risk_methodology NOT NULL DEFAULT 'nist_800_30',
    inherent_score     JSONB NOT NULL DEFAULT '{}'::jsonb,
    treatment          risk_treatment NOT NULL DEFAULT 'accept',
    treatment_owner    TEXT NOT NULL DEFAULT '',
    residual_score     JSONB NOT NULL DEFAULT '{}'::jsonb,
    review_due_at      TIMESTAMPTZ NULL,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- scopes: a tuple over (BU × env × geo × cloud_account × data_class × product_line).
CREATE TABLE scopes (
    id                  UUID PRIMARY KEY,
    tenant_id           UUID NOT NULL,
    business_unit       TEXT NOT NULL DEFAULT '',
    environment         scope_environment NULL,
    geography           TEXT NULL,
    cloud_account       TEXT NULL,
    data_classification scope_data_classification NULL,
    product_line        TEXT NULL,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- evidence_records: append-only ledger entries. Composite FK to controls prevents
-- cross-tenant FK leakage (D3); hash is over canonical JSON of payload (D5).
CREATE TABLE evidence_records (
    id                  UUID PRIMARY KEY,
    tenant_id           UUID NOT NULL,
    evidence_query_id   UUID NULL,
    control_id          UUID NOT NULL,
    scope_id            UUID NULL REFERENCES scopes(id) ON DELETE SET NULL,
    observed_at         TIMESTAMPTZ NOT NULL,
    ingested_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    provenance          JSONB NOT NULL,
    result              evidence_result NOT NULL,
    payload             JSONB NOT NULL DEFAULT '{}'::jsonb,
    payload_uri         TEXT NULL,
    hash                TEXT NOT NULL,
    freshness_class     evidence_freshness_class NOT NULL DEFAULT 'monthly',
    valid_until         TIMESTAMPTZ NULL,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- Composite FK: tenant must match between evidence and control.
    FOREIGN KEY (tenant_id, control_id) REFERENCES controls(tenant_id, id) ON DELETE RESTRICT
);

-- policies
CREATE TABLE policies (
    id                              UUID PRIMARY KEY,
    tenant_id                       UUID NOT NULL,
    title                           TEXT NOT NULL,
    version                         INTEGER NOT NULL DEFAULT 1,
    effective_date                  DATE NULL,
    body_md                         TEXT NOT NULL DEFAULT '',
    owner                           TEXT NOT NULL DEFAULT '',
    approver                        TEXT NOT NULL DEFAULT '',
    acknowledgment_required_role    TEXT[] NOT NULL DEFAULT ARRAY[]::TEXT[],
    status                          policy_status NOT NULL DEFAULT 'draft',
    created_at                      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at                      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- framework_scopes: per-framework subset of cells + controls. Intersected with
-- Control.applicability_expr at evaluation time (canvas §5.5).
CREATE TABLE framework_scopes (
    id                      UUID PRIMARY KEY,
    tenant_id               UUID NOT NULL,
    framework_version_id    UUID NOT NULL REFERENCES framework_versions(id) ON DELETE RESTRICT,
    name                    TEXT NOT NULL,
    predicate               TEXT NOT NULL DEFAULT 'true',
    effective_from          DATE NULL,
    effective_to            DATE NULL,
    status                  framework_scope_status NOT NULL DEFAULT 'draft',
    approved_by             TEXT NULL,
    approval_evidence       TEXT NULL,
    created_at              TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- ===== Indexes =====

CREATE INDEX idx_controls_tenant_scf
    ON controls (tenant_id, scf_id)
    WHERE scf_id IS NOT NULL;

CREATE INDEX idx_evidence_tenant_control_observed
    ON evidence_records (tenant_id, control_id, observed_at DESC);

CREATE INDEX idx_evidence_hash
    ON evidence_records (hash);

CREATE INDEX idx_framework_scopes_version_status
    ON framework_scopes (framework_version_id, status);

-- Note: frameworks(tenant_id, slug) is already btree-indexed by the
-- UNIQUE (tenant_id, slug) constraint above — no separate index needed.

-- ===== Row-Level Security =====
--
-- Single helper function captures "this tenant matches the GUC" so the
-- no-default-allow semantics live in one place. Per-table policies stay
-- explicit (CREATE POLICY per table) so auditors see exactly what each
-- table grants without traversing an indirection.
--
-- FORCE ROW LEVEL SECURITY binds the table owner too — the migration role
-- (atlas_migrate) must be BYPASSRLS to perform DDL.

CREATE OR REPLACE FUNCTION current_tenant_matches(row_tenant uuid)
RETURNS boolean
LANGUAGE sql
STABLE
PARALLEL SAFE
AS $$
    SELECT row_tenant::text = current_setting('app.current_tenant', true)
$$;

ALTER TABLE controls ENABLE ROW LEVEL SECURITY;
ALTER TABLE controls FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON controls
    USING (current_tenant_matches(tenant_id));

ALTER TABLE risks ENABLE ROW LEVEL SECURITY;
ALTER TABLE risks FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON risks
    USING (current_tenant_matches(tenant_id));

ALTER TABLE evidence_records ENABLE ROW LEVEL SECURITY;
ALTER TABLE evidence_records FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON evidence_records
    USING (current_tenant_matches(tenant_id));

ALTER TABLE scopes ENABLE ROW LEVEL SECURITY;
ALTER TABLE scopes FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON scopes
    USING (current_tenant_matches(tenant_id));

ALTER TABLE policies ENABLE ROW LEVEL SECURITY;
ALTER TABLE policies FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON policies
    USING (current_tenant_matches(tenant_id));

ALTER TABLE framework_scopes ENABLE ROW LEVEL SECURITY;
ALTER TABLE framework_scopes FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON framework_scopes
    USING (current_tenant_matches(tenant_id));

ALTER TABLE frameworks ENABLE ROW LEVEL SECURITY;
ALTER TABLE frameworks FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_or_catalog ON frameworks
    USING (tenant_id IS NULL OR current_tenant_matches(tenant_id));

ALTER TABLE framework_versions ENABLE ROW LEVEL SECURITY;
ALTER TABLE framework_versions FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_or_catalog ON framework_versions
    USING (tenant_id IS NULL OR current_tenant_matches(tenant_id));

-- Grant DML on every table to the application role. Bootstrap must run first
-- (creates atlas_app); fail loud if it didn't — RLS is unenforceable without
-- a NOSUPERUSER role anyway.
GRANT SELECT, INSERT, UPDATE, DELETE ON
    controls, risks, evidence_records, scopes, policies,
    frameworks, framework_versions, framework_scopes
TO atlas_app;
GRANT USAGE ON SCHEMA public TO atlas_app;
