# #2061 — a new vendor is not overdue the moment it is created — decisions log

**Ticket:** https://github.com/mgoodric/security-atlas/issues/2061
**Date:** 2026-11-14

Before this change a NULL `last_review_date` counted as overdue in
`vendor.IsOverdueAsOf`, `ListOverdueVendors` and `CountVendorsForBurndown`, so
a vendor added a minute ago was overdue. Triage decided the fix: a vendor
never reviewed has a first review due on its creation date plus an
organisation initial-review window (default 30 days, configurable), and is
overdue only after that day.

**Detection-tier classification (slice 353 / Q-13):**

- `detection_tier_actual`: manual_review (UI design audit, 2026-11-14)
- `detection_tier_target`: unit

The defect was the specified contract, not a slip: `TestIsOverdueAsOf` and
three integration tests asserted "never reviewed is overdue". A unit test that
checked the rule against a vendor created "now" would have shown it.

---

## D1 — the window is a column on `tenants`

`tenants.vendor_initial_review_days INTEGER NOT NULL DEFAULT 30`, CHECK 1–365.
Same home and reasoning as slice 608's `bundle_gate_mode`: `tenants` is the
per-tenant settings row and is already under RLS. A tenant with no `tenants`
row (bare-UUID tenant) gets 30, through `COALESCE` in SQL and `ErrNoRows` in
Go, so the two paths agree.

This change makes the window configurable per tenant in the database. It does
not add it to `PATCH /v1/tenants/{id}` or the settings page; that needs a
`me_audit_log` action and a settings control, and is a follow-up.

## D2 — "after the due day in UTC", matching policy reviews (#2075)

The first review is due on the UTC date of `created_at` plus the window. A
vendor is overdue when the cutoff's UTC date is later than that date; on the
due day itself it is not overdue. SQL compares
`(created_at AT TIME ZONE 'UTC')::date + window < cutoff::date`; Go compares
whole UTC days. A vendor with no first-review date is never overdue.

## D3 — once reviewed, the cadence rule is unchanged

`first_review_due` is set only while `last_review_date` is NULL. After a
review, the existing `last_review_date + cadence` rule applies untouched.

## D4 — calendar and dashboard not changed

The calendar and dashboard "upcoming" queries already exclude never-reviewed
vendors (no anchor to project from). They do not call anything overdue, so the
bug does not reach them. Showing the first-review date there is a separate
feature.

## D5 — board pack test backdates its never-reviewed vendor

The slice 273 board-pack test counted a never-reviewed vendor as past due at a
2026-03-31 period end, though the vendor is inserted today. The test now
backdates that vendor's `created_at` three months before the period end, which
keeps its intent (two past due) under the new rule.
