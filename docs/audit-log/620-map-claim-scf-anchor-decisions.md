# Slice 620 — map an unmapped vendor claim to an SCF anchor — decisions log

**Type:** JUDGMENT. The subjective calls are the anchor-picker UX, the
mapping-write validation shape, and the audit-row reuse. Parent: slice 589
(vendor-claim read + disposition), which surfaced the `unmapped` flag (the
slice-512 `scf_anchor_id IS NULL` case) but deferred the write-side
anchor-mapping flow (its decisions-log D8). Inherits the slice-512 P0-512-1
boundary and canvas invariants #2 and #7.

**This slice does NOT touch the runtime AI-assist boundary.** There is no LLM
anywhere in this path — mapping is a pure operator action (the operator picks a
real SCF anchor from the bundled catalog and the platform sets the crosswalk).
The "no fabricated coverage" rule it honors is the same constitutional rule the
AI-assist boundary names, applied here to a human's deterministic mapping
choice.

**Detection-tier classification (slice 353 / Q-13):**

- detection_tier_actual: none — no bug surfaced during the slice. The boundary
  was designed in (the mapping store method only writes `scf_anchor_id` + the
  append-only event row; it has no code path to `control_evaluations`) and
  verified by the tests below, not discovered by a failure.
- detection*tier_target: integration — the load-bearing behaviours (the PATCH
  sets the crosswalk + clears the unmapped flag under RLS; the append-only
  mapping-audit row records `NULL → scfID`; an unknown anchor 422s; an
  unknown/cross-tenant claim 404s; a non-approver 403s; and — the boundary —
  **nothing** is written to `control_evaluations`) are pinned by
  `internal/api/oscalcomponents/integration_test.go`
  (`TestMapScfAnchor*\*`). The handler validators/branches (role gate, bad
UUID, blank/invalid body, error mapping) are pinned by fast Go-side unit
tests (`handler_test.go`); the BFF route by `web/.../scf-anchor/route.test.ts`;
the map flow by the Playwright spec `web/e2e/oscal-component-claims.spec.ts`
  (AC-5/AC-6/AC-7, BFF-GET route-mocked, hermetic).

---

## THE CONSTITUTIONAL BOUNDARY (the whole slice)

A vendor claim is an ASSERTION, not platform-verified evidence (canvas
invariant #2 / P0-512-1). Mapping a claim to an SCF anchor sets a **crosswalk**
— it does NOT manufacture control coverage. The mapping write touches exactly
two things: (1) the claim's `scf_anchor_id` TEXT column (already present from
slice 512), and (2) an append-only mapping-audit row. It never writes
`control_evaluations` or the evidence ledger; `is_vendor_claim` and
`claim_status` are untouched (the claim stays a claim, still un-dispositioned).

This is enforced structurally, not by convention: `Store.MapScfAnchor` has no
reference to `control_evaluations` anywhere in its transaction, and the
integration test `TestMapScfAnchor_MapsClaimAndAudits` asserts
`SELECT count(*) FROM control_evaluations WHERE tenant_id = $1` returns `0`
after a successful mapping (the slice-619 boundary-test discipline).

Invariant #7 (requirement → SCF anchor only): the endpoint's only target is a
bundled `scf_anchors` row, resolved by SCF code via the existing
`GetSCFAnchorBySCFID` query (`slug='scf' AND status='current'`). There is no
claim id in the request body and no path to a claim → claim mapping.

---

## D1 — Anchor-picker UX: client-filtered reuse of `GET /v1/anchors`

**Decision:** the picker reuses the existing `GET /v1/anchors` catalog read
(via the existing `GET /api/anchors` BFF route + `listAnchors` lib) and filters
client-side over `scf_id` / `family` / `name`; it does NOT add a new
server-side anchor-search endpoint.

**Why:** the slice brief says "reuse the existing `scf_anchors` read path if one
exists." One does (`/v1/anchors`, slice 006/104). The bundled SCF catalog is
~1,400 anchors — small enough to fetch once and filter in the browser; a
search-as-you-type round trip per keystroke would be strictly worse for this
volume. The picker caps the rendered list (blank query → first 25; a query →
first 25 matches) so the DOM never renders the full catalog. The filter logic
is factored into a pure `filterAnchors` function (exported, kept separate from
the React tree for clarity); its observable behaviour — typing `TST-02` narrows
the option list to the matching anchor — is exercised by the Playwright spec
(AC-6). It is NOT a vitest unit (the function lives in a `"use client"` `.tsx`
view module, which the slice-069 P0-A3 node-env vitest tier deliberately
excludes — the e2e tier is the de-facto component-test tier for view logic per
the CLAUDE.md test-tier conventions, Q-3).

**Rejected:** a new `GET /v1/anchors?search=` backend endpoint — unjustified
for ~1,400 rows, and it would add an openapi route + handler + sqlc query for
no benefit over client filtering at this scale.

