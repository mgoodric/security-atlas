"use client";

// Slice 056 — hierarchical risk dashboard view (`/risks/hierarchy`).
//
// The CISO / program-lead surface for the multi-level risk + Decision
// Log work in slices 052-055. Three panels:
//
//   1. Org tree (left)         — collapsible org_unit hierarchy
//   2. Theme heatmap (center)  — themes × org_units matrix
//   3. Decision timeline (right) — Decision Log with revisit markers
//
// Extends the slice 040 program-dashboard pattern: each panel owns its
// own TanStack Query so a slow or failing endpoint degrades only that
// panel (AC-1, anti-criterion P0-2), every panel binds to a real
// backend endpoint via a BFF proxy under `/api/risks-hierarchy/**`, and
// the missing-endpoint pieces (per-org_unit risk counts, themes×org_units
// cell aggregation) render endpoint-naming placeholders rather than
// fabricating data (anti-criterion P0-1). See the slice 056 decisions
// log for the full gap inventory.
//
// Server values live ONLY in the TanStack Query cache and are read
// during render — there is NO useEffect that seeds state from a server
// value (React 19 set-state-in-effect lint, anti-criterion P0-5). The
// single useEffect is the 401 -> /login redirect, matching the slice
// 040 dashboard precedent exactly.
//
// Timeline filter state is held in the URL query string (deep-linkable,
// AC-7) — read with useSearchParams (which requires a <Suspense>
// boundary in the App Router, hence the wrapper component).

import { useQuery } from "@tanstack/react-query";
import Link from "next/link";
import { useRouter, useSearchParams } from "next/navigation";
import { Suspense, useEffect, useMemo } from "react";

import { buttonVariants } from "@/components/ui/button";

import { OrgTreePanel } from "@/components/risk-hierarchy/org-tree-panel";
import { ThemeHeatmapPanel } from "@/components/risk-hierarchy/theme-heatmap-panel";
import {
  DecisionTimelinePanel,
  type TimelineFilters,
} from "@/components/risk-hierarchy/decision-timeline-panel";
import {
  APIError,
  fetchHierarchyAggregationRules,
  fetchHierarchyDecisions,
  fetchHierarchyOrgUnits,
  fetchHierarchyOverdueDecisions,
  fetchHierarchyThemes,
} from "@/lib/api";

// ----- URL <-> filter-state codec -----

function filtersFromParams(params: URLSearchParams): TimelineFilters {
  const csv = (key: string) =>
    (params.get(key) ?? "")
      .split(",")
      .map((s) => s.trim())
      .filter(Boolean);
  return {
    statuses: csv("status"),
    constraints: csv("constraint"),
    decisionMaker: params.get("maker") ?? "",
    revisitFrom: params.get("revisit_from") ?? "",
    revisitTo: params.get("revisit_to") ?? "",
  };
}

function paramsFromFilters(filters: TimelineFilters): URLSearchParams {
  const p = new URLSearchParams();
  if (filters.statuses.length > 0) p.set("status", filters.statuses.join(","));
  if (filters.constraints.length > 0)
    p.set("constraint", filters.constraints.join(","));
  if (filters.decisionMaker.trim() !== "")
    p.set("maker", filters.decisionMaker.trim());
  if (filters.revisitFrom) p.set("revisit_from", filters.revisitFrom);
  if (filters.revisitTo) p.set("revisit_to", filters.revisitTo);
  return p;
}

