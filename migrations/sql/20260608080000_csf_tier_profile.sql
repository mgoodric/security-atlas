-- migrations/sql/20260608080000_csf_tier_profile.sql
--
-- Slice 515 — NIST CSF 2.0 Tier / Profile assessment workflow.
--
-- Slice 480 (CSF crosswalk) + slice 514 (full Subcategory coverage) landed the
-- CSF SHARED REFERENCE data: the framework_versions row for nist_csf:2.0 and
-- its Subcategory rows in framework_requirements (e.g. "PR.AA-01"), plus the
-- fw_to_scf_edges crosswalk to SCF anchors. That data is catalog data — global,
-- not tenant-scoped, no RLS.
--
-- This slice adds the TENANT-CONFIDENTIAL assessment STATE that NIST CSF 2.0
-- layers on top of the crosswalk: a tenant's Tier rating (1-4) and its Current
-- / Target Profiles (a per-Subcategory target-outcome selection). Unlike the
-- shared crosswalk, a tenant's Profile + Tier is confidential self-assessment
-- input — Tenant A's gap view must NEVER leak to Tenant B (threat-model I,
-- LOAD-BEARING). Every table here therefore carries the invariant #6
-- four-policy RLS split under FORCE ROW LEVEL SECURITY.
--
-- JUDGMENT (slice 515, decisions-log D1): these are CSF-SPECIFIC tables, NOT a
-- generalized maturity-assessment primitive. The Tier construct (a fixed 1-4
-- ordinal scale with CSF-defined semantics: Partial / Risk Informed /
-- Repeatable / Adaptive) has no analog in ISO Annex A applicability (binary
-- applicable/excluded) or PCI compensating-controls (per-requirement
-- justification prose). Generalizing now would invent a configurable
-- maturity-scale abstraction with no second real consumer to validate it
-- against — a speculative-generality / Simplicity-Gate (Article VII) violation
-- and the explicit anti-criterion P0-515-4. The tables are already
-- framework-pinned via framework_version_id, so a future generalization (lift
-- the scale + selection shape into maturity_scales + maturity_assessments) is
-- an additive migration, not a rewrite. See docs/audit-log/515-*.md D1.
--
-- Constitutional invariants honored:
--   #1  One control, N framework satisfactions. The gap view does NOT duplicate
--       the crosswalk — it READS framework_requirements (CSF Subcategories) +
--       the existing fw_to_scf_edges → SCF-anchor → coverage traversal
--       (internal/api/ucfcoverage). These tables store only the tenant's
--       per-Subcategory TARGET selection + Tier rating; the
--       Subcategory↔SCF-anchor mapping is never re-stored here (P0-515-2).
--   #6  Tenant isolation at the DB layer. Every table: ENABLE + FORCE RLS,
--       four-policy split (read/write/update/delete) keyed on
--       current_tenant_matches(tenant_id). The audit table is append-only
--       (SELECT + INSERT policies only). RLS denies on missing GUC context
--       (current_tenant_matches returns NULL → false).
--
-- Threat-model coverage:
--   R (Repudiation): csf_assessment_audit records who set which Tier / which
--      Profile selection, when, and against which CSF framework_version.
--      Append-only by construction (no UPDATE/DELETE policy under FORCE).
--   E (Elevation): the role-permission cut (grc_engineer / admin may edit;
--      viewer / auditor / control_owner may read) is enforced at the HTTP
--      handler layer (internal/api/csfassessment); the DB layer enforces tenant
--      isolation only.
--
-- NOT-NULL discipline: every table here is NEW (no ALTER adds a NOT-NULL column
-- to an existing populated table), so no integration-test fixture helper needs
-- patching.
--
-- Reversibility: paired .down.sql drops the four tables + the two enums for a
-- byte-clean up → down → up round-trip.

-- ===== enums =====
--
-- Wrapped in DO/EXCEPTION for re-run idempotency: Postgres has no
-- CREATE TYPE IF NOT EXISTS and the self-host bootstrap re-applies every
-- migration on each `docker compose up` (slice 065 precedent).

DO $$ BEGIN
    -- CSF 2.0 Tiers are a fixed 1-4 ordinal. The label is carried in the
    -- application layer; the enum stores the canonical token so a bad value
    -- can never be persisted (threat-model T).
    CREATE TYPE csf_tier AS ENUM (
        'tier1_partial',
        'tier2_risk_informed',
        'tier3_repeatable',
        'tier4_adaptive'
    );
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

DO $$ BEGIN
    CREATE TYPE csf_profile_kind AS ENUM (
        'current',
        'target'
    );
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

-- ===== csf_tier_ratings =====
--
-- A tenant's overall CSF Tier rating against a CSF framework_version. At most
-- one rating per (tenant, framework_version) — re-rating UPDATEs the row in
-- place and appends a csf_assessment_audit row. Per-function Tier (rating each
-- of the six CSF Functions independently) is a deliberate spillover (slice
-- 515 decisions-log D2): v1 ships the single overall Tier the self-assessment
-- + insurer questionnaires ask for.

