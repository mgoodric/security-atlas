# 253 — UI honesty: control-detail "endpoint not on main yet" empty-states are stale

**Cluster:** Quality / UI hygiene
**Estimate:** 0.5d
**Type:** AFK
**Status:** `ready`

## Narrative

Surfaced during slice 204's per-page audit of `/controls/{id}` (see
`docs/audit-log/204-page-audit-control.md`, Finding 1). The control-detail
page (`web/app/(authed)/controls/[id]/page.tsx`, slice 041) ships five
user-facing surfaces that claim a backing endpoint "is not on main yet":

1. KPI strip line 302-306 — `Evidence records · 30d` card with
   value `—` and sub-text `evidence-list endpoint pending`.
2. Evidence stream section lines 359-384 — full `<Alert>` titled
   "Evidence stream not yet wired" claiming
   `GET /v1/evidence?control_id=…` "does not exist on main yet".
3. Right-rail Policies card lines 458-469 — text "Linked policies
   bind to a per-control policy-link read endpoint that is not on
   main yet."
4. Right-rail Risks card lines 471-483 — text "Linked risks bind
   to a per-control risk-link read endpoint that is not on main
   yet."
5. Right-rail Audit log card lines 485-497 — text "The per-control
   audit log binds to a control-history read endpoint that is not
   on main yet."

All five claims are now false. `internal/api/openapi/routes.go`:

- Line 93: `GET /v1/evidence` (slice 106, accepts `?control_id=…`)
- Line 85: `GET /v1/controls/{id}/history`
- Line 86: `GET /v1/controls/{id}/policies`
- Line 87: `GET /v1/controls/{id}/risks`

Live probes on atlas-edge confirm all four return 200 with the expected
JSON envelope (`{count, evidence|history|policies|risks[], next_cursor?}`).

The slice 041 comment block (lines 18-25) was accurate when slice 041
shipped — slice 106 (evidence list) and the per-control history /
policies / risks endpoints landed after slice 041. The page has not
been re-walked since. The UI-honesty constitutional invariant cuts
both ways: promising what we don't have is the canonical violation;
denying what we do have is the dual — the operator concludes the
platform is less complete than it is, and downstream slices keep
filing "wire this surface" issues that have already shipped backends.

## Threat model

**Verdict.** **no-mitigations-needed.** This is a UI surface-area
correction that adds three GET-only data bindings to existing read
endpoints (history / policies / risks) plus the existing
`/v1/evidence?control_id=…` binding. All four endpoints are already
authz-gated in `internal/api/` and have integration test coverage. No
new auth surface, no new mutation, no new external IO. The fix removes
misleading text and adds read-only data renderings.

## Acceptance criteria

- **AC-1.** The evidence-stream center-column section binds to
  `GET /v1/evidence?control_id=<id>&limit=N`. Render the latest
  ~5 records with timestamp · summary · connector tag · pass/fail badge
  per the mockup (`Plans/mockups/control.html` lines 389-440). Empty
  state when the response is `{count: 0, evidence: []}` — render
  "No evidence records for this control in the last 30 days" (a
  truly-empty state, not an "endpoint pending" state).
- **AC-2.** The Evidence records · 30d KPI card renders the live
  count from the same evidence endpoint (paginate with `count` from
  the response envelope, or expose a `?count_only=true` mode if the
  backend supports it; otherwise compute from a small `?limit=1`
  call that returns the total). Sub-text reads "in last 30 days",
  no longer "evidence-list endpoint pending".
- **AC-3.** The right-rail Policies card binds to
  `GET /v1/controls/{id}/policies` and renders rows: policy title,
  version line, status badge. Empty state when `{count: 0}`.
- **AC-4.** The right-rail Risks treated card binds to
  `GET /v1/controls/{id}/risks` and renders rows: risk title,
  residual score, method/treatment line. Empty state when
  `{count: 0}`.
