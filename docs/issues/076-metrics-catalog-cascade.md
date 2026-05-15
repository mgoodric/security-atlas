# 076 — Metrics catalog + cascade + observation store

**Cluster:** Catalog
**Estimate:** 3-4d
**Type:** JUDGMENT

## Narrative

A solo security-program owner measures the program to communicate it — to the board, to the team, to themselves. Today the platform exposes _operational_ surfaces (control state, evidence freshness, drift) but has no _metrics_ surface: no curated list of "the numbers that matter," no cascade structure that says how a board KPI rolls up from program- and team-level inputs, no observation history, no manual-entry path for the inputs that the platform can't compute itself.

This slice lands that backbone. It is **not** a dashboard slice (that's a follow-on, slice 078). It is a **catalog + schema + observation store + minimal compute** slice that:

1. Defines a curated **metrics catalog** as YAML content in `catalogs/metrics/` — analogous to the SCF catalog (slice 006) for controls, but for measurement. ~40 metrics, opinionated, written for the v1 persona (solo security leader at a 50-150-person security-product startup).
2. Defines the **cascade graph**: each metric carries a `level` (board / program / team) and a set of parent edges so a board metric like _"Audit readiness"_ explicitly composes from program metrics _"Framework coverage"_ + _"Evidence freshness"_ + _"AuditPeriod currency"_, which in turn compose from team metrics _"Open findings by control owner"_ etc.
3. Lands the **data model** — five tables (`metrics_catalog`, `metric_cascade_edges`, `metric_observations`, `metric_targets`, `metric_inputs`) with the four-policy RLS pattern (slice 014/017/018 precedent) for everything except `metrics_catalog` (singleton-tenant-agnostic seed table per the slice 068 pattern).
4. Ships the **read API**: `GET /v1/metrics`, `GET /v1/metrics/{id}`, `GET /v1/metrics/cascade?level={board,program,team}`, `GET /v1/metrics/{id}/observations?since=...&until=...`.
5. Ships the **manual-entry write API** for the catalog metrics that require external input (NIST FIRE drill outcome, board-attestation completed, etc.): `POST /v1/metrics/{id}/inputs` with the four-policy RLS pattern.
6. Ships a **starter computation engine** that computes ~8 of the ~40 catalog metrics from existing data on a 15-minute cron:
   - _Program effectiveness %_ — rolled-up control pass rate, weighted by risk (slices 012 + 016 + 019)
   - _Open risk financial exposure_ — sum of ALE for risks where `treatment != accept` (slice 019)
   - _Audit readiness score_ — % of in-scope frameworks with current SSP + AuditPeriod freshness < 90d (slices 028 + 030)
   - _Evidence freshness %_ — % of controls with evidence ≤30d old (slice 016)
   - _Critical-findings SLA_ — % of P0/P1 findings closed within target time (slice 027 + slice 027's `audit_notes`)
   - _Policy attestation rate_ — % of staff with current-version acknowledgment (slice 023)
   - _Vendor risk concentration_ — top-5 vendors by `data_sensitivity × access_scope` product (slice 024)
   - _Exception expiration runway_ — count of exceptions expiring in next 30 days (slice 021)
7. Leaves ~32 catalog metrics defined but with `compute_strategy: manual_input` — these are the user's tracked metrics that require periodic manual entry (or future integration). Examples: _Mean time to detect_ (requires SIEM/integration), _Mean time to remediate_ (requires ticketing integration), _Board attendance %_ (manual), _Risk-treatment plan completeness rate_ (manual quarterly review), _Patch-window adherence %_ (requires endpoint mgmt integration), _Phishing simulation click-through %_ (manual training-platform export), _Backup restore validation %_ (manual quarterly drill).

**Why the cascade is load-bearing:** a board-level KPI without its composing inputs is just a number. The cascade makes the KPI _interrogable_: when "Audit readiness" dips, the owner clicks down to see WHICH framework, WHICH control surface, WHICH evidence-freshness bucket is driving the change. The data model encodes that relationship as first-class edges, not as ad-hoc dashboard queries — so future dashboard slices (slice 078), board-pack templating slices (extensions to 031/032), and the OSCAL-export pipeline (slice 030) can all read the same cascade.

**Out of scope for this slice (becomes follow-on slice 078):**

- Frontend metrics dashboard / cascade-tree visualization
- Alerting / target-threshold tripping
- Anomaly detection on observation series
- Per-tenant catalog customization (extending or replacing metrics)
- Integration adapters for the ~32 manual metrics (each integration is its own connector slice, slice-044/045/046 shape)
- Time-windowed cascades (rolling 30/60/90; v1 observation reads are point-in-time and historical-by-since-until only)

## Acceptance criteria

### Data model

- [ ] AC-1: Migration `migrations/sql/20260516000001_metrics_catalog.sql` adds:

  - `metrics_catalog` (singleton-tenant-agnostic): `id text PRIMARY KEY`, `level text NOT NULL CHECK (level IN ('board','program','team'))`, `category text NOT NULL`, `name text NOT NULL`, `description text NOT NULL`, `unit text NOT NULL` (e.g., `percent`, `dollars_ale`, `count`, `days`), `cadence text NOT NULL CHECK (cadence IN ('realtime','daily','weekly','monthly','quarterly'))`, `compute_strategy text NOT NULL CHECK (compute_strategy IN ('computed','manual_input','external_integration'))`, `compute_evaluator text NULL` (the Go evaluator function name for `compute_strategy='computed'`), `source_slices text[] NOT NULL` (slice numbers the metric reads from), `created_at`, `updated_at`. RLS: `tenant_id IS NULL OR current_tenant_matches(tenant_id) IS TRUE` (slice 068 pattern; the catalog is platform-seeded but tenants can extend it in a future slice).
  - `metric_cascade_edges` (singleton-tenant-agnostic): `parent_id text NOT NULL REFERENCES metrics_catalog(id)`, `child_id text NOT NULL REFERENCES metrics_catalog(id)`, `weight numeric(5,4) NOT NULL DEFAULT 1.0 CHECK (weight > 0 AND weight <= 1)`, `created_at`, `PRIMARY KEY (parent_id, child_id)`. A CHECK constraint prevents self-loops; a trigger or app-layer guard prevents cycles (a metric can't be both ancestor and descendant of itself — recursive CTE traversal must terminate).
  - `metric_observations` (tenant-scoped, append-only): `id uuid PRIMARY KEY DEFAULT gen_random_uuid()`, `tenant_id uuid NOT NULL`, `metric_id text NOT NULL REFERENCES metrics_catalog(id)`, `observed_at timestamptz NOT NULL`, `numeric_value numeric(20,6) NOT NULL`, `dimensions jsonb NOT NULL DEFAULT '{}'` (e.g., `{"framework":"soc2"}` for per-framework breakdowns), `source text NOT NULL` (e.g., `evaluator:program_effectiveness`, `manual:user-uuid`), `created_at`. RLS: four-policy pattern (read/write only; append-only ⇒ no update/delete policies per the slice 013/036 convention).
  - `metric_targets` (tenant-scoped): `id uuid PRIMARY KEY`, `tenant_id uuid NOT NULL`, `metric_id text NOT NULL REFERENCES metrics_catalog(id)`, `target_value numeric(20,6) NULL`, `warning_threshold numeric(20,6) NULL`, `critical_threshold numeric(20,6) NULL`, `direction text NOT NULL CHECK (direction IN ('higher_is_better','lower_is_better','target_is_better'))`, `owner_user_id uuid NULL`, `notes text NULL`, `created_at`, `updated_at`, `UNIQUE (tenant_id, metric_id)`. RLS: four-policy.
  - `metric_inputs` (tenant-scoped, append-only): `id uuid PRIMARY KEY`, `tenant_id uuid NOT NULL`, `metric_id text NOT NULL REFERENCES metrics_catalog(id)`, `input_at timestamptz NOT NULL`, `numeric_value numeric(20,6) NOT NULL`, `dimensions jsonb NOT NULL DEFAULT '{}'`, `entered_by_user_id uuid NOT NULL`, `notes text NULL`, `created_at`. RLS: read/write only. A trigger on insert appends a matching row to `metric_observations` so the catalog metric's series is unified (a single read of `metric_observations` returns both computed and manual values).
  - Migration is reversible (DOWN drops in reverse FK order).

- [ ] AC-2: `internal/db/queries/metrics.sql` defines the sqlc queries (List / Get / GetCascade / ListObservations / InsertObservation / UpsertTarget / GetTarget / InsertInput). Recursive CTE for `GetCascade` returns the full subtree under a level filter.

### Catalog content

- [ ] AC-3: `catalogs/metrics/` directory holds one YAML file per metric (`<level>-<category>-<slug>.yaml`), each with the catalog-row fields plus a `parents` list (cascade edges) and a `notes` block (the maintainer's rationale for why this metric matters, what NOT to measure, what to revisit). ~40 metrics total across the three levels:
  - **Board level (5-8 metrics)**: Program effectiveness, Open risk financial exposure, Audit readiness, Critical SLA, Investment-vs-coverage, Customer-trust scorecard (manual), Regulatory-exposure score (manual).
  - **Program level (15-20 metrics)**: Per-framework coverage, Evidence freshness %, Control passing rate (uncovered / partial / passing), Open findings by severity, MTTR by severity (manual), Risk treatment progress, Exception runway, Policy attestation rate, Vendor risk concentration, Vendor reviews due, Top-N risks by residual, Walkthrough completion rate (period-scoped), Sample completion rate (period-scoped), SSP currency by framework.
  - **Team level (10-15 metrics)**: Open findings by control owner, Evidence drift events 7d, Stale SCF anchor mappings (manual confirm cadence), Patch-window adherence (external integration), Phishing simulation click-through (manual), Backup restore validation (manual quarterly), Incident response readiness drill outcome (manual), Tabletop exercise outcomes (manual), Hire-onboarding completion rate (manual), Offboarding completion rate (manual), Quarterly access review completion rate, Security training completion rate (manual integration).
- [ ] AC-4: `internal/catalog/metrics/seed.go` loads every YAML file from `catalogs/metrics/` at boot and upserts into `metrics_catalog` + `metric_cascade_edges`. Idempotent: re-running on an unchanged catalog produces zero diffs. Validation: every parent reference resolves; no cycles (recursive-CTE check at seed time); every `compute_strategy='computed'` metric's `compute_evaluator` matches a registered evaluator function name.

### Read API

- [ ] AC-5: `GET /v1/metrics?level={board,program,team}&category=...` returns the catalog filtered by level + category. Public-ish read (requires auth but no tenant context beyond row visibility; catalog itself is platform-shared).
- [ ] AC-6: `GET /v1/metrics/{id}` returns one metric's full definition + its parents + its children (one level only — full cascade traversal is `/cascade`).
- [ ] AC-7: `GET /v1/metrics/cascade?level=board&depth=N` returns the cascade tree rooted at every board-level metric, walking `N` levels deep (default `depth=3`). Recursive-CTE query in sqlc; returns a flat list of `(metric_id, parent_id, depth)` rows + a hint header `X-Cascade-Truncated: true` if a deeper subtree exists.
- [ ] AC-8: `GET /v1/metrics/{id}/observations?since=ISO8601&until=ISO8601&dimensions=k:v,k:v` returns observation series for one metric, tenant-RLS-scoped. Default window: 90d back from now. Pagination via `?cursor=...&limit=...` (keyset paginator pattern; slice 067 precedent).
- [ ] AC-9: `GET /v1/metrics/{id}/target` returns the tenant's target row for that metric (or 404 if unset). `PUT /v1/metrics/{id}/target` upserts.

### Manual-entry write API

- [ ] AC-10: `POST /v1/metrics/{id}/inputs` accepts JSON `{numeric_value, observed_at?, dimensions?, notes?}`. The handler verifies the catalog row's `compute_strategy = 'manual_input'` (returns 409 if the metric is `computed` — that's a programmer error to write to). On insert, the trigger replicates to `metric_observations` so reads of the observation series include manual inputs uniformly.
- [ ] AC-11: `POST /v1/metrics/{id}/inputs` requires `metric_admin` role (a new RBAC role in slice 035's OPA Rego policies; or, in the interim, `admin` role until 035 is extended — engineer decides + records in decisions log).

### Starter computation engine

- [ ] AC-12: `internal/metrics/eval/` package houses one Go function per computed catalog metric (~8 functions). Each takes a `context.Context` + `tenant_id` and returns a `(numeric_value, dimensions, error)`. Pure functions over the existing primitives.
- [ ] AC-13: `cmd/atlas`'s background-jobs registry gains a `metrics_evaluator` job that runs every 15 minutes, iterates each tenant × each computed metric, invokes the corresponding evaluator function, and persists the result to `metric_observations` with `source = "evaluator:<name>"`. Failures on one metric do NOT abort the run for other metrics (per-metric try/log/continue); errors are logged + surfaced via a new `/v1/metrics/_internal/health` endpoint (admin-only).
- [ ] AC-14: Each of the 8 starter evaluators has an integration test (`internal/metrics/eval/<name>_integration_test.go`) that seeds a known-state tenant via the slice-002 helpers, runs the evaluator, asserts the returned value. Real DB; no mocks.

### Observability + audit

- [ ] AC-15: Every observation insert (computed OR manual) writes to OTEL with a `metric.id`, `metric.level`, `metric.numeric_value`, `tenant_id` attribute set so the existing observability bundle (slice 037 docker-compose Grafana/Tempo) can graph the platform's own metric-generation flow.
- [ ] AC-16: `metric_inputs` is append-only (no update or delete policies under FORCE RLS) so manual entries are auditable end-to-end. `decision_audit_log` (slice 035) records every target upsert + every manual input as an audit-relevant event.

### Documentation

- [ ] AC-17: `docs-site/docs/metrics.md` ships in the mkdocs site (slice 058) introducing the catalog: what it is, the cascade structure, how to interpret cadence + compute strategy, how to add a manual input, how to set a target. Includes a worked example: "How _Audit readiness_ rolls up from program + team metrics, and what to do when it dips."
- [ ] AC-18: A reference table at `docs-site/docs/metrics-reference.md` auto-generated from `catalogs/metrics/*.yaml` at docs-build time (a `just metrics-reference` recipe; integrated into `mkdocs build --strict`). Shows every metric's full definition row plus the cascade graph rendered as a Mermaid diagram.
- [ ] AC-19: README.md "Self-hosting" section gets a one-paragraph "Measuring your program" callout linking to the metrics docs page (mirrors the slice-072 + slice-073 README callout pattern).

### Quality

- [ ] AC-20: A `decisions log` for this slice at `docs/audit-log/076-metrics-catalog-cascade-decisions.md` records the JUDGMENT calls, particularly: (1) which ~40 metrics made the cut and which were rejected (and why — there's a galaxy of "metric ideas;" the rejection criteria are at least as important as the selection criteria); (2) cascade edge weights (the `weight numeric(5,4)` field is for future weighted-rollup — v1 hardcodes 1.0 and notes the deferred decision); (3) the 8 starter-evaluator choices vs the 32 manual ones (selection criteria + future-integration-target list); (4) the `metric_admin` role question (extend slice 035 in this slice vs defer to a separate role-extension slice); (5) the recursive-CTE depth limit (AC-7's `depth=3` default — why not unlimited; what failure mode does the limit prevent).
- [ ] AC-21: Pre-commit clean. CI green. New CI checks for migration round-trip + sqlc regen + the 8 evaluator integration tests.

## Constitutional invariants honored

- **Invariant 1 (One control, N framework satisfactions)**: metrics that aggregate across frameworks (e.g., per-framework coverage) read the existing SCF anchor + FrameworkScope graph (slices 006 + 018), not duplicated framework-specific stores. The "Audit readiness" cascade explicitly composes per-framework children, surfacing the N-framework-from-one-anchor model.
- **Invariant 6 (Tenant isolation at DB layer)**: every observation, target, and input table carries `tenant_id` and the four-policy RLS pattern. `metrics_catalog` + `metric_cascade_edges` are intentionally tenant-agnostic (singleton platform catalog) using the slice 068 `tenant_id IS NULL OR ...` pattern; tenant extension is a future slice.
- **Invariant 9 (Manual evidence is first-class)**: the catalog's manual-input metrics get the SAME observation-series read shape as computed metrics. The `metric_observations` table is the unified read surface; `metric_inputs` is the audit-trail source for the manual subset. Consumers (board pack, dashboard view) don't need to special-case manual vs computed.
- **AI-assist boundary**: the catalog defines metrics; it does NOT auto-narrate them. Templated board-pack prose (slices 031/032) reads metric values, but the prose is templated, not LLM-generated. v1 metric stories are written by humans; the platform supplies the numbers.

## Canvas references

- `Plans/canvas/07-metrics.md` — the primary canvas section this slice operationalizes (board reporting + KPIs first-class)
- `Plans/canvas/06-risk.md` — Risk register linkage informs the "Open risk financial exposure" + "Risk treatment progress" metrics
- `Plans/canvas/08-audit-workflow.md` — AuditPeriod freezing (slice 028) is the data source for "Audit readiness" + "AuditPeriod currency"
- `Plans/canvas/04-evidence-engine.md` — Evidence ledger + freshness (slices 013 + 016) is the data source for "Evidence freshness %"

## Dependencies

- **006** (SCF catalog importer) — pattern for YAML-seeded catalogs with idempotent boot-time upsert
- **012** (control state evaluation) — source for "Program effectiveness %" and per-control metrics
- **013 + 016** (evidence ledger + freshness/drift) — sources for "Evidence freshness %", "Open finding drift" metrics
- **017 + 018** (scope + FrameworkScope) — sources for per-framework cascades
- **019** (Risk register CRUD) — source for "Open risk financial exposure", "Risk treatment progress"
- **021** (Exception/waiver workflow) — source for "Exception runway"
- **023** (Policy acknowledgment) — source for "Policy attestation rate"
- **024** (Vendor lite module) — source for "Vendor risk concentration"
- **027** (Walkthrough recording) — source for "Walkthrough completion rate"
- **028** (AuditPeriod freezing) — source for "Audit readiness" + "AuditPeriod currency"
- **035** (RBAC + OPA) — `metric_admin` role candidate; engineer's judgment call on whether to extend in this slice
- **058** (user docs scaffold) — metrics docs page goes in the mkdocs site
- **068** (schema-registry pattern for singleton tables) — `metrics_catalog`'s `tenant_id IS NULL` RLS shape

## Anti-criteria (P0 — block merge)

- **P0-A1**: Does NOT ship a dashboard / cascade-tree visualization. That's slice 078. This slice ships the BACKBONE; rendering is downstream.
- **P0-A2**: Does NOT define a metric that has no clear consumer in v1 ("vanity metrics"). Every metric in the catalog answers a specific question the v1 persona asks: of themselves, their board, their team, their auditor. If a metric is in the catalog "because it might be useful someday," it's rejected — record the rejection in the decisions log.
- **P0-A3**: Does NOT auto-generate metric narrative text via LLM. Templated rollup is fine (e.g., "Program effectiveness was X% this quarter, down N pp from last quarter" — formula); narrative interpretation ("which is concerning because …") is human-authored. Slice 031/032's board-pack templated-only constraint continues.
- **P0-A4**: Does NOT mix `metric_inputs` and `metric_observations` semantics. `metric_inputs` is the audit trail (who entered what, when, why); `metric_observations` is the read-optimized series (the trigger on `metric_inputs` insert appends to `metric_observations`). They are distinct tables for a reason; don't collapse them.
- **P0-A5**: Does NOT add cycles to the cascade. The seeder explicitly detects + rejects cycles at boot; the API explicitly truncates traversal at `depth=3` to prevent runaway recursive CTEs. Cycles in the metric cascade are a content bug, not a runtime feature.
- **P0-A6**: Does NOT couple computed-metric evaluators to specific tenants in the function signatures. Each evaluator takes `(ctx, tenant_id)` — never `(ctx, tenant_a_specific_struct)`. The signature is uniform; the data access is RLS-scoped via `ctx`-injected tenant.
- **P0-A7**: Does NOT batch with slice 031/032/043 (board-reporting slices) or slice 040/056 (dashboards). Those slices CONSUME the metrics catalog; co-batching with their owner-slice 076 invites cross-coupling. Solo-by-design at the slice level (this and any board-pack-extension or dashboard-extension are separate slices).
- **P0-A8**: Does NOT define a `metric_admin` role without coordinating with slice 035's existing RBAC matrix. If the engineer's grill concludes that extending 035 belongs in a separate slice (clean separation of concerns), this slice uses the existing `admin` role as a placeholder + records the deferred role-decision in the decisions log + files a follow-on spillover slice for the role extension.

## Skill mix (3–5)

- **`engineering-advanced-skills:codebase-onboarding`** — the metrics catalog IS a codified onboarding path for understanding the program; same writing discipline as slice 058's framework-setup page
- `database-designer` — the five-table data model is the load-bearing part; getting `metric_inputs` → `metric_observations` trigger semantics right (and the FK + RLS interactions across the singleton vs tenant-scoped tables) is the dominant correctness call
- `security-review` — the manual-entry API + `metric_admin` role question touches credential boundary; the singleton catalog's RLS pattern needs verification against slice 068's precedent
- `simplify` — the docs page (AC-17) and the catalog YAML files (AC-3) need to be tight; sprawl invites metric proliferation
- **`engineering-advanced-skills:runbook-generator`** — the "How to interpret a metric dip" worked example in the docs page is a runbook; treat it that way

## Notes for the implementing agent

- **The selection-vs-rejection call is the dominant scope decision.** ~40 metrics sounds tractable but the "what didn't make the cut and why" list will likely be 80+. Spend grill time defining the **selection criteria** explicitly: (1) does it answer a question the v1 persona actively asks? (2) does it cascade — is it a leaf input to a board metric, OR is it a board metric? (3) is it computable from current primitives or recoverable via plausible near-future integration? (4) does measuring it change behavior in a way the v1 persona cares about?
- **The cascade is opinionated.** Reasonable people will draw different parent-child edges. Pick one shape, document the choices in the decisions log, ship it. The `weight` column gives future-you a tuning knob without schema changes.
- **The 8 starter evaluators are not arbitrary.** Each one composes from already-merged slices' data. If the engineer's grill identifies that one of the 8 actually requires un-merged work (e.g., "Critical-findings SLA" needs slice-027 walkthrough-finding-severity joined with slice-028 freezing semantics that haven't been wired yet), DROP that evaluator from the v1 starter set + add it back as a follow-on slice. Better to ship 6 working evaluators than 8 with 2 broken.
- **The `metric_inputs` → `metric_observations` trigger is the trickiest correctness call.** Get the trigger's idempotency right: an `INSERT` on `metric_inputs` should produce exactly one observation row; replaying a backfill (`INSERT ... ON CONFLICT DO NOTHING` upstream) should still produce one observation row, not zero or two. Test this explicitly.
- **`catalogs/metrics/*.yaml` is the maintainer's narrative.** Each file's `notes` block should read like a security-program person wrote it — not a schema description. The catalog content is content; treat it that way.
- **Follow-on slices to mention in the PR body (but NOT write as separate files in this slice):**
  - Slice 078 — Metrics dashboard + cascade-tree view (visualization)
  - Slice 079 — Metric targets + threshold alerting + delta detection
  - Slice 080 — Per-tenant catalog customization (tenant-scoped catalog rows)
  - Per-metric integration slices (each manual metric that warrants an integration becomes its own connector-shape slice, slice-044/045/046 pattern)
