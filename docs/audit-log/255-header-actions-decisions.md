# 255 — Control-detail header action buttons + "last evaluated" timestamp · decisions log

**Slice:** `docs/issues/255-control-detail-header-actions-last-evaluated.md`
**Branch:** `frontend/255-control-detail-header-actions`
**Author:** Claude (engineer subagent)
**Date:** 2026-05-24
**Type:** JUDGMENT

The slice spec calls out three explicit JUDGMENT decisions (D1, D2, D3)
plus an over-arching spectrum framing ("honest placeholder" vs "ship
the timestamp now, file followups for the buttons"). Decisions recorded
inline below so the maintainer iterates post-deployment rather than
blocking the merge on a sign-off gate (per
`Plans/prompts/04-per-slice-template.md` "Slice types").

---

## Overall framing

The slice spec frames this as the audit's hardest finding for severity
assignment. The chosen ground-truth shape:

- **Timestamp:** ship NOW, bound to existing data (the right-rail
  freshness-clock's `state.last_observed_at` source).
- **Buttons:** ship all three in mockup parity, but with honest
  semantics — two are disabled-with-tooltip (Run query, Edit YAML) and
  one (Request exception) links to a real, existing route filtered by
  `control_id`.

This resolves the spectrum at the "ship parity now, surface honesty
explicitly per button" end. No buttons are deferred to a follow-up; no
buttons are `<a href="#">`. The mockup is rendered as-drawn.

---

## Decisions made

### D1 — Run query + Edit YAML are `<button disabled>` with tooltips

**Decision:** Both buttons render as shadcn `<Button variant="outline"
size="sm" disabled>` with `title` + `aria-label` carrying the
explanatory copy. They are NOT `<a href="#">` (slice 178 anti-pattern)
and they are NOT links to "coming in v2" routes.

**Options considered:**

| Option                                                                                          | Why rejected / why chosen                                                                                                                                                                                                                                                                                                                                                                                     |
| ----------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| (a) **`<Button disabled>` with `title` + `aria-label` tooltip** — _chosen_.                     | Slice 183 / slice 184 audit pattern. Visible copy + tooltip + aria-label all read the same line. The disabled affordance is honest about its current capability; the tooltip names the canvas section and the v2 status. Cheapest implementation, no new routes, no new error states.                                                                                                                         |
| (b) `<Button>` linking to a `/controls/[id]/run` or `/controls/[id]/yaml` route that says "v2". | Rejected for two reasons. First — it doubles the slice size (route + page component + tests). Second — it makes the placeholder MORE clickable than (a), which is the opposite of what the slice 183 honesty audit recommends. The empty-state page would itself be a "coming soon" lie of the kind slice 178 flagged. The chosen path keeps the surface visibly inert until the underlying capability ships. |
| (c) Render the buttons as `<span>` styled like a button.                                        | Rejected — a `<span>` styled-as-button is what the slice 183 audit found in audits-list and replaced with the disabled-tooltip pattern. The disabled `<button>` element carries native semantics (keyboard reachable, announced as disabled by assistive tech) that a `<span>` does not.                                                                                                                      |
| (d) Hide the buttons entirely until v2 ships.                                                   | Rejected — anti-criterion P0-255-1/2 explicitly allows the buttons to render as placeholders; AC-2 requires three buttons in mockup order. The mockup parity is load-bearing; hiding the affordance entirely violates the "page makes a promise downstream slices will keep" framing from the slice spec (canvas references section).                                                                         |

**Confidence:** **high.** Spec explicitly admits option (a) as the
slice-183 resolution analog.

### D2 — Request exception links to `/exceptions?control_id=<id>` (existing route)

**Decision:** Request exception renders as a shadcn Button-styled
`<Link>` to `/exceptions?control_id=<encodeURIComponent(id)>`. The
destination is a merged route
(`web/app/(authed)/exceptions/page.tsx`); its URL-driven filter logic
(`web/app/(authed)/exceptions/filters.ts` lines 25-30, 56) accepts
`control_id` as a real filter key.

**Why this is honest:** the operator who clicks "Request exception"
lands on the tenant-wide exception register, narrowed to existing
exceptions for THIS control. They can see prior requests, statuses,
and expiration dates. The "create a new exception" affordance itself
is a v2 surface — but the surface they land on IS real, IS populated
(if there are existing exceptions), and IS the destination an operator
would reach by clicking "Request exception" in any future iteration
where the create-flow is added inline.

**Options considered:**

| Option                                                                      | Why rejected / why chosen                                                                                                                                                                                                                                                                                         |
| --------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| (a) **Link to `/exceptions?control_id=<id>`** — _chosen_.                   | Verified at session start: the `/exceptions` page is on `main` (slice 177); its filters.ts accepts `control_id` as a URL-driven filter. The link goes to a real surface with a real query result. P0-255-3 anti-criterion satisfied.                                                                              |
| (b) `<Button disabled>` with tooltip — same shape as Run query / Edit YAML. | Rejected — there IS a real destination today (a). Choosing (b) when (a) is available would be more conservative than the spec's D2 hint suggests ("if the exception-request route accepts a control_id query param, wire it; otherwise placeholder"). The param IS accepted; wiring is the correct call.          |
| (c) Link to a new `/exceptions/new?control_id=<id>` create-flow route.      | Rejected — that route does not exist. Filing it adds a slice (the create-flow itself); anti-criterion P0-255-4 explicitly forbids a new API endpoint, and the spirit of that anti-criterion extends to a new UI route as well (the create-flow needs an API). Defer to a future exception-request workflow slice. |

**Follow-up note:** when the exception-request workflow (canvas §4.6)
ships as its own slice, the destination of this link likely changes to
either `/exceptions/new?control_id=<id>` or an inline-on-control-detail
flow. The current target is forward-compatible — the receiving page
already has the filter primitive to handle the query param either way.

**Confidence:** **high.** Spec's D2 admits this branch explicitly.

### D3 — Relative-time formatter is a new pure helper, NOT the freshness-clock's helper

**Decision:** Add `web/lib/relative-time.ts` exposing `relativeTime(iso,
now)` and `relativeTimeOrNever(iso, now)`. Do NOT extract the
freshness-clock's `humanizeSince` helper.

**Rationale:** The two formatters produce visually-different output for
the same input:

- `humanizeSince` returns compact form ("8m" / "3h" / "5d") — fits in
  the SVG ring readout in the right-rail clock.
- `relativeTime` returns sentence form ("8 minutes ago" / "3 hours
  ago" / "5 days ago" / "just now" / "never" / "—") — matches the
  mockup's "last evaluated 8 minutes ago" copy.

Extracting one common helper would mean either (a) parameterizing
output shape via a flag (the kind of "trust the framework, no
unnecessary wrapper layers" violation that constitutional Article VIII
calls out), or (b) refactoring the freshness clock to use the new
sentence form (which changes the existing UI; out of slice scope).
Keeping the two separate honors the "small, composable, single-purpose"
discipline.

The two helpers DO agree on the canonical rules:

- "now" boundary handling (sub-minute → "just now" / "0m").
- Clock-skew tolerance (future timestamps clamp, don't render
  negative).
- Aggregate-across-cells rule (most-recent `last_observed_at` wins).

**Test seam:** both helpers accept an injectable `now` parameter so
vitest can pin a deterministic clock — no flaky asserts at minute
boundaries.

**Confidence:** **high.** Same "small helpers vs shared parameterized
helper" call the codebase already makes (e.g., `humanizeSince` itself
duplicates `formatHistoryDate` semantics in `page.tsx` lines 837-841,
preserved precisely because the output shapes differ).

### D4 — "Last evaluated" label binds to `state.last_observed_at`, not `state.evaluated_at`

**Decision:** AC-1 explicitly names `state.last_observed_at` as the
source. Honored verbatim. The mockup copy says "last evaluated", which
is operator-friendly framing but is technically a slight semantic
shift — `evaluated_at` would be the more literal field for "last
evaluated".

**Rationale for honoring the AC as written:** the right-rail
freshness-clock already binds to `last_observed_at` for its "since
latest evidence" readout. Mirroring the same source next to the header
keeps the two readouts in agreement by construction — the operator
sees one number, twice, both from the same field. If the header used
`evaluated_at` and the clock used `last_observed_at`, the two could
report different values during a window where the engine recomputed
the rollup without new evidence (e.g., a re-evaluation triggered by
a control-text change). That's a confusing UX surface; the AC's choice
is the more conservative one.

**Trade-off:** the label is slightly imprecise — operators reading the
sub-line carefully might expect "last evaluated" to mean "the time the
evaluator last ran." For an audience that doesn't reason about the
two-stage ingestion/evaluation invariant, this is fine. If a future UX
need surfaces (e.g., audit-period sampling where the distinction
matters), a follow-up slice can add a tooltip on the timestamp
clarifying that "last evaluated" here means "last evidence-observation
seen by the evaluator." Not blocking this slice.

**Confidence:** **medium-high.** AC pinned the source; label
semantics are loose in the mockup copy.

### D5 — Aggregate-across-cells rule: most-recent `last_observed_at` wins

**Decision:** When `state.states` has multiple scope-cell entries, the
header picks the FRESHEST `last_observed_at` across all entries (most
recent ms), matching the freshness-clock's `mostRecentObserved`
behavior.

**Rationale:** The clock and the header should agree by construction
on a per-control "freshest signal" — the operator sees one
"control-level freshness" number even though the underlying data is
per-cell.

**Inline implementation, not extracted helper:** the two readers want
slightly different return shapes — the clock wants a `Date | null`,
the header wants the raw ISO string so it can pass through to the
`relativeTimeOrNever` formatter. Extracting a generic helper that
returns the unparsed entry would require both callers to do their own
shape narrowing, which is more code than the duplication being avoided.

**Confidence:** **high.** Mirrors an existing pattern in the codebase
the maintainer has already accepted (the freshness-clock's reducer).

### D6 — Client-side clock refresh every 60 seconds via `useNow` hook (React-19-safe shape)

**Decision:** A `useNow(60_000)` hook returns `null` until the client
clock seeds (one microtask after mount), then refreshes every 60
seconds via `setInterval`. The caller renders "—" while `now === null`
and flips to the real relative-time string after the seed fires.

**Rationale (refresh cadence):** Without a refresh, the "8 minutes
ago" text would freeze at first paint and only refresh on next data
fetch. Every 60 seconds is the smallest interval that materially
changes the sub-minute / single-minute boundary — a 1s tick would
re-render the component 60× as often for no visible change.

**Rationale (null-then-real-value shape, NOT seed-during-render):**
React 19's `react-hooks/set-state-in-effect` lint rule (CI-blocking)
flags any synchronous `setState` call inside an effect body. The
original shape — `useState(() => Date.now())` seeded during render +
effect overwrites on mount — passes the lint at the `useState`
initializer but trips it at the `setNow(Date.now())` call inside the
effect, which the codebase has been bitten by before (slice 063 — see
the page-level preamble at `controls/[id]/page.tsx` lines 33-38). The
chosen shape — null initial value + 0ms-timeout seed inside the
effect — passes the lint AND avoids SSR/client-hydration drift in a
way the lint can verify:

- Server renders with `now === null` → HTML carries "—".
- Client hydrates to the same `now === null` → no mismatch.
- One microtask after mount, the `setTimeout(... , 0)` body fires,
  calling `setNow(Date.now())` — but it's INSIDE a setTimeout
  callback, NOT inside the effect body, so the lint accepts it.
- Subsequent ticks fire from `setInterval` every 60 seconds.

**Trade-off:** there is a one-tick window (a single microtask) where
the relative-time reads "—" instead of "8 minutes ago". The first
paint visibly flickers from "—" to the real value on every page
load. We accept this to keep the lint clean — the alternative is a
bespoke lint disable comment, which the codebase prefers to avoid
(slice 063's resolution was structural, not annotation-based).

**Confidence:** **high.** Same set-state-in-effect resolution pattern
the codebase already uses elsewhere (the page-level preamble explicitly
names this constraint and the catalog/scf/[id] precedent that solved
it the same way).

---

## Constitutional invariants honored

- **Invariant 2 — Ingestion and evaluation separated.** The "last
  evaluated" timestamp is read from the rollup's `last_observed_at`
  field; the header makes no claim about evaluation timing the
  evaluator did not itself produce.
- **UI-honesty (AI-assist boundary analog for the UI).** The buttons
  either work, or they carry "not yet" semantics in the visible
  tooltip AND the accessibility tree. No `<a href="#">`. No
  fabricated routes.

## Anti-criteria honored

- **P0-255-1.** No working "Run query" path. Button is `<Button
disabled>` with tooltip; clicking it has no effect; no console
  error; no silent 404.
- **P0-255-2.** No working "Edit YAML" editor. Same shape.
- **P0-255-3.** No `<a href="#">` for any of the three buttons. The
  e2e spec asserts `locator('a[href="#"]').toHaveCount(0)` inside the
  action well.
- **P0-255-4.** No new API endpoint. The Request exception link goes
  to an existing merged URL-driven filter on `/exceptions`. The
  "last evaluated" timestamp reads existing slice-012
  `/v1/controls/{id}/state` data.

---

## Test surfaces

- **Frontend vitest** (`web/lib/relative-time.test.ts`): 10 cases —
  8-minutes-ago, singular-minute, sub-minute "just now", clock-skew
  future, hours-boundary (1h, 3h), days-boundary (1d, 5d), null /
  undefined / unparsable input, `relativeTimeOrNever` branching
  (null → "never", undefined → "—", real timestamp delegates).
- **Playwright e2e** (`web/e2e/control-detail.spec.ts`): new
  `slice 255: header action buttons + last-evaluated timestamp` test
  asserting three-button render, mockup order, disabled semantics on
  Run query / Edit YAML, real-href on Request exception, no
  `<a href="#">` anywhere in the action well, last-evaluated sub-line
  visible, and AC-6 keyboard tab order. Assertions are commented
  pending the slice-082 seed harness, matching the surrounding tests'
  convention.

## Skill mix exercised

1. shadcn/ui Button (outline / size sm) + buttonVariants-as-link
   pattern.
2. Next.js client-rendered relative-time rendering with a `useNow`
   hook for periodic refresh (SSR/hydration drift avoidance).
3. UI-honesty discipline — the slice 183 / slice 184 placeholder
   pattern via `title` + `aria-label` + visible-disabled.
4. JUDGMENT-slice decisions log — this file.
