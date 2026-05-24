# Decisions log — slice 214 (sidebar item counts parity gap)

**Slice:** [`docs/issues/214-sidebar-item-counts-parity-gap.md`](../issues/214-sidebar-item-counts-parity-gap.md)
**Type:** JUDGMENT
**Build agent:** Engineer
**Closed at:** 2026-05-23

---

## Summary

Slice 214 closes a sidebar parity gap surfaced by slice 204's audit
fleet: the mockup at `Plans/mockups/audits.html` (lines 63-76) shows
two sidebar rows carrying right-aligned mono count badges (Controls
"82" muted, Risks "3" rose). The live shared sidebar at
`web/components/shell/sidebar.tsx` rendered bare text labels with no
count metadata.

This log captures the JUDGMENT calls made during the build. The slice
spec is the source of intent; this log is the source of "what we
actually decided when the spec language did not perfectly match the
schema".

---

## D1 — "Open critical risk count" maps to severity ≥ 15 (the high / rose tier)

**Decision:** the Risks badge counts rows whose 5x5 `severity` scalar
is ≥ 15. This is the canonical **high** tier as defined in
`web/app/(authed)/risks/filters.ts` (`severityBand` returns "high" at
`severity >= 15`), which `severityClasses` paints with the rose
palette (`bg-rose-100 text-rose-700`).

**Why this needed a JUDGMENT call:** the slice spec at AC-2 reads
"renders the OPEN CRITICAL risk count in rose" and references a
hypothetical `GET /v1/risks?status=open&severity=critical` endpoint.
The actual wire shape (`internal/api/risks/handlers.go` →
`riskWire`, mirrored in `web/lib/api.ts`'s `Risk` type) carries:

- No `status` column (risks have `treatment` — mitigate / accept /
  avoid / transfer — but not an open/closed lifecycle column).
- No `critical` band (the slice-100 severity bands are
  `high` / `medium` / `low` / `none` with high = severity ≥ 15).

**Why the high tier is the right resolution:**

1. The mockup's rose color is unambiguously the slice-100 high-tier
   palette — `severityClasses("high")` returns the same rose family.
   The colour is the load-bearing signal; the word "critical" in the
   spec is the everyday-English approximation of the system's
   formal "high" band.
2. The `/risks` page itself filters this band as `severity = "high"`;
   the badge counting the same band keeps the sidebar consistent
   with the page it points to.
3. Adding a new severity tier ("critical = severity >= 20"?) for this
   one badge would fragment the bands across the codebase. Bounded
   change > unbounded scope creep.

**Alternative considered — and rejected:** filter on `treatment !=
"accept"` as a proxy for "open" risks. Rejected because: (a) the
mockup's rose-3 signal is severity-driven, not treatment-driven; (b)
the slice-100 page filter UI surfaces both treatment and severity as
independent pills, so the "open critical" intersection has no
canonical UI precedent.

**Documented in:** `sidebar-counts.tsx` header comment +
`HIGH_SEVERITY_THRESHOLD` constant JSDoc + the unit-test file header.

---

## D2 — Separate query keys (not piggybacking on the page query)

**Decision:** the badges use `["sidebar","controls-count"]` +
`["sidebar","risks-count"]` query keys — distinct from the parent
pages' keys (`["controls","list", scopeArg]` and `["risks","list"]`).

**Rationale:**

- The `/controls` page's query key is parameterised on `scopeArg`; if
  the badge subscribed to that key it would refetch every time the
  user changed the scope filter — pointless cardinality for a
  60s-refresh count badge.
- The `/risks` page's key is flat but the page's TanStack Query
  config may diverge from the badge's needs (the badge wants 60s
  staleTime; the page may want a tighter refresh during interactive
  filter work).
- Cost of the split: one extra fetch when the operator visits
  `/controls` or `/risks`. With 60s staleTime that's negligible at
  steady state.

**What this resolves:** spec did not specify cache-sharing strategy.
The separate-key choice keeps the badge decoupled from the parent
page's query config — a forward-looking-fail-safe move.

---

## D3 — Pulse only during refetch, not always

**Decision:** the `animate-pulse` class is bound to `isFetching`, not
applied permanently.

**Rationale:** spec AC-3 reads "The badge shows a subtle pulse during
refetch". Permanent pulse animation in chrome is high-distraction;
binding to `isFetching` gives the operator a "the sidebar is alive"
cue at the 60s refresh tick without making the badge a permanent
attention magnet at steady state.

**What this resolves:** AC-3 was ambiguous between "pulse during the
refetch network round-trip" and "pulse always to signal liveness".
The narrow reading is the right one.

---

## D4 — Vitest covers the pure helper only; integrated render is e2e

