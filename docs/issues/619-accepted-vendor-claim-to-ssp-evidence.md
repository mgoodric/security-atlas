# 619 — Accepted vendor claim → OSCAL SSP control-implementation evidence

**Cluster:** evidence-pipeline (OSCAL)
**Estimate:** M (2-3d)
**Type:** JUDGMENT (whether/how an accepted vendor claim becomes
control-implementation evidence in an SSP export is a design call with a hard
constitutional boundary)
**Status:** `blocked` — depends on #589 (vendor-claim disposition) landing the
`accepted` state, AND on the OSCAL SSP export surface.
**Parent:** #589 (vendor-claim read + disposition). Spun off from slice 589's
decisions-log D2 — that slice deliberately kept an `accepted` claim a
credited-but-distinct claim and did NOT make it a first-class evidence record.

## Narrative

Slice 589 ships the operator disposition: a claim moves
`asserted -> accepted`, recording that the operator credits the vendor's
assertion. Slice 589 stops there (D2): an accepted claim is metadata on the
claim, NOT an evidence-ledger record and NOT a control satisfaction.

This slice decides the inbound complement to the platform's SSP export: when
the tenant exports an OSCAL SSP, do accepted vendor claims surface as
`by-component` control-implementation statements (attributed to the vendor
component, clearly marked vendor-asserted-and-operator-credited), or do they
stay out of the export entirely?

## Hard boundary (P0 — inherits #589 / P0-512-1)

- An accepted claim NEVER becomes a platform-verified evidence record that
  auto-satisfies a control. If it surfaces in an SSP, it surfaces as a
  vendor-attributed implementation statement with explicit provenance, never
  as platform evidence backing a passing evaluation.
- Tenant isolation (RLS) on every read.

## Key design calls (JUDGMENT)

- SSP `by-component` statement shape + the vendor-attribution + the
  operator-credit annotation.
- Whether an accepted claim with an `unmapped` SCF anchor is excluded until
  mapped (depends on #620).

## Dependencies

- #589 (vendor-claim disposition) — must land first.
- The OSCAL SSP export surface.
