"use client";

// Slice 097 — one board-level metric card on the dashboard summary.
//
// Renders: name, latest value (formatted by unit), target (if set), and
// the 90-day sparkline. Click expands the cascade-tree explorer
// underneath (state is owned by the parent grid). The card itself is a
// shadcn Card; the only bespoke piece is the small inline sparkline.

import { useQuery } from "@tanstack/react-query";
import Link from "next/link";

import { Badge } from "@/components/ui/badge";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";
import { fetchObservations, fetchTarget, type Metric } from "@/lib/api/metrics";

import { formatValue, parseValue } from "./format";
import { Sparkline } from "./sparkline";
import { ThresholdBadge } from "./threshold-badge";

export function BoardMetricCard({
  metric,
  expanded,
  onToggle,
}: {
  metric: Metric;
  expanded: boolean;
  onToggle: () => void;
}) {
  const obsQuery = useQuery({
    queryKey: ["metric-observations", metric.id, "90d"],
    queryFn: () =>
      fetchObservations(metric.id, {
        since: new Date(Date.now() - 90 * 24 * 60 * 60 * 1000).toISOString(),
        limit: 200,
      }),
    staleTime: 30_000,
  });

  const targetQuery = useQuery({
    queryKey: ["metric-target", metric.id],
    queryFn: () => fetchTarget(metric.id),
    staleTime: 60_000,
  });

  const latest = latestObservation(obsQuery.data?.observations);
  const target = targetQuery.data ?? null;

  return (
    <Card
      data-testid={`board-metric-${metric.id}`}
      data-expanded={expanded ? "true" : "false"}
      size="sm"
    >
      <CardHeader>
        <CardTitle>
          <Link
            href={`/dashboards/metrics/${encodeURIComponent(metric.id)}`}
            className="hover:underline"
            data-testid={`board-metric-${metric.id}-detail-link`}
          >
            {metric.name}
          </Link>
        </CardTitle>
        <CardDescription>{metric.description}</CardDescription>
      </CardHeader>
      <CardContent className="space-y-3 px-4">
        {obsQuery.isLoading ? (
          <Skeleton className="h-14 w-full" />
        ) : (
          <div className="flex items-center justify-between gap-3">
            <div className="flex flex-col">
              <span
                className="text-2xl font-semibold tracking-tight"
                data-testid={`board-metric-${metric.id}-value`}
              >
                {formatValue(latest?.value, metric.unit)}
              </span>
              {target && target.target_value ? (
                <span className="text-xs text-muted-foreground">
                  target{" "}
                  {formatValue(parseValue(target.target_value), metric.unit)}
                </span>
              ) : (
                <span className="text-xs text-muted-foreground">
                  no target set
                </span>
              )}
            </div>
            <Sparkline
              observations={obsQuery.data?.observations ?? []}
              testid={`board-metric-${metric.id}-sparkline`}
            />
          </div>
        )}
        <div className="flex items-center justify-between">
          <ThresholdBadge
            value={latest?.raw}
            target={target}
            testid={`board-metric-${metric.id}-badge`}
          />
          {!obsQuery.isLoading && (obsQuery.data?.count ?? 0) === 0 ? (
            <Badge
              variant="ghost"
              data-testid={`board-metric-${metric.id}-empty`}
            >
              No data yet — the 15-min evaluator hasn&apos;t run, or this is a
              manual_input metric
            </Badge>
          ) : (
            <button
              type="button"
              onClick={onToggle}
              className="text-xs font-medium text-muted-foreground hover:text-foreground"
              data-testid={`board-metric-${metric.id}-toggle`}
            >
              {expanded ? "Hide cascade" : "Show cascade"}
            </button>
          )}
        </div>
      </CardContent>
    </Card>
  );
}

type Latest = { raw: string; value: number | undefined } | undefined;

function latestObservation(observations?: { numeric_value: string }[]): Latest {
  if (!observations || observations.length === 0) return undefined;
  // Upstream sort is `ORDER BY observed_at DESC` per the slice-076
  // ListMetricObservations query; the first row is the newest.
  const raw = observations[0].numeric_value;
  return { raw, value: parseValue(raw) };
}
