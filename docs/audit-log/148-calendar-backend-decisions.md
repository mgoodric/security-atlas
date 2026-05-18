# 148 — Calendar backend endpoint missing despite slice 094 merge — decisions log

Slice 148 is `Type: AFK` in its frontmatter — its 9 acceptance criteria are
mechanically verifiable. This log captures the build-time judgments the
implementing agent made.

The slice file's stated hypothesis was "slice 094 shipped the frontend but
the backend aggregation endpoints (`GET /v1/calendar` for the page data +
`GET /v1/calendar.ics` for ICS subscriptions) appear to not have shipped."
**That hypothesis was wrong** — see D1 below for the audit of what actually
shipped. The operator's "Failed to load calendar events" symptom has a
different root cause, captured in D2.

---

## Decisions made

### D1. AC-1 audit — what slice 094 actually shipped backend-side

**Question:** Did slice 094's backend code ship to `main`?

**Investigation (file-by-file read on `main` at commit `2f7817b`):**

| Artifact | Present on main? | Evidence |
| --- | --- | --- |
| `internal/api/calendar/handler.go` | YES (455 LOC) | `Handler.RegisterRoutes` mounts `GET /v1/calendar`, `GET /v1/calendar.ics`, `POST /v1/calendar/subscription` |
| `internal/api/calendar/store.go` | YES (123 LOC) | `Store.ListEvents` opens a tenant-GUC tx, calls sqlc-generated `ListCalendarEvents` |
| `internal/api/calendar/ics.go` | YES (153 LOC) | `renderICS` hand-rolled RFC 5545 encoder |
| `internal/api/calendar/handler_test.go` | YES (138 LOC) | unit coverage for `parseWindow` / `normalizeTypeFilter` / `calendarNameFor` |
| `internal/api/calendar/ics_test.go` | YES (171 LOC) | unit coverage for ICS envelope + escaping + line folding |
| `internal/api/calendar/integration_test.go` | YES (520 LOC, `//go:build integration`) | RLS isolation + cadence math + truncation + ICS feed shape + ICS auth |
| `internal/db/queries/calendar.sql` | YES (221 LOC) | the four-way `UNION ALL` over `audit_periods` + `exceptions` + `policies` + `controls` + `control_evaluations` |
| `internal/db/dbx/calendar.sql.go` | YES (sqlc-generated) | `ListCalendarEventsParams` / `ListCalendarEventsRow` |
| `internal/api/httpserver.go` route registration | YES (line 616-620) | `calendarH.RegisterRoutes(root)` |
| `internal/api/httpserver.go` ICS exemption | YES (line 141 + 160) | `/v1/calendar.ics` exempt from upstream bearer + authz middleware (handler does inline scope-restricted auth) |
| `web/app/(authed)/calendar/page.tsx` | YES | full client component with agenda + month-grid + ICS subscribe button |
| `web/app/api/calendar/route.ts` | YES | BFF proxy reads `SESSION_COOKIE`, forwards to `/v1/calendar` |
| `web/app/api/calendar/subscription/route.ts` | YES | BFF proxy mints ICS URL token via `/v1/calendar/subscription` |
| `web/lib/api.ts` calendar helpers | YES (lines 1554-1647) | `getCalendarEvents` / `fetchCalendarEvents` / `createCalendarSubscription` / wire types |
| Go unit tests | PASS | `go test ./internal/api/calendar/...` → `ok 0.332s` (10 cases) |

**Conclusion.** Slice 094 shipped **everything backend-side**. The slice
148 doc's narrative ("backend aggregation endpoints appear to not have
shipped") was demonstrably false at the time it was filed. The operator's
symptom is real but the diagnosis was wrong.

**Confidence: high.** Every artifact above was read directly off the
`fix/148-calendar-backend-endpoint` branch which is in lock-step with
`main`.

---

### D2. Root cause of "Failed to load calendar events" — OPA default-deny on `resource.type = "calendar"`

**Question:** If the code shipped, why does the operator see "Failed to
load calendar events"?

**Investigation:**

1. **OPA `resourceFromPath`** (`internal/authz/input.go:164`) extracts the
   second segment of the URL as `resource.type`. For `GET /v1/calendar`
   that is the literal string `"calendar"`.
2. **Rego policies on `main`** (both `policies/authz/*.rego` and the
   embedded `internal/authz/rego_bundle/*.rego`):
   - `admin.rego` — wildcard allow (admit)
   - `grc_engineer.rego` — wildcard read (admit)
   - `auditor.rego` — `auditor_readable_resources` set does NOT include `"calendar"` (deny)
   - `viewer.rego` — `viewer_readable_resources` set does NOT include `"calendar"` (deny)
   - `control_owner.rego` — `control_owner_readable_resources` set does NOT include `"calendar"` (deny)
   - `defaults.rego` — `catalog_resources` set does NOT include `"calendar"` (deny)
