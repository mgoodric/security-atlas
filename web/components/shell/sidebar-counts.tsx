// Slice 214 — sidebar item count badges (Controls + Risks).
//
// Closes the parity gap surfaced by slice 204's audit fleet: the
// mockup at `Plans/mockups/audits.html` (lines 63-76) shows two
// sidebar rows carrying right-aligned count badges:
//
//   - Controls   |  mono "82"  (muted)
//   - Risks      |  mono "3"   (rose — high-severity tier)
//
// The live sidebar (`web/components/shell/sidebar.tsx`) rendered
// bare text labels. This module supplies the two badges, mounted in
// the sidebar's NAV map for the Controls + Risks rows only.
//
// Behavior (parallels slice 213's `in-progress-audit-pill.tsx`):
//
//   - Reads the existing per-page BFF routes — `/api/controls`
//     (slice 098) for the controls count, `/api/risks` (slice 100)
//     for the risks count. No new platform endpoint (P0-214-1).
//   - TanStack Query handles the request lifecycle. `staleTime: 60_000`
//     + `refetchInterval: 60_000` per AC-3 — a 60s low-priority
//     refresh that surfaces operator-attention spikes (new risk,
//     deleted controls) without hammering the BFF (P0-214-3).
//   - Renders NOTHING (null) on loading / error / zero (P0-214-2:
//     silent absence > a `0` badge). Sidebar render is not blocked on
//     the count fetch (P0-214-4); the badges fade in on resolve.
//   - Subtle `animate-pulse` is bound to `isFetching` so the pulse
//     marks the active refresh tick, not a permanent decoration.
//
// Query key choice (distinct from the parent pages' keys): the
// `/controls` page uses `["controls","list",scopeArg ?? "all"]` —
// these badges use `["sidebar","controls-count"]`. This split costs
// one extra fetch when on `/controls`, but the alternative
// (subscribing to the page's parameterized key) would couple the
// badge to the page's filter state and refetch on every filter
// toggle. With a 60s stale window the cost is negligible.
//
// Constitutional invariants:
//   - Invariant 6 (tenant isolation): the BFFs forward the bearer
//     cookie; the platform enforces RLS. The badges never read or
//     forward a tenant_id.
//   - Invariant 9 (manual evidence is first-class): the controls
//     count includes manual controls; the count is "anchors with
//     state for this tenant" — manual or automated, same lifecycle.

"use client";

import { useQuery } from "@tanstack/react-query";

import {
  fetchControlsList,
  fetchRisksList,
  type ControlsListResponse,
  type Risk,
  type RisksListResponse,
} from "@/lib/api";

/**
 * The slice-100 `filters.ts` defines severity bands on the 5x5 scalar
 * (0..25):
 *
 *     high   = severity >= 15   (rose)
 *     medium = 8..14            (amber)
 *     low    = 1..7             (emerald)
 *     none   = 0
 *
 * The Risks badge counts only the high tier — the mockup's rose `3`
 * is unambiguously the "rose" band. The schema has no `status` column
 * on the risk wire shape and no `critical` band, so the slice spec's
 * "open critical" phrase resolves to this canonical high tier. See
 * `docs/audit-log/214-sidebar-item-counts-decisions.md` D1.
 */
const HIGH_SEVERITY_THRESHOLD = 15;

/**
 * countHighSeverityRisks returns the count of rows whose 5x5
 * `severity` scalar is at or above the high-tier threshold. Pure;
 * unit-tested in `sidebar-counts.test.ts`.
 */
export function countHighSeverityRisks(risks: Risk[]): number {
  let n = 0;
  for (const r of risks) {
    if (r.severity >= HIGH_SEVERITY_THRESHOLD) n++;
  }
  return n;
}

/**
 * Common Tailwind class set for the count badge. Right-aligned via
 * `ml-auto` (sits inside the sidebar Link, which is a flex row).
 * Mono + tight tracking matches the mockup's `mono text-[10px]`.
 */
const BADGE_BASE =
  "ml-auto inline-flex items-center text-[10px] font-mono tabular-nums leading-none";

/**
 * Subtle pulse during refetch only — not a permanent decoration.
 * Refresh tick at 60s; this gives the operator a visible "the
 * sidebar is alive" cue without being noisy at steady state.
 */
function pulseClass(isFetching: boolean): string {
  return isFetching ? "animate-pulse" : "";
}

export function ControlsCountBadge() {
  const q = useQuery<ControlsListResponse>({
    queryKey: ["sidebar", "controls-count"],
    queryFn: () => fetchControlsList(),
    staleTime: 60_000,
    refetchInterval: 60_000,
    // Fail closed: any error => render nothing.
    retry: false,
  });

  if (q.isLoading || q.isError || !q.data) return null;

  const count = q.data.anchors?.length ?? 0;
  if (count === 0) return null;

  return (
    <span
      data-testid="sidebar-controls-count"
      className={`${BADGE_BASE} text-muted-foreground ${pulseClass(q.isFetching)}`}
      aria-label={`${count} controls`}
    >
      {count}
    </span>
  );
}

export function RisksCountBadge() {
  const q = useQuery<RisksListResponse>({
    queryKey: ["sidebar", "risks-count"],
    queryFn: fetchRisksList,
    staleTime: 60_000,
    refetchInterval: 60_000,
    retry: false,
  });

  if (q.isLoading || q.isError || !q.data) return null;

  const count = countHighSeverityRisks(q.data.risks ?? []);
  if (count === 0) return null;

  return (
    <span
      data-testid="sidebar-risks-count"
      className={`${BADGE_BASE} text-rose-600 dark:text-rose-400 ${pulseClass(q.isFetching)}`}
      aria-label={`${count} high-severity risks`}
    >
      {count}
    </span>
  );
}
