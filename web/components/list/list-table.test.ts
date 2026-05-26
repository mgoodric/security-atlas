// Slice 281 — vitest unit coverage for the `listTableBranchClasses`
// helper that drives the `<ListTable>` mobile-mode rendering branch.
//
// Pure-data tests; no React, no DOM. The web workspace's vitest is
// configured for `node` env per `web/vitest.config.ts` and ships no
// `@testing-library/react` dependency. The JSX rendering itself is
// exercised by the Playwright spec at `web/e2e/mobile-baseline.spec.ts`
// (slice 281 extension) — that spec asserts the card-stack shape at
// 375px and the desktop-unchanged shape at 1280px.
//
// What this file pins (slice 281 AC-7):
//   * `"table"` (the legacy default) → table branch on, cards branch
//     OFF (`cardsWrap === null` — the component MUST NOT mount the
//     cards branch at all so the desktop DOM is byte-identical to
//     pre-281 for legacy callers).
//   * `"cards"` (the slice 281 opt-in) → BOTH branches mounted with
//     reciprocal visibility classes (`hidden md:block` for the table
//     branch, `block md:hidden` for the cards branch).
//   * Exhaustive truth table — both modes covered.
//
// What this file deliberately does NOT cover:
//   * JSX output / DOM tree shape. That's a Playwright concern (the
//     vitest config has no jsdom). See `mobile-baseline.spec.ts`.
//   * Tailwind compilation. We assert the raw class strings, not the
//     compiled CSS — Tailwind's responsive prefix semantics are
//     framework-owned (P0-281-3 — no new responsive lib).

import { describe, expect, test } from "vitest";

import { listTableBranchClasses } from "./list-table";

describe("listTableBranchClasses", () => {
  test('mode "table" mounts the table branch only (no cards branch)', () => {
    const got = listTableBranchClasses("table");
    expect(got.tableWrap).toBe("");
    // `null` is the load-bearing sentinel — the component checks
    // `cardsWrap !== null` to decide whether to render the cards
    // branch at all. Confirm the legacy default does NOT mount it.
    expect(got.cardsWrap).toBeNull();
  });

  test('mode "cards" mounts both branches with reciprocal visibility classes', () => {
    const got = listTableBranchClasses("cards");
    // Table branch hidden at `< md`; visible at `>= md`.
    expect(got.tableWrap).toBe("hidden md:block");
    // Cards branch visible at `< md`; hidden at `>= md`.
    expect(got.cardsWrap).toBe("block md:hidden");
  });

  test("the visibility classes are reciprocal — never both visible at any viewport", () => {
    // Defense-in-depth: the load-bearing claim of slice 281 is that
    // exactly ONE branch is visible at any width when mode === "cards".
    // The two Tailwind selectors must therefore partition the viewport
    // axis exactly at `md`. Pin the class strings so a future drift
    // (e.g. someone "improving" the breakpoint to `sm`) fails this
    // test and forces a deliberate update to the responsive-discipline
    // doc instead of a silent change.
    const got = listTableBranchClasses("cards");
    // Table branch carries the `hidden` (mobile) → `md:block` (≥md)
    // pair. Cards branch carries the inverse: `block` (mobile) →
    // `md:hidden` (≥md). At every viewport exactly one is visible.
    expect(got.tableWrap).toContain("hidden");
    expect(got.tableWrap).toContain("md:block");
    expect(got.cardsWrap).toContain("block");
    expect(got.cardsWrap).toContain("md:hidden");
  });
});
