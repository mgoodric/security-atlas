// Slice 363 — pure className composition for the shadcn-themed
// Checkbox primitive (`web/components/ui/checkbox.tsx`).
//
// Extracted into its own `.ts` module so the project's vitest runner
// (node env per slice 069 P0-A3 — no DOM, no React render) can cover
// the className shape without introducing jsdom + @testing-library/
// react as test deps. The `.tsx` component imports `checkboxClassName`
// and applies it to the Base-UI Checkbox.Root.
//
// The class shape mirrors `web/components/ui/input.tsx` for ring
// (`focus-visible:ring-3 focus-visible:ring-ring/50`) and disabled
// behaviour. Box uses the project's `border-input` token and
// `aria-checked` styling so a checked state reads as accent color
// without needing a separate state-driven className branch.

import { cn } from "@/lib/utils";

/**
 * Compose the class string for the shadcn-themed Checkbox primitive.
 *
 * Carries the same `focus-visible:ring-3 focus-visible:ring-ring/50`
 * shape used by Input + Button so the focus indicator is consistent
 * across form primitives (WCAG 2.4.7).
 *
 * `aria-invalid:*` mirrors Input so the destructive-ring state lights
 * up if a future caller passes `aria-invalid`.
 *
 * @param className caller-supplied class string (Tailwind tokens or
 *                  arbitrary values), merged via cn() so later tokens
 *                  win.
 */
export function checkboxClassName(className?: string): string {
  return cn(
    // Base box: 16x16, rounded, bordered, transparent background.
    "peer inline-flex h-4 w-4 shrink-0 items-center justify-center rounded border border-input bg-transparent transition-colors outline-none",
    // Focus ring: matches Input + Button exactly (slice 363 AC-1).
    "focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50",
    // Checked state: filled with primary color so the indicator reads
    // as a high-contrast accent against the page background.
    "data-[checked]:border-primary data-[checked]:bg-primary data-[checked]:text-primary-foreground",
    // Disabled: matches Input + Button.
    "disabled:pointer-events-none disabled:cursor-not-allowed disabled:opacity-50",
    // Invalid: mirrors Input. Future-proofing in case a form lib wires
    // aria-invalid onto the checkbox.
    "aria-invalid:border-destructive aria-invalid:ring-3 aria-invalid:ring-destructive/20 dark:aria-invalid:border-destructive/50 dark:aria-invalid:ring-destructive/40",
    className,
  );
}

/**
 * Class string for the Indicator span rendered inside the box when
 * checked. Sized to fit a 16x16 box and forces the SVG to inherit
 * the parent's text color.
 */
export function checkboxIndicatorClassName(className?: string): string {
  return cn(
    "flex items-center justify-center text-current [&_svg]:size-3",
    className,
  );
}
