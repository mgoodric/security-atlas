# Slice 234 — /evidence filter row Source + Scope + Since pills (decisions log)

**Type:** JUDGMENT
**Spec:** `docs/issues/234-ui-honesty-evidence-filter-row-missing-three-pills.md`
**Status at slice close:** functionally complete on the slice branch;
backend (sqlc + handler) + BFF + UI wired; vitest green (824 passing,
+9 net new in `filters.test.ts`); Go unit coverage green (+2 cases);
Playwright spec extended (quarantined per the existing slice 099 pattern).

This log records the subjective design calls made during build, so the
maintainer can iterate post-deployment without reverse-engineering the
PR diff.

---

## D1 — Backend filter wiring

**Decision.** The slice extends the existing `ListEvidencePaged` sqlc
query with one new optional named-arg (`scope_cell_id`), rather than
adding a sibling "...Scoped" query. The new `?scope_cell_id=<uuid>`
URL param is parsed by the existing `/v1/evidence` handler (with a
clean 400 on a non-UUID before the SQL round-trip) and forwarded
through `evidenceListPage{scopeCellID: …}` to the store.

**Rationale.** Mirrors slice 224 D6 verbatim — the null-UUID sentinel
pattern (`sqlc.narg('scope_cell_id')::uuid IS NULL OR scope_id =
sqlc.narg('scope_cell_id')::uuid`) keeps the query plan stable in the
no-filter branch, lets sqlc emit a nullable Go parameter, and avoids
the two-query divergence problem.

**P0 honoured.** Per the spec P0-234-3, the new predicate runs inside
the same RLS-bound transaction the list query already uses
(`controldetail.Store.inTx`); the predicate adds no new write surface
and no new endpoint.

**sqlc-narg lesson.** The codebase's hard-won lesson from slice 224's
follow-on fix is: extend SQL with `sqlc.narg('name')::T` or
`sqlc.arg('name')::T` — never bare `$N` positional. Followed here
verbatim.

**Confidence:** high. Pattern is identical to slice 224.

**Revisit once in use.** Watch the query plan once tenants accumulate
millions of `evidence_records` rows under heavy scope-cell narrowing.
A partial index on `(tenant_id, scope_id, observed_at DESC)` may help.
Not needed at current scale.

---

## D2 — Source pill encodes the composite tuple as `type|id`

**Decision.** The Source pill option list enumerates the observed
`(actor_type, actor_id)` tuples in the current result page, encoded
as a `<actor_type>|<actor_id>` composite string. On change the page
sets BOTH `source_actor_type` and `source_actor_id` URL params
atomically; the wire keeps the two separate.

**Rationale.** The Source axis is genuinely two-dimensional on the
backend (the upstream filters drill into `source_attribution->>'actor_type'`
and `source_attribution->>'actor_id'` independently), but operationally
operators reason about ONE source (e.g. "the aws-connector pushes",
not "anything from `connector` X anything from `aws-connector`"). A
single dropdown that binds both params atomically matches operator
intent and avoids the cross-product trap (selecting just one yields
a half-narrowed view nobody asked for).

**Per AC-1, options come from observed tuples only.** No invented
values — same pattern as `buildKindOptions`.

**Confidence:** high. The composite-key pattern is also how the
slice-098 framework pill works (the value is a denormalised string
the BFF expands into multiple upstream predicates).

**Revisit once in use.** If real tenants want to filter by
`actor_type` independently of `actor_id` (e.g. "all connector
pushes"), surface a separate Type-only pill as a follow-on. The
current shape does not block that future split — the composite key
just becomes one entry among siblings.

---

## D3 — Since pill stores the PRESET KEY in the URL, not the resolved timestamp

**Decision.** The Since pill's URL key is `since_preset` (one of
`24h` / `7d` / `30d` / `audit`), NOT `since` (the resolved RFC3339
cutoff). The resolution to a concrete timestamp happens client-side
per render, against the page's pinned `nowAtMount` clock.

**Rationale.** Three concerns drive this.