function HierarchyView() {
  const router = useRouter();
  const searchParams = useSearchParams();

  // Filter state is DERIVED from the URL during render — no useState, no
  // useEffect seeding. The URL is the single source of truth (AC-7,
  // anti-criterion P0-5).
  const filters = useMemo(
    () => filtersFromParams(new URLSearchParams(searchParams.toString())),
    [searchParams],
  );

  const onFiltersChange = (next: TimelineFilters) => {
    const qs = paramsFromFilters(next).toString();
    router.replace(qs ? `/risks/hierarchy?${qs}` : "/risks/hierarchy", {
      scroll: false,
    });
  };

  // The server-side `?status=` filter takes a single value; when the
  // user selects exactly one status we narrow the upstream query,
  // otherwise we fetch all and filter client-side. The query key
  // includes the narrowed status so the cache stays correct.
  const serverStatus =
    filters.statuses.length === 1 ? filters.statuses[0] : undefined;

  const orgUnitsQ = useQuery({
    queryKey: ["risk-hierarchy", "org-units"],
    queryFn: fetchHierarchyOrgUnits,
  });
  const themesQ = useQuery({
    queryKey: ["risk-hierarchy", "themes"],
    queryFn: fetchHierarchyThemes,
  });
  const rulesQ = useQuery({
    queryKey: ["risk-hierarchy", "aggregation-rules"],
    queryFn: fetchHierarchyAggregationRules,
  });
  const decisionsQ = useQuery({
    queryKey: ["risk-hierarchy", "decisions", serverStatus ?? "all"],
    queryFn: () => fetchHierarchyDecisions(serverStatus),
  });
  const overdueQ = useQuery({
    queryKey: ["risk-hierarchy", "decisions-overdue"],
    queryFn: fetchHierarchyOverdueDecisions,
  });

  const overdueIds = useMemo(
    () => new Set((overdueQ.data ?? []).map((d) => d.id)),
    [overdueQ.data],
  );

  // A 401 from any bound query -> the cookie expired mid-session; bounce
  // to /login. The (authed) layout guards the initial load; this covers
  // token expiry while the page is open. Single useEffect by design.
  const firstError =
    orgUnitsQ.error ??
    themesQ.error ??
    rulesQ.error ??
    decisionsQ.error ??
    overdueQ.error ??
    null;
  useEffect(() => {
    if (firstError instanceof APIError && firstError.status === 401) {
      router.push("/login?from=/risks/hierarchy");
    }
  }, [firstError, router]);

  return (
    <div className="space-y-6" data-testid="risk-hierarchy-dashboard">
      <header className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <h1 className="text-2xl font-semibold tracking-tight">
            Risk hierarchy
          </h1>
          <p className="mt-0.5 text-sm text-muted-foreground">
            Navigate risks by org_unit and level, watch theme patterns emerge,
            and track decision-revisit dates.
          </p>
        </div>
        {/* Slice 100 AC-8: reciprocal page-header link to the flat list. The
            hierarchy was lifted out of the sidebar in slice 100 to close
            audit F-3; this link is how a user pivots back to the flat
            register without re-entering through the sidebar. */}
        <Link
          href="/risks"
          data-testid="risk-hierarchy-list-view-link"
          className={buttonVariants({ variant: "outline", size: "sm" })}
        >
          List view →
        </Link>
      </header>

      {/* Three-panel layout: tree | heatmap | timeline. Stacks to a
          single column below the md breakpoint (AC-1). The heatmap gets
          the widest column on xl. */}
      <div className="grid grid-cols-1 gap-4 md:grid-cols-12">
        <div className="md:col-span-3" data-testid="risk-hierarchy-col-tree">
          <OrgTreePanel
            units={orgUnitsQ.data}
            state={{
              isLoading: orgUnitsQ.isLoading,
              isError: orgUnitsQ.isError,
              error: orgUnitsQ.error,
              refetch: () => void orgUnitsQ.refetch(),
            }}
          />
        </div>
        <div className="md:col-span-5" data-testid="risk-hierarchy-col-heatmap">
          <ThemeHeatmapPanel
            orgUnits={orgUnitsQ.data}
            themes={themesQ.data}
            rules={rulesQ.data}
            orgState={{
              isLoading: orgUnitsQ.isLoading,
              isError: orgUnitsQ.isError,
              error: orgUnitsQ.error,
              refetch: () => void orgUnitsQ.refetch(),
            }}
            themeState={{
              isLoading: themesQ.isLoading,
              isError: themesQ.isError,
              error: themesQ.error,
              refetch: () => void themesQ.refetch(),
            }}
          />
        </div>
        <div
          className="md:col-span-4"
          data-testid="risk-hierarchy-col-timeline"
        >
          <DecisionTimelinePanel
            decisions={decisionsQ.data}
            overdueIds={overdueIds}
            filters={filters}
            onFiltersChange={onFiltersChange}
            state={{
              isLoading: decisionsQ.isLoading,
              isError: decisionsQ.isError,
              error: decisionsQ.error,
              refetch: () => void decisionsQ.refetch(),
            }}
          />
        </div>
      </div>
    </div>
  );
}

export default function RiskHierarchyPage() {
  // useSearchParams requires a Suspense boundary in the App Router.
  return (
    <Suspense
      fallback={
        <div
          className="p-6 text-sm text-muted-foreground"
          data-testid="risk-hierarchy-loading"
        >
          Loading risk hierarchy…
        </div>
      }
    >
      <HierarchyView />
    </Suspense>
  );
}
