# 536 — Crosswalk-review / conflict editing UI — decisions log

JUDGMENT slice. The subjective calls are (a) how much of slice 536 slice 483
already absorbed, (b) the conflict-detection heuristics, and (c) the
approve/reject UX. Claude made each call and recorded it here; the slice ships
when CI is green (no human sign-off gate — that is the build-time JUDGMENT
process, NOT the runtime AI-assist boundary, which is untouched: nothing in this
slice auto-approves a mapping).

- detection_tier_actual: `integration`
- detection_tier_target: `integration`

One bug surfaced during the build and was caught at the Go integration tier: the
first cut of the content-edit store reused slice 483's column grant assumption
and issued `UPDATE fw_to_scf_edges SET relationship_type = ..., strength = ...`
as `atlas_app`, which slice 483 deliberately left ungranted (D1 of that slice
excluded every STRM-content column from the column-level grant). The live
integration test failed with `permission denied for table fw_to_scf_edges
(42501)`. Fixed by the deliberate, recorded grant widening in D4 below. Same
class as slice 483's own detection-tier note, and the same conclusion: a mocked
store cannot enforce a Postgres column grant, so the live tier is the right
place to catch it. `actual == target == integration`.

---

## Part 1 — Scope reconciliation against slice 483 (written BEFORE any UI)

The slice-536 brief was filed 2026-06-07 as a spillover of slice 482, three
months before slice 483 shipped (merged `d8a926ec`, 2026-06-14). 483 was scoped
as "mapping-tier governance" and 536 as "review / conflict editing UI", but they
overlap. This section is the reconciliation the acceptance criteria demands
before a line of UI is written.

### What slice 483 SHIPPED (verified by reading the merged tree, not the doc)

| Capability                     | Where it lives on `main`                                                                  |
| ------------------------------ | ----------------------------------------------------------------------------------------- |
| Trust-tier column              | `fw_to_scf_edges.mapping_tier` (`crosswalk_mapping_tier` enum), migration `20260612080000` |
| Tier state machine             | `internal/crosswalktier/tier.go` — `draft → under_review → verified`, `→ rejected` terminal |
| Approve/reject transition API  | `POST /v1/admin/crosswalk-edges/{id}/tier`, `internal/api/admincrosswalktier`               |
| Admin gate on the trust act    | `requireAdmin` / `cred.IsAdmin` → 403 for non-admins                                        |
| Append-only tier audit trail   | `fw_to_scf_edge_tier_transitions`, written in the SAME tx as the tier flip                  |
| Tier on the read path          | `/v1/anchors/{id}/requirements` + `/v1/requirements/{id}/coverage` carry the tier label      |
| Transition-history read (Go)   | `crosswalktier.Store.ListTransitions` — **method exists, no HTTP route mounted**            |

### What 536 imagined that 483 ALREADY covers — REMOVED from this slice

1. **The approval workflow itself.** 536's narrative asked for a surface that
   "promotes a draft mapping to an approved one" and spoke of promoting
   `source_attribution` up the `community_draft → reviewed → authoritative`
   ladder. That ladder is superseded: ADR 0018 split the axis in two —
   `source_attribution` is provenance (immutable, where it came from) and
   `mapping_tier` is trust (mutable, how vetted it is now). **536 does not touch
   `source_attribution` and does not define any promotion semantics.** Approve =
   `POST .../tier {"tier":"verified"}`; reject = `POST .../tier
   {"tier":"rejected"}`. Both are 483's endpoint verbatim.
2. **The tier state machine.** Not re-implemented, not extended, not forked.
   536 imports `internal/crosswalktier`.
3. **The approve/reject audit trail.** 483 already writes one row per tier
   transition in the transition's transaction. 536 adds no second trail for
   approvals.
4. **The elevated-role gate.** 536's threat model S/E asked for a dedicated
   `mapping-curator` capability distinct from the viewer role. ADR 0018 §3
   already decided "any admin/maintainer role" for the trust act, and 483
   implemented `cred.IsAdmin`. **536 reuses `cred.IsAdmin` and does NOT
   introduce a new role.** Inventing a second authz capability for the same act
   would be exactly the "parallel approval path" the brief forbids. The
   multi-operator tightening (super_admin-only, two-person rule) stays on 483's
   "Revisit once in use" list where it belongs.

### What 536 STILL has to add — this slice's real scope

