# Slice 384 — ActionPlan primitive: decisions log

**Type:** JUDGMENT
**Status at build:** the slice doc's `Status:` header said `merged` — that was a
stale/incorrect prior reconcile. Ground truth: the slice was UNBUILT on `main`
(no `internal/actionplan` package, no `action_plans` migration). Built fresh.
The header was left as-is (non-blocking); ground truth is the git history.

This is a JUDGMENT slice: the subjective build-time calls below were made by the
implementing engineer and recorded here rather than blocking the merge on a
human sign-off. The maintainer iterates post-deployment.

---

## Decisions made

### D1 — State-machine shape + double enforcement

Allowed transitions: `draft→in_progress`; `in_progress→{blocked,completed}`;
`blocked→{in_progress,completed}`; `completed→{verified,in_progress}` (reopen);
`verified` terminal; same-status always allowed (a non-status field edit). No
edge back to `draft` from any state (AC-15). Enforced in **two** places:
(a) the Go validator `AllowedTransition` (handler/store layer, returns
`ErrIllegalTransition` → 422), and (b) a DB `BEFORE UPDATE` trigger
(`action_plans_status_transition_trg`) that RAISEs `check_violation` — the store
maps that SQLSTATE back to the same 422. Rationale: the spec asks for validation
"at handler layer AND DB CHECK"; a per-row transition rule cannot be a plain
column CHECK (it needs OLD vs NEW), so a trigger is the DB-layer equivalent.
**Confidence: high.** Both paths are integration-tested (8 transition cases).

### D2 — `completed→in_progress` reopen edge included

The spec's narrative names the forward path and three illegal edges but does not
exhaustively enumerate every legal edge. I included `completed→in_progress`
(reopen if verification fails) and `blocked→in_progress`/`blocked→completed`
because a remediation workflow realistically reopens a "completed" item that
fails verification, and a "blocked" item resumes or is force-completed. These do
not violate any named anti-criterion (no edge to `draft`, `verified` stays
terminal). **Confidence: medium** — a maintainer may want to tighten
`completed→in_progress` to require an explicit reopen action later; that is a
post-deployment tuning call, not a foundation blocker.

### D3 — M2M RLS: subquery policy + composite FK (belt and suspenders)

