# #2144 — user term list and `<Term>` tooltips — decisions log

**Ticket:** https://github.com/mgoodric/security-atlas/issues/2144 (parent #2125)
**Shape:** one JSON term list at `web/help/terms.json` (21 terms). `<Term id>`
(`web/components/help/term.tsx`) shows a term's short definition on hover,
focus or tap and links to its entry on the docs site's Terms page. That page is
generated from the same JSON by the `docs-site/hooks/terms_page.py` mkdocs
hook. The build checks are pure functions in `web/lib/help/term-checks.ts`. Their
fixture tests and the real-tree gate are both in `web/lib/help/terms.test.ts`, so
the `Frontend · vitest` check fails on a defect.

detection_tier_actual: playwright
detection_tier_target: playwright

(The one bug found during the work was a race in an existing e2e test. See D8.)

## D1 — the term list lives under `web/`, not at the repo root

The spec (#2125) calls for one help content directory that the app and the docs
site both read. The web image's Dockerfile copies only `web/` into the build, and
Turbopack resolves imports inside the project root, so a root-level `help/` would
need Dockerfile and bundler changes. `web/help/` works for both readers: the app
imports it as `@/help/terms.json`, and the docs hook reads it by path from the
repo checkout. #2143's per-module Markdown can sit beside it. JSON needs no
parser dependency on either side (the hook is standard library only, like
`openapi_pipeline.py`).

## D2 — what "uses the same term name" means

A term's `glossary` is `GLOSSARY.md#<anchor>`. The check computes GitHub's
heading anchors and fails when the anchor matches no `##`/`###` heading
("broken"). It also fails when the term's `name` is neither that section's
heading (less a trailing `(…)` provenance note) nor one of its bold terms
("renamed"). Names are compared on letters and digits only, so `AuditPeriod`
matches "Audit period" and `evidence_freshness` matches "Evidence freshness".
Bold text in fenced code is ignored.

Only four terms link, because only four have a glossary entry under the same
name: risk treatment, acceptance status, evidence freshness and audit period.
The other terms either have no developer entry yet (verified, completed, org
mapping, self-approved; #2101 adds Self-approval) or use a different name there.
Linking those under a mismatched name would make the check meaningless. A term
gains a link when the glossary gains a matching entry.

## D3 — definitions follow what has merged, or what the in-flight tickets state

The dispatcher asked that definitions match current behaviour:

- **Excepted** follows #2112 (in flight): never shown or counted as a pass;
  posture counts it as covered and reports the share covered by exception
  separately. The developer glossary's Exception entry still says otherwise
  until #2112 corrects it. That's one reason this term has no glossary link yet.
- **Self-approved** follows #2101 (in flight): an org-level, admin-only,
  audit-logged opt-in, and a visible marker.
- **Posture** is per framework version, with "Scope not set" when no scope
  revision is activated (#2190).
- **Roll-up** (added as a 21st term, since the dispatcher named it) follows
  #2182: roll-ups are left out of register counts and top risks.
- **Freeze** covers audit periods and framework scopes (Matt's D3 on #1943,
  enforced by #1965 for the predicate): activated revisions are immutable, and a
  change makes a new revision.

If #2112 or #2101 lands differently, its PR should update `web/help/terms.json`.

## D4 — the adoption points

- **Dashboard posture tiles:** "posture" in the panel title (`PanelCard.title`
  widens from `string` to `ReactNode`) and "freshness" on each tile.
- **Risk register Residual header:** "Residual" is a `<Term>` beside the sort
  button, not inside it, because a button inside a button is invalid and can't
  be reached by keyboard. `SortableHeader` gains an optional `term` prop. The
  visible label is unchanged ("Residual (after controls)"), and so is the sort
  button's accessible name ("Residual (after controls), sorted …").
- **Control state label:** the control detail's Freshness card shows the
  control's state as "status fresh/stale/…". "status" is the `<Term>`, defined
  as evidence freshness. The control header's lifecycle badge (draft/active/
  retired) isn't one of the 20 terms, so it was left alone.

The edits stay inside those three places. #2080 (PageShell) and #2089 (toasts)
are open against the same pages.

## D5 — tooltip behaviour

- The trigger is a `<button>`, so it is focusable and tappable. `aria-describedby`
  points at the short definition, so a screen reader announces it on focus.
- The popup sits right after the button in the DOM, so Tab reaches its "Read
  the full entry" link. Focus leaving the pair closes it. It is
  `position: fixed`, placed from the button's rect, so the register's scrolling
  table wrapper doesn't clip it.
- Escape closes it and nothing moves focus. Escape holds until the pointer
  leaves or focus moves away, so the popup doesn't reopen under the user.
- A click or tap pins it open, and a second one closes it. A pointerdown outside
  unpins it. A 150 ms delay before hover-close lets the pointer cross the gap.
- An unknown id at runtime renders the plain text. The build check is the gate.

No Base UI primitive was used. Its Tooltip is built for hover and focus, not
tap, and its Popover moves focus into the popup by default. The dispatcher ruled
that out ("never takes focus away"). A small component that does exactly these
five things was simpler than overriding either one.

## D6 — the link opens the published docs site

As with `push-cta.ts`, the app serves no `/docs` route, so the link is
`https://mgoodric.github.io/security-atlas/terms/#<id>` with `target="_blank"`
and `rel="noopener noreferrer"`. The Playwright test stubs that host at the
network layer, so CI doesn't depend on GitHub Pages being up. The test asserts
that the new tab lands on the page at the term's anchor. `docs-publish.yml` now
also runs when `web/help/terms.json` changes, so the published page tracks the
list.

## D7 — new devDependency `@axe-core/playwright`

The ticket requires an axe pass, and the repo had no axe binding. The version
(`^4.14.0`) matches the one #2226 (#2080's route sweep) adds, so the two
lockfile diffs agree. It moves the workspace's `axe-core` from 4.11.4 to 4.14.0
(also used by `eslint-plugin-jsx-a11y`). The lockfile diff is only those two
entries.

## D8 — a race in `risks-sort-detail-badge.spec.ts`

"clicking the review-due header orders by date ascending" clicked the header,
then waited for the row order to read review-due descending. That is also the
default order (severity descending), so the wait passed before the first click
landed. The second click could then race the first. Locally against a production
build, the slightly heavier register (one `<Term>` per header render) made it
lose every time. On main it passed under the same conditions, but failed under
parallel `next dev` load. The test now waits for the header's `data-dir="desc"`
before the second click. No product change.
