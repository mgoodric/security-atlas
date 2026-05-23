# 204 — Page audit: `/settings`

**Page URL audited:** `https://atlas-edge.home.gmoney.sh/settings`
**Mockup HTML path:** `Plans/mockups/settings.html`
**Auditor:** parallel audit agent (slice 204 fleet)
**Audited on:** 2026-05-23
**Live build:** SSR'd via Next.js 16; 200 OK; ~36KB HTML payload
**Auth context:** admin JWT bearer (cookie `atlas_jwt`) from `/tmp/atlas-edge-admin-jwt`

## Scope

Slice 204's per-page audit applies four explicit finding categories
against the live `/settings` route:

1. **Layout / chrome parity** — does the page header, sidebar,
   subhead, and section frame match the mockup?
2. **Broken interactions** — toggles that no-op, links that 404,
   forms that don't submit, sections that throw.
3. **Data-bound surfaces that lie** — mockup shows a value that
   doesn't exist on the wire, or the wire returns a value the UI
   misrenders.
4. **Mockup-stale** — features depicted in the mockup that the
   implementation never built (intentional or accidental).

The audit is **read-only** (P0-A1 of slice 204). Every finding files
ONE spillover slice; the audit does not fix anything inline.

## Prior art — slice 154 (settings-only deep audit)

