// Slice 363 — vitest unit coverage for the Checkbox className helper.
//
// Pure string-composition tests under the project's node-env vitest
// runner (slice 069 P0-A3: no DOM, no React, no @testing-library).
// Component-render coverage rides Playwright per `web/testing.md`.

import { describe, expect, test } from "vitest";

import {
  checkboxClassName,
  checkboxIndicatorClassName,
} from "./checkbox-class";

describe("checkboxClassName", () => {
  test("includes the project focus-visible ring shape (WCAG 2.4.7)", () => {
    const out = checkboxClassName();
    // The exact ring shape used by web/components/ui/input.tsx and
    // web/components/ui/button.tsx — keep this in lockstep so a
    // future change to the project ring catches the checkbox too.
    expect(out).toContain("focus-visible:ring-3");
    expect(out).toContain("focus-visible:ring-ring/50");
    expect(out).toContain("focus-visible:border-ring");
  });

  test("includes the base box shape (16x16, rounded, bordered)", () => {
    const out = checkboxClassName();
    expect(out).toContain("h-4");
    expect(out).toContain("w-4");
    expect(out).toContain("rounded");
    expect(out).toContain("border-input");
  });

  test("includes the checked state styling", () => {
    const out = checkboxClassName();
    expect(out).toContain("data-[checked]:bg-primary");
    expect(out).toContain("data-[checked]:text-primary-foreground");
  });

  test("includes the disabled state styling (matches Input + Button)", () => {
    const out = checkboxClassName();
    expect(out).toContain("disabled:pointer-events-none");
    expect(out).toContain("disabled:cursor-not-allowed");
    expect(out).toContain("disabled:opacity-50");
  });

  test("includes aria-invalid styling (forward-compat with form libs)", () => {
    const out = checkboxClassName();
    expect(out).toContain("aria-invalid:border-destructive");
    expect(out).toContain("aria-invalid:ring-destructive/20");
  });

  test("merges caller-supplied className via cn()", () => {
    const out = checkboxClassName("mt-2 custom-token");
    expect(out).toContain("mt-2");
    expect(out).toContain("custom-token");
    // Base shape is still present.
    expect(out).toContain("focus-visible:ring-3");
  });

  test("undefined className is a no-op (no 'undefined' literal)", () => {
    const out = checkboxClassName(undefined);
    expect(out).not.toContain("undefined");
    // Base shape is intact.
    expect(out).toContain("h-4");
  });

  test("empty-string className is treated as no-op", () => {
    const out = checkboxClassName("");
    expect(out).not.toMatch(/\s\s/); // no double-space artifacts
    expect(out).toContain("h-4");
  });
});

describe("checkboxIndicatorClassName", () => {
  test("centers the indicator and sizes the SVG", () => {
    const out = checkboxIndicatorClassName();
    expect(out).toContain("flex");
    expect(out).toContain("items-center");
    expect(out).toContain("justify-center");
    expect(out).toContain("[&_svg]:size-3");
  });

  test("inherits text-current so the glyph picks up the parent color", () => {
    expect(checkboxIndicatorClassName()).toContain("text-current");
  });

  test("merges caller-supplied className", () => {
    expect(checkboxIndicatorClassName("opacity-80")).toContain("opacity-80");
  });
});
