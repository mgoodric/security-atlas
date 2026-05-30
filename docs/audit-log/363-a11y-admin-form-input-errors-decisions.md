# Slice 363 — A11y admin forms: raw input + error association — decisions log

**Parent slice:** 331 (`docs/audit-log/331-a11y-wcag-audit-decisions.md`) finding **A11Y-5** (severity High)
**Branch:** `frontend/363-a11y-admin-form-input-errors`
**Type:** JUDGMENT (per `Plans/prompts/04-per-slice-template.md` slice types)
**Date:** 2026-05-28

This slice closes the High-severity finding A11Y-5 from slice 331's
WCAG audit. Two related gaps in admin form pages:

- **Gap 1 (WCAG 2.4.7 Focus Visible).** Raw `<input type="checkbox">`
  at `web/app/admin/tenants/page.tsx:228-243` had no focus ring.
  Tailwind 4 preflight strips the browser-default focus ring; the raw
  input was not carrying the project's standard
  `focus-visible:ring-3 focus-visible:ring-ring/50` shape used by
  every other form primitive.
- **Gap 2 (WCAG 3.3.1 + 3.3.2).** Admin forms (`tenants/page.tsx`,
  `super-admins/page.tsx`, `api-keys/page.tsx`, `login/page.tsx`)
  render submit-error `<Alert>` with `role="alert"` but inputs had
  no `aria-describedby` linking to the alert. SR users tabbing back
  to fix an error heard no association between input and error.

This log captures the build-time judgment calls.

---

## D1 — Checkbox primitive: wrap `@base-ui/react/checkbox` vs build from scratch

**Decision:** Wrap `@base-ui/react/checkbox` (already at `^1.4.1` in
`web/package.json` — verified via `node_modules/@base-ui/react/
checkbox/` listing).

**Rationale:**

- The slice doc + P0-363-1 explicitly require "no new top-level dep —
  `@base-ui/react` already ships the Checkbox primitive". Honored.
- Slice 277 established the "wrap Base UI's headless primitive +
  shadcn-theme via className" pattern for other form primitives
  (Input at `web/components/ui/input.tsx`, Button at `web/components/
ui/button.tsx`). Sticking to this pattern keeps the codebase's
  primitive library architecturally uniform.
- Base UI's `Checkbox.Root` renders a `<span>` plus a hidden `<input>`
  beside it, so screen readers see a real native checkbox — important
  for SR compatibility under WCAG 4.1.2 Name, Role, Value (which is
  out of scope for this slice but would have been compromised by a
  pure-`<span>` div-based reinvention).

**API surface chosen:**

```tsx
<Checkbox
  id="..."
  checked={boolean}
  onCheckedChange={(checked) => void}
  className="..."
  disabled={boolean}
  aria-describedby={errorId}
  data-testid="..."
/>
```

`onCheckedChange` matches Base UI's contract (not `onChange`); the
admin pages that swap raw `<input>` adapt their setter from
`(e) => setX(e.target.checked)` to `(checked) => setX(checked === true)`
(the `=== true` is defensive — Base UI can emit `'indeterminate'` if
the indeterminate prop is set, though we never set it).

---

## D2 — className helper extracted to a separate `.ts` module

**Decision:** Move className composition into a pure
`web/components/ui/checkbox-class.ts` module; the `.tsx` component
imports `checkboxClassName(className?)` and applies it to
`Checkbox.Root`.

**Rationale (the constraint):**

- The project's vitest runner is **node-env only** per slice 069
  P0-A3 (no jsdom, no `@testing-library/react`). See `web/testing.md`.
  Component-render tests are Playwright; pure-logic tests are vitest.
- AC-5 says "vitest test covers the new Checkbox component". The
  honest fulfillment under the node-env constraint is to factor a
  pure helper out of the `.tsx` component, then vitest the helper.
- A round-trip "lift jsdom + @testing-library/react into the vitest
  setup so we can render `<Checkbox>`" was rejected — that's a
  multi-slice architectural change (slice 069 explicitly deferred it
  via P0-A3) that exceeds the 1d AFK budget and widens beyond the
  slice's a11y goal.

**Helper shape:**

```ts
checkboxClassName(className?: string): string
checkboxIndicatorClassName(className?: string): string
```

Both are pure string composition over `cn()`. Vitest tests assert
that key Tailwind tokens (focus ring shape, base box shape, checked
state, disabled state, aria-invalid state) appear in the output and
that caller-supplied classes merge through. 11 tests, all green.

**Component-render coverage rides Playwright** per `web/testing.md`'s
decision matrix; the AC-6 admin-tenants spec asserts the primitive
forwards `aria-describedby` correctly (the meaningful integration
property for this slice).

---

## D3 — Form-error association idiom: `errorId = error ? "..." : undefined`

**Decision:** Each admin form computes a local `errorId` constant
once at the top of the component:

```tsx
const createErrorId = createError ? "create-tenant-error" : undefined;
```

Every input then passes `aria-describedby={createErrorId}`; the
Alert mounts with `id="create-tenant-error" aria-live="polite"`.

**Rationale:**

- `aria-describedby={undefined}` strips the attribute cleanly (React
  spec: undefined attributes are omitted from the DOM). When the
  alert is not mounted, no input carries a dangling
  `aria-describedby="some-id-that-doesnt-exist"` — which would be a
  WCAG 1.3.1 violation in its own right.
- The naming convention is `<form>-error` (e.g.
  `create-tenant-error`, `grant-super-admin-error`,
  `issue-credential-error`, `signin-local-error`,
  `signin-token-error`). Stable, single-source-of-truth, and matches
  the existing `data-testid="create-tenant-error"` on the
  AlertDescription (no collision — different attribute namespaces).
