# Slice 670 — decisions log (pre-GA copy & metadata pass)

- detection_tier_actual: none
- detection_tier_target: none

JUDGMENT slice (user-facing copy authorship + page metadata). No bug surfaced
during the build — the changes are copy/labels/title metadata only, with no
logic or behavior change. The risk for a copy slice is breaking a test that
pins the old string; that risk was managed up front by grepping the test tree
for every changed literal before editing (see D7) and updating the one
assertion that pinned a phrase being removed (`link-for.test.ts`). All four
local verification surfaces ran green (typecheck, eslint, 1711 vitest tests,
and a manual e2e-assertion audit confirming the touched-page specs assert on
testids, not the changed copy).

## Findings addressed

| Sub                   | Finding                                               | Disposition                                                                 |
| --------------------- | ----------------------------------------------------- | --------------------------------------------------------------------------- |
| ATLAS-016             | "prior- answer" stray-space typo                      | Fixed in `questionnaires/page.tsx`                                          |
| ATLAS-012             | Control empty state printed the raw anchor UUID       | Now shows the SCF code (`scf_id`) with a graceful UUID fallback             |
| ATLAS-011             | Breadcrumb "Oscal" + no control-detail leaf           | `oscal → "Vendor Claims"` map entry; page-level leaf crumb added            |
| ATLAS-010 / ATLAS-040 | Inconsistent / regressed per-page `<title>`           | Title layouts for every primary nav route                                   |
| ATLAS-017             | User-facing copy leaked build internals               | Swept Policies/Audits/Catalog/Risks/Controls/etc. empty-state + helper copy |
| ATLAS-035 (AC-6)      | Calendar/dashboard exception label = raw control UUID | OUT of scope (backend SQL) → spillover slice 732                            |

## Decisions made

### D1 — ATLAS-012: resolve the SCF code via the existing anchor BFF, with a UUID fallback

The control-detail empty state renders on a coverage 404 — at that point the
control data is absent, so the anchor code is NOT already in hand on that path
(the spec's "already available in the data" phrasing holds for the happy path,
not the 404 branch). Rather than thread the code down from the `/controls` list
the operator clicked from (which would couple the two pages), I added a small
TanStack query — enabled ONLY when `errorClass === "notfound"` — that hits the
same `/api/anchors/{id}/requirements` BFF the catalog detail page already uses
and reads `anchor.scf_id`. The happy path makes no extra call.

**Fallback shape:** `{anchorCode ?? id}`. When the anchor lookup is pending OR
the id is genuinely bogus (does not resolve in the catalog either), the copy
falls back to the raw id verbatim. This keeps the empty state always-rendering
and preserves the pre-existing behavior for truly-bogus ids — the quarantined
`control-detail-empty.spec.ts` AC-3 (which expects a bogus id to appear) stays
satisfiable. The label changed from "The id `<uuid>` resolves…" to "SCF anchor
`<code>` resolves…" so the noun names what it is.

### D2 — ATLAS-017: KEEP documented platform-API signposts; REMOVE build-internal jargon

The sweep had to distinguish two kinds of "technical-looking" string:

- **Build-internal jargon** — always removed. "slice 005/006", "future slice",
  "follow-up slice", "canvas §4.5", the internal table name
  `policy_control_links`. These mean nothing to an operator and leak the
  project's development vocabulary. Replaced with plain "is not available yet"
  framing (future-tense, names the capability, never failure-framed —
  satisfying the honesty-discipline tests at `new-control-future.test.ts` /
  the policies future tests).
- **Documented public API endpoints** — KEPT as signposts. `POST /v1/policies`
  and `POST /v1/framework-scopes` are real, OpenAPI-documented routes
  (`internal/api/httpserver.go`, `docs/openapi.yaml`) and the genuine next
  action for a self-host operator whose in-app form has not shipped. Their
  pinned vitest assertions (`toMatch(/POST \/v1\/policies/)`) deliberately keep
  them as "a signpost, not a dead end" — I honored that intent.

The one raw API path I DID remove is `/v1/controls:upload-bundle` (risks/new
control-multi-select empty state): the spec names it explicitly as an ATLAS-017
leak. Even though the route exists, a `:verb` action path reads as jargon to a
non-developer in this surface; the spec author's call was to replace it with the
user action. I rewrote it to "Import your controls from the SCF catalog." For
the framework-scopes empty state I kept the `POST /v1/framework-scopes` signpost
(parity with the policies decision) and removed only "slice 020".

**Replacement wording chosen (the JUDGMENT copy):**

| Surface                           | Before                                                                                  | After                                                                                                             |
| --------------------------------- | --------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------- |
| catalog/scf list                  | "Subset bundled with slice 005; full SCF catalog imports with slice 006."               | "A starter subset of the Secure Controls Framework. The full SCF catalog loads when you run the catalog import."  |
| catalog/scf detail                | "no framework requirements in the slice-005 seed. Full mappings import with slice 006." | "no framework requirements in the starter catalog. Full framework mappings load when you run the catalog import." |
| audits banner                     | "Per-period detail view is coming in a future slice"                                    | "Per-period detail view is not available yet"                                                                     |
| risks/new controls empty          | "Upload a control bundle via /v1/controls:upload-bundle or…"                            | "Import your controls from the SCF catalog, or…"                                                                  |
| control header tooltips           | "Rule-DSL execution lands in a follow-up slice (canvas §4.5 …)"                         | "Rule-based control evaluation is not available yet" / "In-app control-text editing is not available yet"         |
| calendar exception/policy reasons | "… is a future slice — view the … register at /…"                                       | "… is not available yet — view the … register at /…"                                                              |
| policies scaffold/ack/new         | "ships with a future slice — until then …"                                              | "is not available yet — until then …" (POST /v1/policies signpost kept)                                           |
| controls new/bulk-assign          | "lands in a future slice …"                                                             | "is not available yet …" (capability substrings kept for the pinned tests)                                        |
| settings token-issuance           | "User-scoped token issuance is a follow-up slice."                                      | "User-scoped token issuance is not available yet."                                                                |
| coverage chevron title            | "Per-requirement inspector lands in a follow-up slice"                                  | "Per-requirement inspector is not available yet"                                                                  |
| controls/[id] linked policies     | "… via the policy_control_links table (slice 020)."                                     | "Policies linked to this control."                                                                                |
| framework-scopes empty            | "Create one via POST /v1/framework-scopes (the new-scope UI lands in slice 020)."       | "Create one via the platform API (POST /v1/framework-scopes); the in-app create form is not available yet."       |

