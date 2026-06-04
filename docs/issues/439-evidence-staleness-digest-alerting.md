# 439 — Evidence-staleness digest + alerting (honest, named-interval)

**Cluster:** Notifications
**Estimate:** M (1-2d)
**Type:** JUDGMENT (threshold-rollup shape + digest copy are subjective calls)
**Status:** `ready`

## Narrative

The platform **computes** evidence staleness — `internal/freshness` tracks
per-cell freshness and `internal/drift` keeps an append-only drift ledger — but
**nothing tells the operator**. A solo security leader running their whole
program will not discover that an access-review evidence record went stale 95
days ago by happening to open the right control-detail page; they need to be
told. Today the only in-app notification surface is Audit-Hub comments (slice 029) writing to the `/v1/me/notifications` store; staleness is invisible.

This is the **biggest silent gap in v1**. Canvas §7 makes metrics + freshness
first-class, and the v1 binary success test ("run the next SOC 2 audit out of
security-atlas, don't reach for a Google Sheet") fails the moment the operator
has to keep a side-spreadsheet of "what evidence is about to expire."

This slice ships a **scheduled staleness rollup**: a job (reusing the
`internal/metrics/scheduler` cadence primitive) that rolls up evidence crossing
freshness thresholds into the existing `/v1/me/notifications` store, plus a
**weekly digest** notification summarizing what is stale / approaching-stale.
The crucial discipline: this is **honest, named-interval** alerting. The UI
states the interval explicitly ("staleness recomputed every N hours; weekly
digest every Monday 09:00 tenant-time") — it is **not** dressed up as
"continuous monitoring," which the canvas anti-patterns explicitly ban.

**Scope discipline.** This is the **first thin vertical slice**: in-app
notifications + the weekly digest writing to the existing notification store —
shipped **without** email delivery. Email/SMTP delivery-to-inbox depends on
slice 445 (email channel) and is a follow-on; the in-app surface ships and is
useful standalone. It does **not** add per-control custom thresholds (reuses the
existing `eval.FreshnessMaxAge`), does **not** add SMS/Slack delivery, and does
**not** add a notification-preferences settings page beyond the minimum
opt-out. **Follow-on slices:** email delivery of the digest (after 445);
per-control threshold overrides; notification preferences page.

## Threat model (STRIDE)

The job reads tenant-scoped freshness/drift state and writes tenant-scoped
notifications. The single highest risk is a **cross-tenant leak in the rollup**:
a scheduled job that aggregates across tenants must never put Tenant A's stale-
evidence facts into Tenant B's digest. Notifications carry operator-visible
evidence metadata (control IDs, evidence kinds, ages) — confidential per tenant.

**S — Spoofing.** No new authenticated ingress: the digest is delivered through
the existing authenticated `GET /v1/me/notifications` read path; the job runs
server-side under a service identity, not a request.
**Mitigation:** reuse the existing notifications read auth; the writer runs under
the scheduler's existing service-account boundary (same as `metrics/scheduler`).

**T — Tampering.** The job writes notification rows. Risk: a buggy rollup writes
malformed or duplicate digest rows, or writes a digest for a tenant on every
tick (spam).
**Mitigation:** the digest write is idempotent per (tenant, digest-period) — a
unique key on (tenant_id, kind=`staleness_digest`, period_start) prevents
double-delivery; the threshold-crossing rows dedupe per (tenant, evidence_id,
threshold).

**R — Repudiation.** Operators may later ask "was I told this evidence went
stale?" The notification store IS the audit trail.
**Mitigation:** each staleness notification carries the evidence_id, the threshold
crossed, and the computed-at timestamp; the digest references the period it
covers. No separate audit-log write needed beyond the durable notification row.

**I — Information disclosure (PRIMARY).** A scheduled cross-tenant job is the
classic RLS-bypass risk: aggregating freshness across all tenants in one query
then fanning out must NOT let one tenant's stale list reach another's digest.
**Mitigation:** the rollup runs **per-tenant** with `app.current_tenant` set for
each tenant's pass (the slice-016 / metrics-scheduler pattern), OR uses a
tenant-partitioned query whose every row carries its tenant_id and the writer
asserts tenant_id matches the notification target. An integration test MUST
prove Tenant A's stale evidence never appears in Tenant B's notifications. The
digest body includes evidence kinds + ages + control codes — no raw evidence
payloads, no S3 URLs, no cross-tenant IDs.

**D — Denial of service.** A tenant with a very large evidence corpus could
make the rollup query unbounded; the digest body could grow unboundedly.
**Mitigation:** the rollup query is bounded by a freshness-threshold window (only
evidence at/over threshold) and paginated/capped; the digest body summarizes
("47 records stale, top 10 listed; see the freshness view for the full list")
rather than enumerating every stale record. The job has a per-run timeout.

**E — Elevation of privilege.** No new role: notifications are read by the
already-authenticated user; the writer is the scheduler service identity.
**Mitigation:** no new authz surface; the job does not let a tenant trigger
another tenant's rollup.

## Acceptance criteria

**Backend — scheduled rollup**

- [ ] **AC-1.** A scheduled staleness-rollup job lands, driven by the existing
      `internal/metrics/scheduler` cadence primitive (no new scheduler).
- [ ] **AC-2.** The job reads `internal/freshness` + `internal/drift` state and
      identifies evidence at/over the `eval.FreshnessMaxAge` threshold (and an
      "approaching" band) **per tenant**.
- [ ] **AC-3.** Threshold-crossing events write notification rows to the
      existing `/v1/me/notifications` store with kind `staleness_alert`,
      carrying evidence_id + control code + threshold + computed-at.
- [ ] **AC-4.** A **weekly digest** notification (kind `staleness_digest`) is
      written per tenant summarizing stale + approaching-stale counts and a
      capped top-N list; it states the period it covers.
- [ ] **AC-5.** Both writes are **idempotent**: re-running the job for the same
      period does not duplicate the digest or re-fire the same threshold
      crossing.

**Backend — honesty + opt-out**

- [ ] **AC-6.** The recompute interval + digest cadence are surfaced as honest
      named intervals in the notification metadata / UI copy (no "continuous
      monitoring" framing).
- [ ] **AC-7.** A per-user opt-out for the staleness digest exists (minimum:
      a boolean preference honored by the writer); default is opted-in.

**Frontend**

- [ ] **AC-8.** Staleness alerts + the weekly digest render in the existing
      in-app notifications surface (reuses the slice-029 notification UI; new
      notification kinds display correctly).
- [ ] **AC-9.** The digest entry links to the existing freshness view for the
      full stale list.

**Tests**

- [ ] **AC-10.** Integration test (`//go:build integration`): the rollup writes
      the expected staleness notifications for a tenant with known stale
      evidence against a real Postgres.
- [ ] **AC-11.** **Tenant-isolation integration test:** Tenant A's stale
      evidence NEVER appears in Tenant B's notifications (threat-model I — the
      load-bearing test).
- [ ] **AC-12.** Integration test: idempotency — a second job run for the same
      period produces no duplicate digest/alert rows.
- [ ] **AC-13.** Pure-Go unit test for the threshold-band classification
      (stale vs approaching vs fresh) without a DB.

**Docs**

- [ ] **AC-14.** Operator docs note the named intervals + the opt-out; a
      changelog entry for the slice.

## Constitutional invariants honored

- **Anti-pattern: honest interval naming.** The canvas bans "continuous
  monitoring" that is actually polling; this slice names the interval honestly.
- **#6 — Tenant isolation enforced at the DB layer via RLS.** The per-tenant
  rollup runs under `app.current_tenant`; cross-tenant leak is proven absent.
- **#2 — Ingestion/evaluation separation.** The rollup READS freshness/drift
  read-models; it never writes back to the source-of-truth evidence ledger.

## Canvas references

- `Plans/canvas/07-metrics.md` — KPIs + freshness as first-class.
- `Plans/canvas/04-evidence-engine.md` §4.3 — append-only ledger /
  ingestion-evaluation separation (the rollup is a read-only consumer).
- `Plans/canvas/01-vision.md` §1.6 — the "continuous monitoring" anti-pattern.

## Dependencies

- **#016** (evidence freshness + drift read models) — `merged`. The data
  source.
- **#029** (Audit-Hub notifications / `/v1/me/notifications` store) — `merged`.
  The delivery surface.
- **#445** (email/SMTP channel) — **not yet merged.** Email delivery of the
  digest is a follow-on; the in-app surface ships WITHOUT it (no hard dep for
  this slice's ACs).

## Anti-criteria (P0 — block merge)

- **P0-439-1.** Does NOT label the rollup "continuous monitoring" or imply
  real-time alerting — the interval is named honestly (anti-pattern).
- **P0-439-2.** Does NOT leak any tenant's stale evidence into another tenant's
  notifications — proven by AC-11 (threat-model I).
- **P0-439-3.** Does NOT write to the source-of-truth evidence ledger — read-
  only consumer of freshness/drift read-models (invariant #2).
- **P0-439-4.** Does NOT introduce a second scheduler — reuses
  `internal/metrics/scheduler`.
- **P0-439-5.** Does NOT block on slice 445 / email — the in-app digest ships
  standalone.
- **P0-439-6.** Does NOT enumerate an unbounded stale list in the digest body —
  capped top-N + link (threat-model D).
- **P0-439-7.** Does NOT add per-control custom thresholds or a full
  preferences page — out of scope (follow-on).

## Skill mix (3-5)

`grill-with-docs` · `tdd` (integration-first; tenant-isolation test is
load-bearing) · `database-designer` (idempotent notification writes + unique
keys) · `security-review` (cross-tenant rollup) · `simplify`.

## Notes for the implementing agent

- **Phase-2 grill output:** the load-bearing risk is the cross-tenant rollup.
  Follow the slice-016 / metrics-scheduler per-tenant pattern — set
  `app.current_tenant` per tenant pass; do NOT write a single all-tenant query
  that fans out without re-asserting tenant_id on the write.
- **JUDGMENT calls you own:** the "approaching stale" band width, the digest
  cadence default (weekly Monday is the proposed default), and the digest copy.
  Record in the decisions log.
- The digest is the operator's "what do I need to fix this week" surface — keep
  the copy plain and factual (the project's measured-tone discipline applies
  even though there's no LLM here).
- Detection-tier: `none` unless a bug surfaces; a cross-tenant leak caught in
  review would be `target=integration, actual=manual_review`.
