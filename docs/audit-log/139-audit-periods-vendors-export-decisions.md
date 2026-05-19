# Slice 139 — Audit periods + vendors data export decisions

Slice 139 (`docs/issues/139-audit-periods-and-vendors-export.md`) ships
two new per-entity exports on top of slice 135's data-export library
and slice 145's hardening:

1. `GET /v1/admin/audit-periods/export?format=<csv|json|xlsx>`
2. `GET /v1/admin/vendors/export?format=<csv|json|xlsx>`

BFFs at `web/app/api/admin/audit-periods/export/` and
`web/app/api/admin/vendors/export/`. Export buttons on the `/audits`
and `/vendors` pages.

The judgement calls below were made during the build-time pickup and
are recorded here per the JUDGMENT-slice convention.

## D1 — Vendor email masking shape: `*@domain.tld`

**Decision:** Drop the local-part of any email-shaped `owner_user`
value, render the cell as `*@<domain>`. Surface as a NEW column named
`owner_user_masked`. Do NOT keep the raw `owner_user` column alongside
it. Un-masked access deferred to v3 column selection with proper RBAC
gating.

**Why:**

- The slice doc P0-A-V-1 prescribes exactly this masking shape; the
  decision space at pickup time is "exactly how" rather than
  "whether". Keeping the `*@` prefix (instead of, e.g., stripping the
  local-part entirely to render `@domain`) preserves a visual cue
  that masking happened. A consumer eyeballing a CSV in Excel sees
  the `*@` and knows the column was redacted — vs. seeing `@example`
  and wondering if the local-part was always empty.
- Renaming the column to `owner_user_masked` is the load-bearing
  half: a downstream consumer who imports the CSV into a spreadsheet
  binding by column name (`owner_user`) gets a clean break rather
  than silently consuming masked data thinking it was un-masked.
  Operators who explicitly want masked data update one identifier;
  operators relying on the v1 contract get a deliberate signal that
  the schema changed.
- The `MaskEmail` helper is total (panic-free) and pure (no I/O); it
  handles edge cases (empty string, no-`@`, multiple-`@`, trailing
  `@`) by returning empty rather than echoing the unsafe input. The
  unit test suite (`mask_email_test.go`) pins each branch.