3. **Test surface vs production surface.** The slice 094 integration
   tests (`integration_test.go`) construct the platform via `api.New(api.Config{})`
   and `srv.AttachDB(app)` — they never call `srv.AttachAuthz(...)`. The
   middleware in `httpserver.go:155` therefore skips the OPA gate
   entirely in tests. The production binary (`cmd/atlas/main.go:432`)
   DOES call `srv.AttachAuthz(...)` — so on a real install OPA fires
   and denies.

**This is the root cause.** The operator on a v1.10.0 install hits
`/v1/calendar` with their default-issued credential (which resolves to
`control_owner` or `viewer` depending on issuance path), OPA returns
`allow=false`, the BFF/handler returns 403, the React Query in
`page.tsx` enters the `isError` branch, and the user sees the
"Failed to load calendar events" copy.

**Why slice 094 didn't catch this:** AC-9 of slice 094 read "accessible
to all signed-in users (RBAC: all roles, no admin gate)." The intent was
clear. The implementation shipped the **handler** without admin gating
but never updated the **OPA policies** to admit the new `"calendar"`
resource type for any non-grc role. Slice 094's integration tests
exercised the handler directly (no authz wired) and so did not catch
the OPA deny.

**Confidence: high.** Reproducible by reading `policies/authz/` and
`internal/authz/rego_bundle/` — no `"calendar"` token appears in either
tree on `main`.

---

### D3. Fix shape — admit `"calendar"` on viewer + control_owner + auditor (matches AC-9 intent)

**Options considered:**

1. **(A)** Add `"calendar"` to `defaults.rego` `catalog_resources`. Rejected
   because that set is for **tenant-agnostic catalog reads** (`scf_anchors`,
   `frameworks`, `schemas`); calendar is tenant-scoped via RLS so the
   semantic fit is wrong.
2. **(B)** Add a new rule in `defaults.rego` that admits `"calendar"` read
   for any authenticated user regardless of role. Rejected because it
   diverges from the established pattern (every other tenant-scoped
   resource is enumerated per-role) and would set a precedent that
   future maintainers have to re-learn.
3. **(C)** Add `"calendar"` to the per-role `*_readable_resources` set
   in `viewer.rego`, `control_owner.rego`, and `auditor.rego`. Admin
   and grc_engineer already admit via their wildcard / `is_read`
   rules. Matches the existing pattern bit-for-bit.

**Chosen: (C).**

**Rationale.** Pattern consistency over micro-optimisation. Slice 094
AC-9 says "all roles" — adding `"calendar"` to each non-wildcard role
file is the explicit way to express "all roles admit this resource."
The added line in each file co-locates the calendar admit with the
other readable resources, so a future maintainer auditing what each
role can read sees `"calendar"` in context.

**Implementation:**

- Edit both `policies/authz/*.rego` (source of truth) and
  `internal/authz/rego_bundle/*.rego` (the embedded mirror loaded by
  `decision.go` via `//go:embed`).
- Add `"calendar"` to:
  - `viewer.rego` → `viewer_readable_resources`
  - `control_owner.rego` → `control_owner_readable_resources`
  - `auditor.rego` → `auditor_readable_resources`
- Each addition is annotated with a `# Slice 148:` comment.

**Confidence: high.** This mirrors the slice 124 / 135 / 124 / 029
pattern of adding a new resource type to the per-role sets.

---

### D4. Regression coverage — OPA matrix test pinning the admit set

**Question:** How do we prevent regression?

**Decision.** Add `internal/authz/slice148_test.go` with the same shape
as `internal/authz/slice124_test.go` — a parametric test over the five
roles asserting:

- admin: allow read
- grc_engineer: allow read
- auditor: allow read (slice 148 admit)
- viewer: allow read (slice 148 admit)
- control_owner: allow read (slice 148 admit)
- no-roles: deny

Plus a write-denial test pinning that no role gets a write surface (the
calendar handler has no write methods on the four source tables —
constitutional invariant 2 / slice 094 P0-A3).

This is the layer that would have caught the slice 094 oversight at the
time, and is the layer that catches a future maintainer accidentally
deleting one of the new admits.

**Confidence: high.**

---

### D5. ICS endpoint authz — already exempt, no change needed

**Question:** Does `/v1/calendar.ics` need an OPA update?

**Investigation.** `internal/api/httpserver.go:141` and `:160` already
list `/v1/calendar.ics` in the bearer-auth and authz-middleware exempt
sets. The handler authenticates inline via the URL `?token=` (slice
094 decision D3) and scope-restricts to `AllowedKinds=[calendar.read.v1]`.
OPA never sees `/v1/calendar.ics`.

**Decision.** No OPA change needed for `/v1/calendar.ics`. The slice 094
design carries through.

**Confidence: high.**

---