Slice 154 (`docs/audit-log/154-settings-page-audit-decisions.md`,
PR #338, merged 2026-05-18) conducted an exhaustive section-by-
section audit of `/settings` and filed findings F1-F11. The
disposition of those findings is summarized below; **slice 204
does NOT re-file any of them**.

| 154 finding                                          | Disposition                                                                                                                                                                                                                                   | Re-checked here                                                                                                                                   |
| ---------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------- |
| **F1** — Section anchors missing                     | inline-fixed in slice 154 PR #338                                                                                                                                                                                                             | VERIFIED present in live SSR (`id="profile"`, `id="appearance"`, `id="notifications"`, `id="tokens"`, `id="sessions"` on all five card sections). |
| **F2** — Theme picker text-only                      | inline-fixed                                                                                                                                                                                                                                  | VERIFIED — three radio-cards with preview swatches (`data-testid="settings-theme-swatch-{light,dark,system}"`) render in SSR.                     |
| **F3** — Roles tail badge missing                    | inline-fixed                                                                                                                                                                                                                                  | NOT independently verified (requires SSR data; profile section ships skeleton at SSR time). Trusted as merged.                                    |
| **F4** — Time zone read-only                         | inline-fixed (curated 9-zone picker)                                                                                                                                                                                                          | Confirmed by reading `web/app/(authed)/settings/page.tsx` lines 379-410 (`TimeZonePicker`).                                                       |
| **F5** — Notification copy delta                     | inline-fixed                                                                                                                                                                                                                                  | NOT independently verified (Notifications section ships skeleton at SSR). Trusted as merged.                                                      |
| **F6** — Sessions UA/IP/geo missing                  | spillover slice **162** (merged at `a134691`)                                                                                                                                                                                                 | Closed; UA/IP/geo wire-shape extension shipped.                                                                                                   |
| **F7** — Profile avatar block missing                | inline-fixed                                                                                                                                                                                                                                  | NOT independently verified at SSR (skeleton); trusted.                                                                                            |
| **F8** — API tokens Rotate action missing            | spillover slice **163** (merged at `a682c38`)                                                                                                                                                                                                 | Closed.                                                                                                                                           |
| **F9** — In-page left rail nav                       | deliberately omitted                                                                                                                                                                                                                          | NOT re-filed here (slice 154 explicitly declined; reasonable people can disagree, finding remains in slice 154's "deliberately omitted" record).  |
| **F10** — Vestigial `loading` prop on ProfileSection | inline-fixed                                                                                                                                                                                                                                  | Closed.                                                                                                                                           |
| **F11** — Playwright e2e fixture missing             | spillover slice **164** (merged at `3092f3e`) → follow-on **165** (merged at `ed4d1e1`) → **166** (merged at `e76e5cf`) → **168** (merged at `9f70f08`) → **170** (merged at `2c89eb3`) → **171** (merged at `9d01de2`, full 11/11 ACs green) | Closed across the 5-slice chain.                                                                                                                  |

**Adjacent slices not from 154 but affecting `/settings`:**

| Slice                                  | Disposition                | Note                                                                                                                    |
| -------------------------------------- | -------------------------- | ----------------------------------------------------------------------------------------------------------------------- |
| **#203** — Dark-mode stylesheet wiring | `ready` (filed 2026-05-22) | Out of scope to re-file. The Appearance section's dark swatch + theme-picker hydration are in flight via slice 170/203. |

**Net:** all 11 findings from slice 154 are either merged, deliberately
omitted, or in flight (203). Slice 204's settings audit therefore
focuses exclusively on findings NOT in slice 154's set.

## Four-axis comparison — new findings only

### Header / chrome (axis i — layout)

| Mockup element                                        | Live                                                             | Disposition                                                                            |
| ----------------------------------------------------- | ---------------------------------------------------------------- | -------------------------------------------------------------------------------------- |
| `<title>Settings · security-atlas</title>`            | `<title>security-atlas</title>` (global default)                 | **NEW finding 248** — per-page title metadata missing.                                 |
| Tenant breadcrumb `Sentinel Labs > Settings`          | `TenantSwitcher` component (different shape)                     | Chrome-wide; out of scope for settings-specific audit. Defer to global chrome audit.   |
| Status pill `SOC 2 Type II · Q2 2026 in progress`     | "v0 · self-host" badge                                           | Chrome-wide; defer.                                                                    |
| Search box (⌘K)                                       | Absent                                                           | Chrome-wide; defer.                                                                    |
| User avatar pill                                      | "Sign out" form button + TenantSwitcher                          | Chrome-wide; defer.                                                                    |
| Sidebar shows control / risk count badges (`82`, `3`) | No counts                                                        | Chrome-wide; defer.                                                                    |
| Sidebar order matches mockup                          | Live adds `Calendar`, `Metrics`, `Catalog · SCF` (not in mockup) | Chrome-wide; defer (mockup is from earlier slice; live nav reflects shipped features). |

### Subhead + admin cross-link (axis i + ii — layout + broken interaction)

| Mockup element                                                      | Live (admin)                                                                                                                                              | Disposition                                                     |
| ------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------- |
| Subhead text "Tenant administration → /admin (admin role required)" | `Tenant administration ({"->"} /admin)` (literal ASCII `->`)                                                                                              | **NEW finding 252** — ASCII arrow vs Unicode arrow glyph delta. |
| Subhead link rendered with brand color                              | Live: post-hydration the link IS brand-colored                                                                                                            | OK after hydration; flicker covered by 249.                     |
| SSR-time subhead variant                                            | Live SSR ships **non-admin** variant for admin users — `<span class="text-muted-foreground">Tenant administration (admin role required)</span>` (no link) | **NEW finding 249** — admin variants flicker on first paint.    |

### API tokens section (axis ii — broken interaction; axis iii — data lies)

| Mockup element                                  | Live                                                                                                           | Disposition                                                                                                         |
| ----------------------------------------------- | -------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------- |
| Token table with rows (Issue · Rotate · Revoke) | Live SSR ships `data-testid="settings-section-tokens-non-admin"` alert "Admin role required" for an admin user | **NEW finding 249** (admin flicker) covers the SSR/CSR variant swap. Post-hydration the admin tokens table appears. |
| Token rotation action                           | Slice 154 F8 → slice 163 (merged)                                                                              | Already closed; not re-filed.                                                                                       |

### Profile section (axis iii — data-bound surface that lies)

| Mockup expectation                                        | Live `/v1/me` response                                                                                          | Disposition                                                                       |
| --------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------- |
| `display_name: "Sam Rivera"`                              | `display_name: "API key "` (literal, trailing space) — JWT is bound to an `admin_credential`, not an OIDC human | **NEW finding 250** — credential-bearer JWTs surface as degenerate user identity. |
| `email: "sam.rivera@sentinellabs.example"`                | `email: ""`                                                                                                     | Same finding 250.                                                                 |
| `idp_subject: "okta\|00u4f2…"`                            | `idp_subject: ""`                                                                                               | Same finding 250.                                                                 |
| `time_zone: "America/Los_Angeles"` (selected in <select>) | `time_zone: null`                                                                                               | TimeZonePicker handles null correctly (slice 154 F4); not a finding.              |

### Notifications section (axis ii + iii — broken interaction + data lies)

| Mockup expectation                                    | Live `/v1/me/preferences` response                                                                               | Disposition                                                                        |
| ----------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------- |
| Four event rows × two channels rendered with defaults | `{"error": "no preferences for this credential"}` (200/4xx) — keyed on user; credential-bearer has no user-shell | **NEW finding 251** — Notifications section error path for credential-bearer JWTs. |

### Active sessions section (axis iii)

| Mockup expectation                                             | Live `/v1/me/sessions` response                                              | Disposition                                                                  |
| -------------------------------------------------------------- | ---------------------------------------------------------------------------- | ---------------------------------------------------------------------------- |
| Two session rows with OS · Browser · IP · location · timestamp | `{"count": 0, "sessions": []}` — credential-bearer has no human-session rows | Honest empty state; not a finding (the wire/UI both handle empty correctly). |
| UA/IP/geo display                                              | Slice 154 F6 → slice 162 (merged)                                            | Already closed; UA/IP/geo shipped.                                           |

### Mockup-stale (axis iv)

| Mockup feature                                      | Implementation status                         | Disposition         |
| --------------------------------------------------- | --------------------------------------------- | ------------------- |
| Left in-page nav rail (Account / Cross-link groups) | Not built (slice 154 F9 deliberately omitted) | Not re-filed.       |
| Search box (⌘K) in the global header                | Not built                                     | Chrome-wide; defer. |
| Status pill in the global header                    | Not built                                     | Chrome-wide; defer. |

## Findings filed by this audit (5 total)

| Slice   | Title                                                                          | Category                                  | Severity guess                                       |
| ------- | ------------------------------------------------------------------------------ | ----------------------------------------- | ---------------------------------------------------- |
| **248** | Settings page lacks page-specific `<title>` metadata                           | i (layout) + iv (mockup-stale)            | P3 — polish                                          |
| **249** | Settings admin variants flicker between non-admin → admin on first paint       | ii (broken interaction) + iii (data lies) | P2 — affects every admin user on every settings load |
| **250** | Settings Profile section surfaces credential-bearer artifacts as user identity | iii (data-bound surface lies)             | P2 — affects all credential-bearer JWT flows         |
| **251** | Settings Notifications section returns error for credential-bearer JWTs        | ii (broken interaction) + iii (data lies) | P2 — composes with 250                               |
| **252** | Settings admin cross-link renders ASCII `->` instead of Unicode `→`            | i (layout / glyph)                        | P3 — cosmetic                                        |

Total spillovers from this audit: **5** (max budget 248-252; budget
fully spent without overshooting).

## What this audit deliberately did NOT do

- Did NOT re-file slice-154 findings F1-F11. All eleven are either
  merged-via-spillovers (F6, F8, F11), inline-fixed-in-154 (F1-F5,
  F7, F10), or deliberately omitted (F9).
- Did NOT debug the v1.14.0 500-error class (P0-A4 of slice 204).
  All five new findings are surfaced by a working `/settings` page
  on the dev/edge build; no 500s observed during audit.
- Did NOT file global-chrome findings (header/sidebar deltas, search
  box, version-footer "v?" display, status pill). Those are
  cross-page and belong in either the index/dashboard audit, the
  global-chrome audit, or a separate slice. The audit fleet's
  per-page reports will deduplicate via the slice 204 aggregate
  pass.
- Did NOT inline-fix any finding. Per slice 204 P0-A1.
- Did NOT spawn a Playwright browser instance (this audit relied
  on `curl` + the served HTML payload). The slice 204 spec mentions
  Playwright as one option; static HTML inspection sufficed for
  the five settings findings filed.

## Auth artifact scrub (per AC-7 of slice 204)

This audit log was written by an agent that consumed the admin JWT
at `/tmp/atlas-edge-admin-jwt`. No part of that JWT, no cookie
value, no `Bearer` token, and no `atlas_session` cookie value is
present in this file. Verifiable by:

```
grep -E "(Bearer [A-Za-z0-9._-]+|atlas_session=[A-Za-z0-9._-]+|atlas_jwt=[A-Za-z0-9._-]+)" docs/audit-log/204-page-audit-settings.md
```

(expected: zero matches; pre-commit hook from slice 204's AC-12
enforces this).

## Confidence summary

| Finding | Confidence on call                                                                                                                                                                   |
| ------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| 248     | HIGH — mockup line 6 and live `<title>` are observable side-by-side.                                                                                                                 |
| 249     | HIGH — SSR HTML literally contains `data-testid="settings-section-tokens-non-admin"` for an admin-JWT request.                                                                       |
| 250     | HIGH on the observation (live `/v1/me` literally returns `display_name: "API key "`); MEDIUM on the recommended fix (engineer's choice between 3 options recorded in decisions log). |
| 251     | HIGH on the observation; MEDIUM on the fix.                                                                                                                                          |
| 252     | HIGH — mockup uses `→`, live source uses `"->"`, both observable.                                                                                                                    |