- Notes column is INCLUDED un-redacted at v1 — operators rely on it
  for workflow context, and operators can `jq` or `awk` to strip it
  for redacted handoff. Same posture as slice 135 P0-A2 ("column-
  level redaction beyond `payload_json` out of scope").

**Rejected:**

- **Echo the input unchanged when it's not email-shaped.** Tempting
  ("UUID-shaped `owner_user` values aren't PII") but the threat
  model treats `owner_user` as a potential PII surface regardless of
  contents — operators can paste anything in there, and the export
  is hostile to fail-open semantics. Empty cell is the no-leak
  default.
- **Keep both `owner_user` and `owner_user_masked` columns.** Twice
  the surface area, twice the wire-shape break for v3 un-masking,
  and an immediate v1 leak surface. Single column wins.
- **Strip the `@` too — render only the domain.** Removes the
  masking signal — a reader can't distinguish "this column was
  masked" from "this column was always empty before the `@`". The
  `*@` prefix is the universally-recognized masking marker.

## D2 — Row cap per entity: 50,000

**Decision:** Default `defaultExportRowCap = 50_000` for BOTH
audit_periods and vendors. Slice 135 uses 100,000 for the audit-log
export (which is high-volume by design); slice 139's entities are
lower-volume so the cap is correspondingly tighter.

**Why:**

- Audit periods are quarterly artifacts (4 per tenant per framework
  per year). A solo-leader tenant accumulates ~16/year across SOC 2 +
  ISO + PCI; 50K is a 3,000-year ceiling.
- Vendors are sized for 30-80 per tenant at the v1 user (the canvas
  §1.4 "lite TPRM" sizing). 50K is a 600x ceiling — well above any
  realistic operator.
- A tenant exceeding 50K rows in either entity is already a forensic
  event worth narrowing the filter for. The 413 message explicitly
  invites them to do so.
- Setting 50K (rather than 100K to match slice 135) signals "this
  surface is sized smaller" to operators reading the source — the
  audit-log export is the bulk-extraction surface; these are
  operator-list dumps.

**Rejected:**

- **No cap.** Violates slice 135 P0-A8 (row-cap discipline). A
  pathological tenant whose audit_periods grew unbounded would
  saturate the pgxpool during streaming.
- **100K to match slice 135.** Larger than needed; the cap exists to
  invite operators to narrow filters when they exceed it, and the
  signal is more useful at a tighter ceiling.

## D3 — No filter surface at v1

**Decision:** Both endpoints export the FULL tenant register for the
entity; no `?status=`, `?criticality=`, `?from=`, etc. filter
parameters at v1.

**Why:**

- Slice 135 has filter parameters (`from`, `to`, `kind`, `actor`)
  because the audit-log is a time-series stream — without filters, an
  export is unbounded. Audit periods + vendors are operator-curated
  lists; the natural export size is the entire list.
- A future filter surface is a non-breaking additive change. Shipping
  it in v1 would mean shipping integration tests for every filter
  matrix without a clear operator pull.
- The slice doc's narrative confirms this: "Each gets a data-export
  endpoint reusing the slice 135 library." No filter requirement.

**Rejected:**

- **`?criticality=` filter on vendors.** A future operator may want
  it; defer until the demand is articulated.
- **`?status=` filter on audit_periods.** Likewise; a future board-
  pack workflow may need "frozen-only" but the v1 use case is the
  full register.

## D4 — Reuse store.List rather than touching the slice-024/-028 stores

**Decision:** The handlers call `period.Store.List(ctx)` +
`vendor.Store.List(ctx, vendor.ListFilter{})` directly rather than
adding sqlc queries to `internal/db/dbx/`.

**Why:**

- Both stores already apply `tenancy.ApplyTenant` under `atlas_app`.
  Reusing them inherits the RLS contract — no new failure mode where
  the export reads bypass tenancy.
- Slice surface stays small: no new sqlc files, no `internal/audit/
period/` or `internal/vendor/` modifications, no migration to the
  domain layers (slice 139 is a build-on-top spillover).
- The integration test already covers the RLS path via
  `TestVendorsExport_CrossTenantIsolationAllThreeFormats` +
  `TestAuditPeriodsExport_CrossTenantIsolationAllThreeFormats`;
  reusing the store gives us that coverage by composition.

**Rejected:**

- **Add per-entity sqlc query `ListForExport`.** Would let the
  export skip the read-side metadata enrichment (e.g. `IsOverdueAsOf`
  computation), but the savings are immeasurable for 30-80 vendors
  and the duplication invites drift.
- **Bypass the store and read directly via pgxpool.** Same RLS-bypass
  risk as ignoring the slice-035 OPA middleware; rejected on first
  principles.

## D5 — Migration extends the slice 135 CHECK rather than adding a new column

**Decision:** Migration `20260519000000_audit_periods_vendors_export.sql`
extends the `me_audit_log.action` CHECK constraint to permit two new
values: `audit_periods_export` + `vendors_export`. No new column on
`me_audit_log`.

**Why:**

- Both new actions are per-entity bulk-PII-extraction events. The
  `me_audit_log` table is already the canonical ledger for them
  (slice 135 + 145 pattern). Adding two new permitted values in the
  CHECK is the minimum schema change.
- Distinct action values (`audit_periods_export` vs `vendors_export`
  vs `audit_log_export`) enable forensic queries to enumerate one
  bulk-extraction class without inspecting `after->>'format'` or
  similar. This is the slice 135 D-equivalent precedent
  (`audit_log_export` distinct from `audit_log_query_unified`).
- The down migration restores the slice 135 five-value enum exactly;
  rollback warning documents that operators must first archive rows
  with the two new actions.

**Rejected:**

- **Single `bulk_export` action value, distinguish by entity in
  `after`.** Forensic queries become `WHERE action = 'bulk_export'
AND after->>'entity' = 'audit_periods'` — more verbose, harder to
  index, easier to miss when filtering. Loses the slice 135 precedent.
- **No CHECK extension — drop the constraint entirely.** Strictly
  worse: any typo in a future migration would silently insert
  garbage action values. The CHECK is the contract.
