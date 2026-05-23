# 215 — audits page title status tally · decisions log

**Slice:** `docs/issues/215-audits-title-status-tally-missing.md`
**Branch:** `frontend/215-audits-status-tally`
**Type:** `AFK` (slice type; decisions log included per user request for the audit trail)
**Author:** Claude (engineer subagent)
**Date:** 2026-05-23

The slice doc specifies the WHAT. This log records the small set of HOW
calls I made inline. The slice is mechanically verifiable (vitest
formatter + Playwright quarantined spec); these notes exist for the
maintainer's post-merge review rather than to gate the merge.

---

## D1 — Tally derived from `periods` (full set), not `visible` (filter-narrowed)

**Decision:** `statusTallyLabel(periods)` — feed the FULL period list
the TanStack Query returns, not the filtered `visible` set.

**Why:** the tally is the one-glance "this is the right tenant" check
the operator runs BEFORE they touch a filter. If the tally changed as
filters narrowed, it would become "this is the right narrow slice of
this tenant" — useful but a different affordance. The spec quote ("a
one-glance summary the operator uses to confirm 'this is the right
list' before scanning rows") makes the intent explicit.

**Alternatives considered:**

| Approach                      | Why rejected                                                                                                                                            |
| ----------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Tally on `visible`            | Re-frames the tally as a filter readout. The "Showing N of M" meta-text in the filter row already does that job and is the correct surface for it.      |
| Two tallies (full + filtered) | Adds noise to the header. The mockup shows one tally. P0-A1 explicitly forbids new endpoints; adding a second tally is a UX expansion of the same kind. |

**Confidence:** `high`. The spec quote is unambiguous about the intent
("right list", not "right slice of the right list").

---

## D2 — Status terminology: render `in_progress` (underscore), not `in progress` (space)

**Decision:** The renderer outputs the platform's enum string verbatim:
`in_progress`, `frozen`, `closed`, `open`. The mockup at
`Plans/mockups/audits.html` line 111 uses `1 in progress · 4 frozen ·
1 closed` (space-separated), but the live row pill at line 273 of
`web/app/(authed)/audits/page.tsx` already renders `p.status`
verbatim — so the row pill says `in_progress` (underscore).

