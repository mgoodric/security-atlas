// #2135 — the pill is the audit banner: "Audit ends in N days", counted
// to the open period's end date, linking to the period's detail page.
//
// Slice 213 — in-progress audit pill, rendered in the shared authed-shell
// topbar. Closes the audits-page header chrome parity gap surfaced by
// slice 204's audit fleet (and visible on every authed page, since the
// chrome is shared — AC-2).
//
// Behavior:
//
//   - Reads `/api/audits` (existing slice 102 BFF). No new platform
//     endpoint (P0-213-1). TanStack Query handles the request lifecycle
//     and a 60s stale window — AC-3 explicitly calls out the 60s
//     stale-time so a fresh signal arrives without hammering the API.
//   - Filters the returned periods to `status === "in_progress"` and
//     renders the most-recently-started match as an amber pill.
//   - Renders NOTHING (null) on:
//       * loading / pending state — better a brief gap than a flash of
//         "no audit in progress" copy that immediately disappears.
//       * fetch error — a chrome decoration must never surface a stack
//         trace to a non-error-state page.
//       * zero in-progress periods (P0-213-2: silent absence is honest;
//         the per-row pill on /audits already carries the per-period
//         signal).
//
// Constitutional invariants:
//   - Invariant 6 (tenant isolation): the BFF at /api/audits forwards
//     the bearer cookie; the platform enforces RLS. The pill never
//     reads or forwards a tenant_id.
//   - Invariant 10 (audit-period freezing): only `status="in_progress"`
//     matches; frozen periods are explicitly excluded.

"use client";

import { useQuery } from "@tanstack/react-query";
import Link from "next/link";

import {
  fetchAuditPeriods,
  type AuditPeriod,
  type AuditPeriodsListResponse,
} from "@/lib/api/audit-periods";

/**
 * The literal status the period must carry to qualify for the pill.
 * The audit_periods.status CHECK constraint allows {'open','frozen'}
 * in v1 (see `migrations/sql/20260511000020_audit_periods.sql`); an
 * `'open'` period is, semantically, an audit-period that has been
 * opened and has not yet been frozen — which is exactly what the
 * "in progress" pill is meant to surface to the operator. The pill's
 * displayed copy ("in progress") is user-facing UX; the wire-value
 * is the v1 schema's `'open'`. `format.ts` line 208's forward-looking
 * `'in_progress'` enum entry is a future-compat consideration; today
 * no DB row carries that value.
 */
const ACTIVE = "open";

/**
 * pickMostRecent picks the period with the most recent `period_start`
 * (lexicographic ISO-8601 comparison is correct for the schema's
 * date-typed column). Exported for unit-coverage if needed in future;
 * not used outside this file today.
 */
export function pickMostRecentInProgress(
  periods: AuditPeriod[],
): AuditPeriod | null {
  const inProgress = periods.filter((p) => p.status === ACTIVE);
  if (inProgress.length === 0) return null;
  let best = inProgress[0];
  for (let i = 1; i < inProgress.length; i++) {
    if (inProgress[i].period_start > best.period_start) best = inProgress[i];
  }
  return best;
}

// auditDaysRemaining is the whole calendar days from `now`'s date to the
// period's end date (#2135). Both are read as calendar dates in the
// viewer's own timezone: the end date is a date with no time, and "today"
// is the browser's local date, so the count changes at the viewer's
// midnight. 0 is the last day; negative is past the end.
export function auditDaysRemaining(periodEnd: string, now: Date): number {
  const [y, m, d] = periodEnd.slice(0, 10).split("-").map(Number);
  const end = Date.UTC(y, m - 1, d);
  const today = Date.UTC(now.getFullYear(), now.getMonth(), now.getDate());
  return Math.round((end - today) / 86_400_000);
}

// auditCountdownText is the banner's copy for a day count.
export function auditCountdownText(days: number): string {
  if (days > 1) return `Audit ends in ${days} days`;
  if (days === 1) return "Audit ends in 1 day";
  if (days === 0) return "Audit ends today";
  if (days === -1) return "Audit ended 1 day ago";
  return `Audit ended ${-days} days ago`;
}

export function InProgressAuditPill() {
  const q = useQuery<AuditPeriodsListResponse>({
    queryKey: ["audits", "list"],
    queryFn: fetchAuditPeriods,
    staleTime: 60_000,
    // Fail closed: any error => render nothing. We do NOT use the
    // query's error state in the JSX; we just return null.
    retry: false,
  });

  if (q.isLoading || q.isError || !q.data) return null;

  const pick = pickMostRecentInProgress(q.data.audit_periods ?? []);
  if (!pick) return null;

  // #2135: the banner counts down to the open period's end and links to
  // the period's detail page (#2117).
  const days = auditDaysRemaining(pick.period_end, new Date());
  const text = auditCountdownText(days);

  return (
    <Link
      href={`/audits/${encodeURIComponent(pick.id)}`}
      data-testid="in-progress-audit-pill"
      data-days-remaining={days}
      className="flex items-center gap-2 px-2 py-2 lg:px-2.5 lg:py-1 bg-amber-50 dark:bg-amber-950/40 border border-amber-200 dark:border-amber-900 rounded-full hover:bg-amber-100 dark:hover:bg-amber-950/70"
      title={`${pick.name}: ${text.toLowerCase()}`}
      aria-label={`${pick.name}: ${text.toLowerCase()}`}
    >
      <span className="w-1.5 h-1.5 bg-amber-500 rounded-full animate-pulse" />
      {/* #2213 — below `lg` the pill shows only its dot; the text is
          visually hidden there, not removed, so it is still read out
          and still in the hover title. */}
      <span
        className="sr-only text-xs font-medium text-amber-800 dark:text-amber-200 lg:not-sr-only"
        data-testid="in-progress-audit-countdown"
      >
        {text}
      </span>
    </Link>
  );
}
