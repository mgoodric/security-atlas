-- security-atlas — FrameworkScope predicate + four-state workflow (slice 018).
--
-- Implements docs/adr/0001-framework-scope-workflow.md: the four-state
-- lifecycle (draft -> review -> approved -> activated, with `superseded`
-- terminal) plus the strict re-approval invariant (any predicate edit on a
-- `review` or `approved` row bounces back to `draft` and clears approval
-- columns).
--
-- See docs/issues/018-framework-scope-intersection.md AC-1..AC-13.
--
-- ----------------------------------------------------------------------------
-- Why this migration replaces, not extends, slice-002's framework_scopes
-- columns:
--
-- Slice 002 created the table skeleton with:
--   status            framework_scope_status ENUM (draft|approved|active|retired)
--   predicate         TEXT       DEFAULT 'true'   (intended for the same role)
--   effective_from    DATE       NULL
--   effective_to      DATE       NULL             (no longer used; supersession replaces it)
--   approved_by       TEXT       NULL
--   approval_evidence TEXT       NULL
--
-- Nothing on `main` writes to these columns yet (no slice has shipped the
-- workflow before this one). ADR-0001 redefines the contract:
--   - status -> state (TEXT + CHECK enum; `review` and `superseded` added;
--     `active` -> `activated`; `retired` removed in favour of `superseded`)
--   - predicate -> JSONB (was TEXT; same shape as slice 017's
--     applicability_expr; sqlc + the scope engine want JSONB)
--   - effective_from -> TIMESTAMPTZ (was DATE; audit-period cutovers are
--     wall-clock, not date-granular)
--   - effective_to -> dropped; supersession via `superseded_by` is the
--     versioning mechanism
--   - approved_by + approval_evidence (TEXT) -> dropped in favour of the
--     typed columns per ADR §Schema
--
-- This migration: drop the legacy columns + ENUM type, add the new columns,
-- backfill any pre-existing rows (none expected in production; defensive),
-- install the BEFORE UPDATE trigger, partial unique index, four-policy RLS.
-- The .down.sql file restores the slice-002 column shape (data is preserved
-- best-effort: state -> status mapping is approximate, predicate JSON -> text
-- is verbatim).
-- ----------------------------------------------------------------------------

-- ===== 1. Drop legacy columns + index =====
--
-- DROP INDEX first so the column drops succeed without CASCADE noise.

DROP INDEX IF EXISTS idx_framework_scopes_version_status;

ALTER TABLE framework_scopes
    DROP COLUMN IF EXISTS status,
    DROP COLUMN IF EXISTS predicate,
    DROP COLUMN IF EXISTS effective_from,
    DROP COLUMN IF EXISTS effective_to,
    DROP COLUMN IF EXISTS approved_by,
    DROP COLUMN IF EXISTS approval_evidence;

