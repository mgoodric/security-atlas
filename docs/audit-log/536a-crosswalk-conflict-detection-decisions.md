# 536a — Crosswalk conflict detection + slice-483 scope reconciliation — decisions log

JUDGMENT slice (backend half). Slice 536 was filed 2026-06-07 as a spillover of
slice 482, before slice 483 (crosswalk-mapping verified-tier governance, merged
`d8a926ec`) existed. 483 shipped a review/approval path for crosswalk mappings.
This document reconciles the two FIRST — so the 536b UI builds on 483's review
API rather than growing a parallel approval workflow — and then records the
conflict-detection heuristics this slice implements.

Slice 536 is decomposed into three fires:

| Slice | Scope                                                        |
| ----- | ------------------------------------------------------------ |
| 536a  | This one — scope reconciliation + conflict-detection backend |
| 536b  | Review/edit UI + BFF + audit + vitest (depends on 536a)      |
| 536c  | Playwright e2e (depends on 536b)                             |

- detection_tier_actual: none
- detection_tier_target: unit

No bug surfaced during this build. The heuristics are pure functions over a
catalog edge set, so `unit` is the correct target tier: every branch is
reachable with an in-memory fixture and none of them needs Postgres.

---

# Part 1 — Scope reconciliation: what slice 483 already covers of slice 536

## 1.1 Slice 536's stated "What", clause by clause

The slice-536 doc lists three capabilities. Measured against what 483 merged:

| 536 clause                                                                                           | Status after 483             | Evidence                                                                                                                                |
| ---------------------------------------------------------------------------------------------------- | ---------------------------- | --------------------------------------------------------------------------------------------------------------------------------------- |
| "Lists a framework's requirement → anchor edges with STRM type + strength + attribution + rationale" | **Covered (read path)**      | `ListFwToScfEdgesForRequirement` already returns type, strength, `source_attribution`, `mapping_tier`, rationale, scf_id, family, title |
| "…and **approve** an edge (promoting `source_attribution`)"                                          | **Covered — and superseded** | 483 shipped `POST /v1/admin/crosswalk-edges/{id}/tier` + the `draft → under_review → verified` state machine + append-only audit trail  |
| "…lets a reviewer **edit** the strength / relationship-type / rationale"                             | **NOT covered**              | 483 deliberately granted `atlas_app` only `UPDATE (mapping_tier, updated_at)`; the STRM content columns stay import-owned (483 D1)      |
| "**Surfaces conflicts**"                                                                             | **NOT covered**              | 483 has no conflict concept at all                                                                                                      |

Two of the four clauses were absorbed by 483. Roughly **half of slice 536's
backend scope is already shipped**, and the half that shipped is the half 536
would have gotten wrong.

## 1.2 The load-bearing correction: 536's approval model is obsolete

Slice 536 was written to "**approve** an edge (promoting `source_attribution`)"
and its threat model says "a crafted request must not … set an arbitrary
`source_attribution` (e.g. jump straight to `authoritative`)".

**That model is superseded.** ADR 0018 / slice 483 P0-483-3 established that
provenance and trust are orthogonal dimensions:

- `source_attribution` (`scf_official | community_draft | org_internal`) says
  **where a mapping came from**. It is import-time provenance and is never
  promoted by a reviewer — promoting it would falsify history.
- `mapping_tier` (`draft | under_review | verified | rejected`) says **how
  trusted it is now**. This is the reviewer-mutable dimension.

536's "promote `source_attribution`" and its imagined `authoritative` value do
not exist on `main`. **536b MUST NOT implement approval.** The approve action is
already shipped: it is a `POST` to 483's tier endpoint. 536b's "approve" button
calls that route and nothing else. This is the anti-criterion the parent OE
flagged as "do NOT build a second approval workflow alongside slice 483's tier
state machine", and it is now grounded in the merged code rather than in
intent.

Corollary for 536b's threat model: the S / E arms (who may approve) are already
mitigated by 483's `requireAdmin` gate and asserted by 483's `TestNonAdminRejected`.
536b inherits them; it does not re-litigate them.

## 1.3 What 483 also gave 536 for free

- **R (repudiation).** 536's threat model asks for "every edit + approval logged
  with actor, before/after diff, timestamp". For the _tier_ dimension that is
  shipped: `fw_to_scf_edge_tier_transitions` is append-only by GRANT and written
  in the same transaction as the tier change (483 D4 / P0-483-4). 536b needs an
  audit trail only for the **content-edit** dimension it adds.
- **I (information disclosure).** 483 P0-483-6 already keeps reviewer identity
  off the public `/anchors` payload. 536b keeps that boundary.
