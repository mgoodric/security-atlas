// Slice 527 — native <select> primitive, styled to match the Input
// primitive.
//
// Decision D1 (decisions log): a NATIVE <select> rather than
// `@radix-ui/react-select` (the project has no Radix deps — its form
// family is `@base-ui/react`) or base-ui's popup Select (heavier than a
// plain dropdown; the slice is explicitly "a plain select, not a bespoke
// combobox"). A native <select> with an associated <label htmlFor> is
// keyboard-navigable and screen-reader-labelled by the platform with no
// ARIA wiring to get wrong (AC-8 / slice-331/363 a11y lineage), and adds
// zero dependencies (slice-277 "no new top-level deps" P0).
//
// Carries the same focus-ring shape as Input/Button/Checkbox so the
// focus indicator is consistent across every form control (WCAG 2.4.7).
//
// Pure presentation — component-render coverage rides Playwright per
// `web/testing.md`; there is no node-testable logic in this file (the
// option-mapping logic lives in `web/lib/admin/assign-options.ts`).

import * as React from "react";

import { cn } from "@/lib/utils";

function Select({ className, ...props }: React.ComponentProps<"select">) {
  return (
    <select
      data-slot="select"
      className={cn(
        "h-8 w-full min-w-0 rounded-lg border border-input bg-transparent px-2.5 py-1 text-base transition-colors outline-none focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50 disabled:pointer-events-none disabled:cursor-not-allowed disabled:bg-input/50 disabled:opacity-50 aria-invalid:border-destructive aria-invalid:ring-3 aria-invalid:ring-destructive/20 md:text-sm dark:bg-input/30 dark:disabled:bg-input/80 dark:aria-invalid:border-destructive/50 dark:aria-invalid:ring-destructive/40",
        className,
      )}
      {...props}
    />
  );
}

export { Select };