1. **Sliding-window semantics.** A bookmark of
   `/evidence?since=2026-05-16T10:00:00Z` is a frozen window; the
   operator who bookmarks "Last 7 days" expects it to slide.
   Persisting the preset key gives sliding semantics for free.

2. **Audit-period coupling.** The "audit" preset's value
   (`period_start`) depends on which period is active at render
   time. Persisting the resolved timestamp would silently lock the
   bookmark to an old period's start; persisting the key keeps the
   pill self-healing.

3. **Forward-compatibility.** Adding new presets ("Last quarter",
   "Last fiscal year") doesn't reshape the URL contract — only the
   resolution table grows.

**Per-mount pinning.** `nowAtMount` (a `useState(() => new Date())`)
is the React-purity-safe way to capture "now" once per page mount.
This satisfies the `react-hooks/purity` lint rule (no `Date.now()`
inside `useMemo`) and gives the operator a stable window for the
duration of the page session — the window doesn't silently shift
under them while they're reading the table. A future remount (or
explicit refresh) re-resolves to the current time.

**Confidence:** high. The pattern matches the slice 067 calendar's
`useState(() => new Date())` initialisation idiom.

**Revisit once in use.** If a tenant complains the bookmark doesn't
match their assumption ("I bookmarked when?"), surface the resolved
timestamp as a tooltip on the pill, or as a meta-row caption. Not
shipped v0 to avoid bikeshedding.

---

## D4 — Audit-period detection is client-side, not a new `?active=true` endpoint

**Decision.** The "active audit period" surfaced in the Since pill's
"Audit period (current)" option is derived client-side from the
existing `/v1/audit-periods` list: pick the period whose
`status === 'open'` AND whose `[period_start, period_end]` contains
`nowAtMount`.

**Rationale.** The spec mentioned `/v1/audit-periods?active=true` but
no such query-param branch exists on the upstream handler. Two
options:

A. Add a backend `?active=true` filter (new sqlc predicate + handler
branch).
B. Compute it client-side from the existing list endpoint.

(B) is correct here because:

- The active-period concept is a UI surface, not a domain primitive
  (no other backend consumer needs it).
- The list endpoint already returns the full audit-period set
  RLS-scoped to the tenant; the client filter is a one-line predicate.
- A new backend filter would create a contract surface that other
  callers might come to depend on, locking in a coarse definition of
  "active" (just `status` vs status + date-window vs status + role
  membership vs etc.).

The client-side compute keeps the definition flexible while the v1
"open + date-window" heuristic settles.

**Confidence:** medium. The heuristic is "status=open AND period
contains today"; if a tenant has multiple overlapping open periods
the page picks the first by list order (newest-first from the
backend). Documented in code; not a real concern at v0 scale.

