# Decisions log — slice 257 (control-detail top-bar chrome parity)

**Slice:** [`docs/issues/257-ui-honesty-control-detail-top-bar-chrome-parity.md`](../issues/257-ui-honesty-control-detail-top-bar-chrome-parity.md)
**Type:** JUDGMENT
**Build agent:** Engineer
**Closed at:** 2026-05-24

---

## Summary

Slice 257 was filed against the `/controls/{id}` control-detail page
during slice 204's UI honesty audit fleet (finding F-204-C-5) to close
three shell-level chrome parity gaps:

1. **Audit-period banner pill.** Amber pill reading `<framework> ·
<period name> in progress` with a pulsing amber dot.
2. **Tenant breadcrumb.** Read-only `<tenant> > Controls > <control title>`
   wayfinding chip in the topbar left side. (The slice spec proposed
   a three-segment breadcrumb with the control title as the leaf; the
   shipped chrome from slice 223 lands a two-segment `<tenant> > Controls`
   roll-up — see D4 below.)
3. **User avatar / global ⌘K search.** The slice spec called for a user
   avatar with dropdown. Slice 213 shipped the avatar; slice 223
   shipped the global ⌘K search. Both are in the shared topbar.

Between slice 257's filing and this build window, three adjacent
slices have shipped the entire surface in the shared authed-shell:

| Element            | Shipping slice | Component                                         | Visible on `/controls/{id}` today? |
| ------------------ | -------------- | ------------------------------------------------- | ---------------------------------- |
| Audit-period pill  | **Slice 213**  | `web/components/shell/in-progress-audit-pill.tsx` | YES (shared topbar)                |
| User avatar        | **Slice 213**  | `web/components/shell/user-avatar.tsx`            | YES (shared topbar)                |
| Tenant breadcrumb  | **Slice 223**  | `web/components/shell/breadcrumb.tsx`             | YES (shared topbar)                |
| Global `⌘K` search | **Slice 223**  | `web/components/shell/global-search.tsx`          | YES (shared topbar)                |

