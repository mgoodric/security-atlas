# 448 — Bulk operations + saved filter-views — decisions log

JUDGMENT slice. Claude made the subjective UX + persistence-shape calls
below, recorded them here, and the slice ships when CI is green (no human
sign-off gate). The maintainer iterates from the "Revisit once in use"
list post-deployment.

- detection_tier_actual: none
- detection_tier_target: none

(No bug surfaced during the slice. The load-bearing authz-amplifier risk
the spec calls out is deferred to the server-backed spillover, where its
detection target is `integration` per the spec's AC-11 note.)

---

## Decisions made

### D1 — Persistence shape: client-side (localStorage) for v1; server-backed filed as spillover. **(Confidence: high)**

The spec's backend ACs (AC-6…AC-13) describe a server-backed bulk-assign
endpoint + an RLS-scoped (tenant, user) saved-views table, and name a
dependency: "reuse the existing single-item assign-owner authz as the
per-item check."

**That single-item path does not exist on `main`.** Verified directly:

- The `controls` table (`migrations/sql/20260511000000_init.sql:176`)
  carries `owner_role TEXT NOT NULL DEFAULT ''` — a **role string**, not
  an owner-USER FK. There is no `owner_user_id` / `assigned_to` column.
- The control-detail + attest pages render `owner_role` **read-only**
  (`web/app/(authed)/controls/[id]/page.tsx:424`,
  `.../[id]/attest/page.tsx:66`) — there is no edit/assign affordance.
- The only control-mutation endpoint is `POST /v1/controls:upload-bundle`
  (whole control-as-code bundle replace —
  `internal/api/httpserver.go:475`). No owner-assign RPC, single or bulk.

Building the bulk-assign backend faithfully therefore means FIRST
inventing the single-item owner-assign mutation (column/model, authz,
audit), THEN the bulk path with the per-item amplifier defense (AC-11,
load-bearing), PLUS a `saved_views` table + RLS + migration + BFF + four
integration tests. That is a large `internal/api` + migration surface —
exactly the "if it pulls you into internal/api significantly, scope to
client-side persistence for v1 and FILE the server-backed version as
spillover" case the slice directive names.

**Chosen path:** v1 ships the genuinely useful, self-contained frontend
ergonomics — the multi-select machinery (real) and the saved filter-views
(real, persisted client-side per the slice-103 `theme.ts` precedent) —
and files the server-backed bulk-assign + saved-views (incl. the
prerequisite single-item owner-assign path) as spillover slice **467**
(`docs/issues/467-server-backed-bulk-assign-saved-views.md`). The
saved-views module injects its store (`SavedViewStore`), so the spillover
swaps localStorage for a fetch-backed store in one place.

**AC disposition under this decision:** AC-1 / AC-3 / AC-4 / AC-5 / AC-14
/ AC-15 ship in this slice; AC-2 ships as a future-state disclosure (D3);
AC-6 / AC-7 / AC-8 / AC-9 / AC-10 / AC-11 / AC-12 / AC-13 move to slice 467. The threat model travels with the backend ACs — the authz amplifier,
per-item re-check, and RLS isolation are all server-side concerns that
the v1 client surface does not and cannot pretend to satisfy.

### D2 — Selection cap = 200 controls per bulk action. **(Confidence: medium)**

`SELECTION_CAP = 200` (`selection.ts`). A realistic "assign every unowned
control in my SOC 2 family" triage batch is tens of rows (the catalog is
~1,400 SCF anchors; a single family/framework slice is a few dozen). 200
is comfortably above that while keeping a single eventual bulk mutation a
bounded transaction (threat-model D). Above the cap the UI surfaces a
"narrow your filters or deselect" alert (`controls-selection-overcap`) and
does **not** silently truncate. The server-backed spillover re-enforces
the same bound server-side — the client cap is ergonomics, the security
boundary is the server.

### D3 — Bulk-assign ACTION shipped as future-state disclosure, not a vapor button. **(Confidence: high)**

