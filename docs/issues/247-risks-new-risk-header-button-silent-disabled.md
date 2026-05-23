# 247 — Risks list: header "New risk" button is silently disabled, /risks/new is a real route

**Cluster:** Quality / UI hygiene (frontend)
**Estimate:** 0.25d
**Type:** AFK
**Status:** `ready`

## Narrative

Surfaced during slice 204 audit fleet (`/risks` page), captured as a
follow-up per continuous-batch policy. The page header on `/risks`
renders a primary-styled, full-visual-weight button with `disabled`
at the DOM level and no tooltip / banner / explanation:

```tsx
// web/app/(authed)/risks/page.tsx:347
<Button size="sm" disabled>
  New risk
</Button>
```

This is the same slice-178 honesty-heuristic pattern previously
flagged on `/controls` (#225). What makes it sharper on this page:
**the route the button should navigate to exists.** Slice 105
shipped `/risks/new`; the page itself already routes the
empty-state CTA there:

```tsx
// web/app/(authed)/risks/page.tsx:420-428
cta={
  isFilterEmpty
    ? { label: "Clear filters", onClick: clearAll }
    : {
        label: "Add first risk",
        onClick: () => router.push("/risks/new"),
      }
}
```

The page knows `/risks/new` is the right destination — but the
header button is hardcoded disabled. There is no good reason for
the asymmetry. The follow-on gap that the form has open (slice 151:
control-multi-select for `treatment=mitigate`) is separately
tracked; it does not justify disabling the entry-point button. A
user with no risks to create against `mitigate` (e.g. creating an
`accept`-treatment risk) can complete the flow today.

The minimal honest path: replace the disabled button with a `<Link>`
wrapping the same shadcn Button, routing to `/risks/new`. The
mockup's enabled primary CTA shape is preserved.

## Threat model

**Verdict.** **no-mitigations-needed.** The destination route
already exists and enforces tenant context via the existing JWT +
RLS path. Removing a disabled affordance does not add an authz
surface; it only enables a path the same page already takes from
its empty-state.

## Acceptance criteria

- **AC-1.** The page-header `New risk` button is replaced with a
  `<Link href="/risks/new">` wrapping a shadcn `Button size="sm"` —
  matching the mockup's enabled primary CTA shape (mockup lines
  118-121).
- **AC-2.** The button retains the `data-testid="risks-new-link"`
  (new) testid for Playwright addressability. The previously
  rendered `<Button disabled>` is removed.
- **AC-3.** The empty-state CTA at `page.tsx` lines 420-428
  continues to route to `/risks/new`. No regression in the
  empty-state code path; the change is to the header `actions`
  block only.
- **AC-4.** Existing Playwright spec `risks-list.spec.ts` is
  extended with one assertion: clicking the header `New risk` link
  navigates to `/risks/new`. (The form's contents are out of scope;
  this slice owns the entry-point honesty only.)
- **AC-5.** vitest unit coverage for the page header verifies the
  link element renders with the expected `href` (no disabled
  attribute).
- **AC-6.** CHANGELOG entry: "Risks list: header `New risk` button
  routes to `/risks/new` instead of rendering disabled (#247;
  slice 100 + slice 105 + slice 185 follow-on)".

## Constitutional invariants honored

- **Affordance honesty.** Slice 178's heuristic: affordances must
  ADVERTISE their actual behavior. A disabled primary button next
  to a working empty-state CTA is the same internal contradiction
  slice 185 closed for the row-click affordance. This slice closes
  the analogous header-button gap.
- **No new product behavior.** The destination route, the form, and
  the slice-151 follow-on tracking are unchanged. This slice is
  pure entry-point wire-up.

## Canvas references

- `Plans/canvas/06-risk.md` — risk register linkage

## Dependencies

- **#100** Risks list view — `merged`. The page this slice
  modifies.
- **#105** Risk-create UI — `merged`. Provides `/risks/new`.
- **#151** Risk-create form control-multi-select — `ready`. The
  follow-on gap on the FORM does not block this slice; users can
  create `transfer`/`accept`/`avoid`-treatment risks today even
  without the multi-select.
- **#185** Risks row-click honesty — `merged`. Established the
  honesty pattern this slice extends.

## Anti-criteria (P0 — block merge)

- **P0-247-1.** Does NOT ship any change to `/risks/new` or its
  form. Slice 151 owns that follow-on.
- **P0-247-2.** Does NOT add a confirmation modal, a tooltip, or a
  "coming soon" hint to the header button. The fix is the link;
  banners would be MORE honesty-gap, not less.
- **P0-247-3.** Does NOT touch the empty-state CTA. It already
  routes correctly.
- **P0-247-4.** Does NOT alter the slice-185 row-click absence or
  the per-row `View in hierarchy →` link. Scope is the header
  button only.

## Skill mix (3-5)

1. Next.js App Router — header-action link refactor
2. Playwright spec update
3. vitest unit test
