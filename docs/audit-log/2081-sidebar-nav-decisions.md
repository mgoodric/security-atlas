# #2081 — sidebar lists every page, labels match page titles: decisions

detection_tier_actual: none
detection_tier_target: none

## D1 — one route -> title map, read by pages and nav

`web/lib/page-titles.ts` holds the title of every top-level page. The
sidebar's labels, each page's H1 (`PageShell` / `ListPage` `title`) and
each layout's `metadata.title` all read it, so a label and the title of
the page it opens are one string and cannot drift. `lib/nav.test.ts`
asserts every nav entry's label equals its route's title and that every
titled route sits in exactly one group.

The breadcrumb's section map (`lib/page-names.ts`) is left alone: it names
a URL's first segment so detail pages roll up to their section, which is
a different question.

## D2 — titles changed to match the labels the ticket names

The ticket fixes the labels; where a page's H1 differed, the H1 moved:

| Page                           | Old H1                               | New H1                   |
| ------------------------------ | ------------------------------------ | ------------------------ |
| `/dashboard`                   | Program                              | Dashboard                |
| `/calendar`                    | Compliance calendar                  | Calendar                 |
| `/evidence`                    | Evidence ledger                      | Evidence                 |
| `/controls/checklist`          | Role-scoped implementation checklist | Implementation checklist |
| `/action-plans`                | Action Plans                         | Action plans             |
| `/oscal/component-definitions` | Vendor Claims                        | Vendor claims            |
| `/policies`                    | Policy library                       | Policies                 |
| `/personnel-security`          | Personnel Security                   | Personnel security       |
| `/catalog/scf`                 | SCF Anchors                          | SCF catalog              |
| `/csf/<version>`               | `<framework> <version> Assessment`   | CSF assessment           |

The CSF page keeps the framework name and version, now at the head of
the subtitle (`csf-title` testid unchanged); `csf-assessment.spec.ts`
follows. "Program" survives as the sidebar group that holds Dashboard.

Document titles follow the same map. `/risks/hierarchy` and
`/controls/checklist` gained layouts, since they had been inheriting
"Risks" and "Controls" from their parents.

## D3 — Framework scopes needed an index page

`/framework-scopes/<framework_version_id>` existed, but nothing at
`/framework-scopes`, so there was no page for the sidebar entry to open.
The new index lists the catalog's frameworks (`GET /v1/frameworks`, the
same read the SoA tab uses) and links each to its latest version's
scope; a framework with no loaded version says so instead of linking.

## D4 — CSF assessment links to the loaded version

The CSF page is keyed by framework version. The sidebar reads
`GET /v1/frameworks` server-side (`fetchCsfVersionId`,
`lib/api/self.server.ts`) and lists CSF assessment only when the
`nist_csf` framework has a `latest_version_id`, linking to
`/csf/<that id>`. Any probe failure hides the entry (fail-closed, as the
admin and flag probes do).

## D5 — gates unchanged; per-role coverage

`gateNavItems` (flags) and `shouldShowAdminEntry` (Admin) are applied
exactly as before, now inside the pure `buildNav` (`lib/nav.ts`). Vitest
pins viewer and auditor: with flags off they see the same entries as
before plus the four formerly URL-only pages, and never Admin with flags
on or off.

## D6 — the flag-on e2e case uses a dedicated tenant

The flag gate is read server-side, so a browser route-mock cannot turn it
on, and turning it on for the shared demo tenant would break the
flag-off assertions running in parallel workers. `fixtures/e2e/sidebar-nav.sql`
turns `board.reporting` and `oscal.export` on for tenant
`…2081`, and the spec mints a bearer for it via `/v1/test/issue-jwt`.

The CSF-loaded case stays at the vitest tier: NIST CSF is global catalog
data, so loading it in e2e would list it for every tenant and break the
"no CSF framework, no entry" assertion.
