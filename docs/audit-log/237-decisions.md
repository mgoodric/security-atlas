# 237 — Evidence list: cursor-paginated footer · decisions log

**Slice:** `docs/issues/237-ui-honesty-evidence-pagination-footer-missing.md`
**Branch:** `frontend/237-evidence-pagination`
**Author:** Claude (engineer subagent)
**Date:** 2026-05-23

The slice wires a cursor-paginated footer into `/evidence`. The
backend has shipped `next_cursor` end-to-end since slice 106; the UI
ignored it. Operators with >50 records saw only the first page with
no path to the rest. Spec defaults to Path A — cursor-paginated,
forward-only, with a client-side cursor stack to support Previous.

Five decisions landed during the build.

---

## D1 — Sibling primitive `<CursorPagination>` rather than extending `<ListPagination>`

**Decision:** **Create a new sibling primitive at
`web/components/list/cursor-pagination.tsx`**, exported through the
`@/components/list` barrel alongside slice 246's `<ListPagination>`.
The two primitives live side-by-side; consuming pages pick the one
that matches their upstream wire shape.

**Options considered:**

| Option                                                                                             | Why rejected / why chosen                                                                                                                                                                                                                                                                                                                                                                                                                                 |
| -------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| (a) **New sibling `<CursorPagination>` in `web/components/list/`** — _chosen_                      | Slice 246's `<ListPagination>` is fundamentally page-number / offset-based: it requires `currentPage`, `pageSize`, `totalCount` and slices an already-fetched array. The `/evidence` wire is cursor-based — no `currentPage`, no client-side slicing, no `totalCount` driving the math. Forcing one component to support both shapes muddies the contract for every future list consumer. A sibling primitive at the same shell layer is the right level. |
| (b) **Extend `<ListPagination>` with a `mode: "page" \| "cursor"` discriminator + per-mode props** | Hides the wire-shape distinction behind a runtime branch. Two consumers reading the same component for two unrelated semantics is the canonical "this should have been two things" smell. Anti-criterion P0-237-1 explicitly forbids "extending the slice-246 ListPagination primitive structurally"; this option was struck on the user's invocation directly.                                                                                           |
| (c) **Inline JSX inside `web/app/(authed)/evidence/page.tsx`**                                     | Cursor pagination is the canonical wire shape for any append-only ledger surface; the audit-log already paginates via cursor (infinite-scroll) and a future `/exceptions` / `/audit-log` list view will benefit from the same footer chrome. Inlining four times invites drift. Rejected.                                                                                                                                                                 |

**Rationale.** The list-shell barrel was designed (slice 098) so
cross-cutting list features land once. The slice spec narrative and
the user's invocation BOTH said: "if you need a different shape,
create a sibling component". `<CursorPagination>` and
`<ListPagination>` are two siblings of the same shell.

**Confidence:** **high.** The spec narrative and the user's invocation
agree; the implementation cost of a separate primitive is trivial.

---

## D2 — Cursor stack lives in React state; current cursor lives in the URL

**Decision:** **The CURRENT cursor (the keyset token for the page the
operator is looking at) goes in the URL as `?cursor=…` for shareable
deep-links. The cursor STACK (the history of cursors the operator
paged through to get here) lives in React `useState`, never
persisted.**

**Options considered:**

| Option                                                                  | Why rejected / why chosen                                                                                                                                                                                                                                                                                                                                                                                                                                                                             |
| ----------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| (a) **Current cursor in URL, stack in `useState`** — _chosen_           | Anti-criterion P0-237-2 says: "Does NOT persist the cursor stack across navigation (no localStorage, no URL state for the stack — cursors are session-scoped). The CURRENT cursor MAY appear in the URL for shareable deep-links; the stack is not shared." This option implements that policy verbatim.                                                                                                                                                                                              |
| (b) **Stack encoded in URL (e.g. `?cursor_stack=base64(JSON([...]))`)** | Explicitly forbidden by P0-237-2. Sharing a URL would also share the operator's entire navigation path; cursor opacity (the upstream emits base64 keyset tokens) means the URL would balloon as the operator pages deeper. Rejected.                                                                                                                                                                                                                                                                  |
| (c) **Stack in `localStorage`**                                         | Operator opens `/evidence` in tab A, pages to position 5, then opens `/evidence` in tab B → tab B inherits tab A's history with no UI signal. Surprising behaviour. Rejected; also forbidden by P0-237-2.                                                                                                                                                                                                                                                                                             |
| (d) **No URL cursor at all (current cursor also in React state)**       | Deep-linking and refresh would lose pagination state. The slice spec narrative implies deep-link support: "Spec narrative: cursor pagination uses the existing RLS-bound query path. No new endpoints. No client-side session state beyond a short cursor stack in component memory (not persisted; cleared on navigation away)." — and AC-5 says reaching the empty stack returns to "the unparameterized first page", implying a `?cursor=` parameterized state IS the non-default state. Rejected. |