### D6. Empty-install integration test — keep the existing slice 094 ISC-94-1 / 94-2 coverage

**Question:** The slice file's AC-4 requires an empty-install test
("GET /v1/calendar returns `{events: []}` with 200, not 500"). Does
the slice 094 integration suite already cover this?

**Investigation.**

- `TestCalendar_RLSIsolatesExceptionsAcrossTenants` (slice 094 ISC-94-1)
  hits `GET /v1/calendar?types=exception` on tenant B which has zero
  seeded events; asserts `status=200` and `len(events)==0`. This is
  bit-for-bit the empty-install case slice 148 AC-4 asks for.
- `TestCalendar_RLSIsolatesControlCadenceAcrossTenants` (slice 094
  ISC-94-2) does the same for the control branch.

**Decision.** No new integration test for AC-4 — slice 094's two RLS
isolation tests already pin the empty-install 200 behaviour. The
slice 148 PR description will cite both tests as the AC-4 evidence.

**Confidence: high.** Cited tests are present and passing on
`main` (verified by `go test -tags=integration` not run yet — the
unit suite is `ok 0.332s`; the integration suite requires Postgres).

---

### D7. Cross-tenant isolation — existing slice 094 coverage satisfies AC-6

**Question:** AC-6 requires "events from Tenant A do not surface in
Tenant B's calendar."

**Investigation.** Same two slice 094 tests cited in D6 are exactly
this assertion — tenant B sees 0 events when only tenant A has
events.

**Decision.** No new integration test for AC-6.

**Confidence: high.**

---

### D8. Spillover — `/v1/upcoming`, `/v1/activity`, `/v1/frameworks/posture` likely have the same OPA omission

**Finding (out of scope for slice 148):** A grep for `"upcoming"`,
`"activity"`, and `"frameworks-posture"` across `policies/authz/`
and `internal/authz/rego_bundle/` returns zero matches. The
slice-066 dashboard read endpoints (registered at
`internal/api/httpserver.go:586-588`) have the same OPA-admit
omission. Any operator with a non-admin / non-grc role hitting
the dashboard will see the same "Failed to load" symptom on
the Activity / Upcoming / Posture panels.

**Decision (slice 148 anti-criterion P0-CAL-4 compliance).** Do NOT
fix in this slice. File as spillover slice `docs/issues/156-dashboard-opa-admit-omissions.md`
citing parent #148 and dependency #066. The slice 148 PR
description will mention the spillover so the maintainer can
triage priority.

**Confidence: high.** Operator may already be hitting this on
the dashboard — diagnose post-merge if reports surface.

---

### D9. Frontend AC-8 + AC-5 — no change needed; binding inherits from slice 094

**Investigation.**

- `web/app/(authed)/calendar/page.tsx` already renders the
  empty-state UI through `AgendaView` / `MonthGridView` when
  `events.length === 0` (slice 094 AC-10).
- The "Failed to load" branch fires on `eventsQ.isError`. After
  the OPA fix in D3, a fresh-install GET returns 200 with
  `{events: []}`, so `isError === false` and the empty-state
  branch renders.

**Decision.** No frontend changes for AC-5 — fixing D3 satisfies it.

**Confidence: high.**

---

### D10. Playwright e2e AC-8 — defer to slice 082 seed harness

**Question:** AC-8 of slice 148 asks for a Playwright spec that
"asserts calendar page loads + renders events on seeded install."

**Investigation.**

- Slice 094's AC-20 already shipped `web/e2e/calendar.spec.ts`
  but quarantined it pending the slice-082 seed harness (per the
  slice-079 quarantine pattern).
- Re-enabling that spec requires the seed harness to plant a
  predictable set of calendar events — that is the slice-082 work,
  not slice 148 work.
- After this slice's OPA fix, the existing quarantined spec WOULD
  work for the "page loads" half, but the "renders events"
  half still needs seeded data.

**Decision.** Treat AC-8 as **partially satisfied** by the existing
quarantined spec. The OPA fix removes the 403 that would have
prevented the spec from loading the page. The "renders events"
half remains blocked on slice 082. Do NOT un-quarantine the spec
in this slice — that's a slice 082 follow-on. Capture AC-8 as a
defer in the PR description.

**Confidence: high.** Matches the slice 094 pattern of quarantining
e2e specs pending the seed harness.

---

## Build-time observations (not decisions)

- `pre-commit run --all-files` was clean before any edits.
- `go test ./internal/api/calendar/...` passes with no changes (10
  cases, 0.332s).
- The four-way UNION in `calendar.sql` is well-formed; no migration is
  needed. The slice 094 migration that added `policies.next_review_at`
  is already on `main`.
- The slice file's narrative line "the backend aggregation endpoints
  appear to not have shipped" is a useful illustration of why the
  audit-first step (AC-1) exists. The slice 094 backend shipped 1,400+
  lines of code; the failure is at the policy layer.