**Tone discipline (CLAUDE.md):** every replacement is measured/factual, uses
future-tense "is not available yet" rather than failure-framing ("disabled",
"broken", "error"), and avoids the banned superlatives. "Not available yet"
appears across several adjacent surfaces; this is acceptable consistency for a
status disclosure (it is the honest, plain phrasing the honesty-discipline tests
reward), not the kind of jargon-repetition the repetition discipline flags.

### D3 — ATLAS-011: leaf crumb on the PAGE-level breadcrumb, not the shell chip

The shell `Breadcrumb` (`web/components/shell/breadcrumb.tsx`) is single-level
by design (P0-271-2: `tenant › section`), and `control-detail-top-bar.spec.ts`
pins `breadcrumb-page` to exactly "Controls" on `/controls/{id}`. Adding a leaf
there would both break that e2e and violate the component's section-level
contract. The control-detail page ALSO has its own page-level breadcrumb (it was
just a "← All controls" back-link). That is where `anchor.scf_id` is in scope,
so that is where the leaf belongs. I converted it to a proper trail —
`Controls › <scf_id>` (falling back to the control's `bundle_id` when the row is
unanchored) — keeping the "Controls" segment as a working back-link. The "Oscal"
half is a pure data-table entry: `oscal → "Vendor Claims"` in `page-names.ts`,
with a new `page-names.test.ts` case pinning it.

### D4 — ATLAS-010/040: sibling title-layout per route, full primary-route audit

`/settings` (slice 248) established the canonical pattern for a client-component
page: a sibling server-component `layout.tsx` that exports
`metadata = { title: "<Page> · security-atlas" }` and passes children through.
The root `app/layout.tsx` uses a plain-string default ("security-atlas") with no
title template, so each page sets the full title string. AC-7 said "re-audit ALL
primary routes", so I enumerated the sidebar nav (16 routes) and gave every one a
title: new layouts for audits, risks, oscal/component-definitions, controls,
evidence, policies, vendors, calendar, board-packs, exceptions, questionnaires,
activity, catalog/scf, dashboards/metrics; metadata added to the existing
`/dashboard` layout; `/settings` already had it. Section-scoped titles propagate
to detail children (`/audits/new`, `/risks/{id}`, etc.) — accepted as the
correct section-level title. The Vendor Claims title uses the user-facing module
name ("Vendor Claims · security-atlas"), not the wire term "OSCAL".

### D5 — ATLAS-035 is a backend change → spillover, not in-scope

The "Exception on control <uuid>" label is constructed in SQL
(`internal/db/queries/calendar.sql`: `'Exception on control ' || e.control_id::text`),
and the frontend renders `event.title` verbatim. Showing the SCF code + control
name requires a JOIN in the query + a sqlc regen — a Go/backend change, which
this slice's anti-criteria forbid ("no backend/Go changes", `web/`-only). Rather
than force it here (or, worse, half-fix it client-side by re-fetching control
metadata per event), I filed spillover slice **732** (cites parent 670, status
`ready`, backend JUDGMENT). This is the disciplined call: AC-6 is real, but it
lives one layer below this slice's scope.

## Verification

- `npx tsc --noEmit` — clean.
- `npm run lint` (eslint) — 0 errors (2 pre-existing warnings in an untouched
  `scripts/` file).
- `npm run test` (vitest) — 1711 passed / 181 files, including `page-names`
  (new Oscal case), `breadcrumb`, `new-control-future`, the policies future
  tests, and `link-for` (updated assertions).
- Playwright e2e was not run locally (it requires the full docker-compose
  platform on :3000). Mitigation: audited every touched-page spec — audits /
  policies / controls / control-detail-top-bar assert on **testids**, not the
  changed copy; the only e2e title assertion (`settings.spec.ts`) targets the
  unchanged `/settings` title; the only literal-copy assertion on a removed
  phrase ("future slice") lived in `link-for.test.ts` (vitest, updated + green).

## Test updates made (copy a test pinned)

- `web/components/calendar/link-for.test.ts` — two assertions changed from
  `toMatch(/future slice/i)` to `toMatch(/not available yet/i)` +
  `toMatch(/\/(exceptions|policies)/i)` (tightened to the surviving signpost).
- `web/lib/page-names.test.ts` — added a case asserting
  `derivePageName("/oscal/component-definitions") === "Vendor Claims"`.

All other touched copy was either unpinned or pinned only on substrings I
deliberately preserved (capability names, `POST /v1/policies`, sentence-shape,
no-`slice \d+`, no-failure-words) — so the existing future-state honesty tests
pass unchanged.
