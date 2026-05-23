// Slice 102 — pure presentation helpers for the /audits list view.
//
// Status pill color, lock visibility, "days until period_end" math, and
// frozen-metadata tooltip text live here as pure functions so they're
// vitest-testable without React.
//
// Constitutional commitment: no React, no DOM. Pure data → strings.

import type { AuditPeriod } from "@/lib/api";

/**
 * Threshold (in days) for the "amber dot" in-progress cue per AC-6.
 * A non-frozen period whose `period_end` is within this many days of
 * `now` shows the cue so the security leader has an early signal to
 * start fieldwork.
 *
 * The slice text says "within 30 days". Exposed as a constant so tests
 * can reference it directly rather than baking the magic number in.
 */
export const IN_PROGRESS_URGENT_DAYS = 30;

/**
 * One day in milliseconds. Used for the days-until-end computation.
 */
const ONE_DAY_MS = 24 * 60 * 60 * 1000;

/**
 * Tailwind class set for the status pill background + text.
 *
 * The platform's audit_periods.status CHECK constraint allows
 * {'open','frozen'} in v1. The slice text also mentions
 * `in_progress`, `closed`, and `planned` as forward-looking statuses.
 * We render whatever status the backend returns; unknown statuses fall
 * through to the neutral slate token so we never crash on a new value
 * the platform adds later.
 *
 * Color mapping mirrors the slice 093 audits.html mockup:
 *   open / in_progress  → amber  (active, watch it)
 *   frozen              → sky    (locked, deterministic replay)
 *   closed / planned    → slate  (terminal / future)
 */
export function statusPillClass(status: string): string {
  switch (status) {
    case "open":
    case "in_progress":
      return "bg-amber-50 text-amber-700";
    case "frozen":
      return "bg-sky-50 text-sky-700";
    case "closed":
    case "planned":
      return "bg-slate-100 text-slate-600";
    default:
      return "bg-slate-100 text-slate-600";
  }
}

/**
 * Tailwind class for the small status-dot inside the pill. Matches
 * `statusPillClass` semantically; pulses for in-progress periods.
 */
export function statusDotClass(status: string): string {
  switch (status) {
    case "open":
    case "in_progress":
      return "bg-amber-500 animate-pulse";
    case "frozen":
      return "bg-sky-500";
    case "closed":
    case "planned":
      return "bg-slate-400";
    default:
      return "bg-slate-400";
  }
}

/**
 * A period is frozen iff status === "frozen". The lock icon visibility
 * derives from this exact predicate — frozen periods get the lock; all
 * other statuses do not (AC-6).
 */
export function isFrozen(period: AuditPeriod): boolean {
  return period.status === "frozen";
}

/**
 * Whole days from `now` to `period_end`. Negative if `period_end` is
 * in the past. Used for the "in-progress within 30 days" amber cue and
 * for the row-end "Xd left" display.
 *
 * Both inputs are parsed as Dates; `now` defaults to the current wall
 * clock when callers don't pass one (tests do).
 */
export function daysUntilEnd(
  period: AuditPeriod,
  now: Date = new Date(),
): number {
  const end = new Date(period.period_end).getTime();
  const start = now.getTime();
  return Math.ceil((end - start) / ONE_DAY_MS);
}

/**
 * AC-6: visual urgency cue for in-progress periods within 30 days of
 * `period_end`. Returns true ONLY for non-frozen periods whose
 * `period_end` is between 0 and 30 days from `now` (inclusive).
 *
 * Frozen periods never get the urgent cue — they're locked and the
 * lock icon is the only visual marker they need.
 * Past-end periods (negative days) also do not get the urgent cue —
 * the user needs a different signal there (likely "should freeze").
 */
export function isInProgressUrgent(
  period: AuditPeriod,
  now: Date = new Date(),
): boolean {
  if (isFrozen(period)) return false;
  const days = daysUntilEnd(period, now);
  return days >= 0 && days <= IN_PROGRESS_URGENT_DAYS;
}

/**
 * Human-readable "Xd left" / "Xd ago" string for the row meta cell.
 *
 * Examples:
 *   29 → "29d left"
 *   0  → "ends today"
 *   -3 → "3d ago"
 */
export function daysUntilEndLabel(days: number): string {
  if (days === 0) return "ends today";
  if (days > 0) return `${days}d left`;
  return `${Math.abs(days)}d ago`;
}

