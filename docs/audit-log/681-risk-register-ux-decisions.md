# Slice 681 — Risk register UX decisions log

JUDGMENT slice. Two risk-surface UX findings from the 2026-06-10 demo-tenant
audit: ATLAS-039 (no sortable columns + no per-risk drill-in) and ATLAS-036
(ambiguous sidebar "Risks N" badge). This log records the subjective calls,
what to revisit once real operators use it, and a confidence per decision.

- detection_tier_actual: none
- detection_tier_target: none

(No bug surfaced during the build. The slice is a feature/enhancement; the new
logic shipped with unit + e2e coverage from the first pass.)

---

## Decisions made

### D1 — Sorting is CLIENT-SIDE, not a `?sort=` wire round-trip (AC-1)

- **Options considered.**
  - (a) Client-side: sort the in-memory `GET /v1/risks` list (the list view
    already fetches the full list and filters + paginates it client-side).
  - (b) Server-side: extend the platform's `?sort=` param. The handler already
    has `ParseListSort` (`internal/risk/store.go`) — but it exposes exactly ONE
    ordering today (`residual,age`) and is consumed by the dashboard "top risks
    aging" panel, not the list view.
- **Chosen: (a) client-side.** The list view's established pattern (slices 100 /
  244 / 246) is "fetch the whole list, narrow + page it in the browser." Sorting
  joins that pattern with zero wire change, zero sqlc/migration/`-p 1`
  integration cost, and no new column on the response. Option (b) would mean
  extending the wire enum to three new sort keys × two directions, an
  integration test for each, and a server round-trip on every header click — all
  to reproduce an ordering the client already does for free over an in-memory
  array the page has already paid to fetch. The `ListTable` primitive's own
  header note ("Sort + paginate stay out-of-scope for v1 — file as spillover")
  is now satisfied at the page layer, where the data already lives.
- **Shape.** `web/app/(authed)/risks/sort.ts` is the pure comparator + the
  asc/desc toggle math (mirrors the `filters.ts` / `count-label.ts` per-page
  pure-seam convention). The page owns the URL `?sort=<key>:<dir>` state; the
  default (severity desc) is treated as "no param" so the register URL stays
  clean until a non-default sort is chosen. Sort is applied AFTER filtering and
  BEFORE pagination so the rendered page slice is correctly ordered.
- **Confidence: high.** Pattern-matched to three prior list-view slices; the
  comparator is unit-pinned (19 vitest cases) and e2e-pinned (4 order
  assertions).

### D2 — Build the read-only `/risks/{id}` detail; do NOT keep deferring it (AC-2)

