# Slice 218 — board-pack detail breadcrumb chain (build-time decisions)

> AFK-type slice. Engineer made the build-time calls per the
> slice-development workflow. The product still never publishes
> audit-binding artifacts without one-click human approval — this slice
> is chrome only (pure presentational change in the sticky export bar;
> no auth, no data-fetch, no RLS surface — matches the slice spec's
> threat-model verdict of "no-mitigations-needed").

---

## D1 — AC-2 decision: REMOVE the legacy `← All packs` link

**Decision:** The slice-043 `<Link href="/board-packs">← All packs</Link>`
at the left edge of `web/components/board-pack/export-bar.tsx` is
REMOVED. The new `<PackBreadcrumb>` lands in the same slot. The two
options the slice doc offered at AC-2 were (a) remove or (b) coexist;
remove was chosen.

**Why:**

- **Semantic redundancy.** The breadcrumb's first segment is
  `Board packs` → `/board-packs`. The legacy `← All packs` link points
  at the same route with a less-discoverable label. Shipping both
  surfaces would be the "coexist without redundancy" path the spec
  warns against — there's no "without redundancy" framing that holds
  here, the two affordances ARE the same affordance.
- **Industry convention.** Breadcrumbs are the canonical chrome for
  "where am I and how do I get back to the parent". Adding a separate
  back link next to one is a Vanta-y pattern; the v1 binary success
  test (CLAUDE.md "primary user") penalizes that kind of redundancy.
- **Audit-harness alignment.** Slice 178's `captureColdLinks`
  heuristic (`web/e2e-audit/lib/heuristics.ts`) does NOT flag the
  legacy link as a dead anchor — it points at a real route. So
  REMOVING it isn't required to close a HONESTY-GAP; it's the
  better-UX call. Vitest's new spec
  (`pack-breadcrumb-segments.test.ts`) + the Playwright spec
  (`board-pack-detail.spec.ts`) jointly pin the absence so a future
  PR can't quietly re-add it.

**Alternative rejected — coexist.** Could have shipped the breadcrumb
at the left edge AND moved `← All packs` to a secondary position
(e.g. above the breadcrumb, or as a small "Back" affordance). Both
shapes increase chrome density for zero new information. The export
bar is already crowded with three export-action buttons + an
Approve & publish CTA on the right edge.

**Confidence:** high. Spec offered the choice explicitly; this is the
cheaper, cleaner, more conventional path.

---

## D2 — Drop two of the mockup's three breadcrumb segments

**Decision:** The live `<PackBreadcrumb>` renders exactly 2 segments:
`Board packs` → period label. The mockup at
`Plans/mockups/board-pack.html` lines 27–33 ships a 3-segment chain
opening with `Sentinel Labs`, then `Board reports`, then the period.
Both leading segments are dropped.

**Why:**

- **`Sentinel Labs` is a fake tenant name.** The mockup is a static
  artifact and hardcoded `Sentinel Labs` as the seed-tenant fixture.
  The live app has no session-bound tenant name to render in the
  breadcrumb at slice 043's time — the active tenant context is
  conveyed by the post-login sidebar header (slice 005), not the
  page-level breadcrumb. Shipping a hardcoded "Sentinel Labs" or
  inventing a render path for an unloaded tenant name is the slice 218
  spec's P0-218-2 anti-criterion ("does NOT fabricate breadcrumb
  segments").
- **`Board reports` is a dead anchor.** There is no `/board-reports`
  route, no parent landing page distinct from `/board-packs`. The
  mockup invented the intermediate segment for visual depth. The
  slice-178 honesty heuristic `captureColdLinks`
  (`web/e2e-audit/lib/heuristics.ts`) explicitly flags anchors with
  no real destination as HONESTY-GAPs. Shipping a `<span>Board
reports</span>` (non-link, plain text) wouldn't trip that heuristic
  but would still mislead the operator into thinking there's a
  navigable parent. The honesty discipline is "every segment links to
  a real route OR is plain text DERIVED FROM PACK DATA" (P0-218-2):
  the period label IS pack data; `Board reports` is not.
