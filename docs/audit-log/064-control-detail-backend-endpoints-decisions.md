# 064 — Control-detail backend read endpoints — decisions log

Slice 064 is `Type: AFK` — its acceptance criteria are mechanically
verifiable. But the slice surfaced seven build-time judgment calls (wire-shape
mappings between canvas field names and the on-disk schema, the cursor-
pagination shape, and the AC-5 403 enforcement path). This log records them in
the JUDGMENT-slice format so the maintainer can re-evaluate the calls once the
endpoints are wired into the slice-041 control-detail view against real data.

It does NOT block merge.

## Decisions made

### 1. `control_id` resolution: UUID param, slice-012 dual-column predicate

**Options considered:**

- **(A)** Accept `?control_id=` as a free-form string (UUID or SCF anchor like
  `scf:VPM-04`) and resolve against `control_ref` only.
- **(B)** Accept `?control_id=<uuid>` only; resolve against the UUID
  `control_id` column only.
- **(C)** Accept `?control_id=<uuid>` only (400 on non-UUID); resolve with the
  slice-012 predicate `WHERE tenant_id = $1 AND (control_id = $2 OR
control_ref = $3)`, `$3` = the UUID's string form.

**Chosen: (C).**

**Rationale.** AC-1 requires reuse of "slice 012's control->evidence
resolution path" and forbids re-deriving linkage. Slice 012's `loadEvidence`
(`internal/eval/store.go`) calls `ListEvidenceForControlAsOf` with
`controlRef := ctrlID.String()` — it resolves BOTH the UUID `control_id`
column AND the free-form `control_ref` string, because evidence pushed under
an SCF anchor has `control_id = NULL`, and the slice-012 test harness seeds
`control_ref = ctrlID.String()` even when `control_id` is also set. Option (B)
would silently miss anchor-stored evidence — a faithful-reuse violation.
Option (A) would let the UI pass an anchor string, but the slice-041
control-detail view is keyed by control UUID (`/controls/[id]`), so the
anchor-string case is not reachable from the v1 UI; accepting it would be
untested surface. Option (C) is the exact slice-012 path, scoped to the one
caller shape the UI produces.

**Confidence: high.** The slice-012 path was read directly from
`internal/eval/store.go`; the dual-column predicate is verbatim reuse.

### 2. Cursor pagination: opaque base64 keyset over (timestamp, id)

**Options considered:**

- **(A)** `LIMIT/OFFSET` pagination (the slice-013 `ListEvidenceRecordsByControl`
  shape).
- **(B)** Opaque base64 keyset cursor encoding `(observed_at, id)` for evidence
  and `(evaluated_at, id)` for history.

**Chosen: (B).**

