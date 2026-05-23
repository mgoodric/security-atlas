# 248 — Settings page-specific `<title>` metadata · decisions log

**Slice:** `docs/issues/248-settings-page-specific-title-metadata.md`
**Branch:** `frontend/248-settings-page-title`
**Author:** Claude (engineer subagent)
**Date:** 2026-05-23

This slice is `Type: AFK`. The spec is unambiguous in the
acceptance-criteria block (literal title string, page-level metadata
export). One real design call surfaced during implementation:
client-component vs server-component split for the metadata export.
This log records that call plus a couple of small auxiliary choices.

---

## Decisions made

### D1 — Add a sibling `layout.tsx` instead of editing `page.tsx`

**Decision:** **Create `web/app/(authed)/settings/layout.tsx` as a
thin server component that exports `metadata` and renders `{children}`
as a pass-through.** Do NOT add `export const metadata` to `page.tsx`
directly.

**Why this decision was needed.** The slice spec asks for the smallest
possible fix and explicitly names the page-source file
(`web/app/(authed)/settings/page.tsx`) as the change target. On
inspection, that file is a client component (`"use client"` at line
60). Next.js App Router forbids exporting `metadata` from a client
component — the build fails at compile time with a clear error:

> You are attempting to export "metadata" from a component marked
> with "use client", which is disallowed. Either remove the export, or
> the "use client" directive if you no longer need it.

So the spec's "5-line metadata export" cannot land directly on
`page.tsx` without first refactoring the entire 1500-line page from a
client component into a server-component shell + client-component
inner — a refactor far outside the spec's "smallest viable fix"
intent.

**Options considered:**

| Option                                                                                              | Why rejected / why chosen                                                                                                                                                                                                                                                                                                                                                                                                            |
| --------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| (a) **Sibling server-component `layout.tsx`** with pass-through `{children}` — _chosen_             | Canonical Next.js App Router pattern for adding metadata to a route owned by a client component. ~10 lines. No refactor of `page.tsx`. The parent `(authed)/layout.tsx` continues to provide topbar/sidebar chrome; this sub-layout adds only a metadata layer. Slice 248 spec's "5-line metadata export" lands in the layout instead of the page — same surface area, same intent.                                                  |
| (b) Refactor `page.tsx` into a server-component shell + a `settings-client.tsx` inner client module | Rejected. Spec is explicit: "smallest viable fix" (narrative §last paragraph; Article VII Simplicity Gate cited in the constitutional-invariants block). A 1500-line refactor to enable a 5-line metadata export inverts the cost/benefit. Also breaks the slice 103/108/154/163/170/203 audit-trail comment in the page file (those comments rely on the page-file being the routing entry point) — would need to migrate them too. |
| (c) Add a `<title>` element directly via `next/head` inside the page body                           | Rejected as a P0 anti-criterion of the spec (`P0-248-2: does NOT introduce a `<head>` shadow at the page level — the App Router metadata convention is the canonical way`). Also: `next/head` is the Pages Router API and is documented as the wrong choice for App Router pages.                                                                                                                                                    |
| (d) Edit the root `web/app/layout.tsx` to use a `title.template` or per-route default               | Rejected as a P0 anti-criterion of the spec (`P0-248-3: does NOT change the global title default — only the settings route gets a per-page override`). Would also require touching every other authed page to opt into the template, which is a separate slice's scope (parent #204 audit fleet).                                                                                                                                    |

**Rationale for (a).** Three reinforcing factors:

1. **Next.js documentation precedent.** The App Router docs name
   "client components cannot export metadata" as a known constraint
   and name the sibling-layout pattern as the resolution. Following
   the platform's own escape hatch is the path of least surprise for
   any future maintainer.
2. **Scope match.** The slice asks for a metadata export. A
   server-only layout that exports metadata is, semantically, a
   metadata export — placed at a sibling file because the page file
   cannot host it.
3. **Audit-trail preservation.** Slice 103/108/154/163/170/203 layered
   commentary blocks on `page.tsx`. Option (b) would orphan or
   relocate those. Option (a) leaves them untouched, which matches
   the slice's P0-248-1 anti-criterion ("does NOT touch any settings
   section content").

**Confidence:** **high.** The compile-time constraint is hard; the
sibling-layout pattern is the documented resolution; the spec's
"smallest viable fix" intent maps cleanly onto a ~10-line
server-component shim.

### D2 — Title string is exactly `Settings · security-atlas` with U+00B7 middle dot

**Decision:** **Title is the literal `Settings · security-atlas` —
middle dot is U+00B7 (Unicode "MIDDLE DOT"), not U+2022 (bullet) or
ASCII pipe `|` or ASCII dot `.`.**