CREATE TABLE csf_tier_ratings (
    id                    UUID PRIMARY KEY,
    tenant_id             UUID NOT NULL,
    framework_version_id  UUID NOT NULL
        REFERENCES framework_versions (id) ON DELETE CASCADE,
    tier                  csf_tier NOT NULL,
    -- Free-form operator rationale for the rating (P0-515-3: a Tier is NEVER
    -- auto-rated; an operator picks it and may explain why).
    rationale             TEXT NOT NULL DEFAULT '',
    -- The user id (credential id for v1) that set this rating + when. Audit
    -- (threat-model R) — also mirrored into csf_assessment_audit.
    rated_by              TEXT NOT NULL,
    rated_at              TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT csf_tier_ratings_rated_by_nonempty
        CHECK (length(rated_by) > 0),
    -- At most one Tier rating per (tenant, framework_version).
    CONSTRAINT csf_tier_ratings_unique_per_version
        UNIQUE (tenant_id, framework_version_id)
);

CREATE INDEX idx_csf_tier_ratings_tenant_version
    ON csf_tier_ratings (tenant_id, framework_version_id);

ALTER TABLE csf_tier_ratings ENABLE ROW LEVEL SECURITY;
ALTER TABLE csf_tier_ratings FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_read ON csf_tier_ratings
    FOR SELECT
    USING (current_tenant_matches(tenant_id));
CREATE POLICY tenant_write ON csf_tier_ratings
    FOR INSERT
    WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_update ON csf_tier_ratings
    FOR UPDATE
    USING (current_tenant_matches(tenant_id))
    WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_delete ON csf_tier_ratings
    FOR DELETE
    USING (current_tenant_matches(tenant_id));

GRANT SELECT, INSERT, UPDATE, DELETE ON csf_tier_ratings TO atlas_app;

-- ===== csf_profiles =====
--
-- A profile CONTAINER: a tenant's Current OR Target Profile against a CSF
-- framework_version. At most one (current) + one (target) per (tenant,
-- framework_version). The per-Subcategory target outcomes live in
-- csf_profile_selections (a 1:N child) so the container metadata (name, kind,
-- who/when) is small and the selection set can grow to the full CSF
-- Subcategory count without bloating the container row.

CREATE TABLE csf_profiles (
    id                    UUID PRIMARY KEY,
    tenant_id             UUID NOT NULL,
    framework_version_id  UUID NOT NULL
        REFERENCES framework_versions (id) ON DELETE CASCADE,
    kind                  csf_profile_kind NOT NULL,
    -- Operator-supplied label (e.g. "2026 baseline", "Q4 target").
    name                  TEXT NOT NULL DEFAULT '',
    created_by            TEXT NOT NULL,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT csf_profiles_created_by_nonempty
        CHECK (length(created_by) > 0),
    -- At most one current + one target profile per (tenant, framework_version).
    CONSTRAINT csf_profiles_unique_per_kind
        UNIQUE (tenant_id, framework_version_id, kind)
);

CREATE INDEX idx_csf_profiles_tenant_version_kind
    ON csf_profiles (tenant_id, framework_version_id, kind);

ALTER TABLE csf_profiles ENABLE ROW LEVEL SECURITY;
ALTER TABLE csf_profiles FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_read ON csf_profiles
    FOR SELECT
    USING (current_tenant_matches(tenant_id));
CREATE POLICY tenant_write ON csf_profiles
    FOR INSERT
    WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_update ON csf_profiles
    FOR UPDATE
    USING (current_tenant_matches(tenant_id))
    WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_delete ON csf_profiles
    FOR DELETE
    USING (current_tenant_matches(tenant_id));

GRANT SELECT, INSERT, UPDATE, DELETE ON csf_profiles TO atlas_app;