**Rationale.** The split mirrors the wire's own asymmetry: the current
cursor is a deterministic key into the ledger (sharable, refreshable,
deep-linkable); the stack is path-dependent operator state with no
audit-replay value (session-scoped, no persistence). This is the
clean reading of P0-237-2.

**Edge case handled:** when the operator deep-links to
`/evidence?cursor=X` with an empty in-memory stack, the Previous
button stays ENABLED (the URL cursor itself is the signal that there
IS an earlier page). Clicking Previous in that state drops the URL
cursor and returns to the unparameterized first page, matching the
spec AC-5 reading.

**Confidence:** **high.** Spec lock-in. The implementation maps 1:1.

---

## D3 — Footer suppressed when the current page has zero rows

**Decision:** **Render the cursor footer only when `records.length > 0`.**
An empty page delegates entirely to the table's `emptyFallback` (the
`<EmptyState>` zero-state CTA with Clear filters + Set up a connector
buttons).

**Options considered:**

| Option                                                         | Why rejected / why chosen                                                                                                                                                                                                                                                            |
| -------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| (a) **Suppress footer when empty** — _chosen_                  | Slice 246 D3 set the same precedent for `/risks`. The reasoning carries over verbatim: rendering "Showing 0 records on this page" below an empty-state CTA banner is visually noisy and adds zero information. The empty-state CTA already tells the truth about the zero condition. |
| (b) **Always render footer; "Showing 0 records on this page"** | The footer's truth-telling chrome value is in pagination affordances — when there is no page to paginate, the affordance is redundant. Rejected.                                                                                                                                     |
| (c) **Render footer only when `next_cursor` is non-empty**     | Hides the footer on the very last page (when the operator has paged THROUGH the ledger). That removes the Previous button right when the operator might want it most. Rejected.                                                                                                      |

