# 571 — HRIS manager-hierarchy evidence: JUDGMENT decisions log

Slice type: JUDGMENT (evidence-kind shape + identity boundary). This file
records the subjective build-time calls for slice 571 — the
sibling-kind-vs-enrichment decision, the PII / over-collection guard mechanism,
the cycle + orphan handling, the SCF anchor choice, and the stable-field
choices. It does NOT block merge; the maintainer iterates post-deployment from
the "Revisit once in use" notes.

Parent: slice 491 (`docs/issues/491-*` + the base HRIS connectors under
`connectors/hris/**` + `connectors/rippling/**` + `connectors/bamboohr/**`).
Slice 491 shipped the Rippling + BambooHR connectors emitting
`hris.worker_lifecycle.v1`; each worker record already carries the worker's
direct `manager_assignment_id` (the opaque manager worker id). This slice derives
the full reporting tree from that field as a first-class evidence surface for
access-review routing.

## D1 — Sibling kind `hris.manager_hierarchy.v1`, NOT enrichment of `hris.worker_lifecycle.v1`

- **Options considered:** (a) enrich the existing per-worker
  `hris.worker_lifecycle.v1` record with derived hierarchy fields (depth,
  orphaned-report flag); (b) a new sibling kind `hris.manager_hierarchy.v1`
  carrying one hierarchy-edge record per worker.
