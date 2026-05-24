# Decisions log — slice 243 (risks top bar regression protection)

**Slice:** [`docs/issues/243-ui-honesty-risks-top-bar-chrome-parity.md`](../issues/243-ui-honesty-risks-top-bar-chrome-parity.md)
**Type:** JUDGMENT
**Build agent:** Engineer
**Closed at:** 2026-05-24

---

## Summary

Slice 243 was filed against the `/risks` page during slice 204's UI
honesty audit fleet to close four shell-level chrome parity gaps:

1. **Tenant breadcrumb chip.** Read-only `<tenant> > Risks` wayfinding.
2. **Audit-in-progress banner pill.** Amber pill reading
   `<framework> <audit_type> · <period_label> in progress`.
3. **User avatar circle + display name.** Initials from the JWT `name`
   claim.
4. **Global ⌘K search input.** Inline search box with placeholder
   `Search controls, evidence, risks…` + `⌘K` kbd hint (deferred per
   the original spec to slice #223 — but shipped in #223's actual
   scope).

Between slice 243's filing and this build window, two adjacent slices
have shipped the entire chrome surface in the shared authed-shell:

| Element            | Shipping slice | Component                                         | Visible on `/risks` today? |
| ------------------ | -------------- | ------------------------------------------------- | -------------------------- |
| Audit-period pill  | **Slice 213**  | `web/components/shell/in-progress-audit-pill.tsx` | YES (shared topbar)        |
| Tenant breadcrumb  | **Slice 223**  | `web/components/shell/breadcrumb.tsx`             | YES (shared topbar)        |
| Global ⌘K search   | **Slice 223**  | `web/components/shell/global-search.tsx`          | YES (shared topbar)        |
| User avatar + name | **Slice 213**  | (avatar surfaces in topbar / tenant-switcher row) | Surfaced in shared topbar  |

All three load-bearing components are mounted in
`web/components/shell/topbar.tsx`, which is rendered by
`web/app/(authed)/layout.tsx` for every authed route — including
`/risks`. The `page-names.ts` map already carries the
`risks → "Risks"` entry (slice 223, line 33). The slice 213 e2e spec
already proves the in-progress pill is shared chrome by visiting
`/dashboard` (AC-2 — "pill renders on a non-audits page too"). The
slice 223 e2e spec proves the breadcrumb + search render on
`/controls` and `/audits`. Slice 235's `evidence-header.spec.ts`
closed the same gap for `/evidence`.

So the **actual** missing piece on `/risks` post-213/223 is **not a
new component, a new BFF, or a new endpoint** — it is a dedicated
Playwright assertion that the shared chrome renders correctly on
`/risks` specifically. Per slice 204's anti-pattern catalogue, the
moment we have three components passing on `/controls` + `/audits` +
`/dashboard` + `/evidence` but no `/risks` spec, regression risk
concentrates on the one page nobody asserts.

This log captures the JUDGMENT call the engineer made during the
build.

---

## D1 — Subset shipped: zero new components; one new Playwright spec on `/risks`

**Decision:** ship a **single new Playwright spec**
(`web/e2e/risks-top-bar.spec.ts`) that asserts the slice-213 pill +
slice-223 breadcrumb + slice-223 search input all render correctly on
`/risks`. Do **not** add new components, new BFF routes, or new
endpoints. Mark slice 243 as **superseded-in-substance** by the
combination of slices 213 + 223 in the PR body; let the orchestrator
decide whether to flip `_STATUS.md` to `merged` or `superseded` (spec
hard rule — slice does not modify `_STATUS.md`).

**Rationale (why subset vs full scope):**

The slice spec was authored before slices 213 and 223 landed. Every
acceptance criterion the spec names has a satisfying shipping artifact
on `main`:

| Slice 243 AC                                                                                | Shipping artifact                                                                    | Verdict                                                                      |
| ------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------ | ---------------------------------------------------------------------------- |
| AC-1: Tenant breadcrumb chip on every authed page                                           | `<Breadcrumb />` renders `<tenant> > <page>` after the brand mark (slice 223)        | Equivalent                                                                   |
| AC-2: Audit-in-progress banner pill renders only when an `AuditPeriod` row is `in_progress` | `InProgressAuditPill` calls `/api/audits` filtered to `status='open'` (slice 213)    | Equivalent                                                                   |
| AC-3: User avatar circle + display name to right of audit pill                              | Topbar mounts the user-display row alongside the TenantSwitcher (slice 213)          | Equivalent (presentational; spec scope is the three load-bearing assertions) |
| AC-4: Slice does NOT ship global-search affordance                                          | Global ⌘K search shipped by slice 223 anyway                                         | Moot — search is in production already; deferral was narrative not normative |
| AC-5: Existing `risks-list.spec.ts` updated to assert the breadcrumb chip                   | New dedicated `risks-top-bar.spec.ts` asserts breadcrumb + pill + search on `/risks` | Equivalent (separated spec keeps chrome-regression traceable)                |
| AC-6: CHANGELOG entry                                                                       | Added under `## Unreleased` → `### Added`                                            | Done                                                                         |