483 shipped the *verb*. It shipped no way to **find** the mappings that need the
verb, no way to **fix** a mapping that is merely wrong rather than
un-reviewed, and no **surface** at all. Concretely:

| Gap                            | Why 483 does not cover it                                                                                                              |
| ------------------------------ | -------------------------------------------------------------------------------------------------------------------------------------- |
| **G1 — Review queue read**     | There is no endpoint that lists a framework version's edges. `/anchors/{id}/requirements` is anchor-first; a reviewer works requirement-first over one framework version. Nothing paginates edges for review. |
| **G2 — Content editing**       | 483's grant is `UPDATE (mapping_tier, updated_at)` — by construction it **cannot** change `relationship_type`, `strength`, or `rationale`. Canvas §3.2 puts auditor judgment in `strength`; editing it is the product surface 536 exists for. |
| **G3 — Content audit trail**   | `fw_to_scf_edge_tier_transitions` has `from_tier`/`to_tier` `NOT NULL` — it models tier moves only. A strength edit has no trail today. |
| **G4 — Conflict detection**    | Not attempted by 483 in any form.                                                                                                       |
| **G5 — Transition-history HTTP** | `Store.ListTransitions` exists but is unreachable over HTTP. The reviewer needs "who verified this and when" in the UI.               |
| **G6 — The UI**                | No page, no BFF route, no e2e coverage. 483 was backend-only.                                                                            |

**Verdict: 536 is NOT absorbed.** Roughly the approval half is gone; the
editing, discovery, conflict-surfacing, and UI halves remain and are what this
slice ships. The brief's "close as absorbed — that is a good outcome" branch
does not fire.

`docs/issues/536-crosswalk-review-conflict-editing-ui.md` is amended in the same
commit series to record this shrink, so the backlog doc stops describing a
`source_attribution` promotion ladder that ADR 0018 replaced.

---

## Part 2 — Conflict-detection heuristics (the JUDGMENT call)

### Framing constraints

Three constraints bound the design before any heuristic is picked:

- **Catalog-only inputs (threat-model I).** A heuristic may read
  `framework_requirements`, `fw_to_scf_edges`, and `scf_anchors` and nothing
  else. Folding a tenant's evaluated coverage into a conflict score would
  re-introduce slice 482's information-disclosure concern on a write path. No
  tenant table is joined; no `app.current_tenant` GUC is set.
- **Advisory, never blocking.** A conflict is a reviewer prompt, not a
  validation error. Nothing in the system refuses a transition or an edit
  because a heuristic fired. A heuristic that gates a write becomes a second
  approval workflow by the back door.
- **Deterministic and explainable.** Every conflict carries the concrete edge
  ids and values that produced it. A reviewer must be able to disagree with the
  machine from the payload alone. No scoring model, no LLM.

### The five heuristics

Each is grounded in a written source, not invented. Severity is `high` (a
reviewer should look before verifying anything else in this framework) or
`medium` (worth a look).

#### H1 — `duplicate_equal` (high) — competing anchors

**Rule.** A requirement with two or more edges of `relationship_type = 'equal'`
to different anchors.

**Why.** Canvas §3.2 defines `equal` as "logically equivalent". A requirement
cannot be logically equivalent to two distinct SCF anchors unless those anchors
are themselves equivalent — and SCF anchors are the semantic-equivalence classes
(canvas §3.1), so by construction they are not. At most one `equal` is right;
the others should be `subset_of` / `intersects_with`. This is the crispest
formulation of the brief's "competing anchors" and needs no threshold.

**Why not the looser form** ("more than N anchors of any type is suspicious"):
a requirement legitimately intersecting five anchors is normal in STRM and would
drown the reviewer in false positives. `equal` multiplicity is the version that
is actually a contradiction rather than a smell.

#### H2 — `family_strength_divergence` (medium) — contradictory strengths

**Rule.** A requirement with two or more edges into the **same SCF family**
whose strengths span ≥ **0.5**.

**Why.** This is the brief's own example ("two edges from the same requirement to
anchors in the same SCF family with contradictory strengths"). The SCF family is
the right grouping key: anchors inside one family are topically adjacent by
construction, so a requirement being covered at 0.9 by `IAC-01` and at 0.2 by
`IAC-06` is a judgment inconsistency worth a second look, whereas the same spread
across `IAC-*` and `BCD-*` is ordinary.