- **Chosen:** (b), the sibling kind — exactly as the slice doc anticipated
  ("likely a new `hris.manager_hierarchy.v1` rather than bloating the per-worker
  lifecycle record").
- **Rationale:**
  1. **Derivation altitude differs.** A lifecycle record is a per-worker FACT the
     source reports directly (status, dates, title). The hierarchy fields
     (`depth`, `orphaned_report`, `cycle_member`) are DERIVED from the whole
     roster — a worker's depth depends on every ancestor's manager edge, and the
     orphaned flag depends on the manager's terminated status. Putting a
     roster-global derivation onto a per-worker source-fact record conflates two
     different evidence altitudes and would force a tree-wide recompute to be
     attributed to a single worker's lifecycle push.
  2. **Independent evaluation + freshness.** Access-review routing consumes the
     hierarchy; deprovisioning consumes the lifecycle. A sibling kind lets the
     evaluator query the reporting tree as its own surface (and apply its own
     freshness/control mapping) without re-deriving it from lifecycle records.
  3. **Clean append-only ledger semantics (invariant #2).** The two kinds get
     distinct idempotency-key prefixes (`hris.worker_lifecycle|...` vs
     `hris.manager_hierarchy|...`) so they never collide in the ledger; a bug in
     hierarchy derivation can never corrupt a lifecycle record.
  4. **Repo precedent.** This mirrors the slice-488→533 (`monitoring.alert_config`
     → `datadog.siem_rule`) and slice-490→555/556 sibling-kind splits: when a
     follow-on surface carries fields the base shape has no slot for, it gets its
     own kind rather than widening the base into a lowest-common-denominator blob.
- **Shape:** one record per worker. Payload = `source_hris`,
  `worker_assignment_id`, optional `manager_assignment_id` (omitted for a tree
  root), `depth` (int; -1 = undefined), `orphaned_report` (bool), `cycle_member`
  (bool). `additionalProperties:false`; `required` = the five non-optional keys.

## D2 — PII / over-collection guard: opaque ids only, structural reflection pin

- **The slice-491 identity boundary is LOAD-BEARING and unchanged.** The HRIS
  holds the most sensitive PII in the customer's stack. The hierarchy surface
  must NEVER carry a manager's (or any worker's) name, email, phone, address, or
  any personal-contact / sensitive-PII detail — only opaque assignment ids.
- **Mechanism = the type system is the guard.** The `hierarchy.Edge` struct has
  NO field — and no place to put a field — for any PII. Its fields are two opaque
  assignment ids (`WorkerAssignmentID`, `ManagerAssignmentID`) and three derived
  primitives (`Depth int`, `OrphanedReport bool`, `CycleMember bool`). A leak
  would be a compile error.
- **Executable assertion.** `TestEdge_HasNoPIIField` reflects over `Edge` and
  fails if any field name matches a banned-PII concept (name/email/phone/address/
  contact/ssn/salary/…), mirroring + extending slice-491's
  `TestRawWorker_HasNoSensitivePIIField`. The record-level guard
  (`TestBuild_PayloadCarriesHierarchyFactsOnly`) asserts the emitted payload
  carries ONLY the allow-listed keys.
- **No new source read.** The hierarchy is derived purely from the
  `manager_assignment_id` the roster ALREADY carries — the connector reads
  nothing new from the HRIS, requests no new scope, and the existing read-only
  source credential is reused unchanged (the `permissions` subcommand documents
  that the hierarchy is derived from the same read — no extra scope). This is the
  strongest form of the over-collection guard: there is no new collection at all.

## D3 — Cycle handling: detect + flag + terminate (the bounded guarantee)

- **Requirement:** a manager cycle in `manager_assignment_id` (A→B→A, a
  self-manager A→A, or a longer ring) MUST terminate, never loop.
- **Mechanism:** `detectCycles` is an iterative three-colour (white/grey/black)
  walk over the manager chains. A grey node re-encountered on the current path is
  a cycle; every node from that point on the path is flagged `cycle_member`. Each
  node is coloured at most twice, so the whole pass is O(N) and terminates on ANY
  input. `depthOf` additionally caps its walk at `len(nodes)` iterations as a
  belt-and-suspenders bound.
- **Semantics:** a cycle member gets `cycle_member=true` and `depth=-1`
  (undefined — no well-defined approver chain). A worker BELOW a cycle (reports
  into it but is not on the ring) is NOT flagged a cycle member but also gets
  `depth=-1` because its chain never reaches a root.
- **Tests:** `TestBuild_TwoCycleTerminates`, `TestBuild_SelfManagerCycleTerminates`,
  `TestBuild_LongCycleTerminates` each run `Build` in a goroutine with a 2s
  watchdog — a non-terminating build fails the test rather than hanging the suite.
  `TestBuild_ChainIntoCycleHasUndefinedDepth` pins the below-the-cycle semantics.

## D4 — Orphaned-report handling (terminated or absent manager)

- A worker whose `manager_assignment_id` is set but resolves to a manager that is
  EITHER absent from the roster OR present-but-`terminated` gets
  `orphaned_report=true` — the access-review approver chain is broken and the
  review cannot auto-route until repaired.
- An active manager → not orphaned; a tree root (empty manager) → not orphaned
  (`depth=0`).
- **Tests:** `TestBuild_OrphanedWhenManagerTerminated` (the terminated-manager
  case the slice calls out explicitly), `TestBuild_OrphanedWhenManagerAbsent`,
  `TestBuild_ActiveManagerIsNotOrphaned`.

## D5 — SCF anchors: `IAC-22`, `IAC-09`

- **Chosen:** `x-default-scf-anchors = ["IAC-22", "IAC-09"]` — the
  periodic-access-review / least-privilege anchors.
- **Rationale:** the reporting tree exists PURELY to route access reviews to the
  right approver chain and to surface orphaned reports. That is the IAC-22
  (periodic review of accounts/access) + IAC-09 (identity management) signal. The
  lifecycle kind additionally carries `HRS-04` (personnel management — the
  joiner/mover/leaver HR fact); the hierarchy kind deliberately DROPS `HRS-04`
  because it is not a personnel-lifecycle fact, it is an access-routing fact. The
  anchor set is a strict subset of the lifecycle kind's IAC-\* anchors, reflecting
  the narrower purpose. The operator can override per run via
  `--hierarchy-control`.

## D6 — Stable-field choices (idempotency, actor_id, scope, observed_at, interval)

- **Idempotency key:** `idem.ManagerHierarchyKey` = `sha256("hris.manager_hierarchy|<hris>/<worker_id>|<hour>")`
  — same (hris, worker, hour) identity as the lifecycle key, distinct kind prefix
  (D1.3). One hierarchy edge per worker per hour collapses same-run re-pushes.
- **actor_id:** `connector:<vendor>:hierarchy@<version>` — a distinct service
  segment (`hierarchy`) from the lifecycle records' `workers`, per the
  cross-connector `connector:<vendor>:<service>@<version>` convention, so the two
  surfaces are distinguishable in source attribution.
- **scope:** `service` + `environment` (matching the lifecycle records).
- **observed_at:** hour-truncated, and SHARED across the whole run — every edge
  record in one tree carries the roster read's single `observed_at` (taken from
  the normalized roster), so a tree is internally consistent point-in-time.
- **profiles_supported:** `[pull]` (unchanged — the hierarchy is derived from the
  same pull read). Platform-side wire stays push (invariant #3).
- **interval:** unchanged honest `PullInterval` (24h operator-scheduled, NOT
  continuous monitoring).
- **Result:** `RESULT_INCONCLUSIVE` — the connector reports descriptive
  reporting-tree facts; the evaluator owns the routing decision.

## Detection-tier classification

- `detection_tier_actual`: `none` — no defect surfaced during the slice. The only
  build-time corrections were a gofmt alignment + a staticcheck QF1006 lint nit
  (caught by `golangci-lint` locally before push) and an unused struct field
  removed; neither is a behavioural bug.
- `detection_tier_target`: `none`.

## Revisit once in use

- **Multi-page roster.** The hierarchy is derived from the same bounded
  first-page roster read slice 491 ships; cursor pagination for a large directory
  is the slice-491 follow-on (threat-model D) — when it lands, the tree simply
  derives from the larger roster with no change here.
- **Transitive approver chain materialization.** v0 emits one DIRECT edge per
  worker (worker → direct manager) + derived depth/orphan/cycle flags. If
  access-review routing later wants the full ancestor chain materialized per
  record (rather than walking edges at query time), that is an additive field on
  this kind — revisit when the routing consumer's query shape is known.
