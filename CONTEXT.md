# security-atlas — Domain Glossary

> Canonical domain terms. When code or documentation uses one of these terms, this is the meaning. When in doubt, this file wins.

This file is created lazily as terms are resolved during design work. Most of the canon lives under `Plans/canvas/` — this is the short index plus the precise definitions that don't have a single-paragraph home there.

## Control evaluation (slice 012)

The output of the **evaluation stage** (canvas §4.3) — the read-only engine that consumes the append-only evidence ledger and computes per-control state. Lives in `internal/eval`. Always:

- **Writes `control_evaluations`, never `evidence_records`.** The table is named `control_evaluations` (the issue spec's literal `control_state` is superseded). It is an **append-only evaluation ledger** — one row per evaluation run per `(control_id, scope_cell_id)`, latest-row-by-`evaluated_at` wins. Append-only is what makes point-in-time replay meaningful (an upsert "current state" table would destroy prior computed state) and matches the `evidence_audit_log` / `aggregation_rule_evaluations` precedent. Slice 016 (freshness read-model) and slice 020 (risk residual) consume this table by that name.
- **`result`** is the slice-002 `evidence_result` enum (`pass` / `fail` / `na` / `inconclusive`). Zero in-window evidence → `inconclusive`, never `fail` (absence of evidence is not evidence of failure). Any in-window `fail` → `fail`; any in-window `pass` (no fail) → `pass`.
- **`freshness_status`** (`fresh` / `stale` / `no_evidence`) is orthogonal to `result`. Computed inline from raw `observed_at` + the control's `freshness_class` max-age (canvas §2.3) — the materialized freshness read-model is slice 016, which depends on 012. Out-of-window evidence never reaches the `result` computation.
- **Idempotent + replayable.** The computed columns are a deterministic function of the ledger slice; wall clock enters only as the freshness-window cutoff and the `evaluated_at` stamp, never the result. Deleting every `control_evaluations` row and re-running `Replay` reproduces identical computed state.
- **Live vs period-bounded.** `GET /v1/controls/{id}/state` is the slice-012 **live** entrypoint (per the AuditPeriod note below); `GET /v1/audit-periods/{id}/control-state` is slice 028's period-bounded entrypoint. The two share no SQL.

## Evidence freshness + drift (slice 016)

Two derived **leading indicators** (canvas §7.1) over the evidence pipeline. Read-only consumers of the immutable ledgers — they NEVER write or delete `evidence_records` / `control_evaluations` (invariant 2). Live in `internal/freshness`, `internal/drift`, `internal/freshnessdrift`.

- **`evidence_freshness`** is a materialized **current-state** read model — one row per `(tenant_id, control_id)`, **UPSERTed** on refresh. Carries the freshest evidence `observed_at`, the derived `valid_until` (= freshest `observed_at` + the control's `freshness_class` max-age), and a stored `is_stale` flag. Because it is UPSERTed current state it carries the **four-policy** RLS split.
- **`control_drift_snapshots`** is an **append-only** daily snapshot ledger — one row per refresh, latest-row-per-`(tenant_id, snapshot_date)` wins on read. Stores `controls_passing` + the `passing_control_ids` set. Append-only → **two-policy** RLS under FORCE (mirrors `control_evaluations` / `evidence_audit_log`).
- **"A control is passing"** (the drift definition) — worst-cell rollup: a control passes on a day iff EVERY applicable `(control, scope_cell)` tuple's latest evaluation that day is `result='pass'` AND `freshness_status='fresh'`. **Stale evidence does NOT count as passing** — canvas §2.3 says stale evidence drives the drift signal, so a control whose evidence decayed is drifting even with no `fail`. `delta = controls_passing(latest) − controls_passing(earliest)` over the window, signed.
- **The class → max-age mapping is defined once**, in `internal/eval` (slice 012's `freshnessMaxAgeTable`), exposed via the exported `eval.FreshnessMaxAge(class)`. Slice 016 reuses it — never redefines it.
- **Refresh triggers** (AC-4): a third durable JetStream consumer (`evidence_freshness_drift_worker`) on the slice-015 ingest stream refreshes on every evidence write; a daily 00:00 UTC `Scheduler` tick refreshes for time-based decay. Both run as the migrator role to enumerate tenants, then each tenant's refresh runs through app-role Stores under the tenant GUC.
- **Endpoints.** `GET /v1/evidence/freshness?bucket=class` → per-class fresh/stale distribution (`bucket=class` is the only supported bucketing in v1). `GET /v1/controls/drift?since=Nd` → signed delta + the controls that flipped out of passing, each with its last-passing date. Stale records are FLAGGED, never deleted — point-in-time audit replay is preserved (AC-6).

## Coverage (slice 008)

The graph-traversal result that answers "what is the relationship between a framework requirement and a tenant's controls?" — produced by joining `framework_requirements → fw_to_scf_edges → scf_anchors → controls.scf_anchor_id`. Always:

- **Two-hop, not recursive** — canvas §3 fan-out is bounded (req maps to 1–6 anchors; anchor maps to 1–8 reqs; controls anchored 1:1). No recursive CTE is needed; index-backed JOINs suffice.
- **Strength-aware** — each `(requirement, anchor)` row carries the STRM `relationship_type` + `strength` from `fw_to_scf_edges` (canvas §3.2). The handler returns these verbatim; weighted-sum coverage is computed upstream (slice 012's dashboard/eval territory).
- **Effectiveness-free in v1** — the `effectiveness` field that canvas §3.3 mentions for the dashboard is **deferred to slice 012**. Slice 008 returns anchors + controls, not effectiveness numbers. The wire format omits the field rather than emitting null, so slice 012 can add it without a breaking change.
- **`no_relationship` edges filtered out** — STRM stores "confirmed no overlap" as data (canvas §3.2). Coverage responses exclude these; they're surfaced only in the mapping-inspector UI (canvas §10), not the coverage view.

Coverage queries hit three routes:

- `GET /v1/requirements/{id}/coverage` — given a framework requirement (UUID, `slug:version:code`, or `slug::code`), list anchors + controls + edges.
- `GET /v1/anchors/{id}/requirements` — given an SCF anchor (UUID or scf_id), list satisfied framework requirements (DB-backed replacement of the slice-006 in-memory placeholder).
- `GET /v1/controls/{id}/coverage` — given a tenant control (UUID), list the framework requirements its anchor satisfies.

All three accept optional `?framework_version=slug:version` to pin historical mappings. `?as-of=<timestamp>` and `?scf_release=<version>` are accepted-and-no-op in v1; slice 012 / future SCF-release-import work will activate them.

**RLS interaction:** the catalog tables (`framework_requirements`, `fw_to_scf_edges`, `scf_anchors`) have no `tenant_id` and no RLS — they're platform-bundled and global. Only `controls` is tenant-scoped. A traversal across tenant boundaries returns the (global) requirement + anchors but an empty controls list — this is the correct shape (canvas §3.5) and is enforced by Postgres RLS, not by app code. The handler MUST NOT add `WHERE tenant_id = ?` to any query (constitutional invariant 6).

## Exception (slice 021)

A time-bounded, scope-bounded waiver of a control's normal evaluation. Always:

- **Scoped** — applies only to scope cells matching `scope_cell_predicate` (slice-017 JSON-AST shape; reuses `scope.Evaluate`).
- **Time-bounded** — `expires_at` is required, max **365 days** from creation. **Auto-renewal is forbidden** (P0 anti-criterion).
- **Logged** — every state transition writes one row to `exception_audit_log` (append-only). Auto-expiry is not silent.

States (canvas §6.3):

- `requested` — initial state. Set by `POST /v1/exceptions`.
- `approved` — governance approval recorded. `approved_by` populated. **Approval is not the same as activation** — the effect doesn't take hold until `active`.
- `denied` — terminal. To revisit, file a new exception.
- `active` — the effect is in force. A control × scope cell with an active exception evaluates as `excepted` (not `fail`) in downstream dashboards (slice 020 consumer).
- `expired` — terminal. Set by daily auto-expiry job when `expires_at < now()` for a row in `active`. Reverts control evaluation to normal.

Allowed transitions:

```
requested → approved   (approver-role required; segregation of duties: approver != requester)
requested → denied     (approver-role required; segregation of duties: approver != requester)
approved  → active     (operator action; sets effective_from)
active    → expired    (system; daily cron tick)
```

No other transitions. `denied` and `expired` are terminal.

**`compensating_controls`** is a `TEXT[]` — free-form descriptions of what's being done instead. NOT an FK to `controls` (because compensating mitigations are often informal: "weekly manual review by SRE on-call until IAM federation lands"). A future slice can add `compensating_control_ids UUID[]` if a structured link becomes useful.

**Segregation of duties** — `approved_by` MUST differ from `requested_by`. The same credential cannot both file and approve an exception.

**Calendar surface** — `GET /v1/exceptions/expiring?within=30d` powers the "Upcoming items" dashboard panel (canvas §6.3, dashboard mockup).

## Decision Log (slice 055)

A **Decision** captures a non-compliance operational or architectural
tradeoff — "shipping MVP, deferring SAML to v1.2", "skipping IaC because
the tool sunsets Q3". Distinct from an Exception (canvas §6.7): an
Exception is a formal, scoped, time-bounded bypass of a specific control;
a Decision is the broader rationale record. The two are linkable —
together with Risks and Controls they form the audit narrative chain.

- **Human-authored** — `decision_maker` and `decided_at` are required and
  human-set. There is no AI auto-creation path (P0 anti-criterion). AI may
  draft `narrative` / suggest `constraints` tags, but a human owns the
  record.
- **`decision_id`** — the tenant-visible identifier, format
  `DL-YYYY-MM-DD-NNNN` where the date is `decided_at`'s calendar date and
  `NNNN` is a zero-padded per-tenant, per-day sequence. Unique within
  tenant (`UNIQUE (tenant_id, decision_id)`).
- **Linkable** — four separate M:N link tables (`decision_risks`,
  `decision_controls`, `decision_exceptions`, `decision_scope_predicates`,
  all from slice 052). Linkage is idempotent. A link to an entity in
  another tenant returns **404** (existence-leak prevention, P0).
- **Logged** — every mutation (`PATCH`, supersede, cross-tenant link
  attempt, overdue-notification emission) writes one row to
  `decisions_audit` (append-only; slice 055 migration `_030`). The audit
  row for an `overdue_notified` action is the authoritative
  "already notified" marker — the daily job checks for it before emitting.

States (`decision_status` enum, slice 052):

- `active` — initial state. Set by `POST /v1/decisions`.
- `revisited` — reviewed at its `revisit_by` date without being changed.
- `superseded` — terminal. Pairs with the `superseded_by` FK to the
  replacement decision. The old decision is **never deleted** (P0
  anti-criterion) — the auditor trail is preserved.
- `expired` — terminal. The decision's relevance has lapsed.

**Supersession** — `POST /v1/decisions/{id}/supersede` takes
`{superseded_by: <existing decision UUID>}`. The replacement decision must
already exist (a separate `POST` first). Sets the old decision to
`superseded`, populates `superseded_by`, writes a `decisions_audit` row.

**`revisit_by`** is an optional hint date, not a gate (contrast with the
Exception's hard `expires_at`). Decisions with `revisit_by < today AND
status = 'active'` surface in `GET /v1/decisions/overdue`. A daily
background job emits **one** in-app notification per overdue decision to
its `decision_maker` — never repeated (P0 anti-criterion).

**`audit_narrative_opt_out`** — a per-decision boolean (slice 055
migration `_030`, default `false`). When `true`, the decision is excluded
from OSCAL SSP narrative emission. Per-decision rather than per-tenant
because opt-out is a per-record judgement; a tenant-config table is not
warranted by v1.

**OSCAL narrative** — decisions linked to in-scope controls appear in the
SSP export (slice 030) as `<remarks>` blocks, format:
`[DL-id] {title} ({decision_maker}, {decided_at}) — Linked risks: {ids}.
Revisit: {revisit_by or "n/a"}.` Slice 055 ships the emission function
(`internal/decision` exported, unit-tested); slice 030 calls it. Decisions
are audit **context**, not compliance artifacts (canvas §6.7, invariant 8).

## License posture (slice 050)

The project is licensed **Apache 2.0** — the canonical instance of the "permissive license" the canvas §1.2 thesis requires. Permissive matters because the platform is designed to be embedded in commercial deployments (the disqualification of OpenGRC at canvas §1.2 turns specifically on its CC BY-NC-SA license being incompatible with that goal). Copyleft alternatives (AGPL) were considered and rejected because they would block the same embedded-in-commercial-deployments use case the platform targets. Open-question #3 (`Plans/canvas/11-open-questions.md`) is resolved by slice 050.

Bundling posture for third-party catalogs (CLAUDE.md "Licensing constraints"):

- **SCF** — free standard license, but slice 050 does NOT bundle pre-built SCF data in release artifacts (open-question #1 resolution, consistent with slice 006's "users import their own" model).
- **CCM / CAIQ / SIG** — never bundled; opt-in import only. The platform ships the machinery, the operator provides the file.
- **HECVAT** — free; bundleable when a slice has a reason to.
- **OpenGRC code** — never copied; CC BY-NC-SA is incompatible with our license. Concepts and patterns may inform our own implementation.

## Policy (slice 022)

A governance document — title, version, body_md, owner_role, approver_role, linked_control_ids — that references the controls it governs (canvas §2.6). The inverse of "controls implement policies"; a policy without a linked control is a Word doc, and a control without a linked policy is engineer cargo culting.

States (canvas §2.6):

- `draft` — initial state. Set by `POST /v1/policies`. May be orphan (no linked controls); a warning surfaces on read but no transition is blocked.
- `under_review` — submitted for governance approval. Set by `PATCH /v1/policies/{id}/submit`.
- `approved` — governance approval recorded. `approved_by` + `approved_at` populated. Set by `PATCH /v1/policies/{id}/approve`. **Approval is not the same as publication** — the effective_date is set on publish, not approve.
- `published` — the policy is in force. Each call to `POST /v1/policies/{id}/publish` creates a **new versioned row** with `predecessor_id` pointing at the prior version; the prior version simultaneously transitions to `superseded` (single transaction). The first publish has `predecessor_id = NULL`.
- `superseded` — replaced by a newer version. Terminal for that row. The version chain (read via `GET /v1/policies/{id}?versions=true`) walks `predecessor_id` to surface the full history.

Allowed transitions:

```
draft        → under_review   (operator action)
under_review → approved       (approver-role required; cred.IsApprover || cred.IsAdmin)
approved     → published      (approver-role required; orphan-publish blocked; creates new row + supersedes prior)
published    → superseded     (system; happens atomically when a newer version publishes)
```

No other transitions. `superseded` is terminal for that row; revisions continue on the newer row.

**Versioning** — every publish creates a NEW row referencing its predecessor via the self-FK `(tenant_id, predecessor_id) → (tenant_id, id)`. The chain stays within tenant (composite FK enforces it). The `version` column is operator-supplied semver text (e.g. `1.0.0` → `1.1.0`); the application does not auto-bump.

**Orphan policy** — a policy whose `linked_control_ids` is empty is an "orphan". The API:

- Surfaces a `warning: orphan_policy` flag on every read response (AC-7).
- **Blocks publication** of an orphan policy — `POST /v1/policies/{id}/publish` returns 409 if `len(linked_control_ids) == 0`. Anti-criterion P0 ("Does NOT permit publish without linked controls").
- Allows `draft` and `under_review` to remain orphan (the warning is the signal; the user resolves it before requesting approval).

**`linked_control_ids[]`** is a `UUID[]` column. Postgres does not natively enforce per-element array foreign keys, so the application validates the IDs against `controls` at write time (cross-tenant IDs return 400). The column shape matches canvas §2.6 verbatim.

**`source_attribution`** — `community_draft` (agent-authored, ships with the platform; see the 5 stock policies under `policies/stock/`), `tenant_authored` (user-written), or `vendor_provided` (future — third-party policy library imports). Mirrors slice 007's `crosswalk.source_attribution` pattern.

**`effective_date`** — `DATE NULL`. Set on publish (operator-supplied; defaults to the publish-day UTC date when omitted). Null in `draft`, `under_review`, `approved`.

**Approver role gate** — `under_review → approved` and `approved → published` BOTH require `cred.IsApprover || cred.IsAdmin` (slice 034 credential flag). Publish is gated because it creates an audit-binding artifact; defense-in-depth.

**PDF render** — `GET /v1/policies/{id}/pdf` returns a real PDF (not a stub) rendered via chromedp from the markdown body. Magic-byte test (`%PDF-` at offset 0) is the assertion shape.

**Stock policy bundle** — exactly 5 policies under `policies/stock/`:

| File                             | Title                       | Linked SCF anchors           |
| -------------------------------- | --------------------------- | ---------------------------- |
| `information-security-policy.md` | Information Security Policy | `GOV-01`, `GOV-04`, `RSK-01` |
| `access-control-policy.md`       | Access Control Policy       | `IAC-01`, `IAC-07`, `IAC-22` |
| `vendor-management-policy.md`    | Vendor Management Policy    | `TPM-01`, `TPM-03`, `TPM-04` |
| `incident-response-plan.md`      | Incident Response Plan      | `IRO-04`, `IRO-01`, `IRO-02` |
| `change-management-policy.md`    | Change Management Policy    | `CHG-02`, `CFG-02`, `CHG-04` |

Exactly 5 — never 6, never 4 (constitutional anti-pattern: "policy template libraries dressed as a feature"). The CLI `atlas-cli policy seed-stock --tenant-id=...` loads these markdown files, resolves the SCF anchor codes to UUIDs via `scf_anchors`, and INSERTs them as `draft` rows with `source_attribution = 'community_draft'`. Missing anchors warn + drop the link (the warning surfaces under AC-7 if all links resolve empty).

## AuditPeriod (slice 028)

A tenant-scoped, framework-scoped time window over which an auditor evaluates compliance, with a freezing primitive that pins the evidence-universe horizon. Always:

- **Per-(tenant × framework_version)** — the FK targets `framework_versions(tenant_id, id)` so a SOC 2 Q2 freeze and an ISO 27001 Q2 freeze are independent rows. Composite FK blocks cross-tenant linkage (slice 002 D3).
- **Two-state lifecycle** — `open` (default on create) → `frozen` (terminal-for-content; metadata edits still rejected). There is no `closed` or `archived` state in v1; canvas §8 promises one frozen state and we keep it minimal.
- **`name`** is the human-facing handle (`"SOC 2 2026 Q2"`); UNIQUE per `(tenant_id, name)` NULLS DISTINCT. This stands in for the canvas-§8.3-mentioned `audit_id` until an `Audit` parent primitive ships (likely the OSCAL Assessment Plan slice).
- **`frozen_at`** is set on the freeze call; until then it is `NULL`. The append-only evidence ledger makes freezing cheap — we shift the read horizon (`observed_at <= frozen_at`), no snapshot tables (canvas §8.4 verbatim; constitutional anti-criterion P0).
- **`frozen_hash`** is the content commitment computed at freeze time. Inputs and canonical form are pinned in [ADR 0003](../adr/0003-audit-period-freeze-hash-inputs.md): `sha256(canonical_json({audit_period_id, period_start, period_end, framework_version_id, evidence_record_ids_sorted, control_ids_sorted}))`. `frozen_at` is NOT in the hash inputs — that lets AC-7 ("freezing the same content twice produces the same hash") hold.
- **`frozen_by`** records the actor (slice 003 `key_<32hex>` for now; slice 034 OIDC subject post-graduation).
- **Live evaluation isolation** — control-state queries that are NOT period-bounded (i.e., the future slice 012 path) do NOT join `audit_periods`. Period-bounded vs live is determined by _which endpoint the caller hits_. `GET /v1/audit-periods/:id/control-state` is the period-bounded entrypoint; `GET /v1/controls/:id/state` (slice 012 future) is the live entrypoint. AC-5 holds because the two paths share no SQL.
- **Population attachment (AC-4)** — slice 026 already added `populations.frozen_at` and uses `observed_at <= COALESCE(frozen_at, 'infinity')`. Slice 028 adds `populations.audit_period_id` (NULLABLE composite FK to `audit_periods(tenant_id, id)`) and, on freeze, stamps `populations.frozen_at = period.frozen_at` for all populations whose `audit_period_id = period.id`. This is a write-once stamp: once a population's `frozen_at` is set, slice 026's existing query path enforces the horizon.
- **Mutation rejection (AC-6)** — `Store.Freeze` is the only path that mutates `audit_periods` after creation, and it is guarded by `WHERE status='open'` in the SQL. Re-freezing returns `ErrAlreadyFrozen` (HTTP 409). No update path exists for frozen rows.
- **Audit log** — `audit_period_audit_log` is append-only (SELECT + INSERT policies only under FORCE ROW LEVEL SECURITY; slice 011 / 013 / 026 / 035 / 036 pattern). Captures `period_created`, `period_frozen`, `freeze_rejected_already_frozen`, `population_attached`.

Routes:

```
POST   /v1/audit-periods                            create (status=open)
GET    /v1/audit-periods                            list for current tenant
GET    /v1/audit-periods/{id}                       get one
POST   /v1/audit-periods/{id}:freeze                AC-2; 409 on re-freeze (AC-6)
GET    /v1/audit-periods/{id}/control-state?control=...   AC-3 frozen-horizon read
POST   /v1/audit-periods/{id}/populations/{popID}   AC-4 attach + stamp frozen_at if period is frozen
```

P0 anti-criteria: no `evidence_records` snapshot table; no retroactive mutation of frozen rows (UPDATE guard); content-derived hash with no random salt (re-hash idempotence).

## Policy acknowledgment (slice 023)

An affirmative, per-user attestation that a published policy version has been read and accepted. Always:

- **Per-(user × policy_version_id)** — the FK targets the policies row id, not a "logical id". When publish creates a new row with a new id, acks of the prior row do not satisfy the new row (anti-criterion P0-3). Each policy publish forces re-acknowledgment.
- **Annual recurrence** — an ack older than 365 days is treated as expired; the task reappears in `/v1/me/acknowledgments`. Computed at read time (no cron). Canvas §2.3's `annual` evidence freshness class (400 d) governs _evidence_ `valid_until`; 365 d governs _task reappearance_, which is canvas §2.6's "attest annually" reading.
- **First-class evidence** — every ack emits one `policy.acknowledgment.v1` evidence record through the slice-013 ingest service (invariant 9). The record's `control_id` is the non-UUID string `policy:<policy_id>:v<version_id>` so the ledger stores it as `control_ref` only; SCF anchor for the kind is `GOV-04` (matching `manual.attestation.v1`).
- **Role-gated** — a user sees a policy in their pending list iff their credential's `OwnerRoles[]` intersects the policy's `acknowledgment_required_roles[]` OR the credential is `IsAdmin`. The slice-035-future OPA-RBAC graduation will replace this stand-in.
- **Rate at read time** — `GET /v1/policies/{id}/acknowledgment-rate` returns `{numerator, denominator, percent}`. Denominator = distinct `api_keys.issued_by` user_ids in the tenant whose `owner_roles && policy.acknowledgment_required_roles OR is_admin = true`, excluding `revoked_at IS NOT NULL`. Numerator = denominator users with a fresh ack (≤365 d) of the current published version. `percent = null` when denominator = 0 (vs returning `0` which would mis-imply non-compliance).
- **Idempotency** — the ack endpoint derives an idempotency key over `(user_id, policy_version_id, day_bucket)`; double-clicks within a day deduplicate; a re-ack 365 days later produces a fresh evidence record.

Routes:

```
GET  /v1/me/acknowledgments                    (auth required; lists pending for current user)
POST /v1/policies/{id}/acknowledge             (auth required; 409 if id resolves to non-published row)
GET  /v1/policies/{id}/acknowledgment-rate     (auth required; numerator/denominator/percent)
```

P0 anti-criteria: anonymous ack rejected (slice 034 cred required); stale acks not counted; superseded-version ack does not satisfy current.