**Revisit once in use.** First tenant with overlapping open periods
forces a definitive choice. Best path is a backend `?active=true`
filter at that point, with a tie-break documented (probably "most
recent period_start").

---

## D5 — Source pill ignores the half-narrowed URL state

**Decision.** When the URL carries only one of `source_actor_type` /
`source_actor_id` (e.g. an old hand-pasted URL), the Source pill's
current value falls back to `ALL` rather than synthesising a
half-tuple composite key. The URL params are honoured by the BFF
(forwarded to upstream as-is); only the pill's _display_ falls back.

**Rationale.** A half-narrowed state is legal on the wire (the
backend handles each filter independently), but no dropdown option
exists for "connector · anywhere" or "anywhere · aws-connector". The
honest fallback is `ALL` (the dropdown's "All sources" entry); the
URL semantics survive untouched, and the operator sees the data is
still narrowed via the row count + clear-filters CTA.

**Confidence:** high. Consistent with how slice 098's framework pill
handles unknown framework codes (falls back to the sentinel without
losing the URL state).

**Revisit once in use.** If operators are confused by the
"narrowed-but-pill-says-All" state, surface a tooltip or a clear-
individual-filter affordance. Premature today.

---

## D6 — Per-control path ignores `scope_cell_id`

**Decision.** When `/v1/evidence` is called with BOTH `control_id`
AND `scope_cell_id`, the handler honours `control_id` (the per-control
path) and IGNORES `scope_cell_id` — the per-control SQL query does
not have a scope predicate.

**Rationale.** The per-control path already resolves a single control's
evidence; adding a scope narrowing on top would be a marginal feature
nobody asked for, and would require duplicating the new predicate
into `ListEvidenceForControlPaged`. The /evidence page never sends
both params anyway (the Scope pill is for the tenant-wide path).
Better to honour one filter cleanly than to half-implement two.

The handler's doc-comment surfaces this so a future caller doesn't
file a "scope_cell_id silently dropped" bug.

**Confidence:** high. Documented in the handler doc-comment; no
existing caller exercises both params.

**Revisit once in use.** If a future surface needs control-scoped
scope narrowing (probably the slice-041 control-detail evidence
panel), extend `ListEvidenceForControlPaged` with the same pattern.
Out-of-scope for this slice.

---

## D7 — Scope-cells-capped banner reused from slice 224

**Decision.** When the tenant has more scope cells than
`SCOPE_CELL_CAP` (50), an `<Alert>` banner renders above the evidence
table announcing the cap. Same shape, same testid pattern, same copy
as the slice 224 `/controls` banner (just `data-testid="evidence-scope-cells-capped"`).

**Rationale.** Consistency with the sibling /controls page keeps the
UX predictable; the operator's mental model of "Scope pill = up to 50
cells; typeahead pending" doesn't need to learn a second variant per
page. The 50-cap rationale is slice 224 D1 verbatim (newest-first cell
ordering biases toward recently-active cells, the realistic case where
50 is sufficient).

**Confidence:** high. Pattern is established.

**Revisit once in use.** First tenant to exceed 50 cells triggers
the typeahead follow-on slice (still tracked from slice 224).

---

## D8 — CI-delta scan results

**Method.** Greppped for callers of `ListEvidencePagedParams` (added
a field) and `EvidenceListFilters` (added two fields) and the BFF
`FORWARD_PARAMS` (added one entry).

**Findings.**

- `ListEvidencePagedParams`: ONE caller —
  `internal/api/controldetail/store.go`. Updated.
- `evidenceListPage`: ONE producer — `internal/api/controldetail/handler.go`.
  Updated.
- `EvidenceListFilters`: ONE caller — `web/lib/api.ts`
  `fetchEvidenceList` and its consumers
  (`web/app/(authed)/evidence/page.tsx` via `toFetchOptions`). The
  added fields are optional, so the existing callers compile
  unchanged.
- `FORWARD_PARAMS`: read in ONE place (`web/app/api/evidence/route.ts`
  GET handler). Extension is purely additive — no behaviour change
  for callers that don't pass the new key.
- `EvidenceFilters` shape: TWO new fields with `ALL` default; existing
  filters.test.ts adapted; all 19 tests + 9 route tests green.
- Vitest: all 824 tests green; 9 new (+1 modified) in
  `filters.test.ts`.
- Go unit: all controldetail tests green; +2 cases
  (`TestOptUUID`, `TestEvidence_Handler_400_NonUUIDScopeCellID`).
- TypeScript baseline: 15 pre-existing errors before slice 234;
  15 after — no new typecheck errors introduced. (Baseline matches
  slice 224 D9.)
- ESLint: clean on the touched files
  (`web/app/(authed)/evidence/`, `web/app/api/evidence/`,
  `web/lib/api.ts`).

**Confidence:** high.

---

## Closing note on JUDGMENT-vs-runtime boundary

This slice ships a frontend filter UI extension and a server-side
query extension. It does NOT touch any board narrative, audit-binding
artifact, or AI-assist surface. The CLAUDE.md "AI-assist boundary
(hard)" stays untouched. The "JUDGMENT" type applies to the build-
time UX calls captured above (D2, D3, D4, D5, D7) — the product
runtime behaviour is otherwise unaffected.
