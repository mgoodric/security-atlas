# Slice 184 — UI honesty: `/audits` row-click 404 (build-time decisions)

> JUDGMENT-type slice. Engineer made the Option A vs. Option B call
> during build per the `JUDGMENT` slice-development pattern (CLAUDE.md
> "AI-assist boundary": this is about how we build, NOT how the shipped
> product behaves). The product still never publishes audit-binding
> artifacts without one-click human approval — this slice is chrome.

---

## D1 — Option A (disable click) chosen over Option B (ship detail page)

**Decision:** Option A — remove the `onRowClick` prop on the audits
`ListTable` and surface an explicit honesty banner above the table.
Defer Option B (the per-period detail page) to a separate slice once
the detail-page UX is decided.

**Why:**

- **Scope match.** The slice doc estimates Option A at 0.5d and Option B
  at 2.0d. The 0.25d budget allocated to this slice matches Option A
  (in fact undershoots it slightly because the fix is small once the
  approach is locked).
- **Smaller blast radius.** Option A touches one page + one e2e spec +
  one CHANGELOG line + this decisions log. Option B would ship a new
  Next.js route at `web/app/(authed)/audits/[id]/page.tsx`, a BFF at
  `web/app/api/audits/[id]/route.ts`, vitest + Playwright coverage of
  the new wire shape, design decisions about which subset of
  audit-period meta to render (frozen status, sample populations, OSCAL
  export controls, walkthrough chrome), and a full design conversation
  about how the detail page interacts with the existing slice-042
  per-control walkthrough at `/audit/[controlId]`. That is a slice in
  its own right, not a row in a backlog cleanup batch.
- **Honesty discipline.** Slice 178's first-pass surfaced this as a
  HONESTY-GAP. Option A closes the finding by replacing a clickable
  row that 404s with a non-clickable row plus an explicit "coming
  soon" banner. That is more honest to the operator than a thin
  placeholder page would be at this stage — a placeholder page with
  no meaningful content would be a fresh HONESTY-GAP of its own.
- **Reversibility.** Option A is reversible in one line: when the
  detail slice ships, restore `onRowClick={(p) => router.push(...)}`
  on the `ListTable` and delete the banner. The page-level comment
  block documents the exact line to restore.

**Alternative rejected (Option B):** Ship a minimal detail page that
reads `GET /v1/audit-periods/:id` and renders the period's metadata +
frozen status + sample counts. Rejected at this slice's scope because
(a) the wire shape returned by `GET /v1/audit-periods/:id` is a
read-detail endpoint that already exists per slice 048, but (b) the
UX decisions about what to render and how it relates to the existing
audit workspace are non-trivial and warrant their own design pass.
Option B is filable as a follow-on when the maintainer has bandwidth
for the design conversation.

**Spillover filed:** Not in this PR. The slice 184 doc itself already
acknowledges Option B as a separate slice if it becomes the path; the
maintainer can file it via the standard idea-to-slice flow when ready.

---

## D2 — Banner shape: shadcn `Alert` (default variant) above the table

**Decision:** Use the existing `Alert` / `AlertTitle` / `AlertDescription`
primitives from `@/components/ui/alert`, default (non-destructive)
variant, placed inside the `ListPage` children slot above the
`ListTable`. Test-id token: `audits-detail-coming-soon-banner`.

**Why:**

- The `Alert` primitive is already imported on this page (used by the
  query-error path at lines 410-414). Reusing it requires no new
  imports and matches the existing visual rhythm.
- Default variant (not `destructive`) is correct because this is
  informational, not an error condition. The page IS working as
  intended; the disclosure is about a future capability, not a current
  failure.
- Placement inside `ListPage` children (above `ListTable`) keeps the
  banner inside `data-testid="list-page-content"` rather than between
  the filter row and the content. That matches the visual hierarchy
  the operator expects (header → filters → content).

**Alternative rejected — banner inside `ListPage`'s filterRow slot.**
The filter row is reserved for the FilterPills primitive (slice 098).
Mixing informational disclosure into the filter row would muddle the
shell semantics.

**Alternative rejected — toast / inline-on-hover tooltip.**
A toast disappears; a tooltip requires hover-discovery. The audit
finding F-178-4 is that the LIVE UI does not surface the limitation —
the fix needs to be persistent and visible at first render.

---

## D3 — Issue number in banner copy: omitted (no hardcoded `#NNN`)

**Decision:** The banner copy does NOT name a specific tracking-issue
number. It says "is tracked separately" rather than "see issue #NNN to
track."