Because no owner-assign mutation endpoint exists (D1), a "Bulk assign
owner" button that POSTs to a nonexistent endpoint would be vapor — the
exact UI-honesty failure the project rejects (slice 178 honesty harness;
slice 225 closed the identical gap for the "New control" button on this
same page). So the bulk-assign action is a non-button disclosure
(`bulk-assign-future.ts`, mirroring `new-control-future.ts`) carrying
`title` + `aria-label` + a stable test-id. The live selection count + cap
state still render so the operator sees exactly what the action WILL
operate on when slice 467 lands. Reversal is one PR: the `<span>` flips to
a working trigger and the disclosure module deletes.

### D4 — Saved-view naming UX: inline name form, per-user, name-unique (case-insensitive), 20-view cap. **(Confidence: medium)**

- **Inline form** (not a modal Dialog): a "Save current filters" button
  expands an inline name `<Input>` + Save/Cancel, matching the lightweight
  filter-pill row idiom (slice 098) rather than introducing a modal for a
  one-field action.
- **Save disabled when no filter is active** — saving the all-default
  filter set is meaningless; the button is disabled with an explanatory
  `title`.
- **Name uniqueness** is case-insensitive (`addView`) so "Triage" and
  "triage" don't both appear; duplicate + empty + over-cap each return a
  distinct inline error message.
- **20-view cap** (`MAX_SAVED_VIEWS`) + 60-char name cap
  (`MAX_VIEW_NAME_LENGTH`) bound the localStorage payload.

### D5 — Persisted payload is filter CRITERIA only, validated to the slice-224 allow-list on read. **(Confidence: high)**

`sanitizeFilters` narrows any persisted blob to exactly the five filter
keys (`framework`, `family`, `result`, `freshness`, `scope`) — unknown
keys are dropped, non-string values fall back to `ALL`. This is the v1
client-side analogue of the spec's threat-model T mitigation ("the saved-
view filter payload is validated against the known filter schema — no
arbitrary JSON that becomes a query"). A corrupt/hand-edited localStorage
blob degrades to "fewer/empty views", never an injected query fragment or
a thrown render. (The cross-tenant/cross-user **isolation** half of
threat-model I is a server concern and moves to slice 467's RLS.)

### D6 — Select-all is scoped to the filtered view, never a hidden global select-all. **(Confidence: high)**

`toggleSelectAll` operates on the currently-visible (filtered) id set,
not the full catalog. An out-of-view selection made under a prior filter
is preserved on select and only the visible subset is cleared on
deselect, so toggling never silently drops the operator's prior intent.
A global "select all 1,400" would be the unbounded-selection DoS the
threat model rejects; it is intentionally not offered.

---

## Revisit once in use

1. **(from D2 — medium)** Re-check `SELECTION_CAP = 200` once an operator
   actually runs a bulk-assign batch against real data. If triage batches
   routinely brush the cap, raise it AND confirm the server-side bounded
   transaction (slice 467) still holds at the new value; if nobody ever
   approaches it, the cap is fine as a safety rail.
2. **(from D1/D3 — high that the deferral is correct; medium on timing)**
   Prioritize slice 467 (server-backed bulk-assign + saved-views) once a
   single-item owner-assign surface exists — the bulk path MUST reuse it
   as the per-item authz, never re-implement a parallel path that could
   drift (the spec's load-bearing AC-11). If product direction makes
   per-control owner assignment a near-term need, 467's single-item half
   may itself want to split out first.
3. **(from D4 — medium)** Revisit the saved-view naming UX (inline form
   vs. a richer "manage views" surface, rename, reorder) once operators
   accumulate more than a handful of views. The 20-view cap is a guess;
   raise/lower from observed usage.
4. **(from D1/D5 — medium)** When slice 467 introduces the server-backed
   store, decide the migration story for any views a user saved client-
   side in the interim (silent one-time upload on first server-backed
   load, vs. start fresh). The injected-store seam makes either tractable.
5. **(from D6 — low)** If a "select all matching the filter across all
   pages" need surfaces, design it as an explicit server-side bounded
   operation (count-then-confirm), not a client Set — and keep it behind
   the same cap discipline.

---

## Confidence summary

| Decision                                           | Confidence |
| -------------------------------------------------- | ---------- |
| D1 — client-side v1, server-backed spillover (467) | high       |
| D2 — selection cap = 200                           | medium     |
| D3 — bulk-assign as future disclosure              | high       |
| D4 — naming UX / caps                              | medium     |
| D5 — filter-criteria-only validated payload        | high       |
| D6 — filtered-view-scoped select-all               | high       |
