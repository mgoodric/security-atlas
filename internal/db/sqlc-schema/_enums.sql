-- security-atlas — bare enum declarations for sqlc parsing (slice 109).
--
-- WHY THIS FILE EXISTS
--
-- The production migrations under `migrations/sql/` wrap every
-- `CREATE TYPE ... AS ENUM (...)` in an idempotent DO block:
--
--     DO $$ BEGIN
--         CREATE TYPE ... AS ENUM (...);
--     EXCEPTION WHEN duplicate_object THEN NULL; END $$;
--
-- This idiom is required because Postgres has no
-- `CREATE TYPE IF NOT EXISTS` and the self-host bootstrap re-applies
-- migrations on every `docker compose up` (slice 065 bug #3). Removing
-- the DO blocks would break self-host idempotency.
--
-- But sqlc v1.31.1's schema parser cannot reach `CREATE TYPE` inside a
-- `DO $$ BEGIN ... END $$` block. Every column declared with one of
-- the wrapped enum types degrades to `interface{}` in the generated
-- Go code, dropping the type-safe enum constants the rest of the
-- codebase depends on (`ControlImplementationTypeAutomated`, etc.).
--
-- This file restores type-safe generation by declaring every enum as
-- a BARE `CREATE TYPE` statement. `sqlc.yaml` lists this file FIRST
-- in the `schema:` array so sqlc sees the bare declarations before
-- the DO-block migrations. The migration files that follow re-declare
-- the same types inside DO blocks; sqlc silently tolerates the
-- duplicates (verified against v1.31.1).
--
-- THIS FILE IS NEVER APPLIED TO A LIVE DATABASE.
--
-- The justfile migration runner globs `migrations/sql/*.sql` only —
-- it never reaches `internal/db/sqlc-schema/`. Production schema
-- state is governed exclusively by `migrations/sql/`. This file is
-- build-time input to sqlc, not run-time DDL.
--
-- HOW TO ADD A NEW ENUM
--
-- 1. Add the production `DO $$ BEGIN ... CREATE TYPE ... END $$;`
--    block to the appropriate migration under `migrations/sql/`.
-- 2. Mirror the same bare `CREATE TYPE` declaration here. Keep the
--    type name + value list identical — drift between this file and
--    the migration would mean sqlc generates code against one shape
--    while production runs another.
-- 3. Run `sqlc generate`. The new column should emit as the typed
--    enum, not `interface{}`.
--
-- See `docs/audit-log/109-sqlc-toolchain-pin-decisions.md` for the
-- full root-cause analysis.

-- ===== From migrations/sql/20260511000000_init.sql =====

CREATE TYPE control_implementation_type AS ENUM (
    'automated',
    'semi_automated',
    'manual_attested',
    'manual_periodic'
);

CREATE TYPE control_lifecycle_state AS ENUM (
    'draft',
    'proposed',
    'active',
    'deprecated',
    'retired'
);

CREATE TYPE risk_category AS ENUM (
    'confidentiality',
    'integrity',
    'availability',
    'privacy',
    'regulatory',
    'operational',
    'financial'
);

CREATE TYPE risk_methodology AS ENUM (
    'nist_800_30',
    'fair',
    'cis_ram',
    'iso_27005',
    'qualitative_5x5'
);

CREATE TYPE risk_treatment AS ENUM (
    'accept',
    'mitigate',
    'transfer',
    'avoid'
);

CREATE TYPE scope_environment AS ENUM (
    'prod',
    'staging',
    'dev',
    'sandbox'
);

CREATE TYPE scope_data_classification AS ENUM (
    'restricted',
    'confidential',
    'internal',
    'public'
);

CREATE TYPE evidence_result AS ENUM (
    'pass',
    'fail',
    'na',
    'inconclusive'
);

CREATE TYPE evidence_freshness_class AS ENUM (
    'realtime',
    'daily',
    'weekly',
    'monthly',
    'quarterly',
    'annual'
);

CREATE TYPE framework_version_status AS ENUM (
    'current',
    'legacy',
    'withdrawn'
);

CREATE TYPE policy_status AS ENUM (
    'draft',
    'under_review',
    'approved',
    'published',
    'superseded'
);

-- `framework_scope_status` is declared by the slice-002 init migration
-- but slice 018 retired the column in favor of TEXT. Intentionally
-- NOT mirrored here — no committed Go code references the type.

-- ===== From migrations/sql/20260511000006_vendor_lite.sql =====

CREATE TYPE vendor_criticality AS ENUM (
    'low',
    'medium',
    'high'
);

CREATE TYPE vendor_review_cadence AS ENUM (
    'monthly',
    'quarterly',
    'biannual',
    'annual'
);

-- ===== From migrations/sql/20260511000013_framework_requirements_and_edges.sql =====

CREATE TYPE strm_relationship_type AS ENUM (
    'equal',
    'subset_of',
    'superset_of',
    'intersects_with',
    'no_relationship'
);

CREATE TYPE crosswalk_source_attribution AS ENUM (
    'scf_official',
    'community_draft',
    'org_internal'
);

-- ===== From migrations/sql/20260511000014_risk_hierarchy_decisions.sql =====

CREATE TYPE risk_level AS ENUM (
    'team',
    'org',
    'company'
);

CREATE TYPE decision_status AS ENUM (
    'active',
    'revisited',
    'superseded',
    'expired'
);

-- ===== From migrations/sql/20260608080000_csf_tier_profile.sql (slice 515) =====

CREATE TYPE csf_tier AS ENUM (
    'tier1_partial',
    'tier2_risk_informed',
    'tier3_repeatable',
    'tier4_adaptive'
);

CREATE TYPE csf_profile_kind AS ENUM (
    'current',
    'target'
);

-- ===== From migrations/sql/20260611000000_vendor_reviews_ledger.sql (slice 688) =====

CREATE TYPE vendor_review_outcome AS ENUM (
    'pass',
    'pass_with_findings',
    'fail',
    'waived'
);
