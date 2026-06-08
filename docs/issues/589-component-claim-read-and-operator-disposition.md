# 589 — Vendor-claim read API + operator accept/reject disposition (component-definition follow-on)

**Cluster:** evidence-pipeline (OSCAL)
**Estimate:** M (2-3d)
**Type:** JUDGMENT (how a vendor claim surfaces in the UI + the
accept/reject disposition workflow are subjective UX calls)
**Status:** `blocked` — depends on #512 (component-definition import) landing
the `imported_components` + `imported_component_claims` tables and the
`claim_status` column.
**Parent:** #512 (OSCAL component-definition import) — provides the
vendor-claim persistence + the schema-enforced CLAIM-not-satisfaction shape
this slice reads from and dispositions.

## Narrative

Slice 512 lands OSCAL component-definition import: a vendor's
implemented-requirements persist as vendor-attributed CLAIMS
(`imported_component_claims`, `is_vendor_claim=TRUE`,
`claim_status='asserted'`), reconciled requirement → SCF anchor. The import
deliberately stops at `'asserted'`: it never auto-accepts a claim
(P0-512-1 — no fabricated coverage). But slice 512 ships **no read surface and
no operator action** to act on the claims it lands — the "existing operator
action" it references does not yet exist for this hierarchy.

This slice closes that gap:

- **Read API.** `GET /v1/oscal/component-definitions` (list a tenant's
  imported component-definitions) + `GET
/v1/oscal/component-definitions/{id}` (one import's components + their vendor
  claims, with the SCF-anchor mapping + the unmapped flag). Tenant-scoped
  behind RLS + the read authz gate.
- **Operator disposition.** `POST
/v1/oscal/component-claims/{id}:accept` and `:reject` — the existing
  operator action that moves a claim from `'asserted'` to `'accepted'` /
  `'rejected'` (the only writer of those statuses; the import never writes
  them). `grc_engineer`-gated; append-only audit. Accepting a claim still does
  NOT auto-satisfy a control — at most it records that the operator credits
  the vendor's assertion; whether/how an accepted claim becomes
  control-implementation evidence in an SSP export is a design call for this
  slice (the inbound complement to the platform's SSP export).
- **UI.** A vendor-claims view under the OSCAL/import surface listing claims
  per imported component with per-claim accept/reject + the SCF-anchor mapping
  (map an unmapped claim to an anchor — the slice-512 NULL flag).

## Key design calls (JUDGMENT)

- Whether an `'accepted'` claim becomes a first-class evidence record (through
  the ledger — invariant #2) or stays a credited-but-distinct claim.
- The map-an-unmapped-claim UX (the slice-512 `scf_anchor_id IS NULL` flag).
- How accepted vendor claims (if at all) surface in OSCAL SSP / component
  export.

## Anti-criteria (P0)

- Does NOT let acceptance fabricate control coverage with no evidence
  (inherits P0-512-1 — accepting a claim is the operator crediting an
  assertion, not the platform manufacturing a passing evaluation).
- Does NOT let a claim be dispositioned cross-tenant or anonymously.
- Does NOT mutate the `is_vendor_claim` invariant (a claim is always a claim).

## Dependencies

- **#512** (OSCAL component-definition import) — must land first.