The spec says the M2M tables use "subquery-based RLS against
`action_plans.tenant_id`". I implemented exactly that (an `EXISTS (SELECT 1 FROM
action_plans ap WHERE ap.id = … AND current_tenant_matches(ap.tenant_id))`
policy per operation) AND kept a denormalized `tenant_id` column on each M2M
table with a composite FK to both `action_plans(tenant_id,id)` and the target
`risks/controls(tenant_id,id)` (the slice-052 decision-link shape). The composite
FK makes a cross-tenant link structurally impossible even with RLS momentarily
off; the subquery policy is the spec-named gate. Defense in depth for P0-384-4.
**Confidence: high.** Verified by `TestLinkRisk_CrossTenantTargetReturns404` +
`TestLinkControl_CrossTenantTargetReturns404` (live Postgres).

### D4 — Append-only audit log: policy omission AND explicit trigger

AC-9 asks specifically for "a DB-layer trigger denies UPDATE". I implemented
both the slice-013/021/030 append-only RLS shape (SELECT + INSERT policies only)
AND an explicit `BEFORE UPDATE OR DELETE` trigger that RAISEs. The trigger is
strictly stronger: it fires even for a BYPASSRLS / table-owner role that the
missing-policy guard does not stop. Verified by
`TestAuditLog_AppendOnly_UpdateDenied` driving the admin (BYPASSRLS) pool — both
UPDATE and DELETE are denied. **Confidence: high.**

### D5 — Audit-period freezing depth (the load-bearing scope call)

AC-27 + invariant #10. The slice doc itself flags "slice TBD wiring" for deep
period-snapshot integration. I implemented the FOUNDATION:
`action_plans.audit_period_id` FK to `audit_periods(id)` + a
`ListActionPlansSnapshot(frozenAt)` store method + `ListActionPlansSnapshot` SQL
that filters `created_at <= frozen_at`, verified by `TestListSnapshot_FreezeHorizon`
(a backdated plan is included, a post-horizon plan is excluded). I did **not**
wire this snapshot read into the slice-028 AuditPeriod freeze/snapshot
materialization path (that primitive computes its frozen view from the live
ledger at freeze time; integrating action plans into that join is a larger
change touching `internal/audit/period` + the OSCAL aggregate path). That deeper
integration is **deferred and filed as spillover slice (see below)**. P0-384-5
(no retroactive edit of a frozen window) is honored at the foundation level: the
snapshot read is `created_at`-horizoned and the live `UPDATE` path is
independent, so editing a plan today never mutates what a past frozen snapshot
would return. **Confidence: medium-high** for the foundation; the deep wiring is
explicitly out of scope per the slice doc's own note.

### D6 — Actor id is a user UUID from the verified credential

`action_plan_audit_log.actor_id` is `UUID NOT NULL`. The credential `ID` is a
free-form string (e.g. `key_…`), so I resolve the actor via
`jwtmw.SubjectUserID(cred.UserID)` parsed as a UUID — the canonical pattern from
`internal/api/controls/owner_assign.go`'s `identity()` helper. A machine/API-key
credential without a user-shaped id is rejected (403): an action plan is a human
remediation commitment, and the actor is recorded for repudiation. **Confidence:
high** (reuses an established precedent).

### D7 — Owner picker is a UUID text field, not a user-search widget

AC-24 requires searchable multi-selects for _risks_ and _controls_ only. There
is no non-admin tenant-users list BFF to back an owner typeahead in v1, so the
create form takes `owner_id` as a validated UUID text input (the store + a
`ActionPlanOwnerExistsInTenant` probe reject a non-tenant owner with 400). A
proper owner-picker is a reasonable post-v1 polish. **Confidence: medium.**

### D8 — Cursor (keyset) pagination, default 25 / max 100

AC-11 says "pagination (`?limit=25&cursor=...`)". I implemented keyset
pagination on `(created_at DESC, id DESC)`; the list response emits an opaque
`next_cursor` (`<rfc3339nano>,<uuid>`) when the page is full. The handler clamps
`limit` to `[1,100]`, default 25. **Confidence: high.**

### D9 — OpenAPI + route-golden + test-footprint

The 9 core endpoints + 2 read-only linked-plans GETs were added to
`internal/api/openapi/routes.go` (tagged `action-plans`), `docs/openapi.yaml`
regenerated, and the two route-walk golden files
(`internal/api/testdata/routes.golden` + `routes_mw.golden`) updated (all 11
routes land in middleware gating group 8 — the standard bearer+tenant+authz
chain, identical to every other `/v1` bearer endpoint). The `internal/api/
actionplans` HTTP package is added to the slice-069/312 coverage _exclude_ list
(thin wrapper over the store, which carries its own integration + helpers
suites); `internal/actionplan` gets a hard floor of 66 (measured merged 68.7%,
floor = floor(measured − 2pp) per the slice-426 convention). The package is
enrolled in integration shard leg B4. **Confidence: high.**

---

## Revisit-once-in-use list

1. Tighten `completed→in_progress` to require an explicit "reopen" verb if
   operators reopen verified-adjacent work too casually (D2).
2. Graduate the owner UUID text field to a tenant-user typeahead once a
   non-admin users-list surface exists (D7).
3. Promote the `internal/actionplan` coverage floor as the suite grows.
4. Wire the action-plan snapshot read into the slice-028 AuditPeriod freeze
   materialization (the deep AC-27 integration) — filed as a spillover.

---

## Detection-tier classification (slice 353, Q-13)

- `detection_tier_actual`: `none` — no bug surfaced during the slice. Each AC was
  built test-first/test-alongside; the integration suite ran green live against
  real Postgres on the first complete run.
- `detection_tier_target`: `integration` — the load-bearing risks (RLS
  cross-tenant isolation, cross-tenant linkage 404, append-only trigger,
  state-machine edges, freezing horizon) are exactly the class the Go integration
  tier is designed to catch, and they are covered there. Pure-Go validators +
  the state machine are additionally covered at the `unit` tier (fast loop).

Companion fix-forward note: zero fix-forward commits were needed for this slice
(everything verified before the PR opened).

---

## Spillover slices filed

- **OSCAL POA&M export** (slice doc spillover #1) — NOT filed here; remains a
  named future item in the slice doc. Out of scope, no new finding.
- **Deep audit-period snapshot integration** — filed (see the slice number in
  the PR body / `docs/issues/`), citing "surfaced during slice 384, captured as
  follow-up per continuous-batch policy" — the deep AC-27 wiring deferred per D5.

The slice doc's other named spillovers (reminders, approval-gate workflow,
ExternalEvaluation primitive, board-narrative integration) are unchanged future
items; none was started, so none needed a fresh file beyond what the slice doc
already records.