- **Options considered.**
  - (a) Build a minimal read-only `/risks/{id}` detail and link the title to it
    (the slice's stated default lean).
  - (b) Keep the detail deferred but remove the misleading "future slice"
    framing and leave only "View in hierarchy".
- **Chosen: (a) build it.** This is the EXACT situation slice 672 already
  resolved for the identical ATLAS-024 policy finding ("titles linked to a route
  that did not exist"): the backend read API exists and is RLS-scoped, the data
  exists, so the honest fix is to build the page, not to keep apologizing for its
  absence. Here `GET /v1/risks/{id}` (`GetRisk`) already serves the full risk +
  an optional residual breakdown, RLS-tenant-scoped via `store.Get` (a
  cross-tenant id resolves to 404). So the detail page is a pure
  consume-what-exists change — no backend code at all.
- **Shape.** BFF `/api/risks/[id]/route.ts` (mirrors `/api/policies/[id]`),
  `getRisk` + `fetchRiskDetail` lib fetchers, and a client detail page that
  mirrors the slice-672 policies detail (loading skeleton, 401→/login,
  404→in-shell `notFound()`, destructive Alert otherwise). Read-only — no edit /
  delete / link affordances (slice 681 anti-criterion). The slice-185 "future
  slice" banner is removed; the per-row "View in hierarchy" link is RETAINED
  (it sits alongside the title drill-in, not instead of it) so the org-tree
  scoping workflow is unchanged.
- **Coordination with "View in hierarchy".** The title now answers "show me
  THIS risk" (read-only detail); "View in hierarchy" still answers "show me this
  risk IN the org tree." Two distinct intents, two distinct affordances — the
  detail page itself also carries a "View in hierarchy →" link so the workflows
  compose.
- **Confidence: high.** Directly pattern-matched to slice 672; the detail route
  is in the production build manifest and e2e-pinned (title→detail nav).

### D3 — Disambiguate the badge with a marker glyph + label, not a re-count (AC-3)

- **The finding.** The rose "10" reads as a TOTAL risk count (the register had
  20+ rows). It is actually the count of HIGH-SEVERITY (`severity >= 15`) risks.
  The `aria-label` already said "N high-severity risks" (correct for screen
  readers), but nothing visual or on-hover conveyed it to a sighted operator.
- **Options considered.**
  - (a) A leading "▲" marker glyph + a `title` (hover) attribute matching the
    `aria-label`, both saying "N high-severity risks".
  - (b) Change what the badge counts (e.g. show the total). REJECTED — the slice
    anti-criterion forbids changing the high-severity threshold or what drives
    the badge; only its presentation.
  - (c) A text suffix like "10 high". REJECTED — the sidebar slot is tight and a
    word suffix wraps badly at the narrow rail width.
- **Chosen: (a).** A small "▲" reads as "elevated / attention", not "tally", so
  the rose count no longer looks like a neutral total; the matching
  `title`/`aria-label` make the meaning reachable by both sighted-hover and
  assistive tech. Presentation only — the `HIGH_SEVERITY_THRESHOLD = 15` and the
  `countHighSeverityRisks` logic are untouched.
- **Live cadence (the "is it live?" question).** The badge IS live. The
  underlying `RisksCountBadge` uses TanStack Query with `refetchInterval: 60_000`
  (slice 214), so adding a high-severity risk surfaces in the badge within one
  60-second refresh tick. The audit's "it didn't update after creating a 21st
  risk" observation is consistent with that cadence (the 21st risk was likely
  not high-severity, OR the observation was inside the 60s window). The refresh
  cadence is documented here and in the component; it is intentional (a 60s
  low-priority poll, P0-214-3) and unchanged.
- **Confidence: high** on the threshold-preserving presentation fix; **medium**
  on whether "▲ + tooltip" is the clearest possible visual — a maintainer may
  prefer a different glyph or a dedicated "high" pill once seen against real
  sidebar density (revisit item below).

---

## Revisit once in use

1. **Badge glyph (D3, medium).** Re-evaluate "▲ + tooltip" against a real
   populated sidebar. If the triangle still reads as decorative, consider a
   distinct rose "high" micro-pill or moving the count next to a severity-tier
   word. Threshold stays fixed regardless.
2. **Default sort (D1, high).** Severity-descending is the assumed
   worst-first default. Confirm with a real operator that "highest inherent
   severity first" is the order they want on open (vs residual-descending — the
   AFTER-controls exposure, which some programs triage on instead).
3. **Pending-row sort position (D1).** Pending (un-evaluated) rows sort to the
   END in both directions. Confirm that matches operator expectation; an
   alternative is "pending first when sorting ascending review-due" (so the
   not-yet-scheduled risks are surfaced for scheduling). Currently they sink.
4. **Detail page depth (D2, high).** The read-only detail shows title,
   treatment, category, owner, methodology, review-due, the two scoring axes,
   and description. Confirm a real operator does not immediately want the linked
   controls list / residual breakdown surfaced inline (the `GET` already returns
   a `residual` breakdown when the deriver is wired — currently shown only as the
   magnitude). Editing stays explicitly out of scope until separately scoped.
5. **Server-side sort tipping point (D1).** If the register ever outgrows the
   single `GET /v1/risks` payload (the v1 list ships the full unpaginated list),
   client-side sort + filter + paginate all move server-side together — at which
   point the platform's `ParseListSort` enum is the natural home and this
   client comparator retires. Not a v1 concern.

---

## Confidence summary

| Decision                                 | Confidence |
| ---------------------------------------- | ---------- |
| D1 — client-side sort                    | high       |
| D2 — build read-only detail              | high       |
| D3 — badge marker + label (presentation) | high       |
| D3 — glyph choice specifically           | medium     |
