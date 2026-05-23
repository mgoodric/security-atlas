# 248 — Settings page lacks page-specific `<title>` metadata

**Cluster:** Frontend
**Estimate:** 0.1d
**Type:** AFK
**Status:** `ready`
**Parent:** #204 (per-page UI parity audit fleet) — settings audit. Slice 154 (settings-only audit) is the prior-art reference; this finding was not surfaced by 154 because 154 focused on in-page section content, not page-level metadata.

## Narrative

Live `/settings` page on `atlas-edge.home.gmoney.sh/settings` ships the
default app `<title>security-atlas</title>` (visible in the served HTML's
`<head>` and in the Next.js metadata payload at the bottom of the SSR
body). The mockup at `Plans/mockups/settings.html` line 6 sets a
page-specific `<title>Settings · security-atlas</title>`.

The page-source file `web/app/(authed)/settings/page.tsx` (the SettingsPage
component) does NOT export a `metadata` object. Every other authed page
that ships a per-page title does so via Next.js App Router's
`export const metadata: Metadata = { title: ... }` convention.

**Why this matters:**

1. **Tab/window title is the operator's primary disambiguator** when
   they have three security-atlas tabs open (dashboard, controls,
   settings). Today all three render as "security-atlas" in the browser
   tab; the operator can't tell them apart without clicking.
2. **Mockup parity** — the mockup is explicit on the title; the live
   page silently drops it.
3. **A11y / SEO** — title is the first thing a screen reader announces
   on page change; "security-atlas" → "security-atlas" gives no signal
   the route changed.

This is the smallest viable fix: a 5-line metadata export.

## Threat model

**Verdict.** `no-mitigations-needed`. Page title is a pure UI string
with no security surface. No PII, no tenant data, no role-gated content
exposed via title.

## Acceptance criteria

- **AC-1.** `web/app/(authed)/settings/page.tsx` exports a
  `metadata: Metadata = { title: "Settings · security-atlas" }`
  matching the mockup line 6.
- **AC-2.** Curl of `/settings` HTML's `<title>` element returns
  `Settings · security-atlas` (verified against `grep -oE '<title[^>]*>[^<]+</title>' /tmp/settings-live.html`).
- **AC-3.** No regression in the existing 5 settings sections —
  pre-existing vitest + Playwright settings specs continue green.
- **AC-4.** Pattern is portable: future per-page audits (e.g. slice 204
  spillovers for other mockup pages) can use the same metadata-export
  pattern as their fix path.

## Constitutional invariants honored

- **Article VII (Simplicity Gate).** A 5-line metadata export. No new
  component, no new dependency.
- **Slice 204 audit posture.** This is a read-only-audit spillover; the
  audit itself did not fix the finding inline (P0-A1 of slice 204).

## Canvas references

- `Plans/canvas/12-ui-fill-in-design-decisions.md` §4 (settings-page
  ownership) — settings is a user-only page; per-page title aligns
  with the affordance.
- `Plans/mockups/settings.html` line 6 — `<title>Settings ·
security-atlas</title>`.

## Dependencies

- **#204** (this slice's parent — per-page UI parity audit fleet).
- **#154** (settings-only audit) — reference; not blocking.

## Anti-criteria (P0 — block merge)

- **P0-248-1.** Does NOT touch any settings section content. Pure
  metadata export.
- **P0-248-2.** Does NOT introduce a `<head>` shadow at the page level
  (the App Router metadata convention is the canonical way).
- **P0-248-3.** Does NOT change the global `<title>` default in
  `web/app/layout.tsx` — only the settings route gets a per-page
  override.

## Skill mix (1-2)

1. Next.js App Router metadata export
2. Playwright/vitest regression (one assertion against the served
   `<title>`)
