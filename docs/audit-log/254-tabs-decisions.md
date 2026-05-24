# 254 — Control-detail tab strip · decisions log

**Slice:** [`docs/issues/254-control-detail-tab-strip.md`](../issues/254-control-detail-tab-strip.md)
**Branch:** `frontend/254-control-detail-tab-strip`
**Author:** Claude (engineer subagent)
**Date:** 2026-05-24
**Type:** JUDGMENT

The slice spec calls out three explicit JUDGMENT decisions (D1, D2,
D3) plus an over-arching framing question (how to honor P0-254-1
when the referenced primitive does not exist on `main`). Decisions
recorded inline so the maintainer iterates post-deployment rather
than blocking merge on a sign-off gate (per
`Plans/prompts/04-per-slice-template.md` "Slice types").

---

## Overall framing

The slice re-folders an already-shipped scrolling page into a
seven-tab strip. Anti-criterion P0-254-3 is load-bearing: the
Overview tab's data layout is preserved VERBATIM. Every TanStack
Query key, every data-testid, every error branch from slices
041 / 152 / 253 / 255 / 256 / 257 is unchanged. Six dedicated
panels host the per-tab deep-dives the mockup names; their content
is drawn from the same queries the Overview right-rail consumes
(no new endpoints, no new BFF routes, no new components beyond
the page-local panel functions).

This resolves the slice at the "ship pure re-folder, no platform
surface change" end of the spectrum. The richer per-tab deep-dives
the mockup hints at (e.g. paginated evidence on the Evidence tab,
per-edge inspector on the Mappings tab) are explicit v2 follow-ons.

---

## Decisions made

### D0 — Re-resolve P0-254-1 ("reuse `web/components/ui/tabs.tsx`") when the primitive does not exist

**Decision:** Inline the slice 044 tab-list pattern directly into
the page (no new primitive). The slice spec's claim that the
codebase "already ships a Tabs primitive" is a factual error —
`web/components/ui/tabs.tsx` is not present on `main`, and the only
existing tab usage (`web/components/audit/control-workspace.tsx`,
slice 044) inlines its own `role="tablist"` button row directly.
The page-local tab strip mirrors that established pattern: a
`role="tablist"` `<div>` of `role="tab"` buttons with
`aria-selected` + `aria-controls` + `aria-labelledby`. No new
shared primitive is introduced.

**Options considered:**

| Option                                                                                         | Why rejected / why chosen                                                                                                                                                                                                                                                                                                                                                                                                                                  |
| ---------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| (a) **Inline the tab strip in the page, mirroring slice 044's `ControlWorkspace`** — _chosen_. | Honors P0-254-1 in spirit ("does not introduce a new component primitive") by literally not introducing one. Mirrors the established codebase pattern (slice 044). Cheapest implementation. Trade-off: a future slice that wants tab strips on, say, `/risks/[id]` will need to either copy the pattern again OR file a dedicated "tabs primitive" slice. That trade-off is recorded here so the next reader does not duplicate the inlined logic blindly. |
| (b) Create `web/components/ui/tabs.tsx` as a shared primitive in this slice.                   | Rejected — literally violates P0-254-1 ("does NOT introduce a new component primitive"). Even though the spec ALSO says to reuse one, the anti-criterion is the harder constraint. The factual error in the preamble (the primitive doesn't exist) is recorded in this log; a follow-up "tabs primitive consolidation" slice can extract the pattern out of slices 044 + 254 once a third caller files for it.                                             |
| (c) Block on the spec's factual error and request an updated AC text.                          | Rejected — the spec is explicit that the implementer makes JUDGMENT calls (slice type: JUDGMENT). Recording the discrepancy + resolution in this log is the correct mechanism per `Plans/prompts/04-per-slice-template.md` "Slice types".                                                                                                                                                                                                                  |

**Confidence:** **high.** Slice 044 sets the precedent; the
inlined pattern is well-established and passes the a11y contract
(role=tablist + role=tab + aria-selected + aria-controls +
aria-labelledby).

### D1 — URL encoding for tab state: `?tab=<key>` (search param)

**Decision:** Tab state encodes as a search param
(`/controls/<id>?tab=evidence`), NOT a URL fragment
(`/controls/<id>#evidence`). The default tab (Overview) strips the
param entirely so the canonical URL on first visit stays clean
(`/controls/<id>` rather than `/controls/<id>?tab=overview`).

**Options considered:**

| Option                                          | Why rejected / why chosen                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                    |
| ----------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| (a) **`?tab=<key>` search param** — _chosen_.   | The spec's D1 note explicitly admits this trade-off: search params are "more SEO-friendly and survive some redirects better; hash is simpler." The codebase ALREADY has a search-param-driven tab pattern on `/exceptions` (slice 177 filters by `?status=...`) and `/calendar` (slice's mention). Next.js App Router's `useSearchParams` + `router.replace` is the established hydration shape. Hash fragments would require a separate `useEffect` to read `window.location.hash` (which Next strict mode lints against for SSR mismatch risk). The search param is the lower-cost choice. |
| (b) `#<key>` hash fragment.                     | Rejected — hash fragments are client-only (the server never sees them), so server-rendered pre-rendering can't pre-pick the active tab. The detail page is RSC-eligible in principle (currently `"use client"` because of TanStack Query, but the search-param shape leaves the door open). Plus the existing codebase pattern is search params; introducing a hash-fragment-driven tab on this one page would break the precedent.                                                                                                                                                          |
| (c) Sub-routes: `/controls/<id>/evidence`, etc. | Out of scope — anti-criterion P0-254-2 explicitly forbids this ("DOES NOT lazy-load tab content with separate routes — that's a larger refactor").                                                                                                                                                                                                                                                                                                                                                                                                                                           |

