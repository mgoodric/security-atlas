// Slice 255 — relative-time formatter for header "last evaluated" timestamps.
//
// Mirrors the freshness-clock's `humanizeSince` helper
// (`web/components/control/freshness-clock.tsx`) but expresses the result
// in operator-facing copy ("8 minutes ago", "3 days ago", "just now")
// rather than the compact ring-readout form ("8m", "3d"). The two are
// deliberately separate: the freshness-clock's compact form fits inside
// the SVG ring readout, while the header sub-line is full sentence-form
// per the mockup (`Plans/_archive/mockups/control.html` line 100 — "last evaluated
// 8 minutes ago").
//
// Inputs:
//   - ISO 8601 timestamp string, or `null` / `undefined`.
//   - optional `now` override (test seam — defaults to `Date.now()`).
//
// Returns:
//   - "—" for null / undefined / unparsable input.
//   - "never" for explicit-null intent — caller decides between this and
//     "—" at the call site (see `relativeTimeOrNever`).
//   - "just now" for any timestamp within the last minute.
//   - "N minute(s) ago" / "N hour(s) ago" / "N day(s) ago" otherwise.
//   - timestamps in the future (clock skew / pre-seed data) clamp to
//     "just now" rather than rendering a negative duration. The slice
//     012 evaluator is deterministic about not back-dating, but
//     defense-in-depth is cheap here.
//
// This is a pure helper (no React, no hooks) — vitest-coverable per AC-5
// without spinning up jsdom.

export function relativeTime(
  iso: string | null | undefined,
  now: number = Date.now(),
): string {
  if (!iso) return "—";
  const then = new Date(iso);
  const t = then.getTime();
  if (Number.isNaN(t)) return "—";

  const deltaMs = now - t;
  // Clamp future timestamps (clock skew tolerance) to "just now" rather
  // than rendering a negative count.
  if (deltaMs < 60_000) return "just now";

  const minutes = Math.floor(deltaMs / 60_000);
  if (minutes < 60) {
    return `${minutes} ${minutes === 1 ? "minute" : "minutes"} ago`;
  }
  const hours = Math.floor(minutes / 60);
  if (hours < 24) {
    return `${hours} ${hours === 1 ? "hour" : "hours"} ago`;
  }
  const days = Math.floor(hours / 24);
  return `${days} ${days === 1 ? "day" : "days"} ago`;
}

// relativeTimeOrNever distinguishes "we have no state record at all"
// (caller passes `undefined` → "—") from "state record exists but the
// underlying field is null" (caller passes `null` → "never"). The
// slice 255 control-header sub-line uses the latter for the
// state-exists-but-no-evidence case.
export function relativeTimeOrNever(
  iso: string | null | undefined,
  now: number = Date.now(),
): string {
  if (iso === null) return "never";
  return relativeTime(iso, now);
}