**The genuine remaining gap** is the AC-5 `/risks`-specific
assertion. Slice 213's e2e covers `/dashboard` for shared-chrome
verification. Slice 223's covers `/controls` and `/audits`. Slice
235's covers `/evidence`. None of the four currently exercise
`/risks`. Adding the dedicated spec closes the slice-204 `/risks`
finding loop, prevents silent regression of three load-bearing chrome
elements on the `/risks` route, and is the minimum-honest delta the
slice 243 scope requires.

**Pattern provenance:** this slice is an exact structural mirror of
slice 235 (`/evidence` regression protection). The slice 235 D1
rationale applies verbatim to slice 243 — same shared chrome, same
shipping artifacts, same gap shape, same remediation. The two were
filed in lock-step against `/evidence` and `/risks` during slice
204's audit fleet; they ship the same way.

**What this resolves:** the slice spec said the work was "0.5d" of
presentational chrome work. The actual work is the one regression-
protection spec that the parent components' own slices did not file
for `/risks`. The rest of the AC surface is already in production.

---

## D2 — Reuse the `audits-header` + `controls-top-bar` fixtures (no new SQL)

**Decision:** the new Playwright spec uses BOTH
`seedFromFixture("audits-header")` AND `seedFromFixture("controls-top-bar")`
sequenced in `beforeAll`, rather than introducing a new fixture file.

**Rationale:**

Same trade-off slice 235 D2 resolved. The `audits-header.sql` fixture
(slice 213) seeds the `audit_periods` row that drives the in-progress
pill copy; the `controls-top-bar.sql` fixture (slice 223) seeds the
`tenants` row that drives the breadcrumb's left segment. Both
fixtures' INSERTs are idempotent (`ON CONFLICT DO NOTHING`) by the
seed harness's contract (slice 082 D2), so sequencing them adds no
coupling cost.

The alternative — creating a third bespoke `risks-top-bar.sql`
fixture that consolidates both — would expand the fixture surface
area for no architectural benefit. The hard rule from the slice spec
("no new fixture file unless required") aligns directly.

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
`controls-top-bar.spec.ts` P0-223-1 assertion and slice 235's
`evidence-header.spec.ts` — is to use Playwright's auto-waiting
`waitForRequest` over `page.locator(...).waitFor()` /
`expect(...).toBeVisible()` on the debounced popover. The pattern
makes the test wait for the actual network request rather than
guessing about timing.

**What this resolves:** flake risk on the search debounce assertion
in CI. Mirrors slice 274's canonical fix + slice 223's + slice 235's
P0-223-1 / D3 discipline.

---

## D4 — No changes to `_STATUS.md` (spec hard rule)

**Decision:** this slice does NOT modify `docs/issues/_STATUS.md`.
The loop orchestrator owns canonical row flips.

**Rationale:** spec hard rule. The decisions log + PR body document
the "subset shipped" outcome explicitly so the orchestrator has the
information to flip the slice 243 row (and, if applicable, mark the
`/risks` chrome closure on slice 204's row).

---

## CI-delta scan

No CI-delta concerns:

- **New files only** — no modifications to load-bearing existing
  files except `CHANGELOG.md`:
  - `web/e2e/risks-top-bar.spec.ts` — new Playwright spec
  - `docs/audit-log/243-risks-top-bar-regression-decisions.md` — this
    file
- **`CHANGELOG.md`** modified to add a bullet under `## Unreleased`
  → `### Added` documenting the new regression-protection spec.
- **No source file changes.** The three load-bearing components
  (`in-progress-audit-pill.tsx`, `breadcrumb.tsx`, `global-search.tsx`)
  and the topbar that mounts them are untouched. The mount path
  through `(authed)/layout.tsx` is untouched. The BFF routes
  (`/api/audits`, `/api/me/tenants`, `/api/search`) are untouched.
- **No new platform endpoint** — anti-criterion P0-243-1 / P0-243-2 /
  P0-243-3 / P0-243-4 trivially honored (no wire contract changes, no
  hardcoded banner text, no TenantSwitcher removal, no fourth top-bar
  entry).

Local CI parity verified before push:

- `pre-commit run --all-files` — green
- `npm run lint` (web) — green (pre-existing baseline)
- `npm run test` (web) — green (no new vitest files; no source
  changes)
- `npx tsc --noEmit` (web) — pre-existing baseline (no new errors
  from this slice)
- `npm run build` (web) — green (no source changes)
- CHANGELOG bullet added under `## Unreleased` → `### Added`

The Playwright e2e spec (`risks-top-bar.spec.ts`) requires the
slice-082 seed harness + a running platform; it is exercised in CI by
the `Frontend · Playwright e2e` job after the docker-compose bring-up.
The slice 274 AC-9 flake fix (merged 2026-05-23) keeps the spec
reliable in CI.