-- ===== csf_profile_selections =====
--
-- One row per CSF Subcategory the operator has set a target outcome for inside
-- a profile. framework_requirement_id FKs the SHARED CSF Subcategory row (the
-- crosswalk reference data) — this table stores ONLY the tenant's target
-- outcome, never a copy of the Subcategory↔SCF-anchor mapping (P0-515-2,
-- invariant #1). The Subcategory's SCF anchors + coverage are derived at read
-- time via the existing ucfcoverage requirement→anchor→coverage traversal.
--
-- target_outcome is a small ordinal the operator picks for the Subcategory:
--   'not_targeted'  — explicitly out of the profile's target scope
--   'partial'       — partial implementation targeted
--   'largely'       — largely implemented targeted
--   'fully'         — fully implemented targeted
-- Stored as TEXT + CHECK rather than a Postgres enum: the outcome scale is a
-- profile-level convention (slice 515 D3) more likely to gain a label/value
-- than the fixed CSF Tier enum, and a TEXT+CHECK widens with an ALTER rather
-- than the heavier ADD VALUE enum dance.

CREATE TABLE csf_profile_selections (
    id                        UUID PRIMARY KEY,
    tenant_id                 UUID NOT NULL,
    csf_profile_id            UUID NOT NULL
        REFERENCES csf_profiles (id) ON DELETE CASCADE,
    -- The SHARED CSF Subcategory row (framework_requirements). A FK to the
    -- global catalog row, NOT a copy. ON DELETE CASCADE: if a CSF release is
    -- replaced and its Subcategory rows are dropped, the stale selections go
    -- with them.
    framework_requirement_id  UUID NOT NULL
        REFERENCES framework_requirements (id) ON DELETE CASCADE,
    target_outcome            TEXT NOT NULL DEFAULT 'not_targeted',
    -- Optional per-Subcategory operator note.
    note                      TEXT NOT NULL DEFAULT '',
    created_at                TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at                TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT csf_profile_selections_outcome_chk
        CHECK (target_outcome IN ('not_targeted', 'partial', 'largely', 'fully')),
    -- One selection per (profile, Subcategory).
    CONSTRAINT csf_profile_selections_unique_per_subcategory
        UNIQUE (csf_profile_id, framework_requirement_id)
);

CREATE INDEX idx_csf_profile_selections_tenant_profile
    ON csf_profile_selections (tenant_id, csf_profile_id);
CREATE INDEX idx_csf_profile_selections_requirement
    ON csf_profile_selections (framework_requirement_id);

ALTER TABLE csf_profile_selections ENABLE ROW LEVEL SECURITY;
ALTER TABLE csf_profile_selections FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_read ON csf_profile_selections
    FOR SELECT
    USING (current_tenant_matches(tenant_id));
CREATE POLICY tenant_write ON csf_profile_selections
    FOR INSERT
    WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_update ON csf_profile_selections
    FOR UPDATE
    USING (current_tenant_matches(tenant_id))
    WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_delete ON csf_profile_selections
    FOR DELETE
    USING (current_tenant_matches(tenant_id));

GRANT SELECT, INSERT, UPDATE, DELETE ON csf_profile_selections TO atlas_app;

-- ===== csf_assessment_audit =====
--
-- Append-only mutation log for the CSF assessment surface (threat-model R).
-- Every Tier rating set/changed + every Profile create + every selection
-- set/cleared writes one row. No FK to the subject row: the audit trail must
-- survive a future hard-delete of a profile / rating; the subject_id (the row
-- UUID) is preserved verbatim. Append-only by construction: SELECT + INSERT
-- policies only under FORCE RLS (mirrors decisions_audit, evidence_audit_log).

CREATE TABLE csf_assessment_audit (
    id            UUID PRIMARY KEY,
    tenant_id     UUID NOT NULL,
    -- The framework_version this assessment change was made against (which CSF
    -- version) — answers "against which CSF version" from threat-model R.
    framework_version_id  UUID NOT NULL,
    -- 'tier' | 'profile' | 'selection' — which assessment object changed.
    subject_kind  TEXT NOT NULL,
    -- The UUID of the changed csf_tier_ratings / csf_profiles /
    -- csf_profile_selections row (NOT an FK — see above).
    subject_id    UUID NOT NULL,
    action        TEXT NOT NULL,
    -- The credential id / user that drove the change (who).
    actor         TEXT NOT NULL,
    -- Free-form detail: for a tier change, the new tier token; for a selection,
    -- the Subcategory code + new outcome.
    detail        TEXT NOT NULL DEFAULT '',
    occurred_at   TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT csf_assessment_audit_subject_kind_chk
        CHECK (subject_kind IN ('tier', 'profile', 'selection')),
    CONSTRAINT csf_assessment_audit_action_chk
        CHECK (action IN (
            'tier_rated',
            'tier_rerated',
            'profile_created',
            'selection_set',
            'selection_cleared'
        )),
    CONSTRAINT csf_assessment_audit_actor_nonempty
        CHECK (length(actor) > 0)
);

CREATE INDEX idx_csf_assessment_audit_tenant_occurred
    ON csf_assessment_audit (tenant_id, occurred_at DESC);
CREATE INDEX idx_csf_assessment_audit_tenant_subject
    ON csf_assessment_audit (tenant_id, subject_id, occurred_at DESC);

ALTER TABLE csf_assessment_audit ENABLE ROW LEVEL SECURITY;
ALTER TABLE csf_assessment_audit FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_read ON csf_assessment_audit
    FOR SELECT
    USING (current_tenant_matches(tenant_id));
CREATE POLICY tenant_write ON csf_assessment_audit
    FOR INSERT
    WITH CHECK (current_tenant_matches(tenant_id));

GRANT SELECT, INSERT ON csf_assessment_audit TO atlas_app;
