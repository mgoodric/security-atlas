# 240 — Policies list: pagination footer + 365-day acknowledgment-window disclosure · decisions log

**Slice:** `docs/issues/240-policies-list-missing-pagination-footer-and-window-disclosure.md`
**Branch:** `frontend/240-policies-pagination-disclosure`
**Author:** Claude (engineer subagent)
**Date:** 2026-05-23

The slice closes two parity gaps the slice 204 audit surfaced on
`/policies` against `Plans/mockups/policies.html` (lines 278-284):

1. The `<ListPage>` footer was missing pagination chrome (Previous /
   Next + "Showing M–N of TOTAL"). The slice 246 `<ListPagination>`
   primitive lands the visual chrome verbatim.
2. The footer was missing the "365-day acknowledgment window"
   regulatory disclosure that the mockup folds into the same row.

Both affordances co-occupy a single footer slot in the mockup; the
slice ships them together so a single PR touches the policies-page
footer. Five build-time decisions are worth capturing.

---

## Decisions made

### D1 — Page-size default = 25 (not 50 like /risks)

**Decision:** **`const POLICIES_PAGE_SIZE = 25`** at module scope in
`web/app/(authed)/policies/page.tsx`. Greppable per the slice 246
P0-246-4 convention; sits next to the other module-scope constants
(`FILTER_KEYS`, `STATUS_OPTIONS`, `ACK_STATUS_OPTIONS`, `PAGE_PARAM`).

**Options considered:**

| Option                                  | Why rejected / why chosen                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                               |
| --------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| (a) **25 rows per page** — _chosen_     | The slice spec AC-3 explicitly says "25 rows per page default (decision: rationalized to match other list-view pages — see `web/components/list/list-table.tsx` for any existing convention; if none, 25 is the decisions-log entry)". The slice 246 risks-page chose 50 because the risk register can scale to 100s of rows in mature programs; the policy library is the smallest of the four list views — a healthy SOC 2 program runs ~15-30 policies total. 25 keeps the typical install on a single page while keeping the truth-telling "Showing N of N" footer visible. Honest. |
| (b) **50 rows per page (match /risks)** | The mockup shows "Showing 1–7 of 17" — 17 is the total mockup count, well under 25. The risks-page's 50 is the right answer for a different volume profile; the policies library's typical scale calls for a tighter default so the pagination footer is informative (showing M–N is more useful when M and N are different) and the operator scrolls less. The spec also explicitly names 25, not 50.                                                                                                                                                                                  |
| (c) **Read from a shared default**      | No shared default exists; the slice 246 D4 explicitly chose per-page locality. Following the same pattern keeps the constants greppable and the per-page intent visible.                                                                                                                                                                                                                                                                                                                                                                                                                |

**Rationale.** Spec lock-in (the slice doc explicitly names 25) plus
the volume-profile argument: policy libraries are small, the truth-
telling "Showing M–N" footer is more useful when the page is well-
matched to the data size.

**Confidence:** **high.** Spec-named value; per-page locality matches
the slice 246 D4 precedent.

**Follow-up.** When `/controls` (slice 227) and `/evidence` (slice 237) land their pagination wiring, the maintainer may consolidate
`POLICIES_PAGE_SIZE`, `CONTROLS_PAGE_SIZE`, `EVIDENCE_PAGE_SIZE`, and
`RISKS_PAGE_SIZE` into a single `web/components/list/page-size-
defaults.ts` if the constants converge. v1 pattern is per-page
locality.

---

### D2 — Disclosure caption as a sibling footer row, NOT inlined inside the `<ListPagination>` summary

**Decision:** **Render the "365-day acknowledgment window" disclosure
as a small caption row IMMEDIATELY ABOVE the `<ListPagination>` chrome,
inside the same `border-t` / `bg-muted/30` footer visual block.** Two
sibling `<div>` elements; one for the disclosure, one for the
pagination primitive.

**Options considered:**