- **Mockup parity is the secondary goal.** Slice 219 set the
  precedent: "Honesty > parity. The design artifact can drift one
  step ahead of code." The mockup is updated to match (drop both
  leading segments) — see D3.

**Alternative rejected — render a Tenant-name segment from session.**
We have a session-me API surface (slice 060's `getSessionMe`). Loading
the tenant display name and rendering it as the first segment is
_plausible_ but (a) adds a data dependency the export bar doesn't
have today, (b) the tenant-name segment would link to `/dashboard`
(the only real parent for "the tenant" route shape on main), which is
a stretch — the tenant doesn't OWN board packs more than it owns
controls or risks, and we don't put tenant breadcrumbs on those pages.
Consistent chrome density wins.

**Alternative rejected — render `Board reports` as plain text.** Per
the second bullet above. Even non-link, it implies a parent that
doesn't exist.

**Confidence:** high. Both leading segments fail the honesty test;
the spec's P0-218-2 names the exact pattern.

---

## D3 — Update the mockup to match (drop the same two segments)

**Decision:** `Plans/mockups/board-pack.html` lines 27–33 are edited:
the `Sentinel Labs` + `Board reports` segments are removed, replaced
with a single `Board packs` link (mirroring the live 2-segment
chain). An HTML comment in the file explains the divergence.

**Why:**

- **Slice 217 D2 / AC-A3 precedent.** Slice 217 updated
  `Plans/mockups/audits.html` line 116 in the same PR as the live UI
  change, so a future audit-harness run against the mockup-vs-live
  diff doesn't flip on the touched chrome. Same shape here.
- **Mockup drift trap.** Leaving the mockup with the 3-segment chain
  means slice-178's `mockup-diff` step would flag the live 2-segment
  chain as a MOCKUP-STALE gap on the next run. We close that loop in
  one PR.
- **An HTML comment documents the divergence reason.** Future readers
  of the mockup see why two segments are missing relative to the
  iteration-1 design.

**Alternative rejected — leave the mockup unchanged.** Slice 219 did
this for the Author cell, and that _was_ the right call there (the
mockup author was a real-looking value that future Option-B work
could legitimately reinstate). Here, both mockup segments are
permanently dead — no future option recovers them. Editing the
mockup is the right call.

**Confidence:** high. Mirrors the established slice 217 paired-edit
pattern.

---

## D4 — Hoist `periodLabel` out of `pack-header.tsx` into a shared module

**Decision:** Move `periodLabel(periodEnd)` from `pack-header.tsx`
(where it was a private function in slice 043) into the new
`web/components/board-pack/pack-breadcrumb-segments.ts` module. Export it
from there. Re-import it into `pack-header.tsx` (single source of
truth).

**Why:**

- **The slice spec says it.** AC-1 reads "the current pack's period
  label (e.g. `Q1 2026`, derived from `periodLabel(periodEnd)` already
  exported by `pack-header.tsx`)". The spec expected it to be exported.
  It wasn't (it was a private function). Hoisting into a shared module
  closes the spec mismatch and gives the breadcrumb a clean import.
- **Two surfaces, one implementation.** The cover header
  (`pack-header.tsx` line 61) renders `{periodLabel(periodEnd)} Board
Pack` as the page H1; the breadcrumb's trailing segment renders the
  same label. Keeping two copies in two files is the kind of small
  duplication that drifts. One canonical helper in a `.ts` module is
  the slice 222 (posture-coverage-caption.ts) + slice 219
  (pack-header-meta.ts) precedent for this directory.
- **Testability.** The new module is node-env-vitest-friendly (no
  JSX), so the pure logic can be unit-pinned. The
  `pack-breadcrumb-segments.test.ts` file covers `periodLabel` directly +
  via `packBreadcrumbSegments` — a regression in the quarter
  detection trips immediately.

**Alternative rejected — export `periodLabel` from `pack-header.tsx`
in place.** Mechanically cheaper but leaves the helper in a `.tsx`
file. That's awkward for the breadcrumb's `.ts` peer (it would have
to import from a `.tsx` to get a pure function). The shared module
pattern is what every other helper in this directory follows.

**Alternative rejected — duplicate the function into
`pack-breadcrumb-segments.ts`.** Drift trap. Two copies, two test files, one
silent break when somebody adds Q5 (kidding) or fiscal-quarter offset.

**Confidence:** high. Direct slice 219 / 222 precedent.

---

## D5 — Vitest covers the pure logic; Playwright covers the DOM

**Decision:** Vitest spec at `pack-breadcrumb-segments.test.ts` pins:
segment count, parent label + href + testid, trailing-segment shape

- testid + no-href, no-fabricated-segments regression (no `Sentinel
Labs`, no `Board reports`), `periodLabel` quarter parsing + fallback.
  Playwright spec at `board-pack-detail.spec.ts` (NEW file) asserts the
  DOM contract — breadcrumb visibility, aria-label, parent link target,
  trailing segment plain-text + aria-current="page", no `← All packs`
  text, no fake tenant-name text. Playwright spec is quarantined behind
  the slice 082 seed harness like the rest of the e2e directory.

**Why:**

- **Vitest config is node-env, no JSX.** Per `web/vitest.config.ts`
  (slice 069 P0-A3), `@testing-library/react` is NOT a dependency at
  this workspace. The slice 217 / 219 / 222 pattern (pure-logic helper
  module + vitest for the helper + Playwright for the DOM) is the
  established precedent.
- **No existing board-pack-detail Playwright spec.** Slice 219 chose
  NOT to spin one up (D2 there says "creating one would mean writing
  seed fixtures, auth, navigation, and waiters for a page the audit
  harness already partially covers"). Slice 218 spec AC-3 EXPLICITLY
  requires one (`web/e2e/board-pack-detail.spec.ts or equivalent`).
  So we file the spec — quarantined behind the slice 082 seed harness
  like every other quarantined e2e spec — and 219's regression guard
  (no `Author` label) becomes a candidate addition once the spec is
  un-shimmed.
- **AC-3 demands an asserts-the-segments-render + parent-link-routes
  test.** Both are written. The third assertion (parent link routes
  to `/board-packs`) is a `.click()` + `expect(page).toHaveURL` pair,
  which is the canonical Playwright assertion shape for that
  contract.

**Alternative rejected — install `@testing-library/react` + jsdom and
write a JSX render test.** Out of scope. Slice 069's P0-A3 is a
project-level commitment, not a per-slice negotiable.

**Confidence:** high. Exact pattern slice 217 / 219 used.

---

## D6 — `cursor-pointer` styling: don't reuse `linkButtonClasses`

**Decision:** The breadcrumb's parent-segment link uses lightweight
inline Tailwind utilities (`text-xs text-slate-500 hover:text-slate-700`)
instead of the `linkButtonClasses` shadcn-button-mimic from
`export-bar.tsx`.

**Why:**

- **Breadcrumb segments aren't buttons.** They're inline navigation
  text. The shadcn-button visual reads as primary chrome; breadcrumb
  segments are secondary chrome — the conventional muted-text +
  chevron pattern is what the mockup ships (`text-xs text-slate-500`)
  and what users recognize as "you are here".
- **Mockup parity.** The mockup at lines 27–33 uses
  `class="flex items-center gap-1 text-xs text-slate-500"` on the
  `<nav>` and `hover:text-slate-700` on the link. We mirror it exactly
  (sans the dropped segments).
- **Adjacency clarity.** The right-edge of the export bar carries
  THREE button-shaped affordances (Export PDF / Copy Markdown /
  Approve & publish). Making the breadcrumb's parent segment look
  like a button competes with those for visual weight.

**Confidence:** high.

---

## D7 — Anti-criteria scan

- **P0-218-1 (does NOT modify board-pack data wire shape or backend).**
  Verified: no Go file touched, no `proto/` touched, no `migrations/`
  touched, no `internal/board/*.go` touched. The only props change is
  the new `periodEnd: string` parameter on `<ExportBar>` (consumed
  from `pack.period_end`, which already flows into the page from
  `getBoardPack(id)`). ✓
- **P0-218-2 (every segment links to a real route OR is plain text
  derived from pack data).** Verified: the two segments are
  `Board packs` (links to the existing `/board-packs` route) and the
  period label (derived from `pack.period_end`). No fabricated
  segments. Vitest's `does NOT fabricate a tenant-name segment`
  - the Playwright `no fabricated tenant-name segment` assertions pin
    the regression guard. ✓
- **P0-218-3 (does NOT touch export buttons or publish flow).** The
  right-edge of `ExportBar` (the three buttons block) is byte-unchanged
  apart from the `linkButtonClasses` constant declaration position.
  `PublishFooter` is not touched. The page-level `onMutated` /
  `approvalState` logic is not touched. ✓

**Confidence:** high.

---

## Revisit once in use

- **R1 — Per-tenant breadcrumbs (if a future slice surfaces tenant
  name in the chrome).** If slice 005's sidebar adopts a "current
  tenant" header pattern that we want to mirror inline on detail
  pages, the breadcrumb gains a third (leading) segment. The
  precondition is a real session-bound tenant name + a real parent
  route (`/dashboard` or per-tenant landing). When both exist, the
  shape is `Tenant > Board packs > Period`. Until then, the
  2-segment shape stands.
- **R2 — Period label for non-quarter dates.** `periodLabel` falls
  back to the raw YYYY-MM-DD when `periodEnd` isn't a calendar-quarter
  end. If a future slice adds non-quarterly board packs (e.g.,
  monthly), this fallback reads awkwardly in the breadcrumb (`Board
packs > 2026-05-15`). The right fix is a richer label function (e.g.
  `May 2026`) — but only when the data shape exists; today the system
  emits quarterly packs only.
- **R3 — Mockup parity refresh.** When iteration-2 mockups land, the
  HTML comment in `board-pack.html` becomes obsolete and the file
  should be rewritten from scratch. The 218 comment is a stopgap.
- **R4 — Slice 178 audit harness manifest.** `web/e2e-audit/mockup-spec.json`
  currently maps `/board-packs` (the list) to `board-pack.html`, NOT
  `/board-packs/[id]` (the detail). AC-4 says "the audit harness does
  not surface HONESTY-GAP on /board-packs/[id]" — verified by inspection:
  the route is not registered in the manifest, so the diff step doesn't
  run against it at all. If a future slice REGISTERS `/board-packs/[id]`
  in the manifest, this slice's testids
  (`pack-breadcrumb` + `pack-breadcrumb-segment-parent` +
  `pack-breadcrumb-segment-current` + `pack-breadcrumb-sep`) should be
  declared in `allowedExtraTestIds` so the diff step treats them as
  expected. Not blocking on slice 218.

---

## Verification

- **AC-1.** The detail page top bar renders a breadcrumb chain with
  EXACTLY two segments: `Board packs` (linking to `/board-packs`) and
  the period label derived from `periodLabel(periodEnd)`. The spec
  says "at least two" — we ship exactly two (D2). Vitest pins
  segment count + parent href + trailing-segment plain-text shape. ✓
- **AC-2.** The slice-043 `← All packs` link is REMOVED (D1).
  Playwright asserts the link text no longer renders. ✓
- **AC-3.** Playwright spec `web/e2e/board-pack-detail.spec.ts`
  asserts (a) breadcrumb segments rendered, (b) parent link routes
  to `/board-packs` via `.click()` + URL assertion. Quarantined behind
  the slice 082 seed harness, body preserved as a reviewable
  contract. ✓
- **AC-4.** Slice 178 audit harness does not register
  `/board-packs/[id]` in `web/e2e-audit/mockup-spec.json` — verified
  by inspection. The new testids on the live page therefore can't
  surface a HONESTY-GAP on a route the harness doesn't traverse. R4
  tracks the follow-on if the route is ever registered. ✓
- **P0-218-1 (no backend / wire-shape change).** Verified above. ✓
- **P0-218-2 (no fabricated segments).** Two segments, both honest.
  Vitest + Playwright pin the regression guard. ✓
- **P0-218-3 (no export-button or publish-flow touch).** Verified
  above. ✓
- **Local CI parity** (per `feedback_local_ci_parity.md`):
  - `npm run lint` — clean across touched files.
  - `npm run test` — `pack-breadcrumb-segments.test.ts` 11 / 11 green.
  - `npx tsc --noEmit` — zero errors in touched files.
  - `npm run build` — clean.
  - `pre-commit run --all-files` — clean.