**Rationale.** Both `evidence_records` and `control_evaluations` are
append-only ledgers (constitutional invariant #2). `OFFSET` pagination drifts
when rows are appended between page fetches — page 2 can skip or repeat rows.
A keyset cursor over the sort key plus the primary key as a tiebreaker is
stable under concurrent appends, which is the correct property for an
append-only ledger surface. The cursor is base64-opaque so the wire contract
does not leak the `(timestamp, id)` internals and can evolve. AC-1/AC-4
explicitly say `?cursor=<opaque>`, which rules out (A).

**Confidence: high.** Keyset pagination over append-only ledgers is the
standard correct pattern; the issue's `?cursor=<opaque>` wording mandates it.

### 3. Evidence `source` wire field maps to `provenance` JSONB

**Options considered:**

- **(A)** Map `source` to `evidence_records.source_attribution` (slice-013
  JSONB).
- **(B)** Map `source` to `evidence_records.provenance` (slice-002 JSONB —
  canvas §2.3 "Connector ID, source system ID, source record key, query hash,
  runner ID").
- **(C)** Map `source` to `ingestion_path` (the `push|pull|subscribe|...`
  enum).

**Chosen: (B).**

**Rationale.** Canvas §2.3 defines `provenance` as the field carrying source
identity — "Connector ID, source system ID, source record key, query hash,
runner ID". That is precisely what a control-detail "evidence stream" row's
`source` should surface to a human. `source_attribution` (C's sibling) is the
slice-013 tenant-authored-vs-connector classification — narrower than what a
UI `source` column wants. `ingestion_path` is a transport label, not a
source. The endpoint passes `provenance` through as raw JSON so the frontend
can render whichever sub-field it chooses.

**Confidence: medium.** Correct against the canvas field definition, but
"source" is an under-specified wire name; a future UI iteration may want a
flattened `source.connector_id` string rather than the whole JSONB. Revisit
when the frontend binds the evidence stream.

### 4. Evidence `scope_cell` wire field maps to `scope_id`

**Options considered:**

- **(A)** Map `scope_cell` to `evidence_records.scope_id` (nullable UUID FK to
  `scopes`).
- **(B)** Join through to `scope_cells` and return the cell label.

**Chosen: (A).**

**Rationale.** `evidence_records` carries `scope_id` (slice-002), a nullable
UUID. The control-state endpoint (slice 012) returns `scope_cell_id` as a
nullable string in the same spirit. Returning the raw id keeps the endpoint a
single-table read (anti-criterion P0: no N+1, one query per endpoint). A
label join is a presentation nicety the frontend can resolve via its existing
scope-cell client fns. Serialized as a nullable string to match the
slice-012 `scope_cell_id` convention.

**Confidence: high.** Single-table read honors the no-N+1 anti-criterion;
matches the slice-012 nullable-id convention.

### 5. AC-5 403: handler-level defense-in-depth control-read role guard

**Options considered:**

- **(A)** Rely solely on the slice-035 OPA middleware for the 403.
- **(B)** Add a handler-level control-read role guard (admin / grc_engineer /
  control_owner / auditor allowed; viewer denied) as defense-in-depth,
  mirroring slice-062's `cred.IsAdmin` handler check.

**Chosen: (B).**

**Rationale.** AC-5 requires "a role without control-read access gets 403"
and names the control-read role set (auditor + grc_engineer + control_owner
per slice 025/035). The slice-035 OPA middleware is the primary gate in
production, but it is NOT wired in unit/integration test servers
(`api.New(api.Config{})` leaves `authzEngine` nil — confirmed in
`internal/api/controlstate/integration_test.go`). A handler-level guard that
reads the credential from `authctx.CredentialFromContext` and checks the
derived role is testable without standing up OPA, exactly as slice 062's
admin endpoints do their `cred.IsAdmin` check. The middleware remains the
primary gate; the handler check is the defense-in-depth twin (the same
belt-and-suspenders posture slices 059/062 adopted). The derived-role mapping
mirrors `internal/authz/input.go` `derivedRolesFor`: `IsAdmin -> admin`,
`IsApprover -> grc_engineer`, `len(OwnerRoles) > 0 -> control_owner`, bare
tenant credential -> grc_engineer. A `viewer` credential (no flags, explicit
viewer role) is the denied case; in v1 credstore a viewer is represented by
a credential the test constructs with no control-read role.

**Confidence: medium.** The defense-in-depth pattern is well-established
(slices 059/062), but the v1 credstore has no first-class `viewer`
credential, so the "unauthorized role" is modeled in the test rather than
issued by `credstore`. When slice-035's full RBAC graduates the credstore to
DB-backed roles, this guard should re-derive from the resolved role set
rather than the credential flags. Revisit when `user_roles` is the role
source of truth.

### 6. Query file placement: new `control_detail.sql`, no schema-list edit

**Decision.** The four new sqlc queries live in a new file
`internal/db/queries/control_detail.sql`. No migration is added (AC P0
forbids it), so `sqlc.yaml`'s `schema:` list is untouched — only the
`queries:` directory gains a file. `sqlc generate` regenerates
`internal/db/dbx/*.go`; the generated files are never hand-edited.

**Confidence: high.** Matches the established pattern — every slice that adds
read queries without a migration adds a query file only.

### 7. Risks `link_weight` wire field maps to `design_score`

**Options considered:**

- **(A)** `link_weight` = `weight_design` (the design-component weight).
- **(B)** `link_weight` = `design_score` (the human-set 0..1 design-quality
  factor).
- **(C)** `link_weight` = the full weight triple `(weight_design,
weight_operation, weight_coverage)` as a nested object.

**Chosen: (B).**

**Rationale.** AC-3's row shape is `{risk_id, title, inherent_score,
residual_score, link_weight}` — a single scalar `link_weight`, not an object,
so (C) is out. Between the two scalars: `design_score` is the only
human-authored per-link factor (canvas §6.2 / migration `_029`: "the only
component a person sets directly"); `weight_design` is a tuning weight that
defaults to 0.3 and sums-to-1.0 with the other two. For a control-detail UI
rail showing "this control's contribution to this risk", the human-set
design-quality score is the meaningful per-link signal — it answers "how good
is this control's design for this risk". The three `weight_*` columns are the
residual-formula tuning knobs, not a per-link "weight" a reader would expect.
`residual_score` (the risk's computed residual JSONB) is passed through
separately per the row shape.

**Confidence: medium.** `link_weight` is an under-specified wire name; a
future UI may want the computed `control_effectiveness` contribution
(`weight_design*design_score + ...`) instead. That value is derived at read
time from the evaluation ledger (slice 020) and is NOT stored — exposing it
here would require pulling the residual deriver into this read endpoint,
which is scope the issue does not grant. Revisit when the linked-risks rail
is bound and a real user says what number they expect.

## Revisit once in use

- **Re-point slice 041's four frontend placeholders** to these endpoints
  (`evidence-stream-section`, linked-policies / linked-risks / audit-log
  rail). Out of scope for 064 per the issue's "Follow-up" section; slice
  041's decisions log §"Revisit once in use" names the exact seam.
- **Evidence `source` field (decision 3)** — if the frontend wants a
  flattened `source.connector_id` string, flatten it in the wire shape
  rather than passing the whole `provenance` JSONB.
- **`scope_cell` label (decision 4)** — if the UI wants the human-readable
  cell label inline rather than resolving the id client-side, add a join
  (accepting it is then a two-table read, still one query).
- **AC-5 role guard (decision 5)** — when slice-035's DB-backed `user_roles`
  becomes the role source of truth, re-derive the control-read check from
  the resolved role set instead of the credstore flags.
- **`link_weight` (decision 7)** — re-evaluate against what the linked-risks
  rail actually renders; the computed `control_effectiveness` contribution
  may be the number a user expects, at the cost of pulling the residual
  deriver into the read path.

## Confidence summary

| Decision                                      | Confidence |
| --------------------------------------------- | ---------- |
| 1 — control_id UUID param + dual-column reuse | high       |
| 2 — opaque keyset cursor pagination           | high       |
| 3 — evidence `source` -> `provenance`         | medium     |
| 4 — evidence `scope_cell` -> `scope_id`       | high       |
| 5 — handler-level 403 role guard              | medium     |
| 6 — new query file, no schema-list edit       | high       |
| 7 — risks `link_weight` -> `design_score`     | medium     |

The three `medium`-confidence calls (3, 5, 7) are the top of the revisit
list — all three are wire-shape / enforcement-path choices that a real user
iterating against real data may want changed; none is a correctness risk.