- **The AI-assist boundary.** 483 operationalized "no auto-approve its own
  mappings" for this layer: nothing reaches `verified` without a human
  transition. 536's restatement of that boundary is satisfied, not pending.

## 1.4 What slice 536 still needs — the residual, split across 536a/b/c

| #   | Residual work                                                                                                      | Slice    |
| --- | ------------------------------------------------------------------------------------------------------------------ | -------- |
| 1   | Conflict-detection heuristics over the catalog edge set                                                            | **536a** |
| 2   | Content editing of `relationship_type` / `strength` / `rationale` (needs a _widened_ column grant + its own audit) | **536b** |
| 3   | The review surface itself (list, filter by tier, edit form, "approve" wired to 483's tier endpoint) + BFF          | **536b** |
| 4   | Playwright e2e over the review flow                                                                                | **536c** |

536b is materially smaller than the original 536 because #2 is the only new
write surface it introduces — the approval write is 483's.

### Two decisions 536b must make (flagged now, NOT made here)

- **D-536b-1 — does a content edit reset the tier?** Editing a `verified`
  mapping's strength arguably invalidates the verification. 483's state machine
  has no `verified → under_review` demotion edge (its own "Revisit once in use"
  list names this gap). 536b must either add that edge to 483's machine — the
  correct move, extending one state machine — or forbid content edits on
  `verified` edges. It must NOT invent a second lifecycle.
- **D-536b-2 — the content-edit column grant.** 483 D1 deliberately withheld
  `UPDATE` on the STRM-content columns from `atlas_app`. 536b widening that
  grant is a real privilege expansion and needs its own threat-model note; the
  narrow-grant reasoning in 483 D1 is the baseline to argue against.

## 1.5 Reconciliation conclusion

**Slice 483 covers roughly half of slice 536's backend scope — specifically the
approve/governance half — and supersedes 536's approval design.** 536 is NOT
fully absorbed: conflict detection and content editing are untouched by 483.
536b and 536c both survive, with 536b shrunk to "content edit + review surface
on top of 483's review API".

---

# Part 2 — Conflict-detection heuristics

Delivered as `internal/crosswalkconflict` — pure Go over an in-memory catalog
edge set, no DB, no context, no tenant data (slice 353 Q-2 pure-Go convention,
the same shape as slice 482's `internal/api/ucfcoverage/rollup.go`).

## D1 — Detection is a pure function over catalog data only

**Decision.** `Detect(Input) []Conflict` takes a requirement inventory + an edge
slice and returns conflicts. It never opens a transaction, never reads tenant
state, and never persists a finding.

**Why.** Slice 536's threat-model **I** is explicit: "the conflict-detection
heuristics must NOT fold in any tenant's evaluated coverage (that would
re-introduce the slice-482 threat-model I concern on a write path). Conflict
detection runs over catalog edges only." Making the module a pure function over
a catalog-shaped `Input` enforces that **by construction** — there is no field
on `Input` through which tenant coverage could arrive, so the mitigation cannot
be forgotten by a future caller. It also makes every heuristic branch reachable
from a table test with no Postgres.

The DB adapter (`EdgesFromDBRows`) is a separate, trivially-reviewable
conversion from the existing slice-438/483 row type. No new SQL: the module
consumes `ListFwToScfEdgesForRequirementRow` as-is.

## D2 — Competing anchors

**What it flags.** Two sub-reasons, both scoped to a single requirement:

1. `duplicate_equal_claim` (severity high) — the requirement has two or more
   `equal` edges to distinct anchors, in any family.
2. `duplicate_total_claim_in_family` (severity medium) — the requirement has two
   or more **total-claim** edges (`equal` or `subset_of`) to distinct anchors
   **within one SCF family**.

**Rationale for (1).** Canvas §3.1 calls SCF anchors "semantic-equivalence-class
anchors" and NIST IR 8477 `equal` means "logically equivalent". A requirement
cannot be logically equivalent to two _distinct_ equivalence classes — if it
genuinely were, the two anchors should themselves be merged. So a second `equal`
edge is not a richer mapping, it is a mapper who could not choose. High severity
because `equal` carries the strongest downstream weight.

**Rationale for (2).** `subset_of` means "the source is fully covered by the
target". Two full-coverage claims inside one family say the requirement is
completely handled by anchor A _and_ completely handled by anchor B in the same
domain — a duplication the reviewer resolves by keeping one and downgrading the
other to `intersects_with`. Medium severity: unlike `equal`, `subset_of` pairs
are occasionally defensible when a family's anchors are granular.

**Why family-scoped, and why (1) is not.** A requirement legitimately spanning
several SCF domains is the normal case, not a conflict — an ISO requirement can
be `subset_of` an IAC anchor and `subset_of` a CRY anchor because it really does
have two independent obligations. Family-scoping is what keeps rule (2) from
firing on every well-mapped multi-domain requirement. Rule (1) is _not_
family-scoped because the logical-equivalence argument does not weaken across
families: two `equal` claims are incoherent wherever they land.

**Deliberate non-rule.** The 536 doc floats "an edge whose relationship-type
disagrees with the reverse direction". There is no reverse direction to check:
invariant #7 means the graph stores only requirement → anchor, and
`fw_to_scf_edges` is `UNIQUE (framework_requirement_id, scf_anchor_id)` with
exactly one `relationship_type` per pair (NIST IR 8477 §4). A reverse-direction
check would require materializing anchor → requirement edges, which would take
the module toward the requirement → requirement shape invariant #7 forbids. Not
implemented, by design.

**Dedup.** When a family group is composed entirely of `equal` edges, rule (2)
is suppressed — rule (1) already reported that exact edge set at higher
severity, and emitting both would double-count one problem in the reviewer's
queue.

## D3 — Contradictory strengths

Two sub-shapes: per-edge incoherence between type and strength, and divergence
between sibling edges.

### D3a — Type/strength incoherence (per edge)

Canvas §3.2 states the contract directly: "A **strength** field captures auditor
judgment: `(equal, 1.0)` is full confidence; `(intersects_with, 0.4)` flags
partial coverage." The relationship type therefore implies a strength band, and
an edge outside its own band is self-contradictory regardless of any other edge:

| Reason                                | Fires when                              | Severity | Rationale                                                                                                                                                                                                               |
| ------------------------------------- | --------------------------------------- | -------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `no_relationship_with_strength`       | `no_relationship` and strength > 0      | high     | `no_relationship` is "confirmed _no_ overlap" (canvas §3.2). Positive strength on it is a straight contradiction, and it feeds a nonzero term into the 482 rollup                                                       |
| `equal_below_full_strength`           | `equal` and strength < 0.9              | medium   | Canvas pins `(equal, 1.0)` as the full-confidence pairing. A hedged `equal` means the mapper meant `subset_of` or `intersects_with` and reached for the wrong type                                                      |
| `intersects_at_full_strength`         | `intersects_with` and strength ≥ 1.0    | medium   | "Partial overlap" at full confidence is the same error mirrored: full coverage should be `equal` or `subset_of`                                                                                                         |
| `asserted_relationship_zero_strength` | any type except `no_relationship`, == 0 | medium   | An asserted relationship worth zero is indistinguishable from no relationship, and `no_relationship` is the field that already records that — as canvas §3.2 notes, it is real data that "suppresses false suggestions" |

**The 0.9 cut point for `equal`.** A tolerance rather than an exact `== 1.0`
test, so float round-tripping through `DOUBLE PRECISION` cannot manufacture a
finding. 0.9 sits inside slice 482's `strong` band (floor 0.8), so an `equal`
edge that clears this check also reads as strongly covered downstream — the two
judgments agree instead of contradicting each other.

### D3b — Sibling strength divergence

**Fires when** two or more edges from one requirement, to anchors in the **same
family**, with the **same relationship type**, have strengths spanning more than
`0.4`. Severity medium.

**Rationale.** Same requirement, same family, same STRM type is the same auditor
judgment applied twice. A 0.9-against-0.3 pair means one of the two was assigned
carelessly, even though neither is individually incoherent — this is the
heuristic that catches the bulk-authored crosswalk where attention drifted.

**Why 0.4.** It is one full slice-482 confidence band's width. 482 cuts bands at
0.5 and 0.8, so a spread above 0.4 guarantees the two siblings land in different
operator-visible bands — the exact point where the inconsistency stops being
academic and starts showing up in the UI as contradictory coverage. Tying the
threshold to an already-shipped constant rather than inventing a fresh one keeps
the two slices' notions of "materially different strength" aligned. Flagged
"Revisit once in use" — real crosswalk data may want it tighter.

## D4 — Orphaned requirements

**What it flags.** A requirement with no _anchoring_ path into the SCF spine.
Three sub-reasons, deliberately distinguished:

| Reason                | Fires when                             | Severity | Why distinct                                                                                                                          |
| --------------------- | -------------------------------------- | -------- | ------------------------------------------------------------------------------------------------------------------------------------- |
| `unmapped`            | zero edges                             | high     | A loader gap — nobody has looked at this requirement. This is the one a maintainer must act on                                        |
| `zero_strength_only`  | edges exist, every strength is 0       | high     | Mapped on paper, worthless in practice; hides behind an edge count that looks healthy, which is precisely why it needs its own reason |
| `explicitly_unmapped` | edges exist, all are `no_relationship` | low      | A deliberate assertion a reviewer already made. Surfaced for confirmation, not as a defect                                            |

**Rationale.** Canvas §3.1: all framework-to-framework relationships derive
through SCF anchors. A requirement with no anchoring edge can never be covered
by any evidence — slice 482's rollup will report it `uncovered` forever — and
the operator has no way to tell "genuinely not applicable" from "the crosswalk
forgot it". Splitting `explicitly_unmapped` out at low severity is what keeps
that distinction: without it, every deliberate `no_relationship` assertion would
read as a defect and reviewers would learn to ignore the whole category.

**Why the requirement inventory is a separate input.** A requirement with zero
edges produces zero rows in any edge query, so it is invisible to an
edges-only API. `Input.Requirements` is what makes `unmapped` detectable at all.
Documented on the type: omit the inventory and `unmapped` silently cannot fire.

## D5 — Severity is advisory, and detection never writes

**Decision.** `Conflict` carries a `Severity` (`low | medium | high`) and the
module returns findings; it does not block, auto-reject, or transition a tier.

**Why.** Auto-demoting a mapping on a detected conflict would be the platform
auto-adjudicating its own mapping data — the exact inverse of the constitutional
"no auto-approve its own mappings" boundary, and it would bypass 483's state
machine (a tier change with no reviewer and no audit row, breaking P0-483-4).
Severity exists to order the reviewer's queue in 536b, nothing more. Every
transition stays a human act through 483's endpoint.

## D6 — Deterministic, stable output ordering

**Decision.** `Detect` sorts by requirement code, then kind, then reason, then
the edge-ID list; every internal map is iterated over sorted keys.

**Why.** Go map iteration is randomized. Without an explicit sort the same
catalog would produce a differently-ordered review queue on every call, the
536b UI would reshuffle between refreshes, and the tests would flake. Cheap to
do once at the boundary.

## D7 — Invariant #7 holds by construction

`Edge` models exactly one direction: `RequirementID → AnchorID`. There is no
type in this package that can express a requirement-to-requirement relation, and
every `Conflict` carries a single `RequirementID` plus anchor identifiers — a
finding can never name two requirements. Pinned by
`TestNoRequirementToRequirementRelationEmitted`.

---

## Revisit once in use

- **Tune the thresholds against real crosswalk data.** `equalStrengthFloor`
  (0.9) and `siblingStrengthSpread` (0.4) are reasoned from the canvas and slice
  482's bands, not from measured false-positive rates. The ISO/PCI/CSF/HIPAA
  crosswalks (slices 438/447/480/481) are the corpus to calibrate against once a
  reviewer has worked the queue.
- **Tier-aware suppression.** Once 536b is in use, a conflict on an edge already
  `rejected` is noise. The module deliberately ignores `mapping_tier` today
  (findings are about mapping _content_, not trust state); filtering by tier is
  the caller's call and may want to move in here.
- **Cross-requirement conflicts.** Every heuristic here is scoped within one
  requirement. Whole-framework shapes — one anchor claimed `equal` by many
  requirements in the same framework, say — are a real class this slice does not
  attempt. It needs a different traversal and probably its own slice.
- **Persisted findings.** Detection is recomputed per call. If the review queue
  grows large enough that recomputation hurts, a materialized findings table
  with a dismissal state is the follow-on — but it introduces a write surface
  and therefore a threat model, so it is not a quiet optimization.

## Boundaries honored

| Boundary                                                   | Honored | How                                                                                                                       |
| ---------------------------------------------------------- | ------- | ------------------------------------------------------------------------------------------------------------------------- |
| No requirement → requirement mappings (invariant #7)       | Y       | `Edge` is requirement → anchor only; no `Conflict` names two requirements (D7, pinned by test)                            |
| No second approval workflow alongside 483's state machine  | Y       | Nothing in this package transitions a tier or writes anything (D5); §1.2 records the instruction for 536b                 |
| Backend + tests + decisions doc only                       | Y       | One new Go package + its unit suite + this document. No UI, no BFF, no Playwright, no new route, no migration, no new SQL |
| Conflict detection reads catalog edges only (536 threat I) | Y       | `Input` has no tenant-scoped field (D1)                                                                                   |
