# 536b — Crosswalk review/edit UI + content-edit audit trail — decisions log

JUDGMENT slice. Slice 536 (crosswalk review / conflict editing UI) was
decomposed into three fires after slice 483 shipped verified-tier governance:

| Slice | Scope                                                           |
| ----- | --------------------------------------------------------------- |
| 536a  | Scope reconciliation + conflict-detection backend (merged)      |
| 536b  | This one — content edit + review/edit UI + BFF + audit + vitest |
| 536c  | Playwright e2e over the approve/reject flow                     |

536a's reconciliation (`docs/audit-log/536a-crosswalk-conflict-detection-decisions.md`
§1.4) shrank this slice to two residuals: **content editing** of
`relationship_type` / `strength` / `rationale` with its own audit trail, and
**the review surface itself** with approve wired to slice 483's existing tier
endpoint. Everything approval-shaped was already shipped by 483 and is consumed,
not rebuilt.

- detection_tier_actual: none
- detection_tier_target: unit

No bug surfaced during this build. The two new surfaces are a pure-Go validation

- transaction store (unit + integration) and four BFF route handlers whose whole
  job is input reconstruction — every branch is reachable from a table test with no
  Postgres and no browser, so `unit` is the correct target tier. The demotion path
  (D-536b-1) is the one place a mock could have hidden a grant bug, which is why it
  is asserted against real Postgres in the integration suite rather than only in
  unit tests — the same reasoning that caught slice 483's `updated_at` column-grant
  defect at the integration tier.

---

## The two decisions 536a flagged and deferred to this slice

### D-536b-1 — a content edit DEMOTES a verified mapping to `under_review`

