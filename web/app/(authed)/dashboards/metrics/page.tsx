"use client";

// Slice 097 — metrics dashboard route (`/dashboards/metrics`).
//
// AC-1/2/3/4: board-level summary panel + cascade explorer.
//   * `BoardMetricCard` (per metric): name, latest value, target,
//     90-day sparkline, color-coded threshold badge, click-to-expand
//     cascade, empty-state copy for zero observations.
//   * `CascadeTree` (per metric, expanded): the descendants of the
//     selected board metric reassembled into a vertical indented tree
//     per decision D1.
//
// Data fetching: TanStack Query per panel. The page is rendered as a
// Client Component because the cascade-toggle is per-card state. RSC
// rendering of the metric LIST would still work, but TanStack's per-
// card freshness window (each card refetches at its own staleTime)
// matters more for the morning-glance use case than the marginal SSR
// gain on the catalog list.
//
// AI-assist boundary (canvas §AI-assist): this page renders values —
// it never auto-generates narrative interpretation. The threshold
// badge is mechanical (pure function over numbers); copy is templated.
// Anti-criterion P0-A3 is honored.

import { useQuery } from "@tanstack/react-query";
import { useState } from "react";

import { BoardMetricCard } from "@/components/dashboards-metrics/board-metric-card";
import { CascadeTree } from "@/components/dashboards-metrics/cascade-tree";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Skeleton } from "@/components/ui/skeleton";
import { fetchMetricsCatalog } from "@/lib/api/metrics";

export default function MetricsDashboardPage() {
  const boardQuery = useQuery({
    queryKey: ["metrics-catalog", "board"],
    queryFn: () => fetchMetricsCatalog({ level: "board" }),
    staleTime: 5 * 60_000,
  });

  // Expanded card state — at most one cascade open at a time keeps the
  // page short. (Multi-open is a follow-up if asked.)
  const [expanded, setExpanded] = useState<string | null>(null);

  return (
    <div className="space-y-6" data-testid="metrics-dashboard">
      <header className="flex items-baseline justify-between">
        <div>
          <h1 className="text-2xl font-semibold tracking-tight">Metrics</h1>
          <p className="mt-0.5 text-sm text-muted-foreground">
            Board-level KPIs and the cascading program and team metrics that
            feed them. Click a card to walk its cascade.
          </p>
        </div>
      </header>

      {boardQuery.isLoading ? (
        <div className="grid grid-cols-1 gap-4 md:grid-cols-2 xl:grid-cols-3">
          <Skeleton className="h-44 w-full" />
          <Skeleton className="h-44 w-full" />
          <Skeleton className="h-44 w-full" />
        </div>
      ) : boardQuery.isError ? (
        <Alert variant="destructive" data-testid="metrics-dashboard-error">
          <AlertTitle>Could not load board metrics</AlertTitle>
          <AlertDescription>
            {(boardQuery.error as Error)?.message ?? "Unknown error"}
          </AlertDescription>
        </Alert>
      ) : (boardQuery.data ?? []).length === 0 ? (
        <Alert data-testid="metrics-dashboard-empty">
          <AlertTitle>No board metrics defined</AlertTitle>
          <AlertDescription>
            The catalog does not currently define any board-level metrics. See{" "}
            <code>internal/catalog/metrics/</code> for the seed YAML.
          </AlertDescription>
        </Alert>
      ) : (
        <div className="grid grid-cols-1 gap-4 md:grid-cols-2 xl:grid-cols-3">
          {(boardQuery.data ?? []).map((m) => (
            <div key={m.id} className="space-y-2">
              <BoardMetricCard
                metric={m}
                expanded={expanded === m.id}
                onToggle={() =>
                  setExpanded((cur) => (cur === m.id ? null : m.id))
                }
              />
              {expanded === m.id ? (
                <CascadeTree rootMetricID={m.id} testid={`cascade-${m.id}`} />
              ) : null}
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
