# Slice 125 — Frontend `/audit-log` page · decisions log

Filed as part of the slice-125 implementation (branch
`frontend/125-audit-log-page`). Every JUDGMENT call the engineer made
while building this slice is recorded here so the post-merge maintainer
iteration is traceable.

The product runtime AI-assist boundary is constitutional and is NOT
mutated by anything in this log; this log is about how the slice was
BUILT, not how the shipped product behaves (see `CLAUDE.md`
"AI-assist boundary (hard)").

---

## D1 — `actor_name` resolution: ship with truncated `actor_id`, file spillover

**Decision.** The page renders `actor_id` truncated to the first 8
characters with the full UUID exposed via the cell's `title` attribute.
The slice-124 wire shape does not include `actor_name`.

**Trade-off.**

| Option                                       | Pros                                                         | Cons                                                                                                                       |
| -------------------------------------------- | ------------------------------------------------------------ | -------------------------------------------------------------------------------------------------------------------------- |
| (a) Extend slice 124 to include `actor_name` | One round-trip, clean wire shape, human-readable rendering   | Backend slice 124 is already merged on `main`; this would be a small follow-on PR and would block this slice's merge       |
| (b) Per-row client-side resolution           | No backend change                                            | N+1 per row at the BFF (`GET /v1/admin/users/<id>` per row); 1000 rows = 1000 round-trips per page; performance unsuitable |
| (c) Ship with truncated `actor_id`           | Unblocks the page, ZERO backend change, ZERO per-row fan-out | Operator sees a UUID prefix instead of a human name                                                                        |

**Chose (c).** The override directive ("show `actor_id` truncated to first
8 chars and file a spillover slice") aligns with the "ship the user-visible
value now, polish later" principle from slice 098. Filed
**spillover slice 129** to extend the slice-124 endpoint with `actor_name`
(small backend change). When 129 lands, the table cell upgrades to
`{actor_name || truncated actor_id}` with a one-line change.

---

## D2 — Filter URL serialization: comma-separated CSV for `kind`

**Decision.** The `kind` filter serializes to the URL as
`?kind=evidence,me,walkthrough` (CSV) rather than repeated
`?kind=evidence&kind=me&kind=walkthrough` params.

**Why.** The slice-124 handler's `parseUnifiedParams` (lines 301–309 of
`internal/api/adminauditlog/unified.go`) splits on `,`. Repeated-param
form would require BFF translation. The CSV form is the platform contract
— the BFF can pass it through unchanged.

---

## D3 — Actor filter: free-text UUID input, no typeahead

**Decision.** The actor filter is a plain `<Input type="text">` with an
"Apply" button (or Enter key). No typeahead, no autocomplete.

**Why.** The slice doc mentions typeahead from `/v1/admin/users` but
verification shows no such endpoint exists (`ls
web/app/api/admin/users/` returns nothing). Typeahead therefore depends
on a backend endpoint that hasn't shipped — that's a spillover, not part
of this slice. A free-text input handles the common case (operator pastes
a UUID from a log line). The slice-124 endpoint's `actor` parameter is
already string-typed and matches an exact equality on `actor_id`, so the
UI's literal-string path is correct.

---

## D4 — Date pickers: native `<input type="date">`, no calendar library

**Decision.** The from/to pickers use the browser's native
`<input type="date">`. No external library (e.g., react-day-picker).