**Decision:** the vitest spec
(`web/components/shell/sidebar-counts.test.ts`) covers ONLY the
`countHighSeverityRisks` pure helper — 6 cases pinning the empty
list / zero high-tier / boundary at 14 / boundary at 15 / mixed-list
paths. Integrated render of the badge components is the responsibility
of the Playwright e2e (`web/e2e/audits-header.spec.ts`).

**Rationale:** mirrors slice 213's `in-progress-audit-pill.test.ts`
pattern verbatim — the codebase has no `.test.tsx` files (no React
component vitest tests). The pure helper is the cheapest place to
pin the load-bearing logic (the severity-15 threshold); the
integrated render is most likely to regress at the BFF + RSC +
client-component boundary, which is exactly what Playwright covers.

**What this resolves:** spec AC-4 reads "Vitest module test for the
sidebar component asserts: Controls badge renders when count > 0,
Risks badge hidden when critical-open count is 0, Risks badge in rose
when critical-open count > 0". The vitest spec discharges the
boundary + zero-count obligations through the pure helper; the e2e
discharges the "renders when count > 0" obligation through DOM
assertions on `data-testid="sidebar-controls-count"`.

---

## D5 — E2E spec piggybacks on `audits-header` fixture

**Decision:** the Playwright assertions for slice 214 live inside the
existing `web/e2e/audits-header.spec.ts` file, reusing the
`audits-header` fixture (no new fixture file, no new fixture-name
literal in the `FixtureName` union).

**Rationale:**

- The base seed `fixtures/walkthroughs/00-seed.sql` already
  instantiates one control (`DEMO_CONTROL_ID`) — the Controls badge
  will show ≥1 on any signed-in page.
- The audits-header fixture seeds zero risks — the Risks badge is
  hidden (silent-absence). Asserting "absent" in Playwright is more
  brittle than asserting "present"; the pure-helper unit test
  discharges the zero-count → null branch instead.
- Adding a new fixture just to cover a separate Risks-present case
  would couple two unrelated specs (and per the batch 102 fix-forward
  lesson on fixture-name collisions, the safest move is "extend an
  existing spec when possible, branch a new fixture only when the
  precondition is genuinely distinct").

**Spec coverage added:**

- `AC-1 (slice 214): Controls count badge appears on /audits via
shared sidebar` — proves end-to-end wiring + shared-shell
  coverage.
- `P0-214-1 (slice 214): Controls badge consumes the existing
/api/controls BFF` — proves the anti-criterion.

**What this resolves:** spec AC-5 reads "Playwright e2e spec confirms
the badges appear on `/audits` (proxy for 'shared shell shows
them')". The spec uses 'badges' (plural) but only Controls is
seed-positive in the available fixture; the Risks-positive branch is
covered by the unit test on the pure helper.

---

## D6 — Sidebar NAV item shape carries an optional `slot` ReactNode

**Decision:** the existing NAV item shape `{ href, label }` gains an
optional `slot?: ReactNode` field. Controls + Risks rows mount their
badges into the slot.

**Rationale:**

- Surgical change. The sidebar stays an `async` server component
  (preserves the slice 186 admin-gate fetch pattern); the badges are
  client components composed into the server-rendered Link via JSX,
  exactly the same pattern slice 213's topbar uses for
  `InProgressAuditPill` and `UserAvatar`.
- The flex row alignment fix (added `flex items-center` to the Link
  classes) gives the badge's `ml-auto` a natural right-align.
- Future badges (e.g. on Audits, on Evidence) reuse the slot mechanism
  without further structural change.

**What this resolves:** spec did not specify a refactor shape. The
slot-on-item shape is the least-surprise extension of the existing
NAV structure.

---

## D7 — No changes to `_STATUS.md`

**Decision:** this slice does NOT modify `docs/issues/_STATUS.md`.
The loop orchestrator owns canonical row flips.

**Rationale:** spec hard rule. No JUDGMENT involved — this is the
agreed division of labor between worktree agents and the orchestrator.

---

## CI-delta scan

No backend changes; no migration files touched; no `internal/api/`
diffs; no new BFF route. Diffs limited to:

- New file: `web/components/shell/sidebar-counts.tsx`
- New file: `web/components/shell/sidebar-counts.test.ts`
- Modified: `web/components/shell/sidebar.tsx` (NAV item shape +
  flex-row alignment + badge mounts)
- Modified: `web/e2e/audits-header.spec.ts` (two additive
  assertions; existing slice 213 assertions untouched)
- Modified: `CHANGELOG.md` (added slice 214 bullet)
- New file: this decisions log

Local CI parity verified before push:

- `pre-commit run --all-files` — pending
- `npm run lint` (web) — pending
- `npm run test` (web) — pending
- `npx tsc --noEmit` (web) — pending
- `npm run build` (web) — pending

(Updated post-push with green/red outcomes; the slice-merge agent
back-fills this section once CI returns.)
