-- name: CreateVendor :one
-- Insert a vendor. tenant_id is captured directly so RLS evaluates the
-- INSERT WITH CHECK policy. dpa_signed_at is required by CHECK constraint
-- whenever dpa_signed=true.
INSERT INTO vendors (
    id, tenant_id, name, domain, criticality, contract_start, contract_end,
    dpa_signed, dpa_signed_at, review_cadence, last_review_date, owner_user,
    linked_sow_uri, notes, annual_cost, currency, renewal_date, auto_renew,
    license_count, tool_category, cost_owner, commercial_status, billing_cadence
)
VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16,
    $17, $18, $19, $20, $21, $22, $23
)
RETURNING *;

-- name: UpdateVendor :one
-- Full-row update. Caller is responsible for sending every field; partial
-- updates are not supported in lite (no PATCH semantics merge). updated_at
-- is application-owned (no trigger).
UPDATE vendors SET
    name             = $3,
    domain           = $4,
    criticality      = $5,
    contract_start   = $6,
    contract_end     = $7,
    dpa_signed       = $8,
    dpa_signed_at    = $9,
    review_cadence   = $10,
    last_review_date = $11,
    owner_user       = $12,
    linked_sow_uri   = $13,
    notes            = $14,
    annual_cost      = $15,
    currency         = $16,
    renewal_date     = $17,
    auto_renew       = $18,
    license_count    = $19,
    tool_category    = $20,
    cost_owner       = $21,
    commercial_status = $22,
    billing_cadence  = $23,
    updated_at       = now()
WHERE tenant_id = $1 AND id = $2
RETURNING *;

-- name: GetVendor :one
SELECT * FROM vendors
WHERE tenant_id = $1 AND id = $2;

-- name: DeleteVendor :exec
DELETE FROM vendors WHERE tenant_id = $1 AND id = $2;

-- name: ListVendors :many
-- AC-2 filter by criticality. NULL criticality_filter means "all" — the
-- (sqlc.narg('criticality')::vendor_criticality IS NULL OR criticality = sqlc.narg('criticality'))
-- pattern keeps the query plan stable and lets sqlc emit a *VendorCriticality
-- parameter so callers can pass nil for "no filter".
SELECT * FROM vendors
WHERE tenant_id = @tenant_id
  AND (sqlc.narg('criticality')::vendor_criticality IS NULL
       OR criticality = sqlc.narg('criticality'))
  AND (sqlc.narg('tool_category')::vendor_tool_category IS NULL
       OR tool_category = sqlc.narg('tool_category'))
ORDER BY criticality DESC, name ASC;

-- name: ListOverdueVendors :many
-- AC-4 overdue calc — vendors whose last_review_date + cadence is older than
-- the cutoff date. NULL last_review_date means "never reviewed" which always
-- counts as overdue (a vendor with no review on file is by definition past
-- due). Cadence -> interval mapping happens with a CASE in SQL so the query
-- plan is stable. Cutoff is passed as DATE so the caller controls "now"
-- (testability) and timezone semantics (vendor reviews are date-granular,
-- not timestamp-granular).
SELECT * FROM vendors
WHERE tenant_id = @tenant_id
  AND (sqlc.narg('criticality')::vendor_criticality IS NULL
       OR criticality = sqlc.narg('criticality'))
  AND (
        last_review_date IS NULL
     OR last_review_date + (
            CASE review_cadence
                WHEN 'monthly'   THEN INTERVAL '1 month'
                WHEN 'quarterly' THEN INTERVAL '3 months'
                WHEN 'biannual'  THEN INTERVAL '6 months'
                WHEN 'annual'    THEN INTERVAL '1 year'
            END
        ) < @cutoff::date
      )
ORDER BY last_review_date ASC NULLS FIRST, name ASC;

-- name: CountVendorsForBurndown :many
-- AC-3 burndown: returns total + overdue counts per criticality band. Used
-- by the dashboard panel (slice 040) and the quarterly board pack (slice 032).
-- Returns one row per criticality present in the result set; empty bands are
-- not included (callers fill in zero where needed).
SELECT
    criticality,
    COUNT(*)::bigint AS total_count,
    COUNT(*) FILTER (
        WHERE last_review_date IS NULL
           OR last_review_date + (
                  CASE review_cadence
                      WHEN 'monthly'   THEN INTERVAL '1 month'
                      WHEN 'quarterly' THEN INTERVAL '3 months'
                      WHEN 'biannual'  THEN INTERVAL '6 months'
                      WHEN 'annual'    THEN INTERVAL '1 year'
                  END
              ) < @cutoff::date
    )::bigint AS overdue_count
FROM vendors
WHERE tenant_id = @tenant_id
  AND (sqlc.narg('criticality')::vendor_criticality IS NULL
       OR criticality = sqlc.narg('criticality'))
GROUP BY criticality;

-- name: ListVendorSpendRollup :many
-- Commercial spend rollup for purchased security tooling. Aggregates only
-- rows with annual_cost + currency and excludes churned tools from current
-- spend. category NULL is the overall rollup for that currency; non-NULL
-- rows are category subtotals.
SELECT
    NULL::vendor_tool_category AS tool_category,
    currency,
    SUM(annual_cost)::float8 AS annual_cost,
    COUNT(*)::bigint AS vendor_count
FROM vendors v
WHERE v.tenant_id = @tenant_id
  AND v.annual_cost IS NOT NULL
  AND v.currency IS NOT NULL
  AND v.commercial_status <> 'churned'
GROUP BY v.currency

UNION ALL

SELECT
    v.tool_category,
    v.currency,
    SUM(v.annual_cost)::float8 AS annual_cost,
    COUNT(*)::bigint AS vendor_count
FROM vendors v
WHERE v.tenant_id = @tenant_id
  AND v.annual_cost IS NOT NULL
  AND v.currency IS NOT NULL
  AND v.commercial_status <> 'churned'
  AND v.tool_category IS NOT NULL
GROUP BY v.tool_category, v.currency
ORDER BY currency ASC, tool_category ASC NULLS FIRST;

-- name: SetVendorLastReviewDate :exec
-- Slice 688: keep vendors.last_review_date consistent with the vendor_reviews
-- ledger (AC-2, decisions log D2). Called after recording a review when the
-- new review's reviewed_at is the most-recent on file. updated_at is bumped so
-- the detail page's "last updated" reflects the change. RLS scopes the write.
UPDATE vendors SET
    last_review_date = $3,
    updated_at       = now()
WHERE tenant_id = $1 AND id = $2;

-- name: ListVendorScopeCells :many
-- Cells attached to one vendor.
SELECT scope_cell_id FROM vendor_scope_cells
WHERE tenant_id = $1 AND vendor_id = $2
ORDER BY scope_cell_id ASC;

-- name: AddVendorScopeCell :exec
-- Idempotent: ON CONFLICT DO NOTHING because the PK already enforces no
-- duplicates, so re-adding is a no-op.
INSERT INTO vendor_scope_cells (tenant_id, vendor_id, scope_cell_id)
VALUES ($1, $2, $3)
ON CONFLICT (tenant_id, vendor_id, scope_cell_id) DO NOTHING;

-- name: ClearVendorScopeCells :exec
-- Used before re-binding the full cell set on an update.
DELETE FROM vendor_scope_cells
WHERE tenant_id = $1 AND vendor_id = $2;
