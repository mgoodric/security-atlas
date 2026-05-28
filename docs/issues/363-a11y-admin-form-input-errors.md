# 363 — A11y admin forms: raw input + error association

**Cluster:** Frontend / a11y
**Estimate:** 1d
**Type:** AFK
**Status:** `ready`

## Narrative

Slice 331's a11y audit
(`docs/audits/331-a11y-wcag-audit.md` finding A11Y-5, severity
High) surfaced two related gaps in admin form pages:

### Gap 1 — Raw `<input type="checkbox">` in tenants form

`web/app/admin/tenants/page.tsx:228-243`:

```tsx
<input
  id="tenant-join-as-admin"
  type="checkbox"
  className="h-4 w-4 rounded border-input"
  checked={joinAsAdmin}
  onChange={(e) => setJoinAsAdmin(e.target.checked)}
/>
<label htmlFor="tenant-join-as-admin">Join as admin (...)</label>
```

This is the only raw `<input>` in admin form pages. It has a label
association (good) but:

- No `focus-visible:ring-3 focus-visible:ring-ring/50` like every
  other input in the codebase (shadcn `<Input>` primitive carries
  this).
- Tailwind v4 preflight resets browser focus rings; the raw input
  has neither the browser default ring nor the project's
  consistent ring.

Net: a low-vision user tabbing into the checkbox has no visual
indication of focus. WCAG SC 2.4.7 Focus Visible (AA) failure.

### Gap 2 — Form errors not announced

Admin forms (`tenants/page.tsx`, `super-admins/page.tsx`,
`api-keys/page.tsx`) follow this pattern:

```tsx
<form onSubmit={...}>
  ...inputs...
  <Button type="submit">...</Button>
  {error ? (
    <Alert variant="destructive">
      <AlertTitle>Create failed</AlertTitle>
      <AlertDescription>{error}</AlertDescription>
    </Alert>
  ) : null}
</form>
```

The `<Alert>` carries `role="alert"` (good — SR will announce its
appearance). But:

- No `aria-describedby` on the inputs links to the alert's id
  when the alert renders. An SR user who tabs back to fix the
  input after an error doesn't hear which input the error refers
  to.
- The Alert renders at the BOTTOM of the form. An SR linear-read
  hears the inputs first, then the submit button, then the
  error — the wrong order for "fix-the-error" navigation.

WCAG SC 3.3.1 Error Identification (A) + SC 3.3.2 Labels or
Instructions (A): "If an input error is automatically detected,
the item that is in error is identified" — currently NOT
identified at the input level.

### What ships

1. **Checkbox primitive.** Add a shadcn-themed
   `<Checkbox>` primitive at `web/components/ui/checkbox.tsx`
   that wraps `@base-ui/react/checkbox` (the existing dep family;
   slice 277 P0-style "no new deps"). Carries the same
   `focus-visible:ring-3 focus-visible:ring-ring/50` as Input +
   Button. Replace the raw input in `admin/tenants/page.tsx` with
   it.

2. **Form error association pattern.** Establish a project
   convention for form-error a11y:

   - The `<Alert>` carries a stable `id` (e.g.
     `id="create-tenant-error"`).
   - Each input carries `aria-describedby="create-tenant-error"`
     conditionally when the error is mounted.
   - The Alert carries `aria-live="polite"` to ensure
     announcements ride the live-region machinery even if focus
     doesn't move.

   Apply to:

   - `web/app/admin/tenants/page.tsx`
   - `web/app/admin/super-admins/page.tsx`
   - `web/app/admin/api-keys/page.tsx`
   - `web/app/login/page.tsx` (also uses the
     `<Alert variant="destructive">` post-submit pattern)

3. **Document the pattern.** Add a short paragraph to
   `web/testing.md` (or a new
   `web/components/ui/checkbox.tsx` header comment) describing
   the convention so future admin pages adopt it.

4. **Tests.** Vitest / Playwright coverage for the new Checkbox
   primitive + at least one e2e assertion that the form-error
   `aria-describedby` is wired correctly (e.g. spec that submits
   an invalid form and asserts the input's `aria-describedby`
   resolves to the alert's id).

### Why this matters

Admin form pages are the operator's primary write surface for
tenant management, super-admin grants, and API key issuance —
high-touch, high-stakes flows where an SR user making a mistake
and not hearing which input is the error means re-tabbing to
every input to guess.

## Threat model

UI-chrome-only change. STRIDE pass:

- **S / T / R / D / E:** No surface changes.
- **I:** None.

## Acceptance criteria

- [ ] **AC-1.** New shadcn-themed `<Checkbox>` primitive at
      `web/components/ui/checkbox.tsx`, wrapping
      `@base-ui/react/checkbox`.
- [ ] **AC-2.** `admin/tenants/page.tsx` raw checkbox replaced
      with the new primitive; same `htmlFor` / id pairing.
- [ ] **AC-3.** All four admin forms (tenants · super-admins ·
      api-keys · login) wire `aria-describedby` on inputs to the
      error Alert's id when the alert is mounted.
- [ ] **AC-4.** The error Alert carries `aria-live="polite"`
      (Alert's `role="alert"` provides assertive; this is the
      additional live-region announcement).
- [ ] **AC-5.** Vitest test covers the new Checkbox component.
- [ ] **AC-6.** Playwright spec asserts `aria-describedby` is
      wired on at least one admin form submit-with-error flow.
- [ ] **AC-7.** Project convention documented (short paragraph
      in `web/testing.md` or `checkbox.tsx` header).
- [ ] **AC-8.** `pre-commit run --all-files` passes.

## Anti-criteria (P0 — block merge)

- **P0-363-1.** Does NOT introduce a new top-level dep —
  `@base-ui/react` already ships the Checkbox primitive.
- **P0-363-2.** Does NOT widen to non-admin forms in this slice
  unless they were trivially included (the login form is in
  because it shares the post-submit `<Alert>` pattern).
- **P0-363-3.** Does NOT modify form submission behavior — ARIA + primitive only.

## Dependencies

- **#331** (a11y audit) — `merged` (closing this slice).
- **#142** (super-admins management) — `merged`. Form origin.
- **#198** (OIDC first-install) — `merged`. Login form origin.
- **#143** (create-tenant flow) — `merged`. Tenants form origin.

## Notes

The `aria-describedby` pattern intersects with form-validation
libraries. The project does NOT use react-hook-form or zod yet —
this slice introduces the pattern in vanilla `useState` /
`useMutation` shape. A future slice may consolidate onto a
validation lib; this work survives that migration.
