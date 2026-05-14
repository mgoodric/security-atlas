# 040 — Program dashboard view — decisions log

Slice 040 is `Type: AFK` in its frontmatter — its acceptance criteria are
mechanically verifiable. But the slice surfaced four genuine build-time
judgment calls: two acceptance criteria name backend endpoints that do
not exist on main, one names a server-side capability (`sort`) that the
existing endpoint does not support, and the leading/lagging-separation
rule in `Plans/canvas/07-metrics.md §7.2` needed a read against the
operator-vs-board distinction. This log records them in the
JUDGMENT-slice format so the maintainer can re-evaluate the calls once
the dashboard is in real use against a real platform.

## Endpoint gap inventory (for follow-up backend slices)

Verified against `internal/api/` and `internal/api/httpserver.go` on
`main @ e2d7024` at slice-build time:

| Dashboard panel | Endpoint the panel wants | On main? | Slice 040 behavior |
| --------------- | ------------------------ | -------- | ------------------ |
| Recent drift | `GET /v1/controls/drift?since=7d` | yes (slice 016) | fully bound |
| Evidence freshness | `GET /v1/evidence/freshness` | yes (slice 016) | fully bound |
| Top risks aging | `GET /v1/risks?treatment=mitigate` | partial — filter exists, `sort=residual,age` does not | bound to the filter; sort gap noted in-panel |
| Upcoming items | `GET /v1/exceptions/expiring?within=30d` | partial — exceptions exist, no unified upcoming-rollup | bound to exceptions; other categories noted as a gap |
| Framework posture | `GET /v1/frameworks/posture` (per-framework coverage + trend) | no | endpoint-naming placeholder |
| Activity feed | `GET /v1/activity` (NATS event-stream archive read model) | no | endpoint-naming placeholder |

A follow-up backend slice should scope: (1) a per-framework posture
endpoint — coverage + freshness composite + 90-day trend per framework
version; (2) an activity / event-stream archive read endpoint backing
infinite scroll; (3) a `sort=residual,age` capability on `GET /v1/risks`
(or a dedicated `GET /v1/risks/aging` view); (4) optionally a unified
upcoming-rollup endpoint that merges expiring exceptions, policy-ack
expirations, vendor reviews, and audit-period milestones.

## Decisions made

### 1. Operator dashboard mixes leading + lagging signals on one screen

**Options considered:**