**Why 0.5.** The strength scale is 0.0–1.0 and canvas §3.2 anchors two points on
it: `(equal, 1.0)` = full confidence, `(intersects_with, 0.4)` = partial coverage
needing supplemental evidence. A 0.5 span is therefore at least the distance
between "full confidence" and "needs supplemental evidence" — a genuine
disagreement about the same topical area rather than ordinary graduation. A
tighter threshold (0.2, 0.3) fires on normal fine-grained scoring; a looser one
(0.7) only catches 0.9-vs-0.2 outliers and misses the common 0.9-vs-0.35 case.
The constant is named and exported (`FamilyStrengthSpanThreshold`) so tuning it
is a one-line change with a test, not an archaeology exercise.

#### H3 — `type_strength_incoherent` (high / medium) — the type disagrees with the number

**Rule.** Per relationship type:

| Type                                            | Coherent band     | Fires at   | Severity |
| ----------------------------------------------- | ----------------- | ---------- | -------- |
| `no_relationship`                               | strength = 0.0    | any > 0.0  | high     |
| `equal` / `subset_of` / `superset_of` / `intersects_with` | strength > 0.0 | exactly 0.0 | high    |
| `equal`                                         | strength ≥ 0.9    | < 0.9      | medium   |

**Why.** Canvas §3.2: `no_relationship` is "confirmed *no* overlap … it
suppresses false suggestions" — a non-zero strength on such an edge is a direct
self-contradiction and, worse, feeds a non-zero contribution into the slice-482
rollup for a relationship the author asserted does not exist. Symmetrically, a
0.0-strength edge of any real relationship type asserts a relationship that
contributes nothing — indistinguishable in effect from `no_relationship` but not
labelled as such. Both are `high`.

The `equal < 0.9` arm is `medium` and deliberately softer: canvas §3.2 pairs
`equal` with 1.0 as the full-confidence exemplar but never states a floor, so
this arm is a smell, not a contradiction. 0.9 is the judgment call — it admits
the common "equal but I rounded to 0.95" authoring habit while flagging an
`equal` at 0.6, which almost always wants to be `intersects_with`.

**Why this is not a CHECK constraint.** The DB already bounds strength to
[0.0, 1.0] (`fw_to_scf_edges_strength_range`). Encoding these bands as a
constraint instead would make existing rows un-writable and turn an advisory
signal into a hard failure — the "advisory, never blocking" constraint above.

#### H4 — `orphaned_requirement` (high)

**Rule.** A requirement in the framework version with **zero** edges.

**Why.** Constitutional invariant #7 makes the SCF anchor the only route from a
requirement to evidence. A requirement with no edge is structurally
uncoverable — the slice-482 rollup can only ever report it at zero, and no amount
of evidence will change that. This is the highest-value conflict in the set
because it is invisible on every other surface: the coverage rollup shows a
legitimately-zero score, indistinguishable from "mapped but unevidenced".

#### H5 — `no_relationship_only` (medium)

**Rule.** A requirement whose edges are ALL `no_relationship`.

**Why.** The effective twin of H4 — the requirement routes to no anchor that can
carry coverage. Kept as a separate kind rather than folded into H4 because the
remedy differs: H4 means "nobody mapped this yet", H5 means "somebody looked and
recorded only non-relationships", which is either a deliberate and correct
statement about a scoping requirement (SOC 2 has several) or an authoring error.
Because the correct case is real, this is `medium`, not `high`.

### Heuristics considered and REJECTED

- **Reverse-direction disagreement.** 536's narrative floated "an edge whose
  relationship-type disagrees with the reverse direction". There is no reverse
  edge to disagree with: `fw_to_scf_edges` is directed requirement → anchor and
  the schema's `UNIQUE (framework_requirement_id, scf_anchor_id)` permits exactly
  one row per pair (migration `_013`, citing NIST IR 8477 §4). The heuristic as
  written is undetectable against the shipped model. Dropped, and the slice doc
  amended so it is not re-proposed.
