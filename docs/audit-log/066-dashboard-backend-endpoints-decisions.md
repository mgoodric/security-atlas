# 066 — Dashboard backend read endpoints — decisions log

Slice 066 is `Type: AFK` in its frontmatter — its acceptance criteria are
mechanically verifiable. But the slice surfaced five genuine build-time
judgment calls: the issue's AC-2 names a "slice-015 evidence-ingest event
archive" that does not exist as a dedicated table; AC-3 asks
`residual_score` to "become a sortable scalar" without prescribing how;
AC-1's framework-posture aggregation path was unspecified; AC-5's
"dashboard/program-read role check" had no single named referent; and
sqlc v1.31's analyzer forced a SQL-shape decision on the upcoming rollup.
This log records them in the JUDGMENT-slice format so the maintainer can
re-evaluate the calls once the dashboard is in real use against a real
platform.

## Decisions made

### 1. Activity feed reads `admin_audit_log_v`, filtered to the evidence branch

**Options considered:**

- **(A) Return to the caller** — AC-2 names a "slice-015 evidence-ingest
  event archive"; a grep of `migrations/sql/` finds no `event_archive` /
  `activity` / `nats`-named table. Surface it as a blocker.
- **(B) Add a new event-archive table + a migration** — AC P0 explicitly
  forbids a migration in this slice.
- **(C) Read the slice-062 `admin_audit_log_v` view, filtered to
  `source_table = 'evidence_audit_log'`.**

**Chosen: (C).**

**Rationale.** Slice 015 (NATS JetStream ingestion) shipped as a
"substrate swap" with no migration — it did not create an event-archive
table. But the durable, append-only archive of every evidence-ingest
event already exists: `evidence_audit_log` (migration `_004`, slice
013), which records every push attempt — accepted, deduplicated, or
rejected — keyed by `received_at` with `decision`, `evidence_kind`,
`record_id`, `credential_id`. Slice 062 then built `admin_audit_log_v`
(migration `_022`), a UNION-ALL view that projects `evidence_audit_log`
(and six sibling audit-log tables) to a uniform row shape that is
**exactly** AC-2's contract: `{ts, event_type, actor, resource_type,
resource_id, summary}`. The view is RLS-aware (not `SECURITY DEFINER`,
no `BYPASSRLS`; each source table's `tenant_read` policy fires under the
caller's GUC) and append-only by construction. `GET /v1/activity` reads
that view filtered to the `evidence_audit_log` branch — which IS the
"slice-015 evidence-ingest event archive" the issue means, just named
for the table that has held the data since slice 013. Option (B)
violates the AC P0 no-migration rule; option (A) treats a
fully-shipped, RLS-tested read surface as a novel blocker. The
slice-062 `ListAdminAuditLog` query is the keyset-pagination template;
this slice's `ListEvidenceActivity` is the same shape with the
evidence-branch filter baked in.

**Confidence: high.** The view's wire shape matches AC-2 column-for-
column; the evidence branch's RLS + append-only properties are
documented in migration `_022` and verified by slice 062's integration
test.

### 2. `residual_score` sorted via JSONB extraction in Go — no migration

**Options considered:**

- **(A) Add a generated column / index** for a sortable residual scalar
  — the anti-criterion's named escape hatch ("if `residual_score`
  genuinely cannot be sorted without a generated column or index").
- **(B) Extract the scalar in SQL** (`(residual_score->>'likelihood')::
numeric`) and `ORDER BY` it — extends the `ListRisks` SQL.
- **(C) Extract the scalar in Go** after the existing static `ListRisks`
  query and sort in memory.

**Chosen: (C).**

**Rationale.** `residual_score` is the opaque `{likelihood, impact}`
JSONB (canvas §2.2, the 5x5 grid). The "sortable scalar" the issue asks
for is the residual **magnitude** = `likelihood × impact` — the canvas
§7.5 "residual × age" ranking. The escape hatch in option (A) does not
trigger: the score IS sortable without a schema change. The
`HeatmapBuckets` query (slice 019) already proves SQL can extract and
operate on JSONB-number fields. But the established `internal/risk`
design (`Store.List`) deliberately keeps `ListRisks` SQL static and
applies filters in Go — "sqlc's static typing makes optional WHERE
clauses noisy; the row count is bounded by tenant-size anyway" (the
`ListRisks` query comment). The residual/age sort is the same kind of
post-query shaping, so it lives in the same place: a new
`ListSort` field on `risk.ListFilter`, an in-Go decorate-sort. This
keeps the `ListRisks` SQL — which slice 031 (monthly board brief) also
reads in parallel — completely untouched, eliminating a merge conflict.
A risk whose `residual_score` has no numeric `likelihood`/`impact`
sorts as magnitude 0 (it ranks below any scored risk) rather than
erroring the whole list — the dashboard ranking degrades gracefully for
a malformed score.

**Confidence: high.** No migration needed; the `ListRisks` signature
and filters are unchanged (AC-3 "additive"); the in-Go design matches
the established `Store.List` pattern.

### 3. Framework posture aggregates through the SCF anchor spine

**Options considered:**

- **(A) A per-framework `controls.framework_id` link** — there is no
  such column; controls are not bound to a framework directly (that
  would violate constitutional invariant #1, "one control, N framework
  satisfactions").
- **(B) Aggregate `internal/api/ucfcoverage`'s per-control coverage in
  Go** — N round-trips, one per framework version.
- **(C) One SQL query that walks the SCF anchor spine:**
  `framework_versions → framework_requirements → fw_to_scf_edges →
