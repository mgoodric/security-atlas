# 483 — Crosswalk-mapping verified-tier governance — decisions log

JUDGMENT slice. The governance model itself was decided by the maintainer in
[ADR 0018](../adr/0018-crosswalk-mapping-verified-tier.md) (2026-06-15) — this
build implements against it and does NOT re-litigate it. The decisions recorded
here are the BUILD-TIME implementation calls (write mechanism, endpoint shape,
the seed data-migration call, status mapping) that 0018 left to the implementer;
Claude made each call and recorded it here, the slice ships when CI is green (no
human sign-off gate). ADR 0018's "Implementation notes" section was appended with
the load-bearing two (write mechanism + endpoint path).

- detection_tier_actual: integration
- detection_tier_target: integration

One bug surfaced and was caught at the integration tier: the first cut granted
only `UPDATE (mapping_tier)` to `atlas_app`, but the tier-update query also writes
`updated_at = now()`, and Postgres checks column-level UPDATE privilege
per-written-column — so the first live transition failed with `permission denied
for table fw_to_scf_edges (42501)`. Fixed by widening the column grant to
`UPDATE (mapping_tier, updated_at)` (still excludes all STRM-content columns).
This is exactly the class of bug a mock would have hidden (a mock store does not
enforce a column grant) and the live integration tier is the right place to
catch it — `actual == target == integration`. The state-machine legality is
additionally pinned at the pure-Go unit tier (AC-8), and the read-path wire shape
is pinned by the regenerated `control-coverage.golden.json` contract.

---

## D1 — Write mechanism: narrow column-level UPDATE grant (option (a)), not a privileged pool

**Decision.** The transition handler runs as `atlas_app` and flips the tier
through a **narrow `GRANT UPDATE (mapping_tier, updated_at) ON fw_to_scf_edges TO
atlas_app`** column-level privilege, plus `GRANT SELECT, INSERT ON
fw_to_scf_edge_tier_transitions TO atlas_app`. (`updated_at` is in the grant
because the tier-update query stamps `updated_at = now()` alongside the tier and
Postgres checks the UPDATE privilege per-written-column — see the detection-tier
note above; the STRM-content columns `relationship_type` / `strength` /
`source_attribution` / `rationale` remain ungranted for UPDATE.) The store does the tier change +
the audit insert in one `atlas_app` transaction. The legality of the move is
enforced in Go (the `internal/crosswalktier` state machine, read FOR UPDATE
inside the tx), and the trust gate is the admin-role authz check in the handler.

**Why (a) over (b) — routing through the BYPASSRLS migrate/privileged pool.**
The slice brief preferred (a) unless a strong reason for (b) surfaced; none did.
`fw_to_scf_edges` is a catalog table with no RLS, so there is no RLS policy for a
privileged pool to bypass — the only reason to reach for the migrate pool would
be a missing grant, and a _narrow column grant_ gives exactly the privilege
needed (flip the trust tier) and nothing more: `atlas_app` still cannot touch
`relationship_type`, `strength`, `source_attribution`, or `rationale` through
this grant. Routing through a BYPASSRLS pool for a catalog-level edit would widen
the blast radius of any handler bug far beyond the one column. The column grant
is the least-privilege choice and keeps the write on the same role/pool as the
audit insert, so the same-tx atomicity (P0-483-4) is trivially a single
`atlas_app` transaction with no role switch.

## D2 — Endpoint shape: `POST /v1/admin/crosswalk-edges/{id}/tier`