**Rationale.** Slice 246 D3 precedent + the spec implies it ("Visible
when `records.length > 0`" — AC-1 verbatim).

**Confidence:** **high.** Spec lock-in.

---

## D4 — Filter mutations reset both the URL cursor AND the in-memory stack

**Decision:** **Every filter mutator (`updateFilter`,
`updateSourceFilter`, `clearAll`) delete `cursor` from the URL AND
reset `cursorStack` to `[]`.**

**Options considered:**

| Option                                                                    | Why rejected / why chosen                                                                                                                                                                                                                                                                                                                                                                                          |
| ------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| (a) **Reset both on any filter mutation** — _chosen_                      | Cursors are keyset-bound to the FILTER window they were issued in. If the operator was on page 3 of a Result=pass narrowing and then changes Result=fail, the page-3 cursor for the pass window is meaningless against the fail window — the upstream may return a non-deterministic slice (or an error). Resetting both surfaces is the only safe choice. Mirrors slice 246 D2's `?page=` reset on filter change. |
| (b) **Keep cursor; let backend normalize**                                | Couples UI behaviour to backend tolerance of stale cursors. Rejected — the contract is "cursor is opaque keyset; treat as paired with the filter window that issued it".                                                                                                                                                                                                                                           |
| (c) **Only reset cursor when the filter actually narrows the result set** | Requires inspecting filter intent. Too clever; rejected.                                                                                                                                                                                                                                                                                                                                                           |

**Rationale.** Cursor opacity + filter binding leave option (a) as
the only correct choice. Spec AC-6 says: "Navigating to `/evidence`
from a different page resets the cursor stack to empty." This decision
extends the same intent to in-page filter mutations.

**Confidence:** **high.**

**Implementation notes:**

- The three mutators (`updateFilter`, `updateSourceFilter`,
  `clearAll`) each call `sp.delete(CURSOR_PARAM)` AND `setCursorStack([])`.
- The mutators ARE the only paths that change filter URL state, so
  the reset is comprehensive without needing a `useEffect`-keyed-on-
  filter-hash watcher.
- Pure navigation away (mount/unmount) resets the stack via React's
  `useState<string[]>([])` initializer — no special handling needed.
  This satisfies AC-6.

---

## D5 — Footer summary is "Showing N records on this page", NOT "Showing N of M"

**Decision:** **The cursor footer's summary line shows ONLY the
on-page record count.** The tenant-wide ledger total (`M`) is already
surfaced by slice 236's meta line ABOVE the table — re-rendering it
in the footer would print the same number twice.

**Options considered:**

| Option                                                     | Why rejected / why chosen                                                                                                                                                                                                                                                                                                                                                                                                                                 |
| ---------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| (a) **"Showing N records on this page"** — _chosen_        | The slice spec AC-2 says: "The footer renders `Showing N records` (or `Showing N of M records` once slice 236 lands — gated on `total` existence)." Slice 236 HAS landed; the meta line above the table already does the "of M" math. Surfacing the same M-of-N in two places adds zero information AND introduces a consistency risk if the rendering branches drift. Footer = page-navigation chrome only; the table-meta line = ledger-context chrome. |
| (b) **"Showing N of M records" (mirroring the meta line)** | Spec-compatible but doubles the rendered total. The slice 236 audit log explicitly chose to render the "of M" in the meta line; the footer should defer.                                                                                                                                                                                                                                                                                                  |
| (c) **No summary at all (Previous/Next chrome only)**      | Truth-telling-affordance loss: when the operator sees Previous DISABLED and Next ENABLED, "Showing 47 records on this page" tells them the upstream returned a full page (suggesting more pages exist) vs. "Showing 47 records on this page" with Next disabled telling them this is the tail. The count remains informative; only the "of M" is redundant.                                                                                               |

**Rationale.** Slice 236 already surfaces M; the footer's job is to
navigate, not to re-state ledger context. The footer's `recordCount`
prop binds to `records.length` — the current page's row count.

**Confidence:** **medium-high.** Could see arguing (a) vs (b);
landed on (a) because the meta line already wins the "where does
total live" question for `/evidence` specifically.

---

## Operational notes

- **No backend changes.** Per P0-237-3, the `/v1/evidence` handler is
  untouched. The wire shape already returned `next_cursor`; this slice
  is wiring-only.
- **No `_STATUS.md` / `CHANGELOG.md` edits in this branch.** Per the
  user's explicit invocation; maintainer reconciles `_STATUS.md` on
  merge per the established batch policy.
- **No seed top-up required for this slice.** The slice spec AC-7
  contemplated adding seed records if the dev seed didn't reach
  ≥3 pages. The decision is **defer** — the Playwright spec is
  quarantined behind the slice 082 seed-data harness (matching every
  other `/evidence` spec since slice 099), so the seed top-up question
  is for the harness slice to resolve, not this one. The unit-tested
  helper (`pushCursor` / `popCursor` round-trip) plus the typecheck +
  lint pass on the page wiring are the merge gates here.
- **Tests:** 9 new vitest assertions for `pushCursor` and `popCursor`
  (3 push cases, 4 pop cases, 1 full round-trip scenario, 1
  reference-identity assertion). 7 new quarantined Playwright
  assertions at the e2e level. Full vitest suite passes
  (907 / 907 — up from 898 baseline; +9 new tests).
- **Pre-existing typecheck warnings.** Three files carry pre-existing
  TS errors unrelated to this slice
  (`lib/auth/oauth-client.test.ts`, `next-config.test.ts`,
  `scripts/capture-readme-screenshots.test.ts`). They were present on
  base commit `b181ac3f` and the slice 246 audit log already noted
  them. Not introduced or affected here.

---

## Acceptance criteria check

| AC                                                                  | Status                                    | Where                                                    |
| ------------------------------------------------------------------- | ----------------------------------------- | -------------------------------------------------------- |
| AC-1 (footer mounted below `<ListTable>`, visible when records > 0) | done                                      | `app/(authed)/evidence/page.tsx`                         |
| AC-2 (summary line)                                                 | done (per-D5 — on-page count only)        | `components/list/cursor-pagination.tsx`                  |
| AC-3 (Previous/Next button disabled semantics)                      | done                                      | `app/(authed)/evidence/page.tsx` `hasNext`/`hasPrevious` |
| AC-4 (Next pushes cursor + re-issues query with cursor)             | done                                      | `app/(authed)/evidence/page.tsx` `goNext`                |
| AC-5 (Previous pops + empty stack returns to no-cursor)             | done                                      | `app/(authed)/evidence/page.tsx` `goPrevious`            |
| AC-6 (navigating to `/evidence` resets the stack)                   | done                                      | `useState<string[]>([])` initializer                     |
| AC-7 (Playwright spec)                                              | done (quarantined per project convention) | `web/e2e/evidence-pagination.spec.ts`                    |
| AC-8 (slice 204 audit finding F-204-E-5 resolved)                   | resolved by AC-1                          | n/a (audit re-run is a separate slice)                   |

| Anti-criterion                                                 | Status |
| -------------------------------------------------------------- | ------ |
| P0-237-1 (no offset-based pagination)                          | done   |
| P0-237-2 (no persisted cursor stack)                           | done   |
| P0-237-3 (no `/v1/evidence` handler changes)                   | done   |
| ISC-A1 (no `_STATUS.md` / `CHANGELOG.md` edits)                | done   |
| ISC-A2 (no structural changes to slice 246 `<ListPagination>`) | done   |
| ISC-A3 (no offset math in the new component)                   | done   |
| ISC-A4 (no localStorage / cookie / URL-encoded stack)          | done   |
| ISC-A5 (no `internal/api/` changes)                            | done   |