**Confidence:** **high.** AC-8 doesn't strongly favor either over
the other; the precedent + lower-cost call wins.

### D2 — Default tab is Overview when the param is missing or unrecognised

**Decision:** When `?tab=` is missing OR carries a value that
isn't one of the seven keys (`overview` | `evidence` | `mappings`
| `scope` | `policies` | `risks` | `history`), the page falls
through to Overview. The validator (`isTabKey`) lives in the
sibling `./tabs.ts` so vitest covers the narrowing.

**Rationale:**

The slice spec's D2 note explicitly recommends Overview unless
there's a strong reason to land on Evidence — and there isn't on a
fresh-install tenant (every count chip reads `—` because the queries
return zero data). Overview keeps the operator oriented to the
control's identity (KPIs + coverage + freshness) before they
navigate to per-tab deep-dives.

**Defensive behaviour for unrecognised values:** the page falls
through to Overview rather than rendering a "tab not found" error,
because (i) the only way a bad value lands in the URL is a hand-
typed or stale-bookmarked link and (ii) silently recovering is more
useful than blocking the page. The `isTabKey` validator is exact-
match + case-sensitive, so a future tab addition (e.g. `?tab=tests`
for a Q1 follow-up) needs the literal-union and `CONTROL_TABS`
arrays edited in lock-step — vitest pins the validator.

**Confidence:** **high.** Matches D2 + AC-8.

### D3 — Count chip renders full numbers with comma thousands separators

**Decision:** The count chip uses
`count.toLocaleString("en-US")` so `847` renders as `847` and
`1247` renders as `1,247`. Matches the mockup verbatim
(`Plans/mockups/control.html` line 144 — `847`). When the underlying
query is loading or errored, the chip renders `—` (a U+2014 em-dash,
not a hyphen) — never a placeholder integer (AC-2).

**Options considered:**

| Option                                              | Why rejected / why chosen                                                                                                                                                                                                                                                                                                                                                                                  |
| --------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| (a) **`count.toLocaleString("en-US")`** — _chosen_. | Spec D3 explicitly recommends "full numbers, formatted with comma thousands separator." The mockup's `847` literal would render identically through `toLocaleString`. The locale is hard-coded to `en-US` because the chip is a count, not a currency / date / number-localized field — keeping the format predictable across operator locales matches the rest of the page's font-mono numeric rendering. |
| (b) Abbreviated (`1.2k`, `12.3k`).                  | Rejected — D3 explicitly rejects this. The mockup uses full numbers everywhere; consistency wins.                                                                                                                                                                                                                                                                                                          |
| (c) No separator (`1247`).                          | Rejected — the mockup's `847` literal is fine without a separator below 1000, but the rendering MUST scale honestly to four-digit counts (e.g. a busy production tenant's evidence count). `toLocaleString` is the no-op below 1000 and the right thing above.                                                                                                                                             |

