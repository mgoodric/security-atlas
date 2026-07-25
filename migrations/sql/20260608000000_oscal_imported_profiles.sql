-- migrations/sql/20260608000000_oscal_imported_profiles.sql
--
-- Slice 511 — OSCAL profile import (resolve direction of invariant #8).
--
-- A resolved OSCAL profile (e.g. a FedRAMP Low/Moderate/High baseline) is,
-- structurally, an imported control set: the profile's import / merge /
-- modify directives resolve against a supplied catalog into a concrete
-- control list, which persists exactly like a slice-492 imported catalog.
-- Rather than fork a parallel `imported_profiles` table tree, slice 511
-- REUSES the slice-492 tables (imported_catalogs / imported_catalog_controls
-- / imported_catalog_audit_log) and adds a `kind` discriminator + a
-- profile-title provenance column, so the read models, RLS policies, and the
-- append-only audit trail are shared. See slice-511 decisions-log D4.
--
-- This migration is purely ADDITIVE:
--   * imported_catalogs.kind          — 'catalog' (default) | 'profile'.
--                                       A resolved profile baseline is a
--                                       distinct, queryable set.
--   * imported_catalogs.profile_title — the resolved profile's declared
--                                       OSCAL title (provenance / display);
--                                       empty string for a catalog import.
--   * the source CHECK extends to allow 'oscal-profile-import'.
--   * the audit_log action CHECK extends to allow 'profile_imported' +
--     'profile_import_rejected'.
--
-- Constitutional invariants honored (unchanged from slice 492):
--   #6  Tenant isolation at the DB layer (the slice-492 RLS policies cover
--       the new columns by construction — no policy change).
--   #7  scf_anchor_id maps requirement -> SCF anchor; a resolved profile
--       control reconciles to the SCF spine exactly as a catalog control
--       does (slice-511 D3).
--   #8  OSCAL is the wire format (resolve direction).
--
-- P0-511-4: the bundled SCF spine (scf_anchors) is untouched — this is an
-- ALTER on slice-492 tenant-scoped tables only.
--
-- NOT-NULL discipline: the two new columns carry safe defaults
-- (kind DEFAULT 'catalog', profile_title DEFAULT ''), so every pre-existing
-- imported_catalogs row backfills cleanly and no integration-test fixture
-- helper requires patching.
--
-- Reversibility: paired .down.sql restores the slice-492 CHECK shapes and
-- drops the two columns.

-- ---- kind discriminator + profile-title provenance ----

ALTER TABLE imported_catalogs
    ADD COLUMN kind          TEXT NOT NULL DEFAULT 'catalog',
    ADD COLUMN profile_title TEXT NOT NULL DEFAULT '';

ALTER TABLE imported_catalogs
    ADD CONSTRAINT imported_catalogs_kind_chk
        CHECK (kind IN ('catalog', 'profile'));

-- ---- extend the source CHECK to allow the profile-import source ----

ALTER TABLE imported_catalogs
    DROP CONSTRAINT imported_catalogs_source_chk;
ALTER TABLE imported_catalogs
    ADD CONSTRAINT imported_catalogs_source_chk
        CHECK (source IN ('oscal-import', 'oscal-profile-import'));

-- ---- extend the audit-log action CHECK to allow profile actions ----

ALTER TABLE imported_catalog_audit_log
    DROP CONSTRAINT imported_catalog_audit_log_action_chk;
ALTER TABLE imported_catalog_audit_log
    ADD CONSTRAINT imported_catalog_audit_log_action_chk
        CHECK (action IN (
            'catalog_imported',
            'import_rejected',
            'profile_imported',
            'profile_import_rejected'
        ));

-- A partial index over profile baselines so listing resolved profiles for a
-- tenant is index-served (most-recent-first), without slowing catalog reads.
CREATE INDEX idx_imported_catalogs_tenant_profiles
    ON imported_catalogs (tenant_id, imported_at DESC)
    WHERE kind = 'profile';