- **Cross-framework contradiction** ("ISO says 0.9 for this anchor, PCI says
  0.2"). Different frameworks legitimately relate to the same anchor at different
  strengths — that is the whole point of the graph. Pure noise.
- **Anything reading tenant coverage** — excluded by the threat model above.
- **Anything LLM-derived.** The AI-assist boundary permits *suggesting* mappings;
  a suggestion surface here would need the full `ai_assisted` / `human_approver`
  column set and citation discipline. Out of scope, and the deterministic
  heuristics are the cheaper 80%.

---

## Part 3 — Build-time decisions

### D1 — The approve/reject UX: an inline two-step on the review row, not a modal wizard

**Decision.** Each edge row in the review table carries its tier badge and the
transitions legal *from that tier*, rendered as inline buttons: a `draft` row
offers "Start review" and "Reject"; an `under_review` row offers "Verify" and
"Reject"; a `verified` or `rejected` row offers neither and says so. Clicking
opens a small inline confirm strip with an optional note field and a
"Confirm" / "Cancel" pair — no modal, no multi-step wizard.

**Why.** Three reasons. (1) The legal-move set is computed from 483's state
machine and rendered as the ONLY affordances, so the UI cannot offer a move the
server will reject — the affordance set and the server's `ValidateTransition`
agree by construction. (2) The reviewer's loop is "read the mapping, judge it,
act" over dozens of rows; a modal per row costs two extra interactions each and
loses the surrounding context that makes the judgment possible. (3) The confirm
step exists because verification is the trust act — a single misclick must not
promote a mapping. It is one deliberate extra click, not a wizard.

**Rejected: a bulk "verify all drafts in this framework" action.** It is the
fastest possible path to rubber-stamping agent-authored data at scale, which is
precisely what the AI-assist boundary's "no auto-approve its own mappings" is
defending against. A human approving 200 mappings by reading 200 rows is the
point; a human approving 200 mappings with one click is auto-approval with a
human's name on it.

### D2 — Editing is a PATCH on the edge, gated to the pre-trust tiers

**Decision.** `PATCH /v1/admin/crosswalk-edges/{id}` accepts any subset of
`relationship_type`, `strength`, `rationale`. The edit is **rejected 422 when the
edge's current tier is `verified` or `rejected`**.

**Why.** Silently rewriting the content of a `verified` mapping would make the
audit answer to "who verified this?" a lie: the reviewer verified the mapping as
it read at that moment. Blocking the edit keeps the tier's meaning intact without
inventing any new state. And 483's state machine has no `verified → under_review`
demotion (already on its "Revisit once in use" list), so a re-open path would
mean extending that state machine — which the brief forbids. The result is an
honest, recorded limitation rather than a quiet corruption: to change a verified
mapping today, the operator edits the source YAML and re-imports, exactly as
before. The moment 483's demotion edge lands, this restriction becomes the
correct behavior for free.

`rejected` is blocked for the same reason plus its terminality.

### D3 — Content edits get their OWN append-only trail, not a widened tier trail

**Decision.** New table `fw_to_scf_edge_content_revisions`: `edge_id`,
`editor_id`, before/after triples for `relationship_type` / `strength` /
`rationale`, `note`, `created_at`. Append-only by grant (SELECT + INSERT to
`atlas_app`, no UPDATE/DELETE), no RLS — catalog-level, mirroring
`fw_to_scf_edge_tier_transitions` exactly. Written in the SAME transaction as the
content UPDATE.

**Why a second table rather than a generalized one.** The tier table's
`from_tier` / `to_tier` are `NOT NULL` enums; making them nullable to host
content edits would (a) weaken an existing audit invariant, (b) require touching
483's shipped table, and (c) produce a trail where "what changed" is only
knowable by which columns happen to be null. Two narrow append-only tables with a
merged read (`GET .../history`, newest first, both kinds interleaved) is the
cheaper and more honest shape. Both share the same discipline; neither is
mutable.

### D4 — Grant widening: `UPDATE (relationship_type, strength, rationale)`

**Decision.** Migration `20260722090000` adds
`GRANT UPDATE (relationship_type, strength, rationale) ON fw_to_scf_edges TO
atlas_app`, on top of 483's `(mapping_tier, updated_at)`.

**Why, and why it is recorded loudly.** Slice 483's D1 deliberately withheld
exactly these columns, reasoning that the tier handler "still cannot touch the
STRM edge content". That reasoning was correct *for 483* — a tier flip has no
business editing content. 536 is the slice whose entire purpose is editing that
content in-product, so the grant must widen; leaving it narrow and routing the
write through the BYPASSRLS pool instead would be strictly worse (that pool
bypasses every tenant RLS policy in the database for the sake of one catalog
edit). `source_attribution` stays ungranted: provenance is immutable and no
product surface may rewrite where a mapping came from. The narrowing that
matters — the API can change judgment fields, never provenance — is preserved.

### D5 — Conflict scan is whole-version and capped; the review list is paginated

**Decision.** `GET /v1/admin/crosswalk-review?framework_version=…` paginates
(default 50, max 200). `GET /v1/admin/crosswalk-review/conflicts?framework_version=…`
does NOT paginate: it scans the whole framework version and returns every
conflict, but refuses (422) a version whose edge count exceeds
`MaxScannableEdges = 20000`.

**Why.** H4 (orphaned requirement) is a whole-set property — a paginated scan
would report orphans that only look orphaned because their edges are on another
page. So the scan must be whole-version. The DoS mitigation from the threat model
is preserved by the explicit cap plus the natural bound (the largest shipped
framework version is in the low hundreds of requirements; 20 000 edges is roughly
two orders of magnitude of headroom). The cap is a named constant with a test,
and exceeding it is an explicit, legible refusal rather than a slow query.

### D6 — The reviewer id comes from the JWT subject, never the body

**Decision.** Both the edit handler and 483's transition handler derive the actor
from `jwtmw.SubjectUserID(cred.UserID)` and fail closed (403) if it does not
parse.

**Why.** Straight from 536's threat model T ("the approver id is taken from the
session, never the request body"). 483 already does this; the edit handler copies
the pattern verbatim rather than inventing a second convention. A request body
that carries an `editor_id` is ignored — the field does not exist in the DTO.

### D7 — Client-side authz is presentational only

**Decision.** The review page renders for any authenticated user; the action
buttons render disabled with an explanatory note when `/api/me` reports a
non-admin. Authority is enforced entirely server-side (403 from the platform),
and an upstream 403 surfaces inline on the row rather than as a dead button.

**Why.** The slice-097 D3 / slice-479 pattern already established in this repo:
UI role checks are defense-in-depth and a nicety, never the gate. The Playwright
spec asserts both arms so a future refactor cannot quietly make the UI the
authority.

---

## Revisit once in use

- **Verified-mapping edits.** Blocked today (D2). Becomes editable-after-demote
  the moment 483's `verified → under_review` demotion edge lands (already on
  483's revisit list). Deliberately not built here — it belongs to 483's state
  machine, not to this UI.
- **Tuning the H2 threshold and the H3 `equal` floor.** Both are named constants
  with unit tests. Once a maintainer has run a real review pass over ISO and PCI,
  the false-positive rate will say whether 0.5 / 0.9 are right. Treat a retune as
  a one-line change plus a test update, not a slice.
- **Conflict-driven review ordering.** The queue sorts by requirement code today.
  Sorting by "has a high-severity conflict" would be a better default once the
  heuristics have proven their precision. Held back deliberately: promoting an
  unvalidated heuristic to the primary sort order would make its false positives
  the first thing every reviewer sees.
- **Cross-framework conflict view.** Out of scope here and largely slice 537's
  (cross-framework comparison matrix) territory.
- **A `mapping-curator` role distinct from admin.** 536's original threat model
  wanted one; ADR 0018 §3 chose "any admin/maintainer". Revisit alongside 483's
  own multi-operator note, as ONE decision, not two.

## Anti-criteria honored

| Anti-criterion                                        | Honored | How                                                                                                                                                                             |
| ----------------------------------------------------- | ------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| No requirement → requirement mapping (invariant #7)   | Y       | The editor mutates `relationship_type` / `strength` / `rationale` on EXISTING `fw_to_scf_edges` rows only. There is no create endpoint and no endpoint that names two requirements. |
| No second approval workflow alongside 483             | Y       | Approve/reject in the UI call `POST /v1/admin/crosswalk-edges/{id}/tier` — 483's endpoint. `internal/crosswalktier` is imported, not forked. No new tier column, table, or role.    |
| No edit bypasses the audit trail                      | Y       | `crosswalkreview.Store.EditContent` does the content UPDATE + the revision INSERT in one tx; a failure rolls back both. Asserted by an integration test that counts revision rows.  |
| No auto-approval of a mapping                         | Y       | No code path calls the transition store without an admin-authenticated HTTP request. No bulk-verify action (D1). Nothing derives a tier from a conflict result.                     |
| No production deploy / destructive op                 | Y       | Additive migration; no data rewrite; down migration provided.                                                                                                                       |