- **(A) Split leading (drift, freshness) and lagging (framework posture,
  risk-in-treatment) onto separate dashboards** per a literal reading of
  `Plans/canvas/07-metrics.md §7.2` ("We display them on separate
  dashboards — mixing them lets execs misread the program").
- **(B) Render all six panels on the one `/dashboard` screen** the
  mockup specifies.

**Chosen: (B).**

**Rationale.** §7.2's "separate dashboards" rule is scoped to the
*exec-facing board pack* (§7.5) — the sentence's stated failure mode is
"lets **execs** misread the program". Slice 040's `/dashboard` is the
**operator's** home screen: per `CLAUDE.md`, the primary v1 persona is
the solo security leader who runs the whole program, and per the issue
narrative this is "the home screen the primary persona opens every
morning". An operator triaging their own program needs the leading
signals (what is drifting, what is going stale) next to the lagging
context (where posture and risk stand) — that adjacency is the value.
The board pack (§7.5, a separate slice) is where the leading/lagging
discipline is enforced, and it produces pinned snapshots, not a live
operator view. `Plans/mockups/dashboard.html` — canvas-blessed reference
— itself puts all six on one screen.

**Confidence: high.** The §7.2 sentence names execs explicitly; the
mockup and the persona definition both point the same way.

### 2. Framework posture tiles (AC-2): endpoint-naming placeholder

**Options considered:**

- **(A) Return to the caller** — AC-2 needs per-framework posture data;
  no endpoint exists; surface it as a blocker.
- **(B) Scope-creep into backend Go** — add a posture-aggregate endpoint
  as part of this frontend slice.
- **(C) Ship the tiles as an endpoint-naming placeholder**, render the
  six framework slots as a data-free scaffold, mark AC-2 PARTIAL.

**Chosen: (C).**

**Rationale.** AC-2 reads "Framework tiles bind to real data per
framework; trend arrows reflect actual deltas." Codebase verification
found `internal/api/ucfcoverage` serves only **per-control** coverage
(`GET /v1/controls/{id}/coverage`) — there is no per-framework posture
aggregate and no coverage-trend handler anywhere in `internal/api/`.
This is the exact slice-041 / slice-060 situation: a `web/`-only slice
binds to a backend surface that has not shipped, so it renders an
endpoint-naming placeholder rather than blocking or fabricating data.
Option (B) couples a Go endpoint's review into a `web/`-only PR and is
explicitly out of scope. Option (A) treats a documented, recent pattern
as a novel blocker. Option (C) matches precedent: the
`framework-posture-panel` renders an `Alert` naming
`GET /v1/frameworks/posture` plus a six-slot scaffold so the layout
matches the mockup; no percentages or trend arrows are fabricated
(anti-criterion P0-1 honored).

**Confidence: high.** The endpoint absence was verified by grep across
`internal/api/`; the slice-041/060 precedent is explicit and recent.

### 3. Activity feed (AC-6): endpoint-naming placeholder

**Options considered:**

- **(A) Block on the missing endpoint.**
- **(B) Ship the feed as an endpoint-naming placeholder** with disabled
  filter chips, mark AC-6 PARTIAL.

**Chosen: (B).**

**Rationale.** AC-6 reads "Activity feed paginates with infinite scroll;
backed by NATS-driven event stream archive." A grep of `internal/api/`
finds no activity / events / feed handler and no NATS-archive read
model. Same precedent as decision 2: the `activity-feed-panel` renders
an `Alert` naming `GET /v1/activity` and the four filter chips
(All/Evidence/Controls/Approvals) render as a disabled, data-free
scaffold so the layout is faithful to the mockup. Infinite scroll is
wired when the endpoint lands. No activity rows are fabricated.

**Confidence: high.** Same pattern as decision 2; endpoint absence
verified; no data invented.

### 4. Top risks panel (AC-3): bind the `treatment` filter, skip the `sort`

**Options considered:**

- **(A) Block** — AC-3 names `GET /v1/risks?treatment=mitigate&sort=residual,age`;
  the `sort` part does not exist, so block.
- **(B) Sort client-side** — fetch all mitigate risks, parse
  `residual_score`, compute age, sort in the browser.
- **(C) Bind the `treatment=mitigate` filter that exists**, render the
  returned rows in the API's server order, and print an honest in-panel
  note that the residual/age ranking is a follow-up backend gap.

**Chosen: (C).**

**Rationale.** `internal/api/risks/handlers.go ListRisks` supports only
`treatment` / `category` / `methodology` filter params — there is no
`sort` param. `residual_score` is an opaque `json.RawMessage` blob on
the wire (`riskWire`), and there is no `age` / `age_in_treatment` field
exposed at all. Option (B) would have the client parse a blob whose
shape the frontend does not own and invent an ordering — that is
exactly the kind of fabricated-derived-value the slice's P0
anti-criterion forbids, and a client-side ranking could disagree with
whatever a future server-side `sort=residual,age` produces. Option (A)
blocks a whole panel on a missing *sort*, when the `treatment` filter —
the substantive part — works today. Option (C) binds what exists, shows
the real `treatment=mitigate` rows, and the `top-risks-sort-gap` note
states plainly that residual × age ranking needs a server-side
capability. AC-3 is recorded as PARTIAL.

**Confidence: high.** The handler's filter set and the opaque
`residual_score` wire type were read directly from
`internal/api/risks/handlers.go`.

### 5. Upcoming panel (AC-5): bind expiring exceptions, note the rest

**Options considered:**

- **(A) Block** — AC-5 wants exception expiration + policy ack + vendor
  review + audit period; not all have a rollup endpoint.
- **(B) Bind `GET /v1/exceptions/expiring`** as the one real source,
  render its rows, and add an honest in-panel note that the
  board-report / access-review / questionnaire / policy-ack categories
  need a unified upcoming-rollup endpoint.

**Chosen: (B).**

**Rationale.** `GET /v1/exceptions/expiring?within=30d` exists and is
bound. `GET /v1/audit-periods` and `GET /v1/vendors/burndown` also
exist, but there is no single endpoint that *merges* upcoming items into
one chronological feed, and policy-ack expiry is only available
per-policy (`GET /v1/policies/{id}/acknowledgment-rate`), not as a
tenant-wide "acks expiring" list. Rather than render four separate
partially-bound sub-panels — which fragments the mockup's single
coherent "Upcoming" panel — slice 040 binds the one clean source
(expiring exceptions) and the `upcoming-gap` note names what a unified
rollup endpoint would add. AC-5 is recorded as PARTIAL.

**Confidence: medium.** Binding exceptions-expiring is unambiguously
correct; the *presentation* choice (one note vs. several stub
sub-panels) is a judgment a maintainer iterating against real data
might want revisited — e.g. once the audit-period and vendor-review
data is wired in, the panel may want explicit per-category sections.

### 6. BFF shape: typed-client proxy, not the `forwardJSON` raw-path helper

The repo has two BFF patterns: `web/lib/api/bff.ts` `forwardJSON`
(audit cluster — forwards a raw upstream path, passes the upstream text
body through verbatim) and the slice-041 control-cluster shape (a thin
`route.ts` calling a typed client fn from `web/lib/api.ts`). Slice 040
uses the slice-041 shape because the dashboard panels want typed wire
shapes (`DriftReport`, `FreshnessReport`, etc.) the components consume
directly — `forwardJSON`'s verbatim-passthrough gives the client an
`unknown`. The four dashboard routes were otherwise identical, so the
shared bits are collapsed into one `web/app/api/dashboard/proxy.ts`
`dashboardProxy<T>(load)` helper — cookie read, 401 guard, typed call,
error-status passthrough — and each `route.ts` is a one-liner.

**Confidence: high.** Both BFF patterns are established; the
typed-client one is the right fit for typed-panel consumers, and the
single-helper collapse removes the near-duplication the simplify pass
flagged.

## Revisit once in use

- **Bind the framework posture tiles** (decision 2) once a
  `GET /v1/frameworks/posture` endpoint ships — the
  `framework-posture-panel` placeholder is the seam; add the client fn
  + a `dashboardProxy` route following the four already in this slice,
  and replace the six-slot scaffold with real tiles + trend sparklines.
- **Bind the activity feed** (decision 3) once an activity /
  event-stream archive read endpoint ships — wire infinite scroll
  (`useInfiniteQuery`) and activate the filter chips.
- **Replace the risks server-order list with a real ranking**
  (decision 4) once `GET /v1/risks` exposes `sort=residual,age` (or a
  dedicated aging view) — drop the `top-risks-sort-gap` note then.
- **Expand the upcoming panel** (decision 5) once a unified
  upcoming-rollup endpoint exists, or wire audit-period + vendor-review
  + policy-ack-expiry as explicit per-category sections — drop the
  `upcoming-gap` note then.
- **Install `@playwright/test`** and run `web/e2e/dashboard.spec.ts` for
  real — today it is a static `ifPlaywright` contract (the repo-wide
  pattern; installing the runner touches `web/package.json`, a spine
  file, and is a shared follow-up across the frontend slices).
- **Re-evaluate the leading/lagging layout** (decision 1) if the
  operator dashboard and the board pack ever visually converge — keep
  the §7.2 separation firmly on the board-pack side.

## Confidence summary

| Decision                                       | Confidence |
| ---------------------------------------------- | ---------- |
| 1 — operator dashboard mixes leading + lagging | high       |
| 2 — framework posture placeholder              | high       |
| 3 — activity feed placeholder                  | high       |
| 4 — risks: bind filter, skip sort              | high       |
| 5 — upcoming: bind exceptions, note the rest   | medium     |
| 6 — BFF typed-client proxy + shared helper     | high       |

The one `medium`-confidence call (5) is the top of the revisit list — it
is a presentation choice a maintainer iterating against real data may
want changed once the audit-period and vendor-review data is wired in;
it is not a correctness risk. Four ACs (2, 3, 5, 6) land PARTIAL pending
the follow-up backend endpoints inventoried above; AC-1, AC-4, AC-7 and
the evidence-freshness panel are fully bound to merged backends.
