# 152 — Control detail 404 on fresh install — decisions log

Slice 152 is a JUDGMENT slice — the engineer makes the build-time calls
and records them here for the maintainer's post-deployment review. This
file captures the judgment calls surfaced during build that were not
specified in `docs/issues/152-control-detail-404-on-fresh-install.md`.

The architectural decision (the (a)/(b)/(c)/(d) trade-off) is captured
separately in [`docs/adr/0004-control-detail-404-empty-state.md`](../adr/0004-control-detail-404-empty-state.md) — this log captures the build-time
sub-decisions inside the chosen path.

## D-152-1 — D1 chosen: hybrid (b + c). NOT (a) seed-on-bootstrap.

The slice doc framed D1 as binary: (a) seed SOC 2 kit on bootstrap vs
(b) friendly empty-state on controls list. Maintainer leaned (a).

**Decision:** Hybrid (b + c). The full reasoning is in ADR-0004
including the (d-deferred) URL-space option.

**Why not (a):**

1. Partial relief — seeding 50 SOC 2 controls only helps SOC 2
   anchor clicks; the other ~1,350 anchors still 404.
2. Scope mismatch — auto-seed requires extending slice 141's
   bootstrap path (PR #303, in flight, not on main). Coupling
   a 0.5d UX fix to a 3-4d bootstrap landing is brittle.
3. Wrong product call — silently seeding SOC 2 commits a tenant
   to a framework before the operator chooses one. Operator may
   want ISO 27001 (the secondary persona-driven framework per
   canvas §1.4).
4. Vision §1.5 #7 promise is unmet regardless — there is no
   path in main today that ingests the slice-010 kit into a
   tenant on first install. This deserves its own follow-on
   slice gated on slice 141 landing.

**Why (b + c):**

1. (c) Detail page friendly empty-state — load-bearing fix for
   the operator's reported symptom. The 404 backend contract is
   preserved (slice 150 D3 — bare-{id} 404 is a load-bearing
   platform semantic).
2. (b) List page truly-zero defensive branch — small, zero-risk,
   keeps the empty-state shape consistent across both surfaces.
   Genuinely defensive on main because anchors are catalog-global.

**Confidence:** HIGH. The reasoning chain rests on slice 150 D3
(bare-{id} 404 is correct), CONTEXT.md `/v1/controls/{id}/coverage`
glossary (the id is a control UUID, not an anchor UUID), and the
v1 binary success test from CLAUDE.md (operator can run SOC 2 out
of atlas without reaching for Vanta). The fix unblocks the reported
symptom path without committing to a framework auto-seed that has
its own design decisions to make.

**Revisit triggers:**

1. Slice 141 lands on main → file successor slice
   `158-seed-soc2-on-bootstrap` (or similar) to close vision
   §1.5 #7. The seed-on-bootstrap path becomes feasible only
   after the multi-tenant bootstrap surface exists.
2. Operators report the URL-space conflation (anchor.id vs
   control.id in the same `/controls/{id}` slot) is confusing
   enough to warrant the (d) split. The empty-state copy
   currently names the cause honestly which should mitigate
   the confusion; if it does not, file a follow-on slice for
   the route split.
3. SCF importer ships as a bootstrap default → the truly-zero
   branch on the list page becomes dead code and can be
   removed in a cleanup slice.

## D-152-2 — Empty-state copy is HONEST about cause; NOT misleading

The issue narrative suggested copy like "This control wasn't found.
It may have been deleted or you may not have access. Return to
controls list." That copy is misleading — neither cause applies on
a fresh install (the control was never created; permissions are
fine).

**Decision:** New copy reads:

> "This SCF anchor has no control instantiated in your tenant yet.
> The id `{id}` resolves in the global SCF catalog but no tenant
> control is bound to it. This is the expected state on a fresh
> install — controls are tenant-scoped and authored separately
> from the catalog."

**Why:** The operator deserves to understand WHY they hit this
state. Naming the SCF catalog vs tenant control distinction
honestly is the only way to make the empty-state actionable. The
copy also serves as in-app documentation for the URL-space
conflation that D1-d would eventually fix.

**Confidence:** HIGH.

## D-152-3 — Empty-state CTA points to `/controls`, NOT to an in-app "use SOC 2 kit" action

Slice doc PRD considered surfacing a "Use the SOC 2 starter kit"
CTA on the empty-state.

**Decision:** No vapor CTAs. The empty-state CTA is "Back to
controls list" (a `<Link>` to `/controls`). No SOC 2 button.

**Why:** There is no `atlas controls bootstrap --kit=soc2`
one-button affordance in the UI on main today. The only path is
the operator-initiated CLI invocation
(`atlas controls upload ./controls/soc2/...`) documented at
`controls/soc2/README.md`. A CTA naming the kit would imply an
in-app action that does not exist. When the future
seed-on-bootstrap slice (recommended 158-...) lands, this CTA
can be updated then.

**Confidence:** HIGH. Matches anti-criterion P0-CTL-3 ("no scope
creep into reshaping the controls list UI") and the broader
"no vapor CTAs" pattern across the frontend (slice 102 disabled
the audits-create-cta rather than route to nothing; slice 098
disabled the controls Export CSV / New control buttons rather
than ship deadends).

## D-152-4 — Classifier is a pure-logic helper, not inline `if` branches

The detail page had two interlocking error branches: a useEffect
that redirects on `APIError && status === 401`, and a render
branch that shows a destructive Alert on `error && !(status ===
401)`. Adding a third branch (404 → empty-state) inline would
create a thicket of overlapping conditions.

**Decision:** Extract the discriminator into a pure-logic helper
`classifyControlDetailError(err)` returning one of three classes
(`notfound`, `unauthorized`, `other`). Co-locate with the
existing `filters.ts` (slice 098 precedent in the same directory).
Vitest covers eight cases.

**Why:** Vitest scope in `web/vitest.config.ts` is pure logic
only (no JSX). A classifier is the right shape — testable, single
responsibility, future-proof for additional status codes (e.g.
if 410 Gone is ever introduced as a tombstoned-control marker).

**Confidence:** HIGH. Matches the slice 098 `filters.ts`
precedent + slice 130 `canReachAuditLog` route-guard precedent
+ slice 132 `assertCaptureSafe` precedent — pure-logic helpers
covered by vitest, consumed by the JSX in the page.

## D-152-5 — Playwright spec is quarantined per slice 079 / 082 precedent

The PRD called for a Playwright spec at
`web/e2e/control-detail-empty.spec.ts`.

**Decision:** New spec file lands with all assertions commented
behind the slice 082 seed-data harness, matching the existing
`web/e2e/control-detail.spec.ts` precedent. Comments document
the AC each assertion covers; the seed-data preconditions are
named so slice 082 can un-comment them when the harness lands.

**Why:** Slice 079 quarantined the Playwright job until the seed
harness ships. Adding new assertions un-quarantined would either
(a) break CI or (b) require the slice to inline a seed harness
which is far out of 0.5d scope. The precedent is to land the
spec body as reviewable contract with assertions commented.

**Confidence:** HIGH. Direct precedent.

## D-152-6 — Status flip commit is separate from the implementation commit

CLAUDE.md "Branching" + the slice-152 spawn prompt step 9 calls
for a `chore(status): 152 -> in-review` commit appended after
the implementation commit.

**Decision:** Two commits on the branch:

1. `fix(frontend): friendly empty-state on controls list + control detail 404 (#152)`
2. `chore(status): 152 -> in-review (#152)`

**Why:** Matches the established commit pattern across slices —
every recent PR has the implementation commit + the status flip
commit. The status flip commit may be amended after `pre-commit
run --all-files` if prettier reformats `_STATUS.md`; the
implementation commit stays clean.

**Confidence:** HIGH.

## D-152-7 — `internal/api/emptyset` audit sweep NOT extended

Slice 150 D3 explicitly EXCLUDED bare-`{id}` GETs from the audit
set ("they have their own well-defined 404 ErrNotFound semantic
that is orthogonal to empty-set robustness").

**Decision:** Do not extend the audit set to include
`/v1/controls/{id}/coverage`. The classifier vitest covers the
frontend's handling of the 404; the backend 404 contract is
already correct (verified at
`internal/api/ucfcoverage/handlers.go:285`).

**Why:** Reversing slice 150 D3 to add bare-{id} GETs to the
audit sweep would be a separate cross-cutting decision affecting
~20 endpoints, not slice 152's call to make. The frontend half
of the empty-set robustness story is exactly what slice 152
covers; the backend half was slice 150's call.

**Confidence:** HIGH. Slice 150 D3 stands as the canonical
decision for backend behaviour.
