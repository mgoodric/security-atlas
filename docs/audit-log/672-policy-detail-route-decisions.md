# Slice 672 — read-only policy detail route + in-shell 404 — decisions log

**Type:** JUDGMENT
**Branch:** `feat/672-policy-detail-route`
**Scope:** pure `web/` (frontend only). No Go, no migration, no proto, no `evidence_kind`.
**Closes:** `docs/issues/672-policy-detail-route-404.md` (UI-audit ATLAS-024).

## Summary

Policy titles in `/policies` linked to `/policies/{id}`, but no
`web/app/(authed)/policies/[id]/page.tsx` existed, so every click was a
hard Next 404 that rendered **shell-less** (no sidebar/nav — only
browser-back recovered). This slice builds a read-only detail page over
the existing `GET /v1/policies/{id}` backend and adds an in-shell
not-found boundary so authed 404s keep the app shell.

## Decisions

### D1 — Build the route (NOT remove the link)

**Decision:** Build a read-only `/policies/{id}` detail page (path (a) of
AC-1), leaving the existing list-row link intact.

**Rationale:** The backend read surface already exists in full —
`GET /v1/policies/{id}` (`internal/api/policies/handlers.go:397
GetPolicy`) returns the complete `policyWire` (`body_md`, `version`,
`status`, `owner_role`, `effective_date`, `published_at`, plus
`/pdf` and `/acknowledgment-rate` siblings), and the seeded policies
carry real `body_md`. Removing the link (path (b)) would discard a
genuine, already-shipped platform capability. Building the read surface
is the honest fix and is the slice doc's default lean. This matches the
controls/[id] + vendors/[id] precedent (read+display detail over a BFF
`[id]` proxy).

### D2 — Markdown rendering: hand-rolled safe renderer, no new dependency

**Decision:** Render `body_md` with a new pure-TS renderer at
`web/lib/markdown.ts` (`renderMarkdown`), NOT a markdown library.

**Rationale:** The repo ships NO markdown library today — `grep -i
markdown package.json` is empty, and `body_md` is never rendered as
markdown anywhere on `main` (it is only exported raw or PDF-rendered
server-side via chromedp). This detail page is the FIRST markdown render
surface. Pulling in `react-markdown` + `remark-gfm` (and their
unified/micromark transitive tree) for a read-only render of
operator-authored policy text is over-engineering for v1 (constitution
Article VII Simplicity Gate). The renderer covers the subset the seeded
policies use: headings, bold/italic, inline + fenced code, unordered +
ordered lists, links, paragraphs, horizontal rules.

**Security (load-bearing):** the input is **HTML-escaped FIRST**, before
any markdown transform runs; the transforms only ever emit a fixed
grammar of safe tags around already-escaped text. A `body_md` carrying
`<script>` or `<img onerror=...>` renders as inert visible text, not as
live markup. Links carry `rel="noopener noreferrer"`; only
`http(s):` / relative / `#` / `mailto:` hrefs survive (a `javascript:`
href is dropped to plain text). The caller's `dangerouslySetInnerHTML`
is therefore safe by construction — every attacker-controllable byte was
escaped before the markup was built. The renderer is pure and
vitest-covered (15 tests, including the escape/inert-render/href-drop
cases). This is the slice 178 honesty + slice 367 no-leak discipline
applied to a render surface.

### D3 — Not-found boundary at the `(authed)` route-group level

**Decision:** Add `web/app/(authed)/not-found.tsx` (the load-bearing
in-shell boundary) plus a minimal global `web/app/not-found.tsx` for
unauthed routes.

**Rationale:** Before this slice there was NO `not-found.tsx` anywhere
under `web/app`, so any authed 404 rendered the framework default
shell-less 404. Placing the boundary **inside** the `(authed)` route
group means Next's App Router wraps it in that group's `layout.tsx`
(TopBar + Sidebar + skip-link), so a `notFound()` thrown from any authed
page renders WITH the full app shell present — the e2e spec asserts the
desktop sidebar (`sidebar-desktop`) is still visible on the missing-id
render. The page maps a 404 from the detail BFF to `notFound()`
(`if (error instanceof APIError && error.status === 404) notFound()`).
The global page is intentionally minimal — there is no app shell to
preserve for an unauthed route.

### D4 — BFF RLS/auth reuse (invariant #6)