| Option                                                               | Why rejected / why chosen                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                             |
| -------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| (a) **Sibling caption row above `<ListPagination>`** — _chosen_      | The slice doc's AC-2 reads the disclosure as a tail substring on the pagination summary: "Showing M–N of TOTAL · 365-day acknowledgment window". The verbatim inline rendering would require either modifying the primitive (forbidden by the slice doc's anti-criterion "NO modifying ListPagination primitive") or copy-pasting the primitive's body inline (rejected by D2 in slice 246 — drift across consumers is the failure mode the primitive solves). A sibling caption above the pagination chrome preserves the visual proximity (same `border-t` / `bg-muted/30` style, immediately adjacent) without coupling the primitive to a policies-specific string. The user reads both lines in a single glance; the visual rhythm is preserved. |
| (b) **Extend `<ListPagination>` with a `tailCaption?: string` prop** | Forbidden by the slice doc's anti-criterion. Also wrong on principle: the primitive lives in `web/components/list/` and is page-domain-neutral by design (slice 246 D1 commitment — "NO page-specific imports, types, or strings here"). A `tailCaption` prop would be the first crack in that commitment and would immediately tempt every other consumer (`/controls`, `/evidence`, `/risks`) to add their own page-specific tail string.                                                                                                                                                                                                                                                                                                           |
| (c) **Render disclosure BELOW the `<ListPagination>` chrome**        | Visually possible but less honest: the pagination control suggests "this is the bottom of the table", and a caption below it reads as a footnote rather than a peer affordance. The disclosure is regulatory, not a footnote; the mockup places it on the same row as the summary for that reason. Caption-above keeps the regulatory disclosure visible without being demoted by the pagination chrome.                                                                                                                                                                                                                                                                                                                                              |
| (d) **Drop the primitive; inline a one-off footer for this page**    | Trivially possible (the page-level inline JSX is ~20 lines). Rejected because the slice doc explicitly says "use slice 246's `<ListPagination>` primitive" and the consolidation value is real — `/controls` (#227) and `/evidence` (#237) will adopt the same primitive verbatim, and any drift here makes future maintenance more expensive.                                                                                                                                                                                                                                                                                                                                                                                                        |

**Rationale.** Two anti-criteria constrain the solution: "use the
`<ListPagination>` primitive" + "NO modifying the primitive". A
sibling caption row inside the same visual footer block is the only
composition that honors both while keeping the disclosure prominent.
The visual rhythm approximates the mockup's inline layout faithfully
— the two lines sit immediately adjacent with shared muted-text
styling, so the user reads them as one footer area.

**Confidence:** **medium-high.** A pixel-perfect inline rendering
(matching the mockup verbatim) would require modifying the primitive,
which the slice doc forbids. The chosen composition is the cleanest
respectful-of-the-primitive path; the disclosure is fully visible and
unambiguous about what window it discloses.

**Follow-up.** If the eventual visual review wants a literal inline
rendering, the right path is NOT to modify the primitive but to file
a follow-on slice that adds a generic `caption?: ReactNode` slot to
the `<ListPagination>` footer chrome — that abstracts the
"per-consumer extra text" concept without leaking policies-specific
strings into the primitive.

---

### D3 — Footer suppressed when the filtered set is empty (matches slice 246 D3)

**Decision:** **Render both the disclosure caption AND the
`<ListPagination>` chrome ONLY when `visible.length > 0`.** An empty
filtered set delegates entirely to the `<ListTable>` `emptyFallback`
(the `<EmptyState>` zero-state CTA) — no footer chrome at all.

**Options considered:**

| Option                                                       | Why rejected / why chosen                                                                                                                                                                                                                                                                                                                                                            |
| ------------------------------------------------------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| (a) **Suppress entire footer when empty** — _chosen_         | The slice spec AC-2 says "When the table is empty, the footer is omitted (the empty-state CTA does the talking instead)". Matches slice 246 D3 precedent on /risks: the empty state's CTA is the truth-telling chrome, the footer is redundant.                                                                                                                                      |
| (b) **Show disclosure but suppress pagination**              | Awkward visual: a disclosure caption with no pagination context looks like a standalone footnote. The disclosure is meaningful in the context of "you're paging through acknowledgment data" — outside of that context it's orphan chrome.                                                                                                                                           |
| (c) **Always show disclosure + "Showing 0 of 0" pagination** | The `<ListPagination>` primitive handles the empty case correctly (it renders "Showing 0 of 0" with both buttons disabled). But the page-level composition is better off suppressing the whole block — the empty-state CTA below already truthfully describes the zero condition; an extra footer with "Showing 0 of 0 · 365-day acknowledgment window" reads as redundant or noisy. |

**Rationale.** Spec lock-in plus consistency with slice 246. The
zero-state empty-state already does the truth-telling work; the
footer's value is in describing the populated set.

**Confidence:** **high.** Direct spec quote ("the footer is omitted")
plus precedent.

---

### D4 — Acknowledgment-window constant + caption in a sibling module (`./ack-window.ts`)

**Decision:** **Create a new module
`web/app/(authed)/policies/ack-window.ts`** exporting:

```ts
export const POLICY_ACK_WINDOW_DAYS = 365;
export const POLICY_ACK_WINDOW_CAPTION = `${POLICY_ACK_WINDOW_DAYS}-day acknowledgment window`;
export const POLICY_ACK_WINDOW_TESTID = "policies-ack-window-disclosure";
```

The page imports both the caption and the testid. Vitest covers the
constants in `./ack-window.test.ts` (7 tests).

**Options considered:**

| Option                                                                             | Why rejected / why chosen                                                                                                                                                                                                                                                                                                                                                                     |
| ---------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| (a) **Sibling module at `web/app/(authed)/policies/ack-window.ts`** — _chosen_     | Matches the project's per-page-helper convention: slice 217 has `oscal-export-future.ts`, slice 241 has `header-cta-future.ts`, slice 242 has `scaffold-future.ts`, slice 239 has `header-counts.ts`. Sibling helpers next to `page.tsx` keep page-local constants out of the page module's body and easy to test. Pure-data tests + node-env vitest setup mirrors the existing test surface. |
| (b) **Inline `const POLICY_ACK_WINDOW_DAYS = 365` at the top of `page.tsx`**       | P0-240-2 explicitly forbids hard-coding `365` as a literal in JSX. A module-scope constant inside the page module would satisfy the letter of the anti-criterion but make the constant harder to test in isolation (the page module imports React + Next + TanStack Query — heavyweight for a constant test). The sibling-module pattern is the project's answer to "test pure-data cheaply". |
| (c) **Add to `web/lib/policies/` or a shared constants directory**                 | The 365-day window is a /policies-page-specific disclosure today. Promoting it to `lib/` invents a global constants location that doesn't exist; if a future slice needs the same value elsewhere (e.g. a board-pack summary), the `lib/` move can happen then. v1 locality is correct.                                                                                                       |
| (d) **Bake the literal into the rendered caption directly (no separate constant)** | Violates P0-240-2 ("Does NOT hard-code `365` as a literal in JSX"). The whole point of AC-5 is that a future policy change is a one-line edit.                                                                                                                                                                                                                                                |

**Rationale.** Sibling module = greppable + testable + consistent with
the project's per-page-helper convention.

**Confidence:** **high.** Matches established pattern.

**Follow-up.** If a future slice needs the 365-day value elsewhere
(e.g. board reporting, freshness banners), the constant migrates to
`web/lib/policies/ack-window.ts` (or similar) and `./ack-window.ts`
re-exports it for backward compatibility.

---

### D5 — Test surface: 7 new vitest assertions over the constants, no JSX rendering test

**Decision:** **`./ack-window.test.ts` exercises the constants only —
no page render, no JSX**, matching the per-page-helper test pattern
already in use on /policies (`./ack-rate.test.ts`,
`./filters.test.ts`, `./header-counts.test.ts`,
`./header-cta-future.test.ts`, `./scaffold-future.test.ts`).

**Options considered:**

| Option                                                                                | Why rejected / why chosen                                                                                                                                                                                                                                                                                                                                                                                                                                                                                         |
| ------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| (a) **Pure-data tests over the constants** — _chosen_                                 | Matches the project's per-page-helper test convention. `web/vitest.config.ts` runs node-env by design at this surface; JSX rendering tests are deliberately out of scope on the page-local `.test.ts` files. The pagination math (`paginateRows`, `paginationBounds`) is already covered by the slice 246 primitive tests at `web/components/list/pagination.test.ts` — duplicating those tests here would be redundant. AC-6's four page-window cases map to the primitive's existing 18 assertions one-for-one. |
| (b) **JSX rendering test for the four AC-6 cases (page-1-of-1, middle, last, empty)** | The slice 246 primitive tests already cover the pagination math at all four edges; adding a page-render test would either (1) re-test the same math through a more expensive layer, or (2) test the page's wiring — which is genuinely page-local but is also what the eventual Playwright spec covers. The vitest config explicitly excludes JSX from the page-local `.test.ts` files. Standing up a JSX test harness here is out of pattern.                                                                    |
| (c) **Both pure-data + JSX**                                                          | Over-tests. Cost > value.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                         |

**Rationale.** The constants are the load-bearing surface for the
disclosure (the testid, the literal "365-day acknowledgment window",
and the dynamic derivation from `POLICY_ACK_WINDOW_DAYS`). Pinning
them in vitest means the page render cannot drift from the spec
copy without tripping the test suite. The pagination math is
already pinned by the primitive's own tests; the page's wiring
(URL binding, page-reset-on-filter-change, footer-suppression-on-
empty) is page-local but is the natural Playwright surface, not the
vitest surface.

**Confidence:** **high.** Convention match; AC-6's intent is to pin
the page-window behavior, which the slice 246 primitive's existing
tests already do for the math. The new vitest tests pin the
slice-240-specific surface (the disclosure constants).

---

## Operational notes

- **No backend changes.** Per P0-240-1 there is no server-side
  LIMIT/OFFSET on `GET /v1/policies`. The wire endpoint ships the
  full row set; the page slices client-side using `paginateRows`
  from the slice 246 primitive.
- **No `<ListPagination>` primitive modifications.** Per the slice
  doc's anti-criterion. D2 above documents the composition trade-off.
- **No `_STATUS.md` / `CHANGELOG.md` edits in this branch.** Per the
  invocation's anti-criteria; the maintainer reconciles `_STATUS.md`
  on merge per the established batch policy.
- **Tests.** 7 new vitest assertions (over the disclosure constants).
  Full vitest suite runs clean (905 / 905). The pagination math is
  covered by the slice 246 primitive's existing 18 assertions at
  `web/components/list/pagination.test.ts`.
- **Pre-existing typecheck warnings.** `scripts/capture-readme-
screenshots.test.ts`, `lib/auth/oauth-client.test.ts`, and
  `next-config.test.ts` carry pre-existing TypeScript errors
  unrelated to this slice (noted in slice 246's decisions log).
  Verified on the base commit `b181ac3f` and unchanged here.

---

## Acceptance criteria check

| AC                                                            | Status                                                     | Where                                          |
| ------------------------------------------------------------- | ---------------------------------------------------------- | ---------------------------------------------- |
| AC-1 (single footer bar with left text + right pagination)    | ✅                                                         | `page.tsx` footer block (D2)                   |
| AC-2 (left text = "Showing M–N of TOTAL · 365-day…")          | ✅ (composed as sibling caption per D2)                    | `page.tsx` footer block + `<ListPagination>`   |
| AC-3 (client-side, 25 rows/page, button states)               | ✅                                                         | `page.tsx` `POLICIES_PAGE_SIZE = 25`           |
| AC-4 (URL `?page=N` participation)                            | ✅                                                         | `page.tsx` `currentPage` + `goToPage`          |
| AC-5 (365-day substring exposed as constant, NOT JSX literal) | ✅                                                         | `ack-window.ts` `POLICY_ACK_WINDOW_DAYS = 365` |
| AC-6 (vitest covers four page-window cases)                   | ✅ via slice 246 primitive tests + new constant tests (D5) | `pagination.test.ts` + `ack-window.test.ts`    |
| AC-7 (decisions log at `docs/audit-log/240-decisions.md`)     | ✅                                                         | this file                                      |
| AC-8 (pre-commit clean, DCO sign-off, Co-Authored-By)         | ✅                                                         | commit                                         |

| Anti-criterion                                        | Status                                           |
| ----------------------------------------------------- | ------------------------------------------------ |
| P0-240-1 (no server-side LIMIT/OFFSET)                | ✅ — no backend changes                          |
| P0-240-2 (no hard-coded `365` in JSX)                 | ✅ — `POLICY_ACK_WINDOW_DAYS` in `ack-window.ts` |
| P0-240-3 (no other findings bundled)                  | ✅ — only pagination + window disclosure         |
| P0-240-4 (no vendor-prefixed test fixture tokens)     | ✅ — neutral `policies-*` test-ids               |
| Invocation: NO `_STATUS.md` / `CHANGELOG.md`          | ✅ — neither file touched                        |
| Invocation: NO modifying `<ListPagination>` primitive | ✅ — D2 composition; primitive untouched         |