**Rationale.** The spec's AC-1 binds the exact string verbatim, and
the mockup at `Plans/mockups/settings.html` line 6 uses the same
character. Matching peer mockups across the set
(`Plans/mockups/dashboard.html`, `Plans/mockups/controls.html`,
`Plans/mockups/risks.html`) all use the same `Page · security-atlas`
shape with U+00B7. Picking any other separator would break the
emerging mockup convention.

**Confidence:** **high.** Pattern-matched against the mockup set.

### D3 — Single Playwright assertion, not vitest

**Decision:** **Add one `toHaveTitle` assertion as `AC-13` to the
existing `web/e2e/settings.spec.ts`.** Do NOT add a vitest unit test.

**Rationale.** The metadata export is server-rendered HTML; vitest
runs against modules, not the rendered page. Asserting via vitest
would require importing the layout module and inspecting the
exported `metadata` object — which tautologically tests that the
constant we just wrote is the constant we just wrote. Playwright's
`toHaveTitle` asserts the visible-to-operator contract (the browser
tab reads the right thing) at a real cost of one network hop in an
already-running spec file. The slice spec's AC-2 specifically names
curl as the verification method, which is the same shape of
assertion at the HTTP layer — Playwright achieves the same intent at
the layer the operator actually sees.

The orchestrator brief permitted Playwright "if a settings spec
already touches the page title; otherwise vitest is overkill". No
existing spec touches the title — but `web/e2e/settings.spec.ts`
already exists, already loads `/settings`, and adding one assertion
into its existing test.describe block is cheaper than authoring a
vitest target for a one-line constant. Reading the brief
generously, this is the smaller of the two regression nets.

**Confidence:** **high.** Lowest-cost regression net for an
observable browser-tab assertion.

---

## Revisit once in use

Specific items the maintainer should re-evaluate post-merge, in
order of expected priority:

1. **Other authed pages missing per-route `<title>` metadata.** Per
   the orchestrator's spillover directive, this slice did NOT fix
   other pages inline. A grep of `web/app/(authed)/**/page.tsx` for
   `"use client"` finds many candidates; each missing a per-route
   title is a sibling slice in the #204 audit fleet. Suggested
   action: surface a sweep slice that catalogs every authed route
   without its own metadata layer, then file fix-slices in batches
   (e.g., five routes per fix-slice) rather than one slice per page.
2. **Root `title.template` as a future option.** With this slice,
   the settings route now overrides the root title via a sibling
   layout. If five-plus authed routes adopt the same pattern, the
   maintainer should consider switching the root metadata to a
   `title.template: "%s · security-atlas"` so per-route layouts
   only need to declare the per-page segment (`Settings`,
   `Controls`, `Dashboard`). That refactor IS a P0-248-3
   violation **if done in this slice**, so it stays out of scope
   here; doing it as a deliberate template-migration slice once
   the pattern is established is the right move.
3. **`title.template` interaction with the `next/og` social-card
   block.** Root `web/app/layout.tsx` declares `openGraph.title` and
   `twitter.title` as `"security-atlas"`. Per-route layouts that
   only set `title` (not `openGraph.title` / `twitter.title`)
   inherit the root social-card title unchanged — i.e., OG /
   Twitter scrapers for `/settings` see the root title, which is
   the desired behavior (the settings page is not socially shared
   as a distinct surface). This is fine for v1 but worth a
   maintainer pass once any deep-link page wants per-page OG cards.

---

## Confidence summary

| Decision                                                    | Confidence |
| ----------------------------------------------------------- | ---------- |
| D1 — sibling `layout.tsx` (vs. refactor `page.tsx`)         | **high**   |
| D2 — literal `Settings · security-atlas` (U+00B7 separator) | **high**   |
| D3 — single Playwright `toHaveTitle` assertion              | **high**   |

No `medium` or `low`-confidence decisions in this slice.

---

## Anti-criteria honored

All three spec anti-criteria are honored:

- **P0-248-1.** No settings section content touched. The change is a
  new server-component shim (`layout.tsx`) plus one Playwright
  assertion. `page.tsx` is byte-identical pre/post.
- **P0-248-2.** No `<head>` shadow at the page level. Metadata is
  declared via the App Router `metadata` convention on a server
  component, which is the canonical way per the Next.js docs.
- **P0-248-3.** No change to the global `<title>` default in
  `web/app/layout.tsx`. The root metadata is byte-identical
  pre/post; the `/settings` route gets the override via cascade,
  every other route continues to render the root default.

The orchestrator's anti-criteria are also honored: no `_STATUS.md`
change; no `CHANGELOG.md` change; no layout chrome touched.
