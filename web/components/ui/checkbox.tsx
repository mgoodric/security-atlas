// Slice 363 — shadcn-themed Checkbox primitive.
//
// Wraps `@base-ui/react/checkbox` (the existing dep family — slice 277-
// style "no new top-level deps" P0). Base UI renders a visible
// `<span>` and a hidden `<input>` beside it, so screen readers see a
// real checkbox while we control the visual shape via classes.
//
// Carries the same `focus-visible:ring-3 focus-visible:ring-ring/50`
// as `web/components/ui/input.tsx` and `web/components/ui/button.tsx`
// — Tailwind v4 preflight strips the browser default focus ring, and
// every form primitive in the codebase carries this shape so the
// focus indicator is consistent (WCAG 2.4.7).
//
// className composition lives in `./checkbox-class.ts` (a pure `.ts`
// module) so the project's node-env vitest runner can cover the
// classname shape under slice 069 P0-A3 (no React render in vitest).
// Component-render coverage rides Playwright per `web/testing.md`.
//
// ---------------------------------------------------------------------
// FORM-ERROR ASSOCIATION CONVENTION (slice 363 AC-7)
// ---------------------------------------------------------------------
// Every admin form in this codebase follows the same submit-error
// pattern: on validation/submit failure, a destructive `<Alert>`
// mounts below the inputs. Two rules apply for SR/a11y:
//
//   1. The Alert carries a STABLE `id` (e.g. `id="create-tenant-
//      error"`) AND `aria-live="polite"` (the Alert primitive already
//      sets `role="alert"`; the polite live region is complementary).
//
//   2. Every input in the form conditionally carries
//      `aria-describedby` pointing at the alert's id WHEN the alert
//      is mounted. The idiom is:
//
//          const errorId = error ? "create-tenant-error" : undefined;
//          // ...
//          <Input aria-describedby={errorId} ... />
//          {error ? (
//            <Alert id="create-tenant-error" aria-live="polite" ...>
//              ...
//            </Alert>
//          ) : null}
//
//      `aria-describedby={undefined}` strips the attribute entirely
//      when the alert is not mounted (React handles undefined attrs
//      correctly), so no orphan reference exists.
//
// The convention applies to ALL admin forms (tenants, super-admins,
// api-keys, login). Future admin pages should follow the same shape;
// vitest covers the helper, Playwright covers the wiring.
// ---------------------------------------------------------------------

import * as React from "react";
import { Checkbox as CheckboxPrimitive } from "@base-ui/react/checkbox";

import {
  checkboxClassName,
  checkboxIndicatorClassName,
} from "./checkbox-class";

type CheckboxProps = Omit<
  React.ComponentProps<typeof CheckboxPrimitive.Root>,
  "render" | "className"
> & {
  // Slice 363 — restrict className to a plain string. Base UI's
  // primitive accepts `string | ((state) => string)` to compose state-
  // driven classes, but the project's other shadcn wrappers (Input,
  // Button) keep className as a simple string for cn()-mergeable
  // ergonomics. Callers that need state-driven classes should reach
  // for the Base UI primitive directly.
  className?: string;
  // Allow tests + admin pages to attach a data-testid for Playwright.
  "data-testid"?: string;
};

function Checkbox({ className, ...props }: CheckboxProps) {
  return (
    <CheckboxPrimitive.Root
      data-slot="checkbox"
      className={checkboxClassName(className)}
      {...props}
    >
      <CheckboxPrimitive.Indicator
        data-slot="checkbox-indicator"
        className={checkboxIndicatorClassName()}
      >
        <CheckIcon />
      </CheckboxPrimitive.Indicator>
    </CheckboxPrimitive.Root>
  );
}

// Inline SVG check glyph — avoids pulling in a new icon dep.
// Matches the size cues of Lucide's Check icon for visual parity with
// other shadcn primitives in the codebase that use Lucide icons.
function CheckIcon() {
  return (
    <svg
      xmlns="http://www.w3.org/2000/svg"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="3"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
    >
      <polyline points="20 6 9 17 4 12" />
    </svg>
  );
}

export { Checkbox };