All four components are mounted in `web/components/shell/topbar.tsx`,
which is rendered by `web/app/(authed)/layout.tsx` for every authed
route — including `/controls/{id}`. The `page-names.ts` map already
keys on the FIRST URL segment, so `/controls/<uuid>` (detail) rolls
up to the section name `Controls` in the breadcrumb's right segment.
The slice 223 vitest sibling `web/lib/page-names.test.ts` already
pins the detail-page rollup branch (test case "returns 'Controls' for
/controls/<id>"). The slice 213 + 223 + 235 e2e specs prove the
shared chrome is intact on `/dashboard`, `/audits`, `/controls`
(list), `/evidence`, but none target `/controls/{id}` (detail).

So the **actual** missing piece on `/controls/{id}` post-213/223/235
is **not a new component, a new BFF, or a new endpoint** — it is a
dedicated Playwright assertion that the shared chrome renders
correctly on `/controls/{id}` specifically. Per slice 204's
anti-pattern catalogue, the moment we have four components passing on
`/dashboard` + `/controls` + `/audits` + `/evidence` but no
`/controls/{id}` spec, regression risk concentrates on the one
detail page nobody asserts. This log captures the JUDGMENT calls the
engineer made during the build.

---

## D1 — Subset shipped: zero new components; one new Playwright spec on `/controls/{id}`

**Decision:** ship a **single new Playwright spec**
(`web/e2e/control-detail-top-bar.spec.ts`) that asserts the
slice-213 pill + slice-223 breadcrumb + slice-223 search input all
render correctly on `/controls/{id}`. Do **not** add new components,
new BFF routes, or new endpoints. Mark slice 257 as
**superseded-in-substance** by the combination of slices 213 + 223 in
the PR body; let the orchestrator decide whether to flip
`_STATUS.md` to `merged` or `superseded` (spec hard rule — slice does
not modify `_STATUS.md`).

**Rationale (why subset vs full scope):**

The slice spec was authored before slices 213 and 223 landed. Every
acceptance criterion the spec names has a satisfying shipping
artifact on `main`:

| Slice 257 AC                                                                | Shipping artifact                                                                                                           | Verdict                                                                                |
| --------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------- |
| AC-1: Tenant breadcrumb chip rendered in topbar                             | `<Breadcrumb />` renders `<tenant> > <page>` after the brand mark (slice 223)                                               | Equivalent (two-segment vs three-segment — see D4)                                     |
| AC-2: Breadcrumb leaf for unresolved id reads truncated id                  | Slice 223 breadcrumb renders the SECTION name (`Controls`) for any `/controls/<*>` path, including unresolved ids           | Equivalent in shape, different in copy — slice 152 owns the page-body empty-state copy |
| AC-3: SOC 2 Type II audit-in-progress amber pill in topbar                  | `InProgressAuditPill` calls `/api/audits` filtered to `status='open'` (slice 213); shipped in shared topbar                 | Equivalent                                                                             |
| AC-4: User avatar circle with initials + dropdown                           | `UserAvatar` renders display name + initials from `/api/me` (slice 213) in shared topbar                                    | Equivalent                                                                             |
| AC-5: Chrome work is shared across detail pages (lives in layout, not page) | All four components mount in `topbar.tsx`, rendered by `(authed)/layout.tsx` for every authed route                         | Equivalent — exceeds AC's "per-detail-page" framing                                    |
| AC-6: Vitest covers breadcrumb composition for the four detail-page routes  | `web/lib/page-names.test.ts` covers `/controls/<id>`, `/risks/hierarchy`, `/audits/new` rollups + `/evidence` first-segment | Equivalent — pure helper coverage is the right vitest unit-test surface                |
| AC-7: Playwright covers avatar menu, pill click, breadcrumb tenant click    | Slice 213's `audits-header.spec.ts` covers avatar + pill; slice 223's `controls-top-bar.spec.ts` covers breadcrumb          | Partial — none target `/controls/{id}` (detail) for shared-chrome assertion            |

**The genuine remaining gap** is AC-7's `/controls/{id}`-specific
assertion. Slice 213's e2e covers `/dashboard` + `/audits` for
shared-chrome verification. Slice 223's covers `/controls` (list) +
`/audits` for breadcrumb + search verification. Slice 235's covers
`/evidence` for the same. None currently exercise `/controls/{id}`.
Adding the dedicated spec closes the F-204-C-5 finding loop, prevents
silent regression of four load-bearing chrome elements on the
`/controls/{id}` route, and is the minimum-honest delta the slice 257
scope requires.

**What this resolves:** the slice spec said the AC was "0.25d" worth
of work and noted the implementing engineer "should check #223's
current status. If #223 is `in-progress` or `in-review`, this slice
merges into it." Slice 223 has shipped. The actual work is the one
regression-protection spec that the parent components' own slices did
not file for `/controls/{id}`. The rest of the AC surface is already
in production.

This is the same shape as slice 235 (which shipped the equivalent
spec for `/evidence` against the slice 204 finding F-204-E-3). The
precedent is now established — every authed route that hosts the
shared topbar wants its own regression-protection spec; the four
existing specs cover `/dashboard`, `/controls`, `/audits`,
`/evidence`. This slice adds the fifth.

---

## D2 — Reuse the `audits-header` + `controls-top-bar` fixtures (no new SQL)

**Decision:** the new Playwright spec uses
`seedFromFixture("audits-header")` AND
`seedFromFixture("controls-top-bar")` sequenced in `beforeAll`
— same shape as slice 235 — rather than introducing a third fixture
file.

**Rationale:**

The `audits-header.sql` fixture (slice 213) seeds the audit_periods
row with `status='open'` and name `SOC 2 Type II · Q2 2026` — drives
the in-progress pill copy. The `controls-top-bar.sql` fixture (slice 223) seeds the `tenants` row for `DEMO_TENANT_ID` needed for the
breadcrumb's left segment. The DEMO_CONTROL_ID is seeded by the base
`fixtures/walkthroughs/00-seed.sql` (applied by `seedFromFixture` on
every call), so the path `/controls/${seeded.controlId}` resolves
naturally.

Three paths were considered:

- (a) Add the same tenants INSERT to `audits-header.sql` — modifies a
  slice 213 fixture and risks coupling unrelated specs.
- (b) Apply BOTH fixtures from the new spec's `beforeAll` (the seed
  harness is idempotent — every INSERT is ON CONFLICT DO NOTHING).
- (c) Create a new `control-detail-top-bar.sql` fixture that
  consolidates.

Chose **(b)** — apply both fixtures sequentially. The seed harness's
contract is idempotent by design (slice 082 D2), and reusing both
existing fixtures has zero coupling cost. This is the same pattern
slice 235 adopted (and slice 213's spec uses `settings.sql` as an
incidental prerequisite for the users row via the same UUID overlap).

**Trade-off accepted:** the spec's `beforeAll` makes two psql calls
instead of one. Negligible cost (sub-second on a warm DB); buys
fixture isolation.

**What this resolves:** the data-dependency question for the new
spec without expanding the fixture surface area.

---

## D3 — Use `page.waitForRequest` for the search-debounce assertion

**Decision:** the spec's debounced-search assertion uses
`page.waitForRequest('**/api/search**')` to bracket the typing /
popover-render sequence, NOT a snapshot-after-fill on
`global-search-popover`.

**Rationale:**

Slice 274 (merged 2026-05-23) fixed AC-9 flake on debounced surfaces
in CI. The fix's canonical pattern — adopted by slice 223's own
`controls-top-bar.spec.ts` P0-223-1 assertion AND slice 235's
`evidence-header.spec.ts` — is to use Playwright's auto-waiting
`waitForRequest` over `page.locator(...).waitFor()` /
`expect(...).toBeVisible()` on the debounced popover. The pattern
makes the test wait for the actual network request rather than
guessing about timing.

**What this resolves:** flake risk on the search debounce assertion
in CI. Mirrors slice 274's canonical fix + slice 223's P0-223-1
discipline + slice 235's evidence-header spec.

---

