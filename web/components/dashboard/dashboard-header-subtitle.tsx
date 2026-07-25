// Slice 229 — dashboard header subtitle (tenant context + evidence
// freshness pct). Closes the parity gap surfaced by slice 204's audit
// fleet (dashboard slug, finding F-204D-2): the mockup at
// `Plans/_archive/mockups/dashboard.html` (lines 117-120) renders contextual
// orientation copy next to the H1 — the live build rendered generic
// marketing copy that did not communicate which tenant the operator
// was viewing nor what the aggregate freshness posture was.
//
// Behavior (parallels slice 213's `in-progress-audit-pill.tsx` and
// slice 214's `sidebar-counts.tsx` silent-absence pattern):
//
//   - Tenant name: reads /api/me/tenants (existing slice 192 BFF,
//     same as `TenantSwitcher`). Renders the current tenant's name
//     next to the H1 (AC-1).
//   - Freshness pct: reads /api/dashboard/freshness via the SAME
//     TanStack Query key the parent `DashboardPage` already uses
//     (`["dashboard", "freshness"]`) so the badge piggybacks on the
//     cache and does not double-fetch (P0-229-1: no new platform
//     endpoint).
//   - Loading state: renders a skeleton placeholder rather than the
//     prior generic marketing copy (AC-3).
//   - Error state: renders the literal "Snapshot unavailable" (AC-4).
//   - Empty state: when total === 0 (bootstrap seed), renders
//     "No evidence ingested yet" — honest about empty, not
//     "100% fresh of 0" (AC-5, P0-229-2).
//
// JUDGMENT call — snapshot timestamp omitted: the slice spec's AC-2
// text asks for "Snapshot taken {relativeTime} · evidence freshness
// {pct}% within window" where relativeTime comes from "the freshness
// response's most-recent received_at". The current `FreshnessReport`
// wire shape (see `web/lib/api.ts`) exposes `{bucket, buckets[],
// total, total_stale}` only — there is NO `received_at` or
// `refreshed_at` field on the response. Per the slice spec's hard
// rule "honest about snapshot freshness — if data source has no
// timestamp, don't fabricate one", the snapshot timestamp half is
// omitted. The freshness pct half (the load-bearing posture signal)
// ships. See `docs/audit-log/229-dashboard-header-subtitle-decisions.md`
// D1 for the full reasoning + spillover slice for adding the
// timestamp to the wire shape if needed.
//
// Constitutional invariants:
//   - Invariant 6 (tenant isolation): both BFFs (`/api/me/tenants`,
//     `/api/dashboard/freshness`) forward the bearer/JWT cookie; the
//     platform enforces RLS. This component never reads or forwards
//     a tenant_id.
//   - AI-assist boundary: subtitle is deterministic computation, no
//     LLM generation. Hallucination-free by construction.

"use client";

import { useQuery } from "@tanstack/react-query";

import {
  fetchDashboardFreshness,
  type FreshnessReport,
} from "@/lib/api/dashboard";
import { freshnessPctFromReport } from "@/lib/api/freshness-consistency";
import { useCurrentTenantName } from "@/lib/auth/use-current-tenant-name";

// ---------------------------------------------------------------------
// Pure helpers — unit-tested in `dashboard-header-subtitle.test.ts`.
// ---------------------------------------------------------------------

/**
 * computeFreshnessPct turns the freshness response's (total, total_stale)
 * counts into the "% within window" integer percentage. Returns null when
 * total === 0 (bootstrap seed state) — the caller renders the AC-5
 * empty-state copy in that case rather than the meaningless "100% fresh
 * of 0".
 *
 * Defensive against bad inputs: negative total and `total_stale > total`
 * both clamp to a sane value rather than throwing.
 */
export function computeFreshnessPct(
  total: number,
  totalStale: number,
): number | null {
  // Slice 677 / ATLAS-020: delegate to the shared single-source-of-truth
  // definition so the dashboard subtitle and the metrics-view board KPI
  // can never disagree on the freshness figure (they call one function).
  return freshnessPctFromReport({ total, total_stale: totalStale });
}

/**
 * formatFreshnessSubtitle renders the freshness-half of the subtitle:
 *
 *   - null  -> "No evidence ingested yet"     (AC-5)
 *   - 87    -> "evidence freshness 87% within window"  (AC-2)
 */
export function formatFreshnessSubtitle(pct: number | null): string {
  if (pct === null) return "No evidence ingested yet";
  return `evidence freshness ${pct}% within window`;
}

/**
 * formatTenantContext returns the tenant context string for the H1 row
 * (AC-1). Trims whitespace and treats blank/undefined as silent absence
 * (returns null so the H1 row collapses to just "Program").
 */
export function formatTenantContext(name: string | undefined): string | null {
  if (typeof name !== "string") return null;
  const trimmed = name.trim();
  if (trimmed.length === 0) return null;
  return trimmed;
}

// ---------------------------------------------------------------------
// Components
// ---------------------------------------------------------------------

/**
 * TenantContext renders the tenant-name chip next to the dashboard H1.
 * Returns null on silent absence (no tenant context loaded yet, or the
 * fetch failed).
 *
 * Slice 674 — the active-tenant-name resolution now lives in the shared
 * `useCurrentTenantName` hook (`web/lib/auth/use-current-tenant-name.ts`),
 * which re-fetches on the slice-199 `tenant-switched` broadcast. The
 * prior local hook fetched only on mount, so an in-tab switch left this
 * chip showing the origin tenant name until a hard reload.
 */
export function TenantContext() {
  const tenantName = useCurrentTenantName();
  const display = formatTenantContext(tenantName ?? undefined);
  if (display === null) return null;
  return (
    <span
      data-testid="dashboard-header-tenant-context"
      className="text-sm text-muted-foreground"
    >
      {display}
    </span>
  );
}

/**
 * DashboardHeaderSubtitle renders the freshness-half of the subtitle
 * row below the H1.
 *
 *   - loading  -> skeleton placeholder (AC-3)
 *   - error    -> "Snapshot unavailable" (AC-4)
 *   - empty    -> "No evidence ingested yet" (AC-5)
 *   - ok       -> "evidence freshness {pct}% within window" (AC-2)
 *
 * Reuses the parent page's TanStack Query key so the cache is shared
 * (no double-fetch).
 */
export function DashboardHeaderSubtitle() {
  const q = useQuery<FreshnessReport>({
    queryKey: ["dashboard", "freshness"],
    queryFn: fetchDashboardFreshness,
  });

  if (q.isLoading) {
    return (
      <div
        data-testid="dashboard-header-subtitle-loading"
        aria-hidden
        className="mt-0.5 h-4 w-72 animate-pulse rounded bg-muted/60"
      />
    );
  }

  if (q.isError || !q.data) {
    return (
      <p
        data-testid="dashboard-header-subtitle-error"
        className="mt-0.5 text-sm text-muted-foreground"
      >
        Snapshot unavailable
      </p>
    );
  }

  const pct = computeFreshnessPct(q.data.total, q.data.total_stale);
  return (
    <p
      data-testid="dashboard-header-subtitle"
      className="mt-0.5 text-sm text-muted-foreground"
    >
      {formatFreshnessSubtitle(pct)}
    </p>
  );
}