- **AC-5.** The right-rail Audit log card binds to
  `GET /v1/controls/{id}/history` and renders dated bullet entries
  per the mockup (lines 552-557). Empty state when `{count: 0}`.
- **AC-6.** All five "is not on main yet" / "endpoint pending"
  strings are removed from `web/app/(authed)/controls/[id]/page.tsx`.
- **AC-7.** Each of the four new BFF route handlers (or direct
  upstream proxies via the existing pattern in `web/app/api/`) gets
  a vitest unit test covering 200, 401, 404, and 5xx response
  classes. Playwright e2e at `web/e2e/control-detail.spec.ts` (or
  the file's current name) gets an additional assertion that the
  empty-state copy is the truly-empty form, not the
  endpoint-pending form (regression guard).
- **AC-8.** Slice 041's comment block (page.tsx lines 18-25) is
  updated to reflect the post-slice-106 / post-history-policies-
  risks-endpoints reality. The "backend gap" framing is removed
  from the page-level docblock.

## Constitutional invariants honored

- **UI-honesty anti-pattern (canvas §1.6) — both directions.** The
  page no longer claims the platform is less complete than it is.
  Each formerly-"endpoint pending" section renders the true backend
  state (which is often empty for a fresh-install tenant, and the
  empty-state copy honestly reflects that).
- **Invariant 9 (manual evidence is first-class).** The evidence
  stream shows pull AND push records with no preference; the BFF
  proxies whatever the upstream returns.
- **Anti-pattern rejected:** "vanity trust centers" — the dual:
  here we previously erased real platform progress from the UI.
- **No new auth surface.** All four endpoints already enforce
  RLS + tenant-context per main's middleware (slices 060 / 187+).

## Canvas references

- `Plans/canvas/01-vision.md` §1.6 — UI-honesty anti-pattern (both
  directions of "promise / deny" parity)
- `Plans/canvas/04-evidence-engine.md` §4.3 — append-only evidence
  ledger
- `docs/audit-log/204-page-audit-control.md` Finding 1 — this
  finding's full audit trail

## Dependencies

- **#204** (UI parity audit) — parent slice; this is a per-finding
  spillover surfaced by slice 204's audit fleet.
- **#106** (evidence list endpoint) — upstream that ships
  `GET /v1/evidence?control_id=…`. Already on main.
- Per-control history / policies / risks endpoints — already on
  main per `internal/api/openapi/routes.go` lines 85-87.

## Anti-criteria (P0 — block merge)

- **P0-253-1.** Does NOT modify any upstream `internal/api/`
  handler. All four endpoints exist and ship today; this slice
  consumes them. If a handler turns out to need a `?count_only`
  mode for AC-2, split it into a separate backend slice rather
  than bundling it here.
- **P0-253-2.** Does NOT fabricate evidence rows, policy rows,
  risk rows, or audit-log rows when the backend returns
  `{count: 0}`. Empty state renders honestly.
- **P0-253-3.** Does NOT modify slice 041's coverage / state /
  effectiveness data path. Those are unrelated to this finding;
  scoping creep is rejected.
- **P0-253-4.** Does NOT depend on the tab-strip slice (#254).
  This slice ships the in-page sections as-is; if #254 lands first
  the sections relocate behind tabs, but neither slice waits on
  the other.

## Skill mix (3-5)

1. Next.js App Router BFF route handlers — adding three new
   `web/app/api/controls/[id]/{policies,risks,history}/route.ts`
   files (history may exist; check before scaffolding).
2. TanStack Query — adding four `useQuery` hooks to the
   control-detail page with consistent error-class routing
   (existing pattern: `classifyControlDetailError`).
3. Playwright (slice 069 + slice 178) — empty-state regression
   guards.
4. shadcn/ui — list rendering inside `<Card>` with consistent
   row affordances (the mockup's policy/risk rows are
   shadcn `<a class="flex items-center justify-between">`
   pattern).
5. UI-honesty discipline — both directions: empty-states that
   reflect true backend state, not implementation status.
