-- name: CreateVendorReview :one
-- Append a completed review to the ledger. tenant_id is captured directly so
-- RLS evaluates the INSERT WITH CHECK policy. The composite FK
-- (tenant_id, vendor_id) -> vendors enforces the vendor exists for this tenant
-- (a cross-tenant or fabricated vendor_id trips a foreign_key_violation, which
-- the store maps to ErrVendorNotFound). Append-only: there is no UpdateVendorReview.
INSERT INTO vendor_reviews (
    id, tenant_id, vendor_id, reviewed_at, reviewer, outcome, notes
)
VALUES (
    $1, $2, $3, $4, $5, $6, $7
)
RETURNING *;

-- name: ListVendorReviews :many
-- AC-3 per-vendor review history, newest-first. reviewed_at DESC is the
-- primary order; created_at DESC tie-breaks two reviews recorded for the same
-- date so the order is stable. RLS scopes the read to the active tenant.
SELECT * FROM vendor_reviews
WHERE tenant_id = $1 AND vendor_id = $2
ORDER BY reviewed_at DESC, created_at DESC;

-- name: LatestVendorReviewDate :one
-- The most-recent reviewed_at for a vendor, used to keep vendors.last_review_date
-- consistent with the ledger (AC-2, decisions log D2). Returns no row when the
-- vendor has no reviews; the store treats pgx.ErrNoRows as "leave the scalar".
SELECT reviewed_at FROM vendor_reviews
WHERE tenant_id = $1 AND vendor_id = $2
ORDER BY reviewed_at DESC, created_at DESC
LIMIT 1;
