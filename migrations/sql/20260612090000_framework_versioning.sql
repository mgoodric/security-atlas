-- security-atlas — slice 484: framework-versioning capability.
--
-- Implements ADR 0019 (docs/adr/0019-framework-versioning-capability.md). Adds
-- the CAPABILITY LAYER on top of the framework_versions storage that already
-- exists (migration _init: `framework_versions` with UNIQUE(framework_id,
-- version), the `framework_version_status` enum, `effective_from/to`, and
-- `frameworks.latest_version_id`). No schema rebuild — this migration adds:
--
--   1. framework_version_migrations  — the migration-suggest REVIEW QUEUE
--      (one row per suggested/flagged requirement carryover between two
--      adjacent versions of one framework). A human approves each row; the
--      job NEVER auto-applies (AI-assist boundary; P0-484-1 / AC-3 / AC-4).
--   2. framework_version_audit       — append-only audit covering BOTH the
--      version-status lifecycle transitions (promote/revert) AND the
--      migration-suggestion approve/reject acts (threat-model R / AC-1 / AC-4).
--   3. an IMMUTABILITY TRIGGER on framework_requirements that denies
--      UPDATE/DELETE on any requirement whose framework_version is NON-draft
--      (i.e. current/legacy/withdrawn). A frozen version's requirements ship
--      a NEW version, never an in-place edit (§3.3 / threat-model T /
--      P0-484-2 / AC-2 / AC-8).
--   4. narrow column GRANTs so the API (atlas_app) can run the lifecycle +
--      approval writes under its own role (same write mechanism as slice 483).
--
-- # Enum reality (slice 484 decisions-log D1 — READ THIS)
--
-- ADR 0019 §1 says "transition the prior version to `superseded` (reusing the
-- existing enum value)". The ACTUAL `framework_version_status` enum on `main`
-- is { current, legacy, withdrawn } — it has NO `superseded`/`deprecated`
-- value (the ADR's premise was slightly off on the literal value name). The
-- existing enum value that carries the ADR's INTENDED semantic ("replaced by a
-- newer current version, but still valid for historical audits") is `legacy`,
-- and the soc2import loader ALREADY demotes a superseded version to `legacy`
-- (DemoteCurrentFrameworkVersions). So we HONOR THE ADR'S SUBSTANCE by reusing
-- the existing `legacy` value as the "superseded" status (replaced-but-valid,
-- readable-when-pinned), distinct from `withdrawn` (= the ADR's "deprecated"/
-- discouraged). NO enum change is needed — the ADR's core premise ("the enum
-- already has the needed status values, no schema rebuild") holds with the
-- ACTUAL values once `legacy` is read as "superseded". See ADR 0019
-- Implementation notes + decisions-log D1.
--
-- # Catalog tables — NOT tenant-scoped (deliberate; do NOT add tenant RLS)
--
-- frameworks / framework_versions / framework_requirements / fw_to_scf_edges
-- are BUNDLED CATALOG tables (no tenant_id, no RLS — migration _013 header).
-- The two new tables below are catalog-level too: the gate is ADMIN-ROLE
-- AUTHZ (the lifecycle/approval handlers require cred.IsAdmin) + an
-- APPEND-ONLY audit (SELECT + INSERT grants only on the audit table; no
-- UPDATE/DELETE) — NOT the four-policy tenant-RLS shape. This mirrors slice
-- 483's fw_to_scf_edge_tier_transitions discipline exactly.
--
-- # Write mechanism (slice 484 decisions-log D2; same as slice 483 D1)
--
-- The API runs as atlas_app, which today holds only SELECT on
-- framework_versions and frameworks. We grant atlas_app a NARROW column-level
-- UPDATE(status) on framework_versions and UPDATE(latest_version_id) on
-- frameworks so the promotion handler can flip the lifecycle under its own
-- role — and ONLY those columns (never version, requirement_count, the
-- requirement content, etc.). The LEGALITY of a promotion is enforced in Go
-- (internal/frameworkversion) and the trust gate is the admin-role authz
-- check. This avoids routing the lifecycle write through a privileged
-- BYPASSRLS pool for a catalog-level edit. atlas_migrate keeps full writes for
-- the loader.
--
-- Additive + reversible (P0-484: down migration drops both tables + the
-- trigger + function and revokes the grants; it touches NO existing data and
-- adds NO enum value, so the down is clean).
--
-- Migration slot 20260612090000 (after slice 483's _crosswalk_mapping_tier).
-- Plain SQL (Atlas community caveat — no HCL row_security blocks).
-- Issue: docs/issues/484-framework-versioning-capability.md
-- Reversible via 20260612090000_framework_versioning.down.sql.

-- ===== framework_version_migration_status enum =====
--
-- Lifecycle of a single suggested/flagged requirement carryover in the review
-- queue. `pending` = awaiting human review; `approved` = a human accepted the
-- carryover; `rejected` = a human declined it. The job only ever WRITES
-- `pending` rows; approve/reject are the privileged human acts (P0-484-1).
--
-- Wrapped in DO/EXCEPTION for self-host re-run idempotency (slice 065 bug #3).
-- The bare-enum twin lives in internal/db/sqlc-schema/_enums.sql so sqlc
-- v1.31.1 emits a typed Go enum rather than interface{}.
DO $$ BEGIN
    CREATE TYPE framework_version_migration_status AS ENUM (
        'pending',
        'approved',
        'rejected'
    );
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

-- ===== framework_version_migration_match_kind enum =====
--
-- WHY a row is in the queue. `exact_code` = the requirement code matched
-- EXACTLY between the two versions (the ONLY auto-suggested 1:1 carryover —
-- ADR 0019 §3); everything else is FLAGGED for human review:
--   `added`   = a code present in the TO version but not the FROM version.
--   `removed` = a code present in the FROM version but not the TO version.
-- No fuzzy/title-similarity match exists (deferred enhancement); renamed/
-- split/merged requirements surface as an added+removed pair the reviewer
-- reconciles by hand.
DO $$ BEGIN
    CREATE TYPE framework_version_migration_match_kind AS ENUM (
        'exact_code',
        'added',
        'removed'
    );
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

-- ===== framework_version_audit_action enum =====
--
-- The kind of audited act. promote/revert are version-lifecycle transitions;
-- migration_approve/migration_reject are the human review-queue decisions.
DO $$ BEGIN
    CREATE TYPE framework_version_audit_action AS ENUM (
        'promote',
        'revert',
        'migration_approve',
        'migration_reject'
    );
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

-- ===== framework_version_migrations (the suggest review queue) =====
--
-- One row per requirement carryover between two adjacent versions of ONE
-- framework. The migration-suggest job (internal/frameworkversion) populates
-- this table given (from_version_id, to_version_id); it writes ONLY `pending`
-- rows and NEVER mutates a requirement or an edge (P0-484-1 / AC-3). A human
-- approves/rejects each row one at a time through the admin surface (AC-4),
-- and each decision appends a framework_version_audit row in the same tx.
--
-- match_kind = exact_code  -> from_requirement_id AND to_requirement_id both set
-- match_kind = added       -> to_requirement_id   set, from_requirement_id NULL
-- match_kind = removed     -> from_requirement_id set, to_requirement_id   NULL
--
-- The CHECK encodes that invariant so a malformed row can never be inserted.
-- requirement_code is denormalized for the reviewer's convenience (the code
-- that drove the match / flag); both version ids are stored so the queue is
-- self-describing without a join back through the requirements.
CREATE TABLE IF NOT EXISTS framework_version_migrations (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    framework_id        UUID NOT NULL REFERENCES frameworks(id) ON DELETE CASCADE,
    from_version_id     UUID NOT NULL REFERENCES framework_versions(id) ON DELETE CASCADE,
    to_version_id       UUID NOT NULL REFERENCES framework_versions(id) ON DELETE CASCADE,
    from_requirement_id UUID NULL REFERENCES framework_requirements(id) ON DELETE CASCADE,
    to_requirement_id   UUID NULL REFERENCES framework_requirements(id) ON DELETE CASCADE,
    requirement_code    TEXT NOT NULL,
    match_kind          framework_version_migration_match_kind NOT NULL,
    status              framework_version_migration_status NOT NULL DEFAULT 'pending',
    reviewer_id         UUID NULL,
    note                TEXT NOT NULL DEFAULT '',
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    decided_at          TIMESTAMPTZ NULL,
    -- Re-running the suggest job for the same version pair is idempotent: one
    -- queue row per (version-pair, code, match_kind).
    UNIQUE (from_version_id, to_version_id, requirement_code, match_kind),
    CONSTRAINT fw_version_migration_shape CHECK (
        (match_kind = 'exact_code' AND from_requirement_id IS NOT NULL AND to_requirement_id IS NOT NULL)
        OR (match_kind = 'added'   AND from_requirement_id IS NULL     AND to_requirement_id IS NOT NULL)
        OR (match_kind = 'removed' AND from_requirement_id IS NOT NULL AND to_requirement_id IS NULL)
    )
);

CREATE INDEX IF NOT EXISTS idx_fw_version_migrations_queue
    ON framework_version_migrations (to_version_id, status, created_at);

-- ===== framework_version_audit (append-only) =====
--
-- One immutable row per audited act (threat-model R / AC-1 / AC-4). Written in
-- the SAME transaction as the act (internal/frameworkversion.Store), mirroring
-- the slice-483 fw_to_scf_edge_tier_transitions discipline. actor_id is the
-- acting admin's atlas user id (the SubjectUserID from the verified JWT).
--
--   action = promote/revert            -> framework_version_id set (the
--                                         version whose status changed);
--                                         from_status/to_status record the move;
--                                         migration_id NULL.
--   action = migration_approve/reject  -> migration_id set (the queue row
--                                         decided); framework_version_id is the
--                                         to_version_id; from_status/to_status
--                                         NULL.
--
-- Append-only by construction: atlas_app gets SELECT + INSERT ONLY below (no
-- UPDATE/DELETE), so an audit row is immutable once written.
CREATE TABLE IF NOT EXISTS framework_version_audit (
    id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    framework_id         UUID NOT NULL REFERENCES frameworks(id) ON DELETE CASCADE,
    framework_version_id UUID NULL REFERENCES framework_versions(id) ON DELETE CASCADE,
    migration_id         UUID NULL REFERENCES framework_version_migrations(id) ON DELETE CASCADE,
    action               framework_version_audit_action NOT NULL,
    from_status          framework_version_status NULL,
    to_status            framework_version_status NULL,
    actor_id             UUID NOT NULL,
    note                 TEXT NOT NULL DEFAULT '',
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_fw_version_audit_framework
    ON framework_version_audit (framework_id, created_at DESC);

-- ===== immutability trigger on framework_requirements (§3.3 / AC-2 / AC-8) =====
--
-- A frozen (NON-draft) version's requirements are immutable: an attempt to
-- UPDATE a framework_requirements row IN PLACE whose framework_version is
-- current/legacy/withdrawn is REJECTED (P0-484-2 / threat-model T / §3.3 /
-- AC-8). Changes ship as a NEW version, never an in-place edit.
--
-- WHY UPDATE-ONLY (slice 484 decisions-log D6): §3.3 + threat-model T are about
-- EDITING a frozen version's requirements in place ("changes ship as a new
-- version, not in-place edits"). DELETE is deliberately NOT guarded — deleting
-- an entire obsolete version (its requirements via FK CASCADE, or an explicit
-- catalog GC) is a legitimate catalog-lifecycle operation, NOT an in-place
-- edit, and a DELETE guard would make every frozen version permanently
-- undeletable by ANY role (it fires even on a CASCADE from the parent version
-- and even for the superuser), breaking catalog GC + test teardown. The freeze
-- that the ADR/threat-model actually demands is "you cannot silently rewrite a
-- shipped requirement"; that is exactly an UPDATE. For the atlas_app role the
-- freeze is additionally GRANT-enforced (it holds only SELECT on
-- framework_requirements — no UPDATE at all), so this trigger is the
-- defense-in-depth that also stops the privileged atlas_migrate (loader) role
-- from a content-changing re-import of a frozen version (it must ship a new
-- version). An unchanged re-import produces no UPDATE (the loader skips
-- identical rows), so idempotent re-imports still pass.
--
-- INSERT is not guarded: a fresh load INSERTs the requirement set of a new
-- version. NOTE: there is no `framework_version_status = 'draft'` value on
-- `main` today (the enum is current/legacy/withdrawn). The trigger treats
-- current/legacy/withdrawn as the frozen set; if a future slice adds a `draft`
-- value for staged loads, this predicate already accommodates it (a draft
-- version's requirements stay editable).
CREATE OR REPLACE FUNCTION enforce_framework_requirement_immutability()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    v_status framework_version_status;
BEGIN
    SELECT status INTO v_status
        FROM framework_versions
        WHERE id = OLD.framework_version_id;

    IF v_status IS NOT NULL AND v_status IN ('current', 'legacy', 'withdrawn') THEN
        RAISE EXCEPTION
            'framework_requirements % belongs to a frozen framework_version (% / status=%); a frozen version''s requirements are immutable (canvas 3.3) — ship a new version',
            OLD.id, OLD.framework_version_id, v_status
            USING ERRCODE = 'raise_exception';
    END IF;

    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS trg_framework_requirement_immutability ON framework_requirements;
CREATE TRIGGER trg_framework_requirement_immutability
    BEFORE UPDATE ON framework_requirements
    FOR EACH ROW
    EXECUTE FUNCTION enforce_framework_requirement_immutability();

-- ===== grants =====
--
-- Lifecycle write mechanism (decisions-log D2): NARROW column-level grants so
-- the promotion handler can flip the lifecycle under the atlas_app role and
-- ONLY those columns.
GRANT UPDATE (status) ON framework_versions TO atlas_app;
GRANT UPDATE (latest_version_id) ON frameworks TO atlas_app;

-- Review queue: atlas_app inserts the suggestions and updates a row's
-- status/reviewer/decided_at/note on approve/reject. It does NOT delete queue
-- rows (a decided row stays for the audit trail; the audit table is the
-- forensic record). The migrate role keeps full writes for cleanup/backfill.
GRANT SELECT, INSERT ON framework_version_migrations TO atlas_app;
GRANT UPDATE (status, reviewer_id, decided_at, note) ON framework_version_migrations TO atlas_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON framework_version_migrations TO atlas_migrate;

-- Audit table: append-only — SELECT + INSERT to atlas_app, NO UPDATE/DELETE.
GRANT SELECT, INSERT ON framework_version_audit TO atlas_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON framework_version_audit TO atlas_migrate;
