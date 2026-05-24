# Decisions log — slice 235 (evidence header chrome parity)

**Slice:** [`docs/issues/235-ui-honesty-evidence-header-chrome-missing-audit-banner-search.md`](../issues/235-ui-honesty-evidence-header-chrome-missing-audit-banner-search.md)
**Type:** JUDGMENT
**Build agent:** Engineer
**Closed at:** 2026-05-24

---

## Summary

Slice 235 was filed against the `/evidence` page during slice 204's UI
honesty audit fleet (finding F-204-E-3) to close three shell-level
chrome parity gaps:

1. **Audit-period banner pill.** Amber pill reading `<framework> ·
<period name> in progress` with a pulsing amber dot.
2. **Tenant breadcrumb.** Read-only `<tenant> > <page>` wayfinding
   chip in the topbar left side.
3. **Global `⌘K` search input.** Inline search box with placeholder
   `Search controls, evidence, risks…` + a `⌘K` kbd hint.

Between slice 235's filing and this build window, two adjacent slices
have shipped the entire surface in the shared authed-shell:

| Element            | Shipping slice | Component                                         | Visible on `/evidence` today? |
| ------------------ | -------------- | ------------------------------------------------- | ----------------------------- |
| Audit-period pill  | **Slice 213**  | `web/components/shell/in-progress-audit-pill.tsx` | YES (shared topbar)           |
| Tenant breadcrumb  | **Slice 223**  | `web/components/shell/breadcrumb.tsx`             | YES (shared topbar)           |
| Global `⌘K` search | **Slice 223**  | `web/components/shell/global-search.tsx`          | YES (shared topbar)           |

All three components are mounted in `web/components/shell/topbar.tsx`,
which is rendered by `web/app/(authed)/layout.tsx` for every authed
route — including `/evidence`. The `page-names.ts` map already carries
the `evidence → "Evidence"` entry (slice 223). The slice 213 e2e spec
already proves the in-progress pill is shared chrome by visiting
`/dashboard` (AC-2 — "pill renders on a non-audits page too"). The
slice 223 e2e spec proves the breadcrumb + search render on `/controls`
and `/audits`.

So the **actual** missing piece on `/evidence` post-213/223 is **not a
new component, a new BFF, or a new endpoint** — it is a dedicated
Playwright assertion that the shared chrome renders correctly on
`/evidence` specifically. Per slice 204's anti-pattern catalogue, the
moment we have three components passing on `/controls` + `/audits` +
`/dashboard` but no `/evidence` spec, regression risk concentrates on
the one page nobody asserts.

This log captures the JUDGMENT calls the engineer made during the
build.

---

## D1 — Subset shipped: zero new components; one new Playwright spec on `/evidence`

**Decision:** ship a **single new Playwright spec**
(`web/e2e/evidence-header.spec.ts`) that asserts the slice-213 pill +
slice-223 breadcrumb + slice-223 search input all render correctly on
`/evidence`. Do **not** add new components, new BFF routes, or new
endpoints. Mark slice 235 as **superseded-in-substance** by the
combination of slices 213 + 223 in the PR body; let the orchestrator
decide whether to flip `_STATUS.md` to `merged` or `superseded` (spec
hard rule — slice does not modify `_STATUS.md`).

**Rationale (why subset vs full scope):**

The slice spec was authored before slices 213 and 223 landed. Every
acceptance criterion the spec names has a satisfying shipping artifact
on `main`:

| Slice 235 AC                                                                                  | Shipping artifact                                                                      | Verdict                                                                      |
| --------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------- |
| AC-1: `AuditPeriodBanner.tsx` calls `/api/audit-periods?status=active`                        | `InProgressAuditPill` calls `/api/audits` filtered to `status='open'` (slice 213)      | Equivalent                                                                   |
| AC-2: Banner mounted in shell, visible on every authed route                                  | Pill mounted in `topbar.tsx`, rendered by `(authed)/layout.tsx` (slice 213)            | Equivalent                                                                   |
| AC-3: Tenant breadcrumb after brand mark                                                      | `<Breadcrumb />` renders `<tenant> > <page>` after the brand mark (slice 223)          | Equivalent                                                                   |
| AC-4: Playwright spec asserts (a) banner renders, (b) silent absence, (c) tenant name appears | Slice 213's `audits-header.spec.ts` AC-5 + slice 223's `controls-top-bar.spec.ts` AC-7 | Partial — covers `/controls` + `/audits` + `/dashboard`; no `/evidence` spec |
| AC-5: F-204-E-3 resolved on next audit run                                                    | All three findings have shipping artifacts                                             | Will resolve                                                                 |

The Path C global ⌘K search the spec said to defer (P0-235-1) was
**also shipped** by slice 223 — the spec's deferral was a narrative
preference, not a hard exclusion, and slice 223's PR body explicitly
folded spillover slice 272 into its scope. So the deferral is moot:
the search is in production already.