- The Alert primitive already carries `role="alert"` (assertive live
  region — announces interrupting the current SR utterance).
  `aria-live="polite"` is complementary — it announces after the
  current utterance finishes. Both together gives the most coverage
  for SRs that interpret the two differently. WAI-ARIA permits both
  on the same element.

**Pattern documented** in `web/components/ui/checkbox.tsx` header
comment (per AC-7). Sibling primitives are the canonical reference
point for future admin pages.

---

## D4 — api-keys IssueForm: add submit-error Alert (was: no error surface)

**Decision:** Add a new submit-error `<Alert>` to the api-keys
IssueForm (`web/app/admin/api-keys/page.tsx`), surfacing
`issueMut.error` with the same `id="issue-credential-error"` +
`aria-live="polite"` + `aria-describedby` pattern as the other three
forms.

**Rationale (the trickiness):**

- AC-3 says "all four admin forms (tenants · super-admins · api-keys
  · login) wire `aria-describedby` on inputs to the error Alert's
  id WHEN the alert is mounted". Honored conditionally: if the
  alert is never mounted, the criterion is vacuously satisfied.
- But the **api-keys IssueForm had NO submit-error Alert prior to
  this slice** — `issueMut.error` was swallowed (the FreshSecretCallout
  only renders on success; failures returned no visual feedback).
  So AC-3 was technically vacuously satisfiable, but the spirit of
  the slice is to give SR users error feedback — vacuous compliance
  defeats the slice's purpose.
- The minimal, lowest-disturbance fix: surface `issueMut.error` via
  a new destructive Alert under the submit button (same shape as
  the other three forms). Mutation behavior is unchanged
  (`issueMut.mutate(body)` still fires; the error is rendered if
  the promise rejects). P0-363-3 ("does NOT modify form submission
  behavior") is honored — the mutation contract is identical; only
  the visual feedback widens.
- The IssueForm wraps the error rendering in its own component
  state via a new `error: string | null` prop (parent passes
  `issueMut.error?.message ?? null`). Keeps the form-shape
  consistent with how tenants/super-admins/login carry their
  `createError` / `grantError` / `error` state.

---

## D5 — api-keys "Admin credential" raw checkbox: flagged but NOT swapped

**Decision:** Do NOT swap the raw `<input type="checkbox">` at
`web/app/admin/api-keys/page.tsx:391` (the "Admin credential" toggle
inside IssueForm) in this slice. Flag for future a11y audit
follow-up.

**Rationale:**

- P0-363-2 says "does NOT widen to non-admin forms in this slice
  unless they were trivially included". The api-keys raw checkbox
  IS in an admin form (so the "non-admin forms" carve-out doesn't
  apply), but the slice doc Gap 1 explicitly names only the tenants
  page raw checkbox (per the slice 331 audit finding). The audit
  did not enumerate api-keys' raw checkbox.
- Engineer-as-collaborator discipline (per CLAUDE.md): "if you spot
  another raw `<input>` in admin pages, do NOT widen scope in this
  slice — but note in PR body for future audit". Honored.
- Functional impact: the api-keys checkbox is inside a
  `<label className="flex items-center gap-2">` so the focus-ring
  gap shows as a Tab-stop missing visual indicator, same WCAG 2.4.7
  failure shape. Future slice fixing this should swap to the same
  `<Checkbox>` primitive landed by this slice — the work is
  estimated < 0.25d once the primitive is upstream.
- A follow-up slice 364 (or 365) would close this. Filed for
  triage; not in this slice.

---

## D6 — Login page: two distinct errorIds (signinLocal vs signinToken)

**Decision:** The login page (`web/app/login/page.tsx`) has TWO
conditionally-rendered Alerts wrapping TWO independent forms
(signInLocal email/password card + signIn bearer-paste card). Each
gets a distinct id:

- `signin-local-error` on the email/password card Alert
- `signin-token-error` on the bearer-paste card Alert

**Rationale:**

- The two cards mount under different conditions
  (`bootstrapTenantID` truthy vs falsy + `error` searchParam). They
  wrap independent `<form>` elements with independent inputs.
- A single shared `signin-error` id would create a stale reference
  if both Alerts mounted simultaneously (impossible given the
  current `error && bootstrapTenantID` vs `error && !bootstrapTenantID`
  branching, but the invariant would be brittle).
- Distinct ids per form preserve the 1:1 input-to-alert association
  even if a future refactor mounts both cards.

---

## Verification

- **Vitest:** `web/components/ui/checkbox-class.test.ts` — 11 tests,
  green. Full suite: 1165 tests, green.
- **Typecheck:** `npx tsc --noEmit` — 0 errors introduced by this
  slice (3 pre-existing errors in unrelated files —
  `lib/auth/oauth-client.test.ts`, `next-config.test.ts`,
  `scripts/capture-readme-screenshots.test.ts` — unchanged).
- **Lint:** `npx eslint` on all touched files — 0 errors, 0
  warnings.
- **Next.js build:** `npm run build` — succeeds.
- **Playwright:** `web/e2e/admin-tenants.spec.ts` extended with
  one new spec asserting the aria-describedby wiring on
  submit-with-error flow. Local run defers to CI (Playwright
  harness requires docker-compose bring-up per `web/e2e/README.md`).
- **pre-commit:** `pre-commit run --all-files` — all hooks green
  (prettier auto-fixed two files on first run; second run clean).