**Why.** Matches the slice 094 precedent (compliance calendar) of
hand-rolling vs adding a library. The picker resolution is single-day
(slice-124 cap is 90 days; sub-day precision is irrelevant for the
operator's investigation flow). Keeps the bundle small and avoids the
"new dependency" review tax.

---

## D5 — Row expand-for-payload: hand-rolled details-row, no shadcn Collapsible

**Decision.** Each table row renders an additional `<tr>` below it when
expanded, gated by local `useState`. No
`@radix-ui/react-collapsible` / shadcn Collapsible primitive.

**Why.** The shadcn install (`web/components/ui/`) ships
table/dialog/alert/badge/button/card/input/progress/skeleton — no
collapsible. Adding one is a new dependency for this slice. A
hand-rolled details-row using React state matches the existing
patterns (the `/admin/audit` legacy scaffold already uses a similar
inline shape) and stays domain-agnostic.

---

## D6 — Auto-load ceiling: 10 pages, then manual "Load more"

**Decision.** The IntersectionObserver auto-loads up to 10 pages
(`AUTO_LOAD_PAGE_LIMIT = 10`). After that, the operator must click
"Load more" to fetch additional pages.

**Why.** The slice threat-model entry (STRIDE-D, denial of service) calls
out "infinite-scroll fetches all 1M rows" as a hazard and prescribes
"degrade to load-more button after 10 pages (10K rows)" as the mitigation.
10 pages × the 1000-row backend page cap = 10,000 rows. Beyond that, the
cost should be visible to the operator before being incurred.

---

## D7 — Route guard: server-component layout + `redirect()`, not edge middleware

**Decision.** The route guard lives in `web/app/audit-log/layout.tsx`
(a server component) that calls `/api/admin/me` and `redirect()`s
non-admins to `/dashboard?error=admin-only`. The `web/proxy.ts` Next.js
16 request interceptor is NOT extended with audit-log-specific logic.

**Why.** `proxy.ts` runs at the edge and cannot make a backend call to
check role membership — it only sees the cookie. The slice-060 pattern
(used by `/admin/*`) shows the canonical shape: a server-rendered layout
that issues the `/api/admin/me` preflight and conditionally renders or
redirects. This pattern:

- Runs server-side on every render (a stale client-side cache cannot
  bypass it).
- Issues a single `/api/admin/me` round-trip (cheap — slice 060 returns
  `{is_admin: boolean}`).
- Composes cleanly with the existing `proxy.ts` cookie-presence gate:
  unauthenticated callers bounce to `/login` BEFORE the layout runs, so
  the layout only ever sees a session-bearing request.

The slice doc says "route guard in `web/middleware.ts`" — the project's
actual Next.js 16 file is `web/proxy.ts` (renamed per Next.js 16
convention, per slices 091/092). The semantic guard is implemented; the
file name diverges intentionally.

---

## D8 — BFF cookie forwarding: bearer-only (do NOT broaden slice 110 surface)

**Decision.** The BFF forwards only `Authorization: Bearer <bearer>`.
The slice-110 `buildSessionsForwardHeaders` helper is NOT imported, and
the `atlas_session` cookie is NOT forwarded.

**Why.** Slice 110 P0-A2 narrow-scope rule: the OIDC session id cookie
is forwarded ONLY on `/api/me/sessions*` routes. Broadening that surface
to `/api/audit-log/unified` would leak the session id into a request
path the platform handler has no reason to see. The slice-124 handler
authenticates on the bearer alone (see
`internal/api/adminauditlog/unified.go` — there is no cookie path in the
handler).

---

## D9 — Frontend admin gate: `is_admin === true` only (auditor handled at backend)

**Decision.** The route-level guard requires `is_admin === true` for the
UI to render. Auditor and grc_engineer roles are gated at the backend
(slice-124 OPA policy) but cannot reach the UI today.

**Trade-off.** Slice 124's OPA gate allows admin OR auditor OR
grc_engineer. The slice-060 `/api/admin/me` endpoint as shipped returns
only `{is_admin: boolean}` — there is no role-list field.

| Option                                              | Pros                                            | Cons                                                                                               |
| --------------------------------------------------- | ----------------------------------------------- | -------------------------------------------------------------------------------------------------- |
| (a) Extend `/api/admin/me` with role enumeration    | Page renders for all three intended audiences   | Cross-cutting change to a slice-060-owned endpoint; blocks this slice on a sibling backend change  |
| (b) Add a new `/api/me/roles` endpoint              | Narrower than (a); keeps `/api/admin/me` stable | Two endpoints answering similar questions; future drift risk                                       |
| (c) Gate UI strictly on `is_admin`; rely on backend | Ship the page now; backend 403 still the gate   | Auditor caller would see redirect-to-dashboard rather than the page; defense-in-depth still intact |

**Chose (c).** The slice doc explicitly says "Backend 403 is
defense-in-depth, not the only line" (P0-A2). The route-level guard is
the FIRST line; the backend is the second. Strict-mode admin-only is the
correct conservative posture for the route guard.

Filed **spillover slice 130** to extend `/api/admin/me` with role
enumeration so auditor and grc_engineer callers can also reach the page.

---

## D10 — Coexistence with the legacy `/admin/audit` page

**Decision.** The new `/audit-log` route lives alongside the legacy
`/admin/audit` (slice 060) page. The legacy page is NOT deleted in this
slice.

**Why.** The legacy `/admin/audit` was a scaffold predicting a backend
that didn't exist yet. It now serves as documentation of the
seven-table union the team originally pictured. Removing it is a
separate concern (cleanup spillover; not on this slice's hook list).
The two pages have different paths, different roles, and different
information layers; no user is confused by the coexistence in the
short term.

---

## Spillovers filed

- **129** — extend slice-124 endpoint with `actor_name` (small backend
  change; unblocks D1 upgrade)
- **130** — extend `/api/admin/me` with role enumeration so auditor
  callers can reach `/audit-log` (unblocks D9 upgrade)