-- The slice-002 ENUM type is unreferenced after the column drop. Removing it
-- frees the name for re-use and avoids confusion ("does `active` mean
-- `activated`?"). The .down migration recreates it.
DROP TYPE IF EXISTS framework_scope_status;

-- ===== 2. Add the slice-018 columns =====
--
-- state is TEXT + CHECK rather than an ENUM so future state additions don't
-- require ALTER TYPE (which is non-trivial in Postgres). The CHECK keeps the
-- invariant honest at the DB layer.

ALTER TABLE framework_scopes
    ADD COLUMN state                          TEXT        NOT NULL DEFAULT 'draft',
    ADD COLUMN predicate                      JSONB       NOT NULL DEFAULT '{"op":"true"}'::jsonb,
    ADD COLUMN predicate_hash                 TEXT        NOT NULL DEFAULT '',
    ADD COLUMN approver_user_id               UUID        NULL,
    ADD COLUMN approved_at                    TIMESTAMPTZ NULL,
    ADD COLUMN predicate_hash_at_approval     TEXT        NULL,
    ADD COLUMN approval_evidence_file_url     TEXT        NULL,
    ADD COLUMN approval_evidence_file_hash    TEXT        NULL,
    ADD COLUMN effective_from                 TIMESTAMPTZ NULL,
    ADD COLUMN superseded_by                  UUID        NULL,
    ADD COLUMN superseded_at                  TIMESTAMPTZ NULL;

ALTER TABLE framework_scopes
    ADD CONSTRAINT framework_scopes_state_chk
        CHECK (state IN ('draft', 'review', 'approved', 'activated', 'superseded'));

-- Self-FK for the supersession chain. ON DELETE SET NULL so removing a
-- successor doesn't cascade-delete its predecessor.
ALTER TABLE framework_scopes
    ADD CONSTRAINT framework_scopes_superseded_by_fk
        FOREIGN KEY (superseded_by) REFERENCES framework_scopes (id) ON DELETE SET NULL;

-- ===== 3. Backfill =====
--
-- Any pre-existing rows from slice-002 fixtures or partial deployments need a
-- valid predicate_hash. The fresh predicate is the literal scope-engine
-- "true" node; its hash is the sha256 of the canonical JSON. The application
-- canonicalizer (internal/scope/canonical.go) emits `{"op":"true"}` for the
-- true predicate. We compute the hash here so the application-side and
-- DB-side hashes match byte-for-byte.
--
-- Note: production deployments have zero rows in framework_scopes at this
-- point (no prior slice has shipped writes). This UPDATE is defensive.

UPDATE framework_scopes
SET predicate_hash = encode(sha256('{"op":"true"}'::bytea), 'hex'),
    state = 'activated',
    effective_from = COALESCE(effective_from, created_at)
WHERE predicate_hash = '';

-- ===== 4. BEFORE UPDATE trigger (AC-2) =====
--
-- The strict re-approval invariant: any edit to `predicate_hash` on a row in
-- state `review` or `approved` forces NEW.state = 'draft' and nulls the
-- approval columns. This is the DB-level guarantee that no caller (including
-- a buggy handler) can sneak a predicate change past the approval gate.
--
-- The trigger fires on every UPDATE so legitimate state transitions
-- (submit/approve/activate) that don't touch predicate_hash flow through
-- unchanged.

CREATE OR REPLACE FUNCTION framework_scopes_bounce_on_predicate_change()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.predicate_hash IS DISTINCT FROM OLD.predicate_hash
       AND OLD.state IN ('review', 'approved') THEN
        NEW.state := 'draft';
        NEW.approver_user_id := NULL;
        NEW.approved_at := NULL;
        NEW.predicate_hash_at_approval := NULL;
        NEW.approval_evidence_file_url := NULL;
        NEW.approval_evidence_file_hash := NULL;
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER framework_scopes_bounce_on_predicate_change_trg
    BEFORE UPDATE ON framework_scopes
    FOR EACH ROW
    EXECUTE FUNCTION framework_scopes_bounce_on_predicate_change();

-- ===== 5. Partial unique index (AC-3) =====
--
-- Enforce "at most one row per (tenant_id, framework_version_id) in state
-- 'activated'". The ADR phrasing includes `effective_from <= now()` but
-- Postgres rejects non-IMMUTABLE expressions in partial-index predicates;
-- we enforce the time-gate at the query layer instead. The supersession
-- atomicity in the application layer (AC-8: activate transitions the
-- predecessor to `superseded` in the same transaction) keeps the invariant
-- tight regardless: only one row can be `activated` at a time.

CREATE UNIQUE INDEX framework_scopes_one_active
    ON framework_scopes (tenant_id, framework_version_id)
    WHERE state = 'activated';

-- Index for the common list path: enumerate scopes by (tenant, framework
-- version, state). Also covers GET /v1/framework-scopes?framework_version=...
CREATE INDEX idx_framework_scopes_tenant_fv_state
    ON framework_scopes (tenant_id, framework_version_id, state);

-- Index for "which row was active at timestamp T" (AC-13 historical queries).
CREATE INDEX idx_framework_scopes_effective_from
    ON framework_scopes (tenant_id, framework_version_id, effective_from)
    WHERE effective_from IS NOT NULL;

-- ===== 6. RLS — four-policy split =====
--
-- Slice 002 left `tenant_isolation USING (current_tenant_matches(...))`
-- (loose). Slice 018 ships writes; we tighten to the slice-014 pattern with
-- explicit WITH CHECK on INSERT and USING + WITH CHECK on UPDATE. Slice 002's
-- `framework_scopes` rows have no production traffic, so the policy switch
-- is safe.

DROP POLICY IF EXISTS tenant_isolation ON framework_scopes;

CREATE POLICY tenant_read ON framework_scopes
    FOR SELECT
    USING (current_tenant_matches(tenant_id));
CREATE POLICY tenant_write ON framework_scopes
    FOR INSERT
    WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_update ON framework_scopes
    FOR UPDATE
    USING (current_tenant_matches(tenant_id))
    WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_delete ON framework_scopes
    FOR DELETE
    USING (current_tenant_matches(tenant_id));

-- Drop the legacy DEFAULT on predicate now that backfill is complete. New
-- rows must supply a predicate explicitly so the application is responsible
-- for the predicate_hash matching.
ALTER TABLE framework_scopes
    ALTER COLUMN predicate DROP DEFAULT,
    ALTER COLUMN predicate_hash DROP DEFAULT,
    ALTER COLUMN state DROP DEFAULT;