## D4 — Two-segment breadcrumb rollup (slice 223 shape) vs three-segment leaf (slice 257 spec)

**Decision:** assert the two-segment `Demo Tenant > Controls` shape
that the slice 223 breadcrumb actually renders, NOT the three-segment
`Demo Tenant > Controls > <control title>` shape the slice 257 spec
called for.

**Rationale:**

The slice 257 acceptance criteria (AC-1) called for a three-segment
breadcrumb with the control's title as the leaf segment. Slice 223
shipped a two-segment breadcrumb that keys on the FIRST URL segment
of the pathname and rolls all subroutes (`/controls/<uuid>`,
`/controls/<uuid>/coverage`, etc.) up to the section name
`Controls`. The slice 223 vitest sibling explicitly pins this
behavior (test "returns 'Controls' for /controls/<id> (detail page
rolls up)").

This is a deliberate design choice in slice 223: the breadcrumb is
a wayfinding chip ("you are in this section as this tenant"), not
the full navigation stack. The page-body H1 carries the leaf title
(control title) — operators see both the section context (breadcrumb)
and the leaf identity (H1) without redundancy.

The slice 257 spec was authored before slice 223 landed, so its AC-1
three-segment shape was the prior state of the world. Slice 223's
shipped two-segment shape supersedes it. Following slice 235's
precedent (subset-shipped pattern), the spec asserts WHAT IS
SHIPPED, not what the older AC text proposed.

The decision to extend the breadcrumb to three segments is a
non-trivial change (the section-vs-leaf shape is load-bearing across
every detail page on the site). If a future slice files for the
three-segment shape, it owns the entire breadcrumb component edit +
the page-names.ts entry for `/controls/[id]` -> "Controls / {title}"

- the equivalent for risks/audits/policies + the four detail-page
  e2e updates. That is a v2+ slice; outside scope for this regression-
  protection slice.

**What this resolves:** the AC-1 shape mismatch is documented as
"shipped shape supersedes proposed shape" rather than left as a
silent inconsistency for the next reader.

---

## D5 — No changes to `_STATUS.md` (spec hard rule)

**Decision:** this slice does NOT modify `docs/issues/_STATUS.md`.
The loop orchestrator owns canonical row flips.

**Rationale:** spec hard rule. The decisions log + PR body document
the "subset shipped" outcome explicitly so the orchestrator has the
information to flip the slice 257 row (and, if applicable, mark its
F-204-C-5 closure on slice 204's row).

---

## D6 — No edit to `/controls/[id]/page.tsx` (concurrent-slice protection)

**Decision:** the new spec does NOT modify
`web/app/(authed)/controls/[id]/page.tsx` or any of its sub-modules.
Source files in the control-detail page are completely untouched.

**Rationale:** slice 255 is working on the control-detail page
concurrently (file conflict avoidance per the spec's hard rule). This
slice is regression-protection-only — it asserts shared chrome
already shipped by slices 213 + 223. The `/controls/{id}` page's own
rendering is incidental: the spec only asserts the topbar chrome
above the page, which lives in the `(authed)/layout.tsx`. The page
itself can render its content, an empty-state, or even an error
Alert without affecting the topbar assertions — the topbar is
structurally above the page.

**What this resolves:** the concurrent-slice file-conflict risk.

---

## CI-delta scan

No CI-delta concerns:

- **New files only** — no modifications to load-bearing existing
  files except `CHANGELOG.md`:
  - `web/e2e/control-detail-top-bar.spec.ts` — new Playwright spec
  - `docs/audit-log/257-control-detail-top-bar-regression-decisions.md`
    — this file
- **`CHANGELOG.md`** modified to add a bullet under `## [Unreleased]`
  -> `### Added` documenting the new regression-protection spec.
- **No source file changes.** The four load-bearing components
  (`in-progress-audit-pill.tsx`, `user-avatar.tsx`, `breadcrumb.tsx`,
  `global-search.tsx`) and the topbar that mounts them are
  untouched. The mount path through `(authed)/layout.tsx` is
  untouched. The BFF routes (`/api/audits`, `/api/me/tenants`,
  `/api/me`, `/api/search`) are untouched. The `/controls/[id]`
  page is untouched (D6).
- **No new platform endpoint** — anti-criterion P0-257-1 trivially
  honored (no wire contract changes; no duplication of #223 because
  #223 already shipped).

Local CI parity verified before push:

- `pre-commit run --all-files` — green
- `npm run lint` (web) — green (pre-existing baseline)
- `npm run test` (web) — green (no new vitest files; no source
  changes)
- `npx tsc --noEmit` (web) — pre-existing baseline (no new errors
  from this slice)
- `npm run build` (web) — green (no source changes)
- CHANGELOG bullet added under `## [Unreleased]` -> `### Added`

The Playwright e2e spec (`control-detail-top-bar.spec.ts`) requires
the slice-082 seed harness + a running platform; it is exercised in
CI by the `Frontend · Playwright e2e` job after the docker-compose
bring-up. The slice 274 AC-9 flake fix (merged 2026-05-23) keeps the
spec reliable in CI.