**Decision.** A single admin-gated route, `POST
/v1/admin/crosswalk-edges/{id}/tier`, body `{ "tier": "<target>", "note":
"<optional>" }`. The `{id}` is the `fw_to_scf_edges.id`. Mounted in
`registerAdmin` (`internal/api/register_admin.go`) via the slice-436 per-domain
registrar, gated by `cred.IsAdmin` (reusing the exact `requireAdmin` shape the
slice-509 `admingroupmappings` surface uses — ADR 0018 §3 chose "any
admin/maintainer role", NOT super_admin-only, so no new role is introduced).

**Why this shape.** The act is "transition THIS edge to THAT tier" — a sub-resource
write on a single edge, so a POST to a `…/tier` sub-path keyed by the edge id is
the natural REST shape and mirrors the existing `…/{id}/rotate` / `…/{id}/revoke`
admin verbs. A PATCH on the edge was rejected: the edge's STRM content is
catalog-import-owned (`atlas_migrate`); only the trust tier is operator-mutable,
and a dedicated verb makes that boundary explicit. The `note` is optional (a
reviewer may verify without commentary); the reviewer id is NOT in the body — it
is taken from the verified admin JWT subject (`jwtmw.SubjectUserID`), so a caller
cannot spoof who performed the act.

**Status mapping.** 200 on success; 400 malformed tier / bad edge id; 403
non-admin (threat-model E); 404 unknown edge; 422 illegal transition (the
`draft → verified` skip and any move out of a terminal tier). The 422 (rather
than 400) for an illegal-but-well-formed transition distinguishes "your request
was malformed" from "your request was understood and rejected by the state
machine".

## D3 — `scf_official → verified` seed as an in-migration data UPDATE

**Decision.** The migration sets existing `scf_official` edges directly to
`verified` via a one-shot `UPDATE … WHERE source_attribution = 'scf_official' AND
mapping_tier = 'draft'` immediately after adding the column (which defaults all
rows to `draft`). `community_draft` and `org_internal` rows keep the `draft`
default.

**Why.** ADR 0018 §2 makes this the seed policy: a publisher's official crosswalk
is trusted on arrival. Doing it as an idempotent data UPDATE in the same
additive migration (a) keeps the seed policy co-located with the column it
governs, (b) is re-run-safe for the self-host bundle's
re-apply-every-migration-on-`up` model (the `AND mapping_tier = 'draft'` guard
makes it a no-op on re-run), and (c) never rewrites `source_attribution`
(P0-483-7). The state machine has NO `draft → verified` operator edge, so this
seed path is deliberately modeled as a load/seed-time step OUTSIDE the operator
state machine — exactly as ADR 0018 §1 frames it.

## D4 — Append-only audit WITHOUT tenant RLS (catalog-level)

**Decision.** `fw_to_scf_edge_tier_transitions` is append-only by GRANT
(`SELECT, INSERT` to `atlas_app`; no UPDATE/DELETE) — mirroring the slice-035 /
slice-509 append-only audit DISCIPLINE — but with NO `ENABLE/FORCE ROW LEVEL
SECURITY` and no `tenant_id`, because it is a catalog-level table (the tier is
reference data, not tenant-confidential). The reviewer-scoped transition history
(`ListFwToScfEdgeTierTransitions`) is the admin/maintainer read; it is NOT on the
public `/anchors` payload (P0-483-6).

**Why no RLS.** Bolting four-policy tenant RLS onto a table with no tenant_id
would be incoherent (there is no `app.current_tenant` to scope on) and would
contradict the slice-013 catalog-vs-tenant boundary the migration \_013 header
established for `fw_to_scf_edges` itself. The integrity guarantee the threat
model needs (R: "who verified this, and they can't erase it") is provided by the
append-only grant, not by RLS.

## D5 — Read exposure: tier label only, slice-482 formula unchanged

**Decision.** `/anchors` (`/requirements/{id}/anchors`) and `/coverage`
(`/requirements/{id}/coverage`, `/controls/{id}/coverage`,
`/anchors/{id}/requirements`) gain an additive `mapping_tier` string field on
the edge wire. The slice-482 confidence/coverage formula is UNCHANGED — the tier
is exposed but does NOT yet weight the score.

**Why.** ADR 0018 §4 defers tier-weighting to avoid coupling two slices' scoring
changes. Exposing the label now lets the operator and the 482 rollup _see_ the
tier; weighting it is a clean follow-on (captured in "Revisit once in use"
below).

---

## Revisit once in use

- **Tier-weight the slice-482 confidence formula.** The 482 coverage rollup can
  later discount a `draft`/`under_review` edge vs a `verified` one. Deliberately
  deferred (D5 / ADR 0018 §4) to avoid a two-slice scoring entanglement. This is
  the most likely first follow-on.
- **Tighter promotion gate for multi-operator deployments.** ADR 0018 §3 chose
  "any admin/maintainer role". A multi-operator deployment may want
  super_admin-only verification, or a two-person rule. Revisit when a real
  deployment asks; the audit trail already records who-did-what regardless.
- **A demote / re-review path.** The current operator state machine has no
  `verified → under_review` demotion (a re-review of a verified mapping). If a
  published mapping is later found wrong, today the path is `verified` stays and
  a NEW review cycle would need a demotion edge. Add when the re-review workflow
  is designed.
- **Contributed-CONTROL tier + public marketplace.** Explicitly out of scope
  (P0-483-5 / ADR 0018 §5); these depend on the public-launch governance
  posture.

## Anti-criteria honored

| Anti-criterion                              | Honored | How                                                                                                                                                                                                                            |
| ------------------------------------------- | ------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| P0-483-1 no auto-promote                    | Y       | The state machine REQUIRES a human admin transition to reach `verified`; nothing auto-promotes a `community_draft`. The `scf_official → verified` seed is a load-time data step, not an auto-promotion of agent-authored data. |
| P0-483-2 no non-admin verify                | Y       | `requireAdmin` (cred.IsAdmin) gate; non-admin → 403. Asserted in `TestNonAdminRejected`.                                                                                                                                       |
| P0-483-3 provenance ≠ tier                  | Y       | `mapping_tier` is a NEW enum/column distinct from `source_attribution`; neither is derived from the other.                                                                                                                     |
| P0-483-4 audit in same tx                   | Y       | `Store.Transition` does the tier UPDATE + audit INSERT in one `atlas_app` tx; a validation failure rolls both back (asserted: 0 audit rows on 403 / illegal skip).                                                             |
| P0-483-5 no portal/control-tier/marketplace | Y       | None built; deferred per D-list above.                                                                                                                                                                                         |
| P0-483-6 no reviewer identity on /anchors   | Y       | The read wire carries only the tier label; reviewer id is only on the admin transition response + the admin-scoped `ListTransitions`.                                                                                          |
| P0-483-7 additive + reversible migration    | Y       | Column defaults `draft`; seed is a data UPDATE; down migration drops column+table+enum, leaves `source_attribution` untouched.                                                                                                 |
