# ADR 0004 — Control detail 404 is a friendly empty-state, not a destructive error

- **Status:** Accepted
- **Date:** 2026-05-18
- **Slice:** [#152](https://github.com/mgoodric/security-atlas/issues/152)
- **Decisions log:** [`docs/audit-log/152-control-detail-404-decisions.md`](../audit-log/152-control-detail-404-decisions.md)

## Context

On a fresh install, navigating to a control detail page returns
"Could not load control · 404 Not Found" (operator report from
v1.10.0). The root cause is broader than the symptom suggests:

1. The `/controls` list page renders **SCF anchors** from
   `/v1/anchors` — a global catalog of ~1,400 rows that always
   exists on a deployment with the slice-006 SCF importer run.
2. Each row links to `/controls/{anchor.id}`.
3. The detail page calls `/v1/controls/{id}/coverage`, which expects
   a **tenant control UUID**, not an SCF anchor UUID
   (CONTEXT.md `/v1/controls/{id}/coverage` glossary entry).
4. On a fresh install with zero tenant controls, every click 404s.
   Even on a NON-fresh install with 5 tenant controls, clicking any
   of the other ~1,395 anchors still 404s.

The frontend URL space `/controls/{id}` therefore overloads two
distinct conceptual ids (anchor.id and control.id) into one slot.
The backend contract is correct; the frontend wiring is what
surfaces the failure mode.

## Decision

**Hybrid (b + c).** Slice 152 ships:

- **(c) Detail page friendly empty-state.** When
  `/v1/controls/{id}/coverage` returns 404, the page renders a
  centered empty-state explaining honestly that "this SCF anchor
  has no control instantiated in your tenant yet" with a back-to-
  list CTA. The backend 404 semantic is **preserved** — the UI
  changes, not the platform contract.
- **(b) List page truly-zero defensive empty-state.** When the
  anchor catalog returns zero rows (a defensive branch on main
  today because anchors are catalog-global), the page renders an
  honest "catalog not seeded" empty-state pointing at the user
  guide.

The classifier that drives the detail-page branch is a pure-logic
helper (`web/app/(authed)/controls/error-classifier.ts`) with eight
vitest cases, matching the slice-098 `filters.ts` precedent in the
same directory.

## Alternatives considered

### (a) Auto-seed the SOC 2 stock kit on bootstrap — REJECTED

Seed the slice-010 SOC 2 starter (50 SCF-anchored controls) into
the tenant database on first OIDC sign-in. The operator then sees
50 tenant controls + ~1,350 unseeded anchors and clicking the
seeded controls succeeds.

**Why rejected:**

1. **Partial relief.** Seeding 50 controls helps SOC 2 anchors;
   clicking any of the other ~1,350 anchors still 404s. The
   underlying URL-space conflation (D1-d below) is unfixed.
2. **Scope mismatch.** Slice 152 is filed as 0.5d JUDGMENT.
   Auto-seeding requires extending the slice 141 multi-tenant
   bootstrap path (PR #303, in flight, not on main). Coupling
   a 0.5d UX fix to a 3–4d bootstrap landing is brittle slice
   ordering.
3. **Wrong product call.** SOC 2 is the v1 stock kit but the
   solo operator may want ISO 27001 (the prospect-driven
   secondary framework per canvas §1.4). Auto-seeding SOC 2
   silently is a magic default that imposes a framework
   commitment. The seeding-policy decision deserves its own
   slice with its own decisions log.
4. **Vision §1.5 #7 is unmet, separately.** The "installable,
   seeded, producing first evidence within 4 hours" promise is
   currently unmet on main regardless of slice 152: there is
   no path in main that ingests the slice-010 kit into a tenant
   on first install. This deserves its own follow-on slice
   (recommended id: 158-seed-soc2-on-bootstrap) gated on
   slice 141 landing.

### (b-only) List page empty-state, no detail-page change — INSUFFICIENT

Show a friendly empty-state on the controls list when there are
zero rows. Leave the detail page's 404 path untouched.

**Why insufficient:** The list page on a fresh install is NOT
empty — it shows ~1,400 anchors from the global catalog. The
operator's symptom path (click an anchor → see a 404) is never
triggered by the list's empty branch. (b) alone does not fix the
operator's reported bug. We ship (b)'s defensive branch anyway
because it is a small zero-risk addition, but the load-bearing
fix is (c).

### (d) URL-space split between anchor.id and control.id — DEFERRED

Make `/controls/anchor/{id}` and `/controls/{id}` two routes:
the former always exists and shows anchor metadata; the latter
only exists for instantiated tenant controls. The list page
links to the anchor route.

**Why deferred:** This is the correct long-term fix but it
reshapes a load-bearing URL space across multiple pages
(detail page, attest page, the BFF route table). Slice 152's
0.5d JUDGMENT scope cannot absorb that. The decisions log
records this as a recommendation for a follow-on slice.

## Consequences

**Positive:**

- The operator's reported symptom (generic 404 page) is gone.
  The empty-state copy is honest about cause and offers
  orientation.
- Slice 150's empty-set robustness audit (D3) and slice 152
  are complementary: slice 150 audits backend empty-set; slice
  152 audits frontend 404-on-id rendering.
- The error classifier is a single testable unit; future
  regressions that misroute 404 vs 5xx fail vitest before
  reaching e2e.

**Negative / acknowledged debt:**

- Vision §1.5 #7 ("installable, seeded, producing first evidence
  within 4 hours") remains unmet. The empty-state names this
  honestly rather than papering over it. A successor slice
  (recommended: 158-seed-soc2-on-bootstrap) is the right home
  for the auto-seed work and is gated on slice 141 landing.
- The URL-space conflation (D1-d above) remains. The empty-state
  copy explains the cause but the underlying frontend wiring is
  still suboptimal. A follow-on slice can split the routes.

**Neutral:**

- The platform contract for `/v1/controls/{id}/coverage` is
  unchanged. The bare-`{id}` 404 semantic remains canonical per
  slice 150 D3.