**The question 536a left open.** Editing a `verified` mapping's strength
arguably invalidates the verification, but slice 483's state machine had no
`verified → under_review` edge (483 listed the gap on its own "Revisit once in
use"). 536a §1.4 named the fork: extend 483's machine with the demotion edge, or
forbid content edits on verified mappings entirely. It explicitly forbade a third
option — inventing a second lifecycle.

**Decision.** Extend 483's machine. `internal/crosswalktier.legalTransitions`
gains exactly one edge, `verified → under_review`, and
`crosswalkedit.Store.EditContent` performs the demotion by calling that package's
own `ValidateTransition` and writing that package's own
`fw_to_scf_edge_tier_transitions` audit row — inside the same transaction as the
content change.

**Why extend rather than forbid.** Forbidding would freeze every verified
mapping's content permanently and push maintainers back to hand-editing
`data/crosswalks/*.yaml` and re-running the importer — the exact workflow slice
536 exists to replace. It would also make the verified tier a trap: the better a
mapping is reviewed, the less fixable it becomes.

**Why extending is safe.** The new edge is strictly trust-REDUCING, so it cannot
become a path to approval. `under_review → verified` remains the only way into
`verified`, it remains a human act through 483's admin endpoint, and there is
still no `verified → draft` and no `verified → rejected` edge — a demoted mapping
re-enters review, it does not silently reset or die. One machine, one lifecycle:
the anti-criterion against a second approval workflow holds because the edit
store does not implement a transition, it _calls_ the one that exists.

**Why the demotion is automatic rather than a prompt.** The alternative — asking
the reviewer whether to keep the verified badge — would let a human assert that
an edited mapping is still the mapping that was verified. That is a
one-click approval of unreviewed content wearing a different label. The UI warns
BEFORE the edit instead (see D-536b-6) so the reviewer chooses knowing the cost.

The tier-transition row written by a demotion is labelled
`content edited (slice 536b auto-demotion)` so a reader of the tier trail alone
can tell an operator-driven demotion from an edit-driven one without joining the
content-edit table.

### D-536b-2 — the content-edit column grant (a real privilege expansion)

**The baseline to argue against.** 483 D1 deliberately withheld `UPDATE` on the
STRM-content columns from `atlas_app`: its grant was
`UPDATE (mapping_tier, updated_at)` and nothing more, so a reviewer could promote
a mapping but never fix one. Widening that is the privilege expansion this slice
introduces, and 536a §1.4 required it carry its own threat-model note.

**Decision.** Migration `20260612110000_crosswalk_content_edit.sql` widens the
grant to `UPDATE (mapping_tier, relationship_type, strength, rationale,
updated_at)` — and stays narrow in the two ways that carry the invariants:

| Column not granted         | What the omission enforces                                                                                                                                                                  |
| -------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `framework_requirement_id` | Constitutional invariant #7. `atlas_app` cannot re-point an edge at a different requirement through **any** code path, so no reviewer edit can change the shape of the graph                |
| `scf_anchor_id`            | Same — an edit changes what a mapping MEANS, never which anchor it lands on. The requirement → requirement shape is unreachable, not merely unimplemented                                   |
| `source_attribution`       | ADR 0018 / 483 P0-483-3. Provenance is import-time history; promoting it would falsify where a mapping came from — precisely the obsolete approval model 536 was written around (536a §1.2) |

**Why the grant and not only the handler.** The handler and the BFF both refuse
these fields, but a grant is the layer that cannot be bypassed by a future code
path that forgets. Invariant #7 is held by the database, with the Go layers as
defence in depth rather than as the enforcement.

**Repudiation.** `fw_to_scf_edge_content_edits` is append-only by GRANT
(`SELECT, INSERT` to `atlas_app`, no `UPDATE`/`DELETE`) and is written in the
same transaction as the content change. There is exactly one write path to the
STRM content columns from the app role — `Store.EditContent` — so no edit can
bypass the trail. It records a full before/after of all three columns, so an edit
is reconstructible from the trail alone. Same discipline as 483 P0-483-4 for tier
transitions, and the same shape as `decision_audit_log` (slice 035) /
`group_role_audit_log` (slice 509).

---

## The editing surface

### D-536b-3 — a content edit is all-or-nothing, and a no-op is refused

**Decision.** `EditRequest` requires all three content fields; a partial PATCH is
not supported. An edit that would leave all three exactly as they are is refused
`422 "the edit changes nothing"`.

**Why all three.** The audit row records a before/after of all three columns. A
partial edit makes "what did the reviewer intend for the field they omitted?"
ambiguous in the trail — and a trail that has to be interpreted is not a trail.

**Why a no-op is refused rather than silently accepted.** Writing an audit row
that records no change is noise in the queue a future reviewer reads, and 483
already rejects the analogous no-op self-transition. Strength is compared
exactly, not with a tolerance: a reviewer who deliberately nudges 0.80 to 0.81 is
making a real edit that must be audited, not rounded away.

### D-536b-4 — a `rejected` mapping cannot be edited

**Decision.** An edit against a mapping in the terminal `rejected` tier is
refused `422` rather than applied.

**Why.** 483 made `rejected` terminal — nothing transitions out of it. An edit
there would produce a mapping whose result can never re-enter review: silently
applied to a dead row, invisible in every tier-filtered queue, and yet counted in
the content-edit trail as though a reviewer had improved something. Refusing is
the honest answer. The form surfaces the reason rather than disabling the row
without explanation.

---

## The approve/reject UX (the JUDGMENT call the parent OE asked to be recorded)

### D-536b-5 — approve/reject is 483's endpoint, rendered as a per-edge confirm step

**Decision.** The UI calls `POST /v1/admin/crosswalk-edges/{id}/tier` — slice
483's route — through a thin BFF proxy. It ships no approval endpoint of its own.
The control renders one button per transition `nextTiers` reports legal from the
edge's CURRENT tier, and each button opens a confirm step carrying an optional
note before anything is posted.

**Why per-edge and not bulk.** A bulk "approve all drafts" is the obvious
efficiency win and is deliberately not built. Approving a mapping is an
audit-binding act that moves coverage scores; a bulk control would let a reviewer
verify mappings they never read, which is the constitutional "no auto-approve its
own mappings" boundary defeated by a single human click rather than honored by
it. The one-click-per-mapping shape is what makes "a human approved this" true of
each individual row in the trail.

**Why a confirm step rather than a direct fire.** The note is the reviewer's
justification and belongs to the decision, not to a follow-up edit. A one-click
approve with the note collected afterwards produces trails where the most
consequential rows are the ones with no reason attached.

**Why the UI mirrors the state machine at all.** `nextTiers` in
`web/lib/api/crosswalk-review.ts` duplicates
`internal/crosswalktier.legalTransitions` so illegal moves are not offered. This
is a clarity affordance only — the server refuses an illegal move `422`
regardless of what the UI renders, and the mirror is pinned by vitest precisely
because a mirror that drifts is worse than no mirror: it would either offer a
reviewer an approval the platform then refuses, or hide a legal one.

**Vocabulary.** The buttons read "Approve" / "Reject" / "Send to review" rather
than the raw tier names — that is the verb an operator thinks in. The payload is
still the tier value, so the vocabulary difference stays in the UI and never
reaches the wire.

### D-536b-6 — the demotion is warned about BEFORE the edit, not reported after

**Decision.** Opening the edit form on a `verified` mapping shows a destructive
alert explaining that saving will move it back to `under review` and that it will
need approving again. The response also reports the demotion after the fact.

**Why both.** Reporting only afterwards is technically complete and practically
a trap: a reviewer fixing a typo in a rationale would discover they had cost the
mapping its verified badge only once it was done. Warning first makes D-536b-1's
automatic demotion a choice rather than a surprise, which is what lets the
demotion stay automatic (and therefore unbypassable) without being hostile.

### D-536b-7 — conflicts render at requirement level, with per-edge echoes

**Decision.** The slice-536a findings render as a list on the requirement card,
above its mappings, ordered high → medium → low. Findings that name specific
edges are additionally echoed as badges on those rows.

**Why requirement level is the primary placement.** Several 536a heuristics are
statements about a SET of edges (competing anchors, sibling strength divergence),
and the orphaned-requirement family is a statement about the ABSENCE of any edge.
A purely per-row rendering could not show the orphan findings at all — they would
silently vanish from a requirement with no rows to hang them on, which is the
exact class of defect (a requirement nobody mapped) that most needs surfacing.

**Why echo per-edge anyway.** A reviewer working a specific row needs to know
that row is implicated without re-reading the requirement header. The echo is
derived by `conflictsForEdge`, and a finding naming several edges appears on each
of them — the finding is about the pair, so showing it on only one would misstate
which mapping is in question.

**Severity is advisory.** Consistent with 536a D5: severity orders the reviewer's
queue and nothing more. Nothing in the UI blocks a transition on a conflict,
auto-rejects, or dismisses a finding — every transition stays a human act through
483's endpoint.

### D-536b-8 — no framework picker; the version id comes from the query string

**Decision.** The page reads `framework_version_id` from the URL and offers a
text input to change it. There is no dropdown of framework versions.

**Why.** No framework-version LIST endpoint exists on `main` — the
`adminframeworkversions` routes are promote / revert / migration-suggest, and
`/v1/frameworks/posture` returns a tenant-scoped posture rollup carrying a
version _label_, not the id this catalog route needs. Adding a list endpoint is
backend scope this slice does not own, and inventing one to make a dropdown work
would grow the slice past the residual 536a scoped it to. Reading the id from the
query string also makes a review session linkable, which a component-local
dropdown would not.

**Cost, stated plainly.** Pasting a UUID is poor UX for a first-time operator.
The follow-on is a framework-version list endpoint plus a picker; it is named in
"Revisit once in use" rather than smuggled in here.

---

## The BFF layer

### D-536b-9 — every BFF route reconstructs its input rather than forwarding it

**Decision.** All four handlers rebuild the query string or JSON body from
individually shape-checked fields. None forwards `url.search` or the client's
object verbatim.

**Why it is more than style.** Reconstruction means a field the platform does not
currently read — `editor_id`, `reviewer_id`, `source_attribution`, `mapping_tier`,
`tenant_id`, an edge endpoint — cannot reach the upstream decoder at all, rather
than reaching it and being ignored. The invariants therefore do not depend on the
upstream decoder's tolerance of unknown fields staying the way it is today. The
approving and editing identities in particular are taken from the verified admin
JWT upstream and can never ride in from a browser.

**What the BFF explicitly does NOT do.** It replicates no authorization. The
admin gate lives in the platform (`requireAdmin`, asserted by the Go tests) and
the BFF never attempts to reproduce it — a second gate that could drift from the
first is worse than one gate that holds. Upstream refusals (403, 404, 422) pass
through with the platform's own wording so the operator reads the backend's
reason, never a BFF paraphrase of it.

### D-536b-10 — mutating and audit responses are `no-store`

The PATCH, the tier POST and the audit GET all carry `Cache-Control: no-store`
(the slice-746 posture). The audit trail is the load-bearing one: a
browser-cached copy would show a reviewer their own edit as absent from the log —
exactly the reading the trail exists to refute.

---

## Test surfaces

| Surface             | What it covers here                                                                                                                              |
| ------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------ |
| Go unit             | `crosswalkedit` validation + content equality; `admincrosswalkreview.buildQueue` filter semantics and conflict assembly, with no Postgres        |
| Go integration      | The edit transaction against real Postgres: the audit row, the column grant, the D-536b-1 demotion, the rejected-tier refusal, the no-op refusal |
| Frontend vitest     | All four BFF handlers (66 tests) plus the client vocabulary — the tier-machine mirror, the conflict helpers, and the reconstruction guarantees   |
| Frontend Playwright | Deferred to slice 536c per the parent OE                                                                                                         |

Coverage floors were seeded for the five newly-covered frontend modules at the
standard `floor(measured − 2pp)` (`web/coverage-thresholds.json`
`$how_to_extend`). The four BFF routes measure 100% across all four metrics.

---

## Revisit once in use

- **A framework-version list endpoint + picker** (D-536b-8). The paste-a-UUID
  entry point is the honest shape given what `main` exposes, not a good one.
- **Tier-aware conflict suppression.** 536a already flagged that a conflict on an
  already-`rejected` edge is noise. The queue filters by tier AFTER detection so a
  filtered view never changes what a conflict says; whether findings on rejected
  edges should be suppressed outright is a judgment better made against a real
  reviewer's queue than in advance.
- **Bulk actions, carefully.** D-536b-5 rejects bulk approve. A bulk action that
  is not trust-increasing — bulk _reject_ of orphaned mappings, say — does not
  carry the same objection and may be worth its own slice once the queue's real
  shape is known.
- **Conflict counts are per page.** `conflict_count` describes the current page,
  not the framework version, because a catalog-wide total would need the
  unbounded scan pagination exists to avoid. A framework-level conflict summary
  is a separate read model.

## Boundaries honored

| Boundary                                             | Honored | How                                                                                                                                             |
| ---------------------------------------------------- | ------- | ----------------------------------------------------------------------------------------------------------------------------------------------- |
| No requirement → requirement mappings (invariant #7) | Y       | Edge endpoints are absent from the update statement, the wire types, the BFF body, the UI form, and the `atlas_app` column grant (D-536b-2)     |
| No second approval workflow alongside 483's machine  | Y       | Approve/reject posts to 483's tier route; the one tier move this slice performs is a demotion through 483's own `ValidateTransition` (D-536b-1) |
| No edit bypasses the audit trail                     | Y       | One write path, audit row in the same transaction, append-only by GRANT — and it is the only path the app role's grant permits (D-536b-2)       |
| No auto-approval; human approval required            | Y       | Nothing transitions a tier except a human pressing a per-edge confirm; conflict detection stays advisory and never adjudicates (D-536b-5)       |
| vitest (BFF) this slice; Playwright is 536c          | Y       | Four BFF suites + the client vocabulary suite; no e2e spec added                                                                                |