scf_anchors ← controls.scf_anchor_id`.

**Chosen: (C).**

**Rationale.** Invariant #1 means a control "covers" a framework
requirement only transitively, through a shared SCF anchor — exactly
the path `internal/api/ucfcoverage` walks per-control. AC-1 asks for
the framework-version grain, so this slice walks the same spine but
aggregates: a requirement is covered when at least one active
(`superseded_by IS NULL`) tenant control is anchored on an SCF anchor
that a non-`no_relationship` STRM edge connects to that requirement;
`coverage_pct` = covered / total requirements. `freshness_composite`
left-joins the slice-016 `evidence_freshness` read model over the
covering controls (a control with no freshness row counts as stale —
it is, definitionally, not currently fresh). `trend_delta_90d`
reconstructs coverage as it stood 90 days ago from the slice-012
`control_evaluations` ledger (a covering control counted then iff its
latest evaluation on/before the cutoff was a `pass`) and subtracts.
The whole thing is one statement with CTEs — no N+1 (anti-criterion
P0). Option (A) does not exist and would violate invariant #1; option
(B) is the N+1 the anti-criterion forbids.

**Confidence: high.** The spine is the same one `ucfcoverage` (slice 008) walks; the integration test verifies coverage, freshness, and
trend numbers against a deterministic fixture across two framework
versions.

### 4. Program-read authz reuses the slice-064 control-read role set

**Options considered:**

- **(A) Invent a new `dashboard_read` / `program_read` role** — adds a
  role to the RBAC vocabulary mid-slice with no schema or OPA support
  for it.
- **(B) Reuse the slice-064 `control-read` role derivation** (admin +
  grc_engineer + control_owner).

**Chosen: (B).**

**Rationale.** AC-5 says "read authz reuses the existing
dashboard/program-read role check" but no single function by that name
exists on main. The closest established referent is slice 064's
`controldetail.requireControlRead` — the handler-level defense-in-depth
guard for the control-detail view, the operator/auditor UI. The program
dashboard (slice 040) is the **same audience**: the operator's morning
home screen, which links to those very control-detail views. Reusing
the identical derivation (`IsAdmin || IsApprover ||
len(OwnerRoles) > 0`) keeps the two backend-for-frontend slices
coherent — a credential that can read a control detail can read the
program dashboard that links to it — without enlarging the RBAC
vocabulary. The slice-035 OPA middleware remains the primary gate in
production; this handler-level guard is its testable defense-in-depth
twin, exactly as slices 059/062/064 do.

**Confidence: medium.** Binding the program dashboard to the
control-read role set is unambiguously safe (it is strictly an
operator/auditor surface). The judgment a maintainer might revisit:
whether a future read-only "viewer" or "board-observer" role should be
able to see the program dashboard but not control internals — at which
point program-read and control-read diverge and each needs its own
derivation. Until that role exists, one shared derivation is correct.
This is the same "revisit once slice-035's DB-backed `user_roles`
becomes the role source of truth" item slice 064's decisions log D5
records.

### 5. Upcoming-rollup keyset predicate pushed into each UNION branch

**Options considered:**

- **(A) An outer `WHERE` over the UNION-ALL result** (cleanest to read).
- **(B) The keyset + category predicates pushed into each of the four
  UNION branches.**

**Chosen: (B).**

**Rationale.** sqlc v1.31's query analyzer cannot resolve column names
in an outer `WHERE` / `ORDER BY` over a UNION-ALL CTE (or derived
table) whose columns are all computed expressions — it has no
base-column lineage to bind `due_date` / `category` / `resource_id` to,
and fails generation with `column "due_date" does not exist` or `table
alias "rollup" does not exist`. Three forms of option (A) were tried
(inline derived table, named CTE, `rollup.`-qualified refs) — all
failed. Pushing the decomposed keyset predicate + the optional category
filter into each branch lets sqlc analyze each branch against real
tables. sqlc deduplicates the `sqlc.arg()` named params, so
`cursor_date` / `cursor_id` / `category_filter` / `row_limit` each
appear exactly once in the generated `Params` struct. It is verbose —
the four-times-repeated predicate, and the vendor branch repeats its
`CASE ... cadence ... INTERVAL` expression in both the projection and
the keyset comparison — but it is still ONE statement / ONE `UNION ALL`
(anti-criterion P0: no N+1), and the verbosity is a tool constraint,
not a design choice. It is documented inline in
`internal/db/queries/dashboard.sql`.

**Confidence: high.** The sqlc limitation is reproducible (it failed
three ways before this form generated); the resulting query is one
round-trip and the integration test verifies the merge, the date sort,
the category filter, and keyset pagination.

## Revisit once in use

- **Re-point slice 040's four frontend placeholders** (out of scope per
  the issue's "Follow-up" section) — `framework-posture-panel`,
  `activity-feed-panel`, `top-risks-panel` sort, `upcoming-panel` — to
  these endpoints. A small mechanical `web/**` change; slice 040's
  decisions log identifies the seams. Slice 040's AC-2/3/5/6 flip
  PARTIAL → PASS once done.
- **FrameworkPosture CTE fan-out** (decision 3) — the final query
  left-joins `control_freshness` on `framework_version_id` only (not
  `requirement_id`), so the row set fans out to roughly R×C
  (requirements × covering controls) per version before the
  `COUNT(DISTINCT ...)` collapses it. The counts are **correct** —
  `DISTINCT` removes the fan-out duplication — but the intermediate row
  set is wasteful. At v1 cardinality (canvas §1.1, a solo security lead
  with a handful of frameworks) this is negligible. If a tenant ever
  carries many frameworks × many controls, restructure
  `control_freshness` into a pre-aggregated per-version count CTE rather
  than a per-control join. Not a correctness risk — a scale-only
  revisit.
- **Program-read vs control-read role divergence** (decision 4) — when a
  read-only "viewer" or "board-observer" role enters the RBAC
  vocabulary, `requireProgramRead` and `requireControlRead` will need
  separate derivations. Until then the shared derivation is correct.
- **Activity feed scope** (decision 1) — `GET /v1/activity` today
  surfaces only the `evidence_audit_log` branch of `admin_audit_log_v`,
  matching slice 040's activity-feed-panel "Evidence" filter chip. If
  the panel's other chips (Controls, Approvals) are wired later, the
  endpoint can widen to additional `source_table` branches behind a
  `?source=` filter — the view already carries them.

## Confidence summary

| Decision                                           | Confidence |
| -------------------------------------------------- | ---------- |
| 1 — activity feed reads admin_audit_log_v branch   | high       |
| 2 — residual sorted via JSONB extraction in Go     | high       |
| 3 — framework posture through the SCF anchor spine | high       |
| 4 — program-read reuses control-read role set      | medium     |
| 5 — keyset predicate pushed into each UNION branch | high       |

The one `medium`-confidence call (4) is the top of the revisit list — it
is a role-vocabulary judgment a maintainer may want changed once a
read-only viewer role exists; it is not a correctness or security risk
(the shared derivation is strictly an operator/auditor surface). No AC
lands PARTIAL — all eight are fully satisfied; the only deferred work is
the documented out-of-scope frontend re-pointing.
