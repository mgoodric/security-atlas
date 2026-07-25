-- security-atlas — vendor_reviews ledger (slice 688).
--
-- Per-review history surface. Slice 024 (vendor lite) carried a single
-- `last_review_date` scalar on the vendors row — there was no per-review
-- record (no reviewer, no outcome, no notes-per-review, no cadence-
-- adherence trail). Slice 686's read-only detail page named that gap with
-- an honest placeholder card; this slice replaces the placeholder with a
-- real timeline drawn from an append-only ledger.
--
-- One table lands in this slice:
--
--   vendor_reviews     — append-only ledger; one row per completed review.
--                        tenant-scoped, RLS. A past review is never edited
--                        in place (canvas Invariant 2 — append-only spirit;
--                        the store exposes no UPDATE path). The most-recent
--                        reviewed_at is the source for the vendor row's
--                        last_review_date scalar, which the store keeps
--                        consistent on each recorded review (decisions log
--                        D2).
--
-- RLS pattern mirrors slice 024's vendors table byte-for-byte
-- (tenant_read / tenant_write / tenant_update / tenant_delete predicated on
-- current_tenant_matches(tenant_id)) so cross-tenant reads AND writes are
-- denied at the database, not the application (canvas Invariant 6). The
-- tenant_update policy is present for shape-parity with every other tenant-
-- scoped table; the store never issues an UPDATE against this table — the
-- append-only guarantee is a store-layer invariant, not a DB trigger.

-- ===== Enum types =====
--
-- review_outcome captures the disposition of a completed review. Four
-- buckets are enough for a 30–80-vendor portfolio: a clean pass, a pass
-- with findings to track, a fail (relationship at risk / remediation
-- required), and a waived review (risk-accepted for the cycle, recorded
-- so the cadence trail is honest about the gap).
--
-- Wrapped in a DO/EXCEPTION block for re-run idempotency (slice 065 bug #3):
-- Postgres has no `CREATE TYPE IF NOT EXISTS`, and the self-host bootstrap
-- re-applies every migration on each `docker compose up`.

DO $$ BEGIN
    CREATE TYPE vendor_review_outcome AS ENUM (
        'pass',
        'pass_with_findings',
        'fail',
        'waived'
    );
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

-- ===== vendor_reviews =====
--
-- Append-only ledger. Composite FK (tenant_id, vendor_id) -> vendors
-- (tenant_id, id) keeps the cross-tenant door shut at the DB layer and
-- CASCADE-deletes a vendor's review history when the vendor is removed.
--
-- reviewed_at is DATE (vendor reviews are date-granular, matching the
-- vendors.last_review_date column). created_at is the immutable insert
-- timestamp (when the row was recorded, distinct from when the review
-- happened). There is intentionally no updated_at column — a recorded
-- review is immutable.

CREATE TABLE vendor_reviews (
    id                  UUID PRIMARY KEY,
    tenant_id           UUID NOT NULL,
    vendor_id           UUID NOT NULL,
    reviewed_at         DATE NOT NULL,
    reviewer            TEXT NOT NULL DEFAULT '',
    outcome             vendor_review_outcome NOT NULL DEFAULT 'pass',
    notes               TEXT NOT NULL DEFAULT '',
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),

    FOREIGN KEY (tenant_id, vendor_id)
        REFERENCES vendors(tenant_id, id) ON DELETE CASCADE
);

-- Hot-path index for the per-vendor history read (AC-3): newest-first by
-- reviewed_at, tie-broken by created_at so two reviews recorded for the
-- same date have a stable order.
CREATE INDEX idx_vendor_reviews_tenant_vendor
    ON vendor_reviews (tenant_id, vendor_id, reviewed_at DESC, created_at DESC);

-- ===== Back-fill (AC-2) =====
--
-- One ledger row per existing vendor with a non-null last_review_date so
-- no history is silently lost when the ledger lands. The back-filled row
-- carries the scalar date as reviewed_at, an empty reviewer (unknown —
-- the scalar never recorded who), the default 'pass' outcome, and a notes
-- breadcrumb marking it as a migration back-fill. created_at defaults to
-- now() (when the back-fill ran), which is correct: the row was recorded
-- at migration time, even though the review it represents happened on
-- last_review_date. Decisions log D2.

INSERT INTO vendor_reviews (id, tenant_id, vendor_id, reviewed_at, reviewer, outcome, notes)
SELECT
    gen_random_uuid(),
    v.tenant_id,
    v.id,
    v.last_review_date,
    '',
    'pass',
    'Back-filled from vendor.last_review_date (slice 688 migration).'
FROM vendors v
WHERE v.last_review_date IS NOT NULL;

-- ===== Row-Level Security =====
--
-- Mirrors slice 024's vendors tenant_read/write/update/delete split with
-- explicit WITH CHECK. atlas_migrate has BYPASSRLS so DDL + the back-fill
-- above apply under FORCE.

ALTER TABLE vendor_reviews ENABLE ROW LEVEL SECURITY;
ALTER TABLE vendor_reviews FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_read ON vendor_reviews
    FOR SELECT
    USING (current_tenant_matches(tenant_id));
CREATE POLICY tenant_write ON vendor_reviews
    FOR INSERT
    WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_update ON vendor_reviews
    FOR UPDATE
    USING (current_tenant_matches(tenant_id))
    WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_delete ON vendor_reviews
    FOR DELETE
    USING (current_tenant_matches(tenant_id));

GRANT SELECT, INSERT, UPDATE, DELETE ON
    vendor_reviews
TO atlas_app;
