# 536b-1 — Crosswalk review/edit BFF + content-edit audit trail — decisions log

JUDGMENT slice (backend/BFF half of 536b). Slice 536 (crosswalk review /
conflict editing UI) was decomposed after slice 483 shipped verified-tier
governance and slice 536a (merged #1505) reconciled the two:

| Slice  | Scope                                                         |
| ------ | ------------------------------------------------------------- |
| 536a   | Scope reconciliation + conflict-detection backend (merged)    |
| 536b-1 | This one — content edit + audit trail + BFF contract + vitest |
| 536b-2 | The React review UI, wiring to this slice's endpoints         |
| 536c   | Playwright e2e over the review flow                           |

536a's reconciliation (`536a-crosswalk-conflict-detection-decisions.md` §1.4)
shrank 536b to two residuals: **content editing** of `relationship_type` /
`strength` / `rationale` with its own audit trail, and **the review surface**
with approve/reject wired to slice 483's existing tier endpoint. Everything
approval-shaped was already shipped by 483 and is consumed here, not rebuilt.

- detection_tier_actual: none
- detection_tier_target: unit

No bug surfaced during this build. The new surfaces are a pure-Go
validation-plus-transaction store, a thin admin HTTP wrapper, and three BFF
forwards — every validation branch is reachable from a table test with no
Postgres and no browser, so `unit` is the correct target tier. The one place a
mock could hide a real defect is the column-level grant discipline (D2), which
is why the same-transaction audit row, the tier gate, and the no-bypass arms
are asserted against real Postgres in
`internal/api/admincrosswalkreview/http_integration_test.go` rather than only
in unit tests.

---

## D1 — verified content is immutable in place; the reviewer demotes FIRST (resolves 536a D-536b-1)

**The question 536a left open.** Editing a `verified` mapping's strength
arguably invalidates the verification, but 483's state machine had no
`verified → under_review` edge (483 named the gap in its own "Revisit once in
use"). 536a §1.4 named the fork — add the demotion edge to 483's machine, or
forbid content edits on verified mappings — and forbade inventing a second
lifecycle.

**Decision.** Both halves, kept separate:

1. `internal/crosswalktier.legalTransitions` gains exactly one edge,
   `verified → under_review`. It is strictly trust-reducing;
   `under_review → verified` remains the only way into `verified`, and
   `verified → draft` / `verified → rejected` stay illegal (trust is withdrawn
   into review first, so the tier trail shows WHY before any rejection
   verdict).
2. `internal/crosswalkedit.Store.Edit` refuses content edits on `verified` and
   `rejected` edges (HTTP 409). The reviewer demotes through 483's endpoint,
   edits, and re-verifies — one state machine, both dimensions audited.

**Why the edit does NOT auto-demote.** The tempting alternative — the edit
transaction demoting a verified edge itself, in the same commit — was
considered and rejected. A content write that also transitions a tier makes
"an edit never changes trust state" false, and it puts a tier transition on a
code path whose actor intent was "fix the rationale", not "withdraw trust".
Keeping the two acts separate means every row in 483's transition trail
corresponds to a reviewer who explicitly chose that transition and could
attach a note explaining it. The cost is one extra call in the
verified-edit flow; 536b-2's UI can compose the two requests, but each stays
individually audited and individually intentional. This also keeps the rule
the handlers advertise absolute: a mapping edit transitions NOTHING — no
auto-approve, and symmetrically no auto-demote (the platform never
adjudicates trust on its own initiative, in either direction — 536a D5's
reasoning applied to the write path).

## D2 — the content-edit column grant (resolves 536a D-536b-2)

**The baseline argued against.** 483 D1 deliberately withheld `UPDATE` on the
STRM content columns from `atlas_app` (`UPDATE (mapping_tier, updated_at)`
only): the app role could flip a tier but never rewrite what a mapping says.
536a §1.4 required the widening to carry its own threat-model note.

**Decision.** Migration `20260804000000_crosswalk_edge_content_edits.sql`
grants `atlas_app` `UPDATE (relationship_type, strength, rationale)` — the
three reviewer-curated content columns — and nothing else:

| Column not granted         | What the omission enforces                                                                                                                              |
| -------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `framework_requirement_id` | Invariant #7: `atlas_app` cannot re-point an edge at a different requirement through ANY code path — the requirement → requirement shape is unreachable |
| `scf_anchor_id`            | Same: an edit changes what a mapping means, never which anchor it lands on                                                                              |
| `source_attribution`       | 483 P0-483-3 / ADR 0018: provenance is import-time history; rewriting it would falsify where a mapping came from                                        |

**Why the grant and not only the handler.** The handler and BFF also refuse
these fields, but the grant is the layer a future code path cannot forget.
Invariant #7 and provenance immutability are held by the database; the Go
layers are defence in depth.

**Repudiation (the audit trail).** `fw_to_scf_edge_content_edits` is
append-only by GRANT (`SELECT, INSERT` to `atlas_app`; no UPDATE/DELETE) and
is written in the SAME transaction as the content UPDATE
(`crosswalkedit.Store.Edit`, the only write path to those columns from the
app role) — so no edit can bypass the trail; a failed audit insert rolls the
edit back. Each row carries editor id (from the verified JWT subject, never
the request body), the full before/after of all three columns, an optional
note, and the timestamp. Same discipline as 483's
`fw_to_scf_edge_tier_transitions` (P0-483-4).

## D3 — approve/reject UX: two verbs on 483's endpoint, per-edge, no bulk (the OE's named JUDGMENT call)

**Decision.** The review surface's approve/reject actions are, contractually:

| UI act                    | Wire act                                                             |
| ------------------------- | -------------------------------------------------------------------- |
| Claim for review          | `POST /api/admin/crosswalk-edges/{id}/tier` `{tier: "under_review"}` |
| Approve                   | same route, `{tier: "verified"}` (legal only from `under_review`)    |
| Reject                    | same route, `{tier: "rejected"}`                                     |
| Demote for edit (from D1) | same route, `{tier: "under_review"}` (from `verified`)               |

The BFF tier route is a THIN forward to slice 483's
`POST /v1/admin/crosswalk-edges/{id}/tier` — 483's state machine stays the one
review lifecycle and the sole authority on transition legality (an illegal
move, e.g. the `draft → verified` skip, surfaces as 483's 422 verbatim).
There is no bulk-approve verb: promotion to `verified` is the trust act, and a
select-all-approve affordance would reduce it to a formality — each approval
is a per-edge human act with its own audit row (the AI-assist boundary's
"no auto-approve", operationalized as "no approve-without-looking" either).
An optional free-text `note` rides on every transition; the UI should prompt
for one on reject (the trail should say why a mapping died).

## D4 — partial-patch edit semantics

**Decision.** `PATCH /v1/admin/crosswalk-edges/{id}` accepts any non-empty
subset of `{relationship_type, strength, rationale}` (omitted field = keep
current value), plus an optional `note`. Guards, all validated before the
write and all unit-tested in `internal/crosswalkedit`:

| Arm                                | Status                                         |
| ---------------------------------- | ---------------------------------------------- |
| No editable field in the body      | 400 (`ErrNoFields`)                            |
| Unknown STRM type                  | 400                                            |
| Strength outside [0, 1]            | 400 (Go-validated so it is not a mid-tx 500)   |
| Patch identical to current content | 422 (`ErrNoChange` — no audit rows for no-ops) |
| Edge is `verified` / `rejected`    | 409 (D1 — demote first)                        |
| Unknown edge id                    | 404                                            |

Partial patch (vs an all-fields-required PUT) matches what a reviewer working
a conflict queue actually does — fix ONE field the finding names — and the
audit row is unambiguous anyway because it snapshots the full before/after of
all three columns regardless of which were patched. The current content and
tier are read `FOR UPDATE` in the edit transaction, so a concurrent edit or
tier transition cannot race the read-validate-write window.

## D5 — conflict surfacing: full-set computation, windowed edge list

**Decision.** `GET /v1/admin/crosswalk-review?framework_version_id=` returns
the edge list windowed by `limit`/`offset` (default 500, max 2000 — slice 536
threat-model D), but the slice-536a conflict findings are ALWAYS computed
over the framework version's FULL edge set plus its full requirement
inventory. A windowed conflict computation would be wrong, not just partial:
the heuristics need every sibling edge of a requirement (competing anchors,
sibling divergence), and `unmapped` findings only exist because the
requirement inventory rides in (536a D4). Findings stay advisory (536a D5):
severity orders the 536b-2 queue; nothing acts on a finding server-side.
Conflict detection reads catalog data only — `crosswalkconflict.Input` has no
tenant-scoped field, so the slice-536 threat-model I mitigation holds by
construction.

---

## The 536b-2 UI contract (endpoint shapes)

All three BFF routes require the atlas bearer cookie (401 without it, before
any upstream call) and an ADMIN credential upstream (403 otherwise). All
responses are `Cache-Control: no-store` (mutable admin state — the slice 746
precedent).

### GET `/api/admin/crosswalk-review?framework_version_id=<uuid>[&limit=<1..2000>&offset=<n>]`

→ `GET /v1/admin/crosswalk-review` (new, this slice). 200 shape:

```json
{
  "framework_version_id": "uuid",
  "total_edges": 123,
  "limit": 500,
  "offset": 0,
  "edges": [
    {
      "edge_id": "uuid",
      "requirement_id": "uuid",
      "requirement_code": "A.5.15",
      "requirement_title": "…",
      "anchor_id": "uuid",
      "anchor_scf_id": "IAC-01",
      "anchor_family": "Identification & Authentication",
      "anchor_title": "…",
      "relationship_type": "equal|subset_of|superset_of|intersects_with|no_relationship",
      "strength": 0.9,
      "rationale": "…",
      "source_attribution": "scf_official|community_draft|org_internal",
      "mapping_tier": "draft|under_review|verified|rejected"
    }
  ],
  "conflicts": [
    {
      "kind": "competing_anchors|contradictory_strength|orphaned_requirement",
      "reason": "duplicate_equal_claim|…",
      "severity": "low|medium|high",
      "requirement_id": "uuid",
      "requirement_code": "A.5.15",
      "edge_ids": ["uuid"],
      "anchor_scf_ids": ["IAC-01"],
      "detail": "…"
    }
  ]
}
```

No reviewer/editor identity appears on list payloads (483 P0-483-6 held).
Errors: 400 (bad uuid / window), 401, 403.

### PATCH `/api/admin/crosswalk-edges/{id}`

→ `PATCH /v1/admin/crosswalk-edges/{id}` (new, this slice). Body:
`{ "relationship_type"?, "strength"?, "rationale"?, "note"? }`. 200 shape:

```json
{
  "edge_id": "uuid",
  "from": { "relationship_type": "…", "strength": 1, "rationale": "…" },
  "to": { "relationship_type": "…", "strength": 0.7, "rationale": "…" },
  "editor_id": "uuid",
  "note": "…",
  "mapping_tier": "draft|under_review",
  "created_at": "RFC3339"
}
```

`mapping_tier` is the edge's UNCHANGED tier (an edit never transitions it).
Errors: 400 / 401 / 403 / 404 / 409 (verified|rejected — demote first) / 422
(no-op) per the D4 table.

### POST `/api/admin/crosswalk-edges/{id}/tier`

→ `POST /v1/admin/crosswalk-edges/{id}/tier` (slice 483, unchanged). Body:
`{ "tier": "under_review|verified|rejected", "note"? }`. 200 returns 483's
transition record; 422 on an illegal move. This is the approve/reject verb
(D3) — the BFF adds nothing to it.

---

## Boundaries honored

| Boundary                                                  | Honored | How                                                                                                                                            |
| --------------------------------------------------------- | ------- | ---------------------------------------------------------------------------------------------------------------------------------------------- |
| No React UI (536b-2)                                      | Y       | `web/` changes are three `app/api` route handlers + vitest; no page, no component                                                              |
| No second approval workflow alongside 483's state machine | Y       | Approve/reject is a thin forward to 483's tier endpoint (D3); the one state-machine change is the trust-reducing demotion edge inside 483 (D1) |
| No edit bypasses the audit trail                          | Y       | Same-transaction append-only audit row; the grant makes `Store.Edit` the only app-role write path (D2); no-bypass arms integration-tested      |
| No auto-approve                                           | Y       | An edit never touches `mapping_tier` in either direction (D1); `under_review → verified` stays a human act through 483's endpoint              |
| No requirement → requirement mappings (invariant #7)      | Y       | Edge endpoints have no input field AND no UPDATE grant (D2) — the shape is unforgeable through this surface                                    |

## Revisit once in use

- **Content-edit history read.** `Store.ListEdits` exists but is not yet
  routed; 536b-2 decides whether the UI needs a per-edge history panel, and
  the route lands with it (admin-scoped, mirroring 483's transition-trail
  read).
- **Demote-and-edit composition.** If the two-call verified-edit flow (D1)
  proves noisy in real review sessions, a composed endpoint could wrap the
  two acts — but each must still write its own trail row, and the tier
  transition must still carry explicit reviewer intent.
- **Conflict recomputation cost.** Conflicts are recomputed per review-list
  call over the full edge set. Fine at current crosswalk sizes; 536a's
  "persisted findings" note is the follow-on if it hurts.