**Why:** The slice 184 spec's AC-2 suggests "see issue #<NNN> to
track." but the follow-on detail-page slice does not exist yet in the
backlog — there is no real number to substitute. Picking a number
unilaterally would either (a) burn a number we don't actually own
yet, or (b) put a placeholder `#NNN` in user-facing copy, which is
itself a HONESTY-GAP. The honest formulation is "tracked separately"
until the slice is actually filed.

**Alternative rejected — file the follow-on slice first and link to
it.** That doubles the slice scope (two PRs: one to file the
follow-on, one to land this fix), violates the one-fix-per-slice
discipline that produced this slice, and assumes maintainer intent
about whether Option B even ships.

**When the detail-page slice is filed:** the banner copy can be
updated in that slice's PR to name the tracking issue.

---

## D4 — Playwright spec AC-7 reversed (not deleted)

**Decision:** The original AC-7 spec body (commented-out assertion
that a row click navigates to `/audits/[id]`) is REPLACED, not deleted,
with the inverse assertion: (a) the row does not carry
`cursor-pointer`, (b) clicking does not navigate away from `/audits`,
(c) the honesty banner is visible.

**Why:**

- Deleting the spec block entirely would leave the contract under-tested.
  The current behavior IS testable — the absence of navigation is a
  positive property worth pinning.
- The whole `audits-list.spec.ts` file is quarantined behind slice 082
  (the seed-data harness) per slice 079's decision, so the assertions
  are commented out anyway. The reversal preserves the file's role as
  a reviewable contract — when the harness lands, the assertions turn
  on and gate this behavior going forward.
- The spec header comment ("AC-7 (slice 184)") names the slice that
  changed the contract, so a future reader sees the provenance
  without having to spelunk the git log.

**Alternative rejected — delete the AC-7 block entirely.**
Discards the contract. Future regressions (e.g., somebody restores
`onRowClick` without realizing why it was removed) would not trip a
spec failure.

---

## D5 — `useRouter` import preserved (still in use)

**Decision:** Do NOT remove the `useRouter` import or the `router`
local variable, even though the `onRowClick` prop no longer needs them.

**Why:** `router` is still used by the toolbar "New audit period" CTA
(slice 149 — `onClick={() => router.push("/audits/new")}`), the
empty-state CTA (`onClick: () => router.push("/audits/new")`), and
the filter URL updates (`router.replace(...)`). Removing the import
would break the page. The slice doc's P0-184-2 ("does NOT modify the
audits list-page data contract or backend") and P0-184-3 ("does NOT
touch the slice-178 audit harness") implicitly require leaving these
unrelated routes alone.

---

## D6 — No second spillover slice filed

**Decision:** This slice does not file a follow-on for "ship the
per-period detail page" (Option B from the slice doc).

**Why:** Per Amendment 2 (spillover policy), out-of-scope findings
during build are filed as spillover slices. The detail page is not a
NEW finding discovered during this build — it is the alternate path
the slice doc explicitly defers ("Option B (if chosen) ships exactly
that one page + nothing else"). Filing it as a spillover would be
substituting engineer judgment for maintainer prioritization. The
maintainer can file it via the standard idea-to-slice flow when
ready.

---

## Verification

- **AC-1.** `web/app/(authed)/audits/page.tsx` no longer passes
  `onRowClick` to `ListTable`. `web/components/list/list-table.tsx`
  lines 93-94 confirm: when `onRowClick` is undefined, the table drops
  both the click handler AND the `cursor-pointer` class. Rows are
  unambiguously non-clickable in both behavior and styling. ✅
- **AC-2.** Honesty banner rendered above the table via
  `<Alert data-testid="audits-detail-coming-soon-banner">`. Per D3,
  the copy says "tracked separately" rather than naming a placeholder
  issue number. ✅
- **AC-3.** `web/e2e/audits-list.spec.ts` AC-7 spec body replaced with
  the inverse assertion per D4. Quarantined behind slice 082's seed
  harness as before. ✅
- **AC-4.** Closing F-178-4 from `docs/audit-log/178-ui-honesty-first-pass.md`
  is the merge gate — the sticky comment on this PR is the audit
  evidence. ✅ (verified on PR build)
- **Local CI parity** (per `feedback_local_ci_parity.md`):
  - `npx tsc --noEmit` — zero errors in files I touched
    (`audits/page.tsx`, `audits-list.spec.ts`); the pre-existing
    `scripts/capture-readme-screenshots.test.ts` ProcessEnv errors are
    on `main` already and not caused by this slice.
  - `npx eslint app/(authed)/audits/page.tsx` — clean.
  - `npx prettier --write` — applied.