/**
 * Tooltip text for the lock icon on frozen periods. Renders both
 * `frozen_at` (ISO date prefix YYYY-MM-DD) and `frozen_by` (the actor
 * ID who issued the freeze). The frozen_hash is intentionally omitted
 * from the tooltip to keep it short — it surfaces on the period detail
 * page instead.
 *
 * If either field is missing on the wire (the platform omits them
 * when status !== "frozen"), the tooltip falls back to a generic label
 * so we never show a broken "frozen at undefined" string.
 */
export function frozenTooltip(period: AuditPeriod): string {
  if (!isFrozen(period)) return "";
  const at = period.frozen_at
    ? `frozen at ${period.frozen_at.slice(0, 10)}`
    : "frozen";
  const by = period.frozen_by ? ` by ${period.frozen_by}` : "";
  return `${at}${by}`;
}

/**
 * Compact period range label: "2026-04-01 → 2026-06-30". Used in the
 * Period column. Both ends are rendered as ISO YYYY-MM-DD prefixes —
 * the periodWire serializes them as RFC3339 timestamps, and the date
 * prefix is the human-meaningful part.
 */
export function periodRangeLabel(period: AuditPeriod): string {
  const start = period.period_start.slice(0, 10);
  const end = period.period_end.slice(0, 10);
  return `${start} → ${end}`;
}

/**
 * Frozen-meta column text: "2026-04-03 · <actor>". Empty string for
 * periods that are not frozen — the cell renders an em-dash instead.
 */
export function frozenMetaLabel(period: AuditPeriod): string {
  if (!isFrozen(period)) return "";
  const at = period.frozen_at ? period.frozen_at.slice(0, 10) : "";
  const by = period.frozen_by ?? "";
  if (!at && !by) return "";
  if (!at) return by;
  if (!by) return at;
  return `${at} · ${by}`;
}

/**
 * Slice 215 — status-tally formatter for the /audits page header.
 *
 * Derives an inline "1 in_progress · 4 frozen · 1 closed" string from
 * the same periods list the table renders. Pure data → string; no
 * React, no DOM.
 *
 * Rendering rules (mirror the slice 215 ACs):
 *   - AC-1 / P0-215-1: statuses with count 0 are OMITTED. The string
 *     "0 closed · 0 open" is noise we explicitly reject.
 *   - AC-1 ordering: canonical statuses render in the slice-prescribed
 *     order — `in_progress · frozen · closed · open`. Any status that
 *     appears in the data but is NOT one of those four (e.g. `planned`
 *     once the backend lifts the CHECK constraint, or an unknown new
 *     value) is appended after the canonical four in alphabetical
 *     order, so the rendering is deterministic.
 *   - P0-215-2: only statuses with at least one matching period render.
 *     We never invent statuses from a hard-coded enum — the data is the
 *     source of truth.
 *   - AC-2: empty input → empty string. The caller can use the empty
 *     string as a render-or-skip flag (e.g. `tally ? <span>{tally}</span>
 *     : null`).
 *
 * Separator is " · " (U+00B7 MIDDLE DOT with single spaces) to match
 * the mockup at `Plans/mockups/audits.html` line 111 verbatim.
 */
export const TALLY_STATUS_ORDER: readonly string[] = [
  "in_progress",
  "frozen",
  "closed",
  "open",
];

export function statusTallyLabel(periods: AuditPeriod[]): string {
  if (periods.length === 0) return "";

  // Count per status — only statuses present in the data appear in the
  // map, so the rendering never invents statuses (P0-215-2).
  const counts = new Map<string, number>();
  for (const p of periods) {
    counts.set(p.status, (counts.get(p.status) ?? 0) + 1);
  }

  // Canonical ordering first, then any non-canonical statuses
  // alphabetically. Both lists drop count-0 entries (P0-215-1) — the
  // map only contains statuses we saw, so this is automatic for the
  // canonical pass, but we still guard explicitly for clarity.
  const canonical: string[] = [];
  for (const status of TALLY_STATUS_ORDER) {
    const n = counts.get(status);
    if (n && n > 0) {
      canonical.push(`${n} ${status}`);
      counts.delete(status);
    }
  }

  // Remaining (non-canonical) statuses — sort alphabetically for
  // deterministic output. count-0 entries can't appear here because we
  // only insert into the map when we see a period.
  const remaining = [...counts.entries()]
    .sort((a, b) => a[0].localeCompare(b[0]))
    .map(([status, n]) => `${n} ${status}`);

  return [...canonical, ...remaining].join(" · ");
}