**Decision:** Add `web/app/api/policies/[id]/route.ts` that mirrors the
list BFF's cookie-session auth (`web/app/api/policies/route.ts`): read
the `atlas_jwt` cookie server-side, forward it as `Authorization:
Bearer` to `GET /v1/policies/{id}` via `apiFetch`. The route does NOT
accept or forward any client-supplied `tenant_id` — the cookie session
is the only tenant context, and the upstream enforces RLS
(invariant #6). The vitest test asserts the upstream URL never contains
`tenant_id`.

**Error mapping (slice 367 — no internal-error leak):** missing cookie →
401 `{error: "unauthenticated"}`; upstream 404 → 404 `{error: "policy
not found"}` (the page maps that to `notFound()`); upstream 401 → 401
(the page redirects to `/login`); any other upstream error → the
upstream status with the clean `APIError` status line (the raw upstream
body is never echoed — the 5xx test asserts the response does not contain
the upstream stack-trace string).

### D5 — PDF link handling: same-origin link, no new BFF

**Decision:** The detail page links to `/v1/policies/{id}/pdf` directly
(a same-origin `<a target="_blank" rel="noopener noreferrer">`), NOT
through a new BFF proxy.

**Rationale:** The PDF endpoint streams `application/pdf` (chromedp
render) and is reached same-origin through the reverse proxy that already
fronts `/v1` (see `web/lib/api/base.ts` CLIENT_DEFAULT = same-origin
relative URLs; the Next rewrites + proxy route `/v1` to atlas). A bytes
proxy through a Next route would add a streaming BFF for no isolation
gain — RLS is enforced upstream regardless of whether the bytes pass
through Next. Keeping it a plain link is the minimal honest surface. The
link is the page's ONE outbound action; the page is otherwise read-only
(no submit/approve/publish/edit — slice 672 anti-criteria).

### D6 — Ack-rate is a best-effort secondary detail

**Decision:** The detail BFF composes the acknowledgment rate into its
response (`{ policy, ack_rate }`) by making ONE additional server-side
call to `GET /v1/policies/{id}/acknowledgment-rate`, but ONLY for
published policies, and swallows every non-200 (the upstream returns 409
for non-published, 404 on a delete race) to `null`.

**Rationale:** AC-2 says "the acknowledgment rate **if present**". The
ack-rate lives behind a separate endpoint that 409s for non-published
policies. Composing it server-side (one detail-page call, not a
client-side per-row fan-out) keeps the list view's P0-A2 discipline and
degrades gracefully — the ack-rate's absence never breaks the page
render. The page renders the cell only when `ack_rate` is non-null.

## Coverage

- New pure module `lib/markdown.ts` — 15 vitest cases (escape, inert
  `<script>`/`<img onerror>` render, `javascript:` href drop, safe link
  attrs, heading/list/code/hr/emphasis grammar, inline-code-not-emphasis).
  Floor added: statements 93 / branches 86 / functions 98 / lines 93.
- New BFF `app/api/policies/[id]/route.ts` — 6 vitest cases (401, draft
  one-call, published two-call ack compose, ack 409 graceful, 404→clean,
  5xx no-leak). Floor added: statements 92 / branches 79 / functions 98
  / lines 92.
- `lib/api/policies.ts` floor lifted (29→45 statements, 48→58 functions,
  29→45 lines) in the same PR, backed by the new BFF test exercising
  `getPolicy` / `getPolicyAckRate`.
- The page (`.tsx`) and not-found boundaries (`.tsx`) are outside the
  vitest coverage `include` set (`.ts` only — slice 069 P0-A3
  node-only tier); they are covered by the Playwright e2e tier instead.
- New Playwright e2e `e2e/policy-detail.spec.ts` — route-mocked
  (hermetic, per `feedback_e2e_shared_db_hermetic_mock`; policies have no
  contract golden yet, #410/#411): (1) click a policy title → 200 detail
  page rendering title + markdown body (`<h1>`, `<strong>`, `<li>`) +
  ack-rate + PDF link; (2) missing-uuid → in-shell not-found with the
  desktop sidebar still visible (the load-bearing AC-3 assertion) and the
  default shell-less 404 copy absent.

## Detection-tier classification

- **detection_tier_actual:** `none` — no latent bug surfaced during the
  slice; the missing route was a known finding (ATLAS-024), and the build
  proceeded against an existing, working backend.
- **detection_tier_target:** `none` — a missing-route / missing-boundary
  gap is a build-completeness gap, not a defect a test tier "should have
  caught". Going forward the new `policy-detail` e2e spec is the
  regression guard against the route or the in-shell 404 silently
  regressing.

## Backend-untouched verification

`git diff --stat origin/main...HEAD -- internal/ migrations/ proto/ cmd/`
is empty. This is a pure `web/` slice.
