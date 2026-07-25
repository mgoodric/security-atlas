// Slice 668 — pure "today" detection for the calendar month grid.
//
// The month grid (`month-grid-view.tsx`) walks day cells from the Sunday
// before the 1st through the Saturday after the last day of the month and
// keys each cell on a LOCAL calendar date (`isoDay` uses getFullYear /
// getMonth / getDate, not a UTC slice). The "today" highlight therefore has
// to compare on the same local-date basis — comparing against a UTC-derived
// string would mis-mark the cell for users east/west of UTC near midnight.
//
// `isSameLocalDay` is the load-bearing primitive: it compares two Date
// objects on their local (year, month, day) triple only. `isToday(cell,
// reference)` is the thin wrapper the view calls; the `reference` arg is
// injectable so the unit + Playwright assertions can pin "now" to a fixed
// date and assert deterministically (AC-3). The view passes a single
// `new Date()` captured once per render as the reference — the only
// non-injected clock read, kept out of this module so the logic stays pure
// and testable.
//
// AC mapping:
//   AC-1 — the view uses `isToday` to apply the distinct today treatment.
//   AC-2 — the view sets aria-current="date" on the same cell.
//   AC-3 — `reference` is a parameter (not an inline clock read), so the
//          today match is deterministic under test. Covered by
//          `is-today.test.ts` (pure logic, node-env vitest) + the calendar
//          Playwright spec.

/**
 * True when `a` and `b` fall on the same calendar day in LOCAL time.
 * Compares the (year, month, day) triple only — time-of-day is ignored.
 */
export function isSameLocalDay(a: Date, b: Date): boolean {
  return (
    a.getFullYear() === b.getFullYear() &&
    a.getMonth() === b.getMonth() &&
    a.getDate() === b.getDate()
  );
}

/**
 * True when `cell` is the same local calendar day as `reference` ("now").
 * `reference` is injected so callers/tests control the clock (AC-3).
 */
export function isToday(cell: Date, reference: Date): boolean {
  return isSameLocalDay(cell, reference);
}