**The picker never lets the operator type a free-form anchor.** Every option is
a real `scf_anchors` row; clicking one sends its `scf_id`. The platform
re-validates server-side regardless (D2), so a stale/forged code still 422s.

## D2 — Mapping-write validation shape

**Decision:** the validation order is (1) authz (approver/admin, else 403),
(2) claim-id parse (bad UUID → 400), (3) body decode + non-blank `scf_anchor_id`
(else 400, whitespace-trimmed), then in the RLS transaction (4) the target
anchor must resolve to a bundled SCF anchor (else **422 Unprocessable Entity**),
(5) the claim must resolve in the tenant (else **404**). Only then does the
crosswalk get set and the audit row appended.

**Why 422 (not 404 or 400) for an unknown anchor:** the request is
syntactically well-formed (valid JSON, non-blank code) but semantically
unprocessable — the named anchor does not exist in the catalog. 400 would imply
a malformed request; 404 is reserved for the claim resource in the path. 422 is
the precise RFC 4918 shape and lets the UI distinguish "you picked something
that no longer exists" from "the claim is gone."

**Anchor validated BEFORE the claim is touched:** the anchor existence check
runs first inside the transaction, so a bad anchor code never mutates the claim
(asserted by `TestMapScfAnchor_UnknownAnchor422`: the claim stays unmapped).

## D3 — Audit-row reuse: generalize the disposition table into a claim event log

**Decision:** reuse the slice-589 `imported_component_claim_dispositions` table
for the mapping audit, generalizing it into a claim **event log** rather than
adding a second audit table or distorting the disposition row's status
semantics. The migration `20260608060000_oscal_component_claim_scf_mapping.sql`
adds `event_kind TEXT NOT NULL DEFAULT 'disposition'` (discriminator) +
nullable `from_scf_anchor_id` / `to_scf_anchor_id`, relaxes the slice-589
`iccd_to_status_chk` so the status CHECK applies only to `'disposition'`
events, and adds `iccd_scf_mapping_anchor_chk` requiring a non-empty
`to_scf_anchor_id` on a `'scf_mapping'` event.

**Why:** the slice brief is explicit — "reuse the existing audit-log table the
disposition flow uses." A disposition is a status transition
(`from_status → to_status`); a mapping is an anchor transition
(`from_scf_anchor_id → to_scf_anchor_id`). The two share the same who/when/why
columns (`actor`, `occurred_at`, `note`) and the same append-only,
RLS-scoped, INSERT-only-grant semantics. Generalizing one table preserves a
single forensic timeline per claim ("who dispositioned it, who mapped it, in
what order") — the diligence-the-diligence-tool story — without a second table
to join. The `event_kind` DEFAULT means existing rows + the slice-589
disposition INSERT need no change and no data migration; the NOT-NULL column
carries a default so no integration-fixture helper needs patching.

**Rejected:** a separate `imported_component_claim_scf_mappings` table — cleaner
in isolation but it splits the per-claim audit trail across two tables and
contradicts the brief's reuse instruction. Also rejected: stuffing the anchor
into the existing `from_status`/`to_status` columns — that would corrupt the
status-typed CHECK and make the disposition history query lie.

**Migration ordering:** the file sorts after
`20260608050000_oscal_component_claim_disposition.sql` (it is `…060000…`), is
appended to the `sqlc.yaml` schema list in order, and ships a paired
`.down.sql` that restores the slice-589 unconditional `iccd_to_status_chk`. No
new column lands on `imported_component_claims` — the `scf_anchor_id` TEXT
column the mapping sets already exists from slice 512.

## D4 — The no-fabricated-coverage guard (mirrors slice 619)

**Decision:** add an integration assertion that `control_evaluations` is empty
for the tenant after a successful mapping, mirroring the slice-619 boundary
discipline. This is the executable proof of the constitutional boundary above —
not a convention but a red-on-regression test.

**Why:** the highest-risk failure mode for this surface is a future change that
"helpfully" creates a coverage/evaluation row when a claim is mapped (the
operator mapped it, surely that means the control is covered?). It does NOT —
the claim remains an un-dispositioned vendor assertion; coverage requires the
evaluation engine over real evidence. The empty-`control_evaluations` assertion
makes that regression fail loudly.

## D5 — `claim_status` and `is_vendor_claim` are orthogonal to mapping

**Decision:** mapping does not change `claim_status` (a mapped claim stays
`'asserted'` until separately dispositioned) and does not touch
`is_vendor_claim`. Mapping and disposition are independent operator actions:
you can map an unmapped claim without crediting it, and you can disposition a
claim without mapping it.

**Why:** conflating the two would let "I told the tool which SCF control this
claim is about" silently read as "I credit the vendor's assertion." They are
different decisions with different authority semantics; keeping them orthogonal
keeps each audit trail honest. Both the unit test (`TestMapScfAnchor_OK`
asserts `claim_status` stays `asserted`) and the integration test pin this.