**The genuine remaining gap** is AC-4's `/evidence`-specific assertion.
Slice 213's e2e covers `/dashboard` for shared-chrome verification
(AC-2). Slice 223's covers `/controls` and `/audits` for breadcrumb +
search verification. None of the three currently exercise `/evidence`.
Adding the dedicated spec closes the F-204-E-3 finding loop, prevents
silent regression of three load-bearing chrome elements on the
`/evidence` route, and is the minimum-honest delta the slice 235
scope requires.

**What this resolves:** the slice spec said the AC was "2.0d" worth
of work. The actual work is the one regression-protection spec that
the parent components' own slices did not file for `/evidence`. The
rest of the AC surface is already in production.

---

## D2 — Reuse the `audits-header` fixture (no new SQL)

**Decision:** the new Playwright spec uses
`seedFromFixture("audits-header")` rather than introducing a new
fixture file.

**Rationale:**

The `audits-header.sql` fixture (slice 213) seeds two rows the slice
235 chrome assertions need:

1. An `audit_periods` row with `status='open'` and name
   `SOC 2 Type II · Q2 2026` — drives the in-progress pill copy.
2. A `users` row with `display_name='Sam Operator'` for the avatar
   (incidental to slice 235 but harmless).

The breadcrumb's left segment (`Demo Tenant`) needs a `tenants` row
for `DEMO_TENANT_ID`. The `controls-top-bar` fixture (slice 223)
seeds this row but the `audits-header` fixture does NOT. Two paths:

- (a) Add the same tenants INSERT to `audits-header.sql` — modifies
  a slice 213 fixture and risks coupling unrelated specs.
- (b) Apply BOTH fixtures from the new spec's `beforeAll` (the seed
  harness is idempotent — every INSERT is ON CONFLICT DO NOTHING).
- (c) Create a new `evidence-header.sql` fixture that consolidates.

Chose **(b)** — apply both fixtures sequentially. The seed harness's
contract is idempotent by design (slice 082 D2), and reusing both
existing fixtures has zero coupling cost. This is the same pattern
slice 213's spec uses (`settings.sql` is incidentally a prerequisite
for the users row via the same UUID overlap).

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
`controls-top-bar.spec.ts` P0-223-1 assertion — is to use Playwright's
auto-waiting `waitForRequest` over `page.locator(...).waitFor()` /
`expect(...).toBeVisible()` on the debounced popover. The pattern
makes the test wait for the actual network request rather than
guessing about timing.

**What this resolves:** flake risk on the search debounce assertion
in CI. Mirrors slice 274's canonical fix + slice 223's P0-223-1
discipline.

---

## D4 — No changes to `_STATUS.md` (spec hard rule)

**Decision:** this slice does NOT modify `docs/issues/_STATUS.md`.
The loop orchestrator owns canonical row flips.

**Rationale:** spec hard rule. The decisions log + PR body document
the "subset shipped" outcome explicitly so the orchestrator has the
information to flip the slice 235 row (and, if applicable, mark its
F-204-E-3 closure on slice 204's row).

---

## CI-delta scan

No CI-delta concerns:

- **New files only** — no modifications to load-bearing existing
  files except `CHANGELOG.md`:
  - `web/e2e/evidence-header.spec.ts` — new Playwright spec
  - `docs/audit-log/235-evidence-header-chrome-decisions.md` — this
    file
- **`CHANGELOG.md`** modified to add a bullet under `## Unreleased`
  → `### Added` documenting the new regression-protection spec.
- **No source file changes.** The three load-bearing components
  (`in-progress-audit-pill.tsx`, `breadcrumb.tsx`, `global-search.tsx`)
  and the topbar that mounts them are untouched. The mount path
  through `(authed)/layout.tsx` is untouched. The BFF routes
  (`/api/audits`, `/api/me/tenants`, `/api/search`) are untouched.
- **No new platform endpoint** — anti-criterion P0-235-2 trivially
  honored (no wire contract changes).

Local CI parity verified before push:

- `pre-commit run --all-files` — green
- `npm run lint` (web) — green (pre-existing baseline)
- `npm run test` (web) — green (no new vitest files; no source
  changes)
- `npx tsc --noEmit` (web) — pre-existing baseline (no new errors
  from this slice)
- `npm run build` (web) — green (no source changes)
- CHANGELOG bullet added under `## [Unreleased]` → `### Added`

The Playwright e2e spec (`evidence-header.spec.ts`) requires the
slice-082 seed harness + a running platform; it is exercised in CI by
the `Frontend · Playwright e2e` job after the docker-compose bring-up.
The slice 274 AC-9 flake fix (merged 2026-05-23) keeps the spec
reliable in CI.