**Why:** P0-215-2 ("DOES NOT invent statuses outside the platform's
enum") plus the slice's "AI-assist tone discipline" anti-pattern note
("uses status terms literally — no marketing copy like '5 audits in
flight'") both point at literal enum strings. Translating `in_progress`
to `in progress` is a presentation transform that diverges the tally
text from the pill text. The operator scans both in the same eye-line;
divergence is a tiny cognitive tax for no payoff.

**Alternatives considered:**

| Approach                                      | Why rejected                                                                                                                                                            |
| --------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Match mockup string literally (`in progress`) | Diverges from the row pill (`in_progress`) — same value, two different renderings on the same page. Mockup is an early-pass iteration; the enum is the source of truth. |
| Title-case the status ("In Progress")         | Tone-discipline violation. The mockup itself uses lowercase; the row pills are lowercase. Consistency wins.                                                             |

**Confidence:** `medium`. There is a real chance the maintainer prefers
the prettier "in progress" form once they see it in context. If so, the
fix is a single-line transform in `statusTallyLabel` (`status.replace("_",
" ")`) — easy to flip. Logged on the revisit list.

---

## D3 — Ordering: canonical four first (per AC-1), then unknowns alphabetically

**Decision:** `TALLY_STATUS_ORDER = ["in_progress", "frozen", "closed",
"open"]` — the order AC-1 prescribes. Statuses NOT in that list
(e.g. `planned` once the backend lifts the CHECK constraint, or any
future addition) render AFTER the canonical four, sorted
alphabetically.

**Why:** AC-1 specifies the canonical order. P0-215-2 says "only
statuses present in `periods[].status`" — which implies future
statuses (whatever the backend ships) are also fair game once they
appear in data. Alphabetical ordering for the tail is the cheapest
deterministic choice; "insertion order" depends on data shape and
would make tests flaky.

**Confidence:** `high`. The slice spec leaves the tail-order
unspecified, so any deterministic rule works; alphabetical is the
default expectation.

---

## D4 — `ListPage.titleAdornment` slot (additive prop, not a renamed `title`)

**Decision:** Added a new optional `titleAdornment?: ReactNode` prop to
`web/components/list/list-page.tsx` that renders inline with the H1
inside a `flex items-baseline gap-3` row, mirroring
`Plans/mockups/audits.html` lines 108-112.

**Why:** The mockup puts the tally on the SAME baseline as the H1 (not
in the subtitle slot below it). The existing `subtitle` slot renders
in a `<p>` below the H1 — wrong vertical position. The tally is not
part of the H1's text content (semantically it's a count, not a
heading), so embedding it inside the `<h1>` would also be wrong. A
named adornment slot is the smallest correct change.

**Alternatives considered:**

| Approach                                                     | Why rejected                                                                                                                                                                 |
| ------------------------------------------------------------ | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Promote `title: string` to `ReactNode`                       | Breaks all five list-view pages' prop types (controls/evidence/risks/policies/audits) for a single one-call cosmetic. Too broad a change for a single-slice surface.         |
| Put the tally in `subtitle`                                  | Mockup explicitly stacks them — tally on the H1 baseline, "Period-level index" subtitle below. Stacking them into one line erases the design intent.                         |
| Inline the H1 + tally in `audits/page.tsx` (no shell change) | Re-implements the page chrome inside one route; the shell would diverge from the other list views. Future list views that want a header adornment would each re-do the work. |

**Confidence:** `high`. The prop is opt-in; all five other list pages
continue to render exactly as they did. The shell now has a clean seat
for the next list view that needs a header adornment (e.g.
`risks/page.tsx` if a "5 critical · 12 high · …" tally lands later).

---

## D5 — Tally hidden in loading + error states

**Decision:** The `titleAdornment` is only passed to the populated
`<ListPage>` branch. The loading-skeleton branch and the error-alert
branch render the page chrome WITHOUT the tally.

**Why:** AC-2 says "when `periods.length === 0`, no tally renders" —
the loading branch has no periods yet (the query is pending), so it
falls under that condition. The error branch has no trustworthy
periods (the query failed) and showing a stale tally would be wrong
(or showing "0 of everything" violates P0-215-1). The empty-string
sentinel from the formatter handles this automatically because
`statusTallyLabel([])` returns `""` which the renderer treats as "do
not render".

**Confidence:** `high`. Falls out naturally from the formatter contract.

---

## Revisit once in use

Concrete items the maintainer should re-evaluate when real tenants exist:

1. **Mockup vs platform-enum spelling (`in_progress` vs `in progress`).**
   If operators read the tally and the row pill side-by-side and the
   underscore looks jarring, switch to the space form via a single
   transform in `statusTallyLabel`. Low-effort flip. (See D2.)
2. **Tail status ordering.** Once `planned` actually ships from the
   backend, see if the alphabetical-tail rule reads sensibly with the
   real status mix or if operators would prefer a different scheme
   (e.g. "active states first, then terminal states").
3. **Tally re-fetch on visibility change.** Today the tally counts
   reflect whatever the TanStack Query cache holds, which staleness-
   wise is bounded by the query's `refetchOnWindowFocus` config. If
   tenants run with very large period counts and the cache lag becomes
   visible, consider tightening the cache policy for `["audits",
"list"]` specifically — but only after real operator feedback.
4. **A11y test surface.** AC-3 requires the `aria-label`. If a future
   AC-pack lands for screen-reader smoke tests, fold the tally into
   that pack — today the assertion lives in the Playwright spec which
   is itself quarantined behind seed-data harness slice 082.

## Confidence summary

| Decision                                         | Confidence |
| ------------------------------------------------ | ---------- |
| D1 — full-set vs filtered-set tally              | `high`     |
| D2 — `in_progress` (underscore) literal          | `medium`   |
| D3 — canonical-first, alphabetical-tail ordering | `high`     |
| D4 — `titleAdornment` slot on `ListPage`         | `high`     |
| D5 — tally hidden in loading/error states        | `high`     |

Top of the revisit list: D2 (the only `medium`-confidence call).