**Note on the Evidence chip's "5+" hint:** the Overview-tab's evidence
query is bounded to `EVIDENCE_STREAM_LIMIT` rows (5, mirroring the
mockup's stream card). When more records exist (`next_cursor !==
""`), the chip renders `5+` rather than fabricating a higher total
the backend hasn't returned (slice 253 honesty discipline). The
authoritative tenant-wide count lives on the `/evidence` list page
(slice 236's `total` field).

**Confidence:** **high.** Matches D3 + AC-2 + the slice 253 honesty
trail.

---

## Implementation shape

### Hook ordering + Suspense

`useSearchParams` requires a Suspense boundary under Next.js 16
App Router strict mode. The page is split into
`ControlDetailPage` (Suspense wrapper, default export) and
`ControlDetailPageInner` (the body). Pattern borrowed verbatim
from `web/app/(authed)/calendar/page.tsx`. The Suspense fallback
mirrors the page's `coverageQ.isLoading` skeleton so the page
shell is visually identical during the brief client boot.

### Tab → panel routing

Active tab is conditionally rendered — when the tab is Evidence,
ONLY the Evidence panel mounts; the other six panels are removed
from the tree. This differs from slice 044's audit workspace
(which uses `hidden`-class CSS toggles to keep all panels mounted
so in-progress draft state survives). Two reasons the control-
detail page does conditional rendering instead:

1. **No draft state.** The control-detail page has zero local
   mutable state — every value comes from TanStack Query. There's
   nothing to discard on tab flip.
2. **Cheap re-mount.** TanStack Query keeps the data in cache
   (24h default) so flipping back to a previous tab renders
   instantly from cache; no re-fetch.

The conditional rendering also means the panel-level vitest tests
can navigate to a tab via `?tab=<key>` and assert on a single
subtree without DOM hiding interference.

### Counts that read from in-flight queries

Each chip reads from its tab's underlying TanStack Query payload.
While the query is loading OR errored, the chip renders `—`
(AC-2). This is the same query the panel itself consumes — when
the operator switches to the Evidence tab the chip's count is
already populated from the prefetched Overview-tab call.

History has no chip per the mockup (line 149). The History tab's
panel reads the same `historyQ` payload the Overview right-rail
audit-log card uses.

---

## Files touched

```
web/app/(authed)/controls/[id]/page.tsx           # tab strip + panels
web/app/(authed)/controls/[id]/tabs.ts            # NEW — pure helpers
web/app/(authed)/controls/[id]/tabs.test.ts       # NEW — vitest sibling
web/e2e/control-detail-tabs.spec.ts               # NEW — e2e
docs/audit-log/254-tabs-decisions.md              # NEW — this file
CHANGELOG.md                                       # unreleased bullet
```

No platform / Go / SQL changes. No new BFF routes. No new shared
component primitives. Pure frontend re-folder.

---

## CI-delta scan

- **`page.tsx` shape change.** The page wraps `useSearchParams` in
  Suspense and the default export becomes a thin wrapper. Existing
  imports of `default` work unchanged. All data-testids from prior
  slices (slices 041 / 152 / 253 / 255 / 256 / 257) are preserved
  verbatim — `control-detail`, `control-detail-loading`,
  `control-detail-empty`, `control-detail-error`, `control-header`,
  `control-title`, `scf-anchor-pill`, `lifecycle-badge`,
  `kpi-strip`, `kpi-card`, `coverage-section`, `ucf-viz-section`,
  `evidence-stream-section`, `evidence-stream-list`,
  `evidence-stream-empty`, `evidence-stream-row`,
  `evidence-stream-view-all`, `evidence-stream-error`,
  `freshness-section`, `effective-scope-section`,
  `effective-scope-row`, `policies-section`, `policies-list`,
  `policies-empty`, `policies-error`, `policy-row`,
  `risks-section`, `risks-list`, `risks-empty`, `risks-error`,
  `risk-row`, `audit-log-section`, `audit-log-list`,
  `audit-log-empty`, `audit-log-error`, `audit-log-entry`. The
  slice 255 header-actions component is mounted unchanged (its
  internal testids are owned by that slice's test). The slice 256
  coverage-column testids are owned by `CoverageTable` (also
  unchanged).
- **Vitest deltas.** 15 new tests in
  `app/(authed)/controls/[id]/tabs.test.ts` (1084 → 1099 totals).
  All pre-existing tests pass unchanged.
- **Playwright deltas.** One new spec
  `web/e2e/control-detail-tabs.spec.ts` (8 tests). Spec is
  un-quarantined (live assertions, not commented) because it uses
  `page.route` mocks for the seven BFF endpoints, sidestepping
  the `control-detail.sql` fixture's stub shape. The mocks are
  shape-conformant to production responses — no fabricated fields.
- **Existing e2e specs.** None of the existing specs on the
  `/controls/{id}` page (`control-detail.spec.ts` (mostly
  commented), `control-detail-empty.spec.ts`,
  `control-detail-top-bar.spec.ts`) target the tab strip; they
  continue to assert their own surfaces. The new spec is additive.

Local CI parity verified before push:

- `pre-commit run --all-files` — pending (run before commit)
- `npm run lint` (web) — clean (2 pre-existing warnings in
  `scripts/capture-readme-screenshots.ts`, untouched by this slice)
- `npx tsc --noEmit` (web) — clean (pre-existing test-file
  errors in `lib/auth/oauth-client.test.ts`,
  `next-config.test.ts`, `scripts/capture-readme-screenshots.test.ts`
  are unchanged baseline)
- `npm run test` (web) — 1099 / 1099 passing
- `npm run build` (web) — green
- CHANGELOG bullet added under `## [Unreleased]` → `### Added`

---

## D-claim correction protocol

If any of the above D-claims are wrong on a re-read, file the
correction as a follow-up slice (not an amendment) — the engineer-
feedback-loop discipline (`feedback_engineer_d_decision_false_positive`)
applies here as everywhere else.
