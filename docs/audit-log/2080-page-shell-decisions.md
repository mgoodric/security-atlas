# Issue #2080 — PageShell on every route, with the route sweep in report mode

One page frame (`web/components/shell/page-shell.tsx`) and one app chrome
(`web/components/shell/app-shell.tsx`) for every authenticated route, plus a
Playwright route sweep (`web/e2e/route-sweep.spec.ts`) that enforces title,
no horizontal scroll and focus rings at 1316px and 1200px, and reports axe
serious and critical findings without failing.

- `detection_tier_actual`: playwright
- `detection_tier_target`: playwright

The two bugs the sweep surfaced while it was written (admin subpages losing
the title template, and Tab order leaking between widths in the spec itself)
were both caught by the sweep on its first local run.

## D1 — Two components: AppShell for the chrome, PageShell for the page

**Decision.** `AppShell` owns the skip link, top bar, sidebar and the
`main#main-content` column. `(authed)`, `/admin`, `/audit`, `/audit-log` and
the root 404 all render through it. `PageShell` owns the page's H1,
subtitle, actions, optional tabs and width, and every page renders through
it. `ListPage` is now a thin layer over `PageShell`.

**Why.** Three of the four authenticated trees outside `(authed)` had
rebuilt part of the chrome by hand or skipped it. One server component
removes that drift.

## D2 — Document titles are route metadata, not a PageShell prop

**Decision.** The root layout sets `title.template: "%s · security-atlas"`.
Every section layout or server page sets a bare name. Client pages that
cannot export metadata get a metadata-only `layout.tsx` (admin subpages,
`/csf`, `/framework-scopes`). The admin layout repeats the template, because
a plain `title` string on a layout stops the root template reaching its
children's titles.

**Why.** Most pages are client components and cannot export metadata. A
`<title>` rendered from the body would compete with the one Next writes.
Detail routes inherit their section's name ("Risks · security-atlas"), which
is specific without a per-record fetch.

## D3 — Widths

**Decision.** `reading` (760px max, centred) for Settings, Demo tools, Board
packs, Attest, every create/edit form, and the two detail pages that were
already a narrow column (action plan, personnel checklist). `full` for
everything else.

## D4 — Pages left partly outside PageShell

- `/board-packs/[id]`'s main render is a full-bleed `.paper` print sheet that
  renders its own header and `<main>`. Its loading and error states use
  PageShell. The sheet itself is unchanged, and axe reports its nested
  `<main>`.
- `/audit` and `/audit/[controlId]` render the auditor workspace in
  `<AppShell bleed>`. The workspace's period bar is its header, and its
  panes scroll independently. The no-period and error states use PageShell.
  The workspace's inner `<main>` became a labelled `<section>` so the page
  has one main landmark.

## D5 — The route sweep derives its routes from the filesystem

**Decision.** Every directory under `app/(authed)`, `app/admin`,
`app/audit` and `app/audit-log` that holds a `page.tsx` is a route. Dynamic
segments resolve through `PARAM_VALUES`, using seeded rows where the harness
has them and otherwise a well-formed id that matches no row, so the detail
page's not-found or error state is what gets checked. A dynamic segment
with no entry fails the sweep and names itself. The root 404 is added
explicitly.

**Why.** A hand-written list goes stale the first time a page is added.

## D6 — Focus rings

**Decision.** A base-layer rule draws `outline: 2px solid var(--ring)` on
`:focus-visible` for links, buttons, form controls, `summary` and positive
`tabindex`. It is in `:where()`, so any component with its own
`focus-visible:ring-*` overrides it. The two native filter selects that set
`focus:outline-none` with no replacement (`FilterPills`, the controls
saved-view select) now draw the token outline.

The sweep checks the first tabbable control (the skip link) and the first
tabbable control inside the main column.

## D7 — axe is reported, not enforced

**Decision.** One JSON file per route in `web/axe-report/`, uploaded on
every run as the `route-sweep-axe-report` artifact, and attached to each
test. On the first local run all 54 routes reported
`label-content-name-mismatch`, three reported `color-contrast`, one `label`
and one `aria-prohibited-attr`. Enforcement is a later ticket.

`@axe-core/playwright` is a new web devDependency, added because the ticket
names axe. It also moves the workspace's `axe-core` from 4.11 to 4.14.
