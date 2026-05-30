"use client";

// Slice 097 — per-metric detail page (`/dashboards/metrics/[id]`).
//
// AC-9/10/11/12:
//   * Metric definition + immediate parents/children (GET /v1/metrics/{id})
//   * Line chart of observation series with target/warning/critical
//     horizontal overlays
//   * Admin-only "Submit value" modal (manual_input + external_integration)
//   * Audit-trail panel of recent manual inputs (filter observations
//     where source LIKE "manual:%")

import { useQuery } from "@tanstack/react-query";
import Link from "next/link";
import { use } from "react";

import { LineChart } from "@/components/dashboards-metrics/line-chart";
import { ManualInputModal } from "@/components/dashboards-metrics/manual-input-modal";
import { ThresholdBadge } from "@/components/dashboards-metrics/threshold-badge";
import {
  formatRelative,
  formatValue,
  parseValue,
} from "@/components/dashboards-metrics/format";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import {
  fetchMetric,
  fetchObservations,
  fetchTarget,
  type Observation,
} from "@/lib/api/metrics";
import { getSessionMe } from "@/lib/api/board";

export default function MetricDetailPage({
  params,
}: {
  params: Promise<{ id: string }>;
}) {
  const { id } = use(params);

  const detailQuery = useQuery({
    queryKey: ["metric-detail", id],
    queryFn: () => fetchMetric(id),
    staleTime: 60_000,
  });
  const obsQuery = useQuery({
    queryKey: ["metric-observations", id, "90d"],
    queryFn: () =>
      fetchObservations(id, {
        since: new Date(Date.now() - 90 * 24 * 60 * 60 * 1000).toISOString(),
        limit: 500,
      }),
    staleTime: 30_000,
  });
  const targetQuery = useQuery({
    queryKey: ["metric-target", id],
    queryFn: () => fetchTarget(id),
    staleTime: 60_000,
  });
  // Admin gate for the manual-input modal trigger (decision D3) —
  // mirrors slice 043's approve-button gate.
  const meQuery = useQuery({
    queryKey: ["session-me"],
    queryFn: getSessionMe,
    staleTime: 60_000,
  });
  const canSubmit = meQuery.data?.is_admin === true;

  if (detailQuery.isLoading) {
    return (
      <div className="space-y-4">
        <Skeleton className="h-8 w-72" />
        <Skeleton className="h-40 w-full" />
        <Skeleton className="h-40 w-full" />
      </div>
    );
  }
  if (detailQuery.isError || !detailQuery.data) {
    return (
      <Alert variant="destructive" data-testid="metric-detail-error">
        <AlertTitle>Could not load this metric</AlertTitle>
        <AlertDescription>
          {(detailQuery.error as Error)?.message ?? "Unknown error"}
        </AlertDescription>
      </Alert>
    );
  }

  const detail = detailQuery.data;
  const metric = detail.metric;
  const observations = obsQuery.data?.observations ?? [];
  const target = targetQuery.data ?? null;
  const latest = latestValue(observations);

  const manualInputs = observations.filter(
    (o) => o.source?.startsWith("manual:"),
  );

  const supportsManualInput =
    metric.compute_strategy === "manual_input" ||
    metric.compute_strategy === "external_integration";

  return (
    <div className="space-y-6" data-testid="metric-detail">
      <header className="flex flex-wrap items-baseline justify-between gap-4">
        <div>
          <Link
            href="/dashboards/metrics"
            className="text-sm text-muted-foreground hover:underline"
          >
            ← All metrics
          </Link>
          <h1 className="mt-1 text-2xl font-semibold tracking-tight">
            {metric.name}
          </h1>
          <p className="mt-0.5 text-sm text-muted-foreground">
            {metric.description}
          </p>
          <div className="mt-2 flex flex-wrap items-center gap-2">
            <Badge variant="outline">{metric.level}</Badge>
            <Badge variant="outline">{metric.category}</Badge>
            <Badge variant="outline">{metric.cadence}</Badge>
            <Badge variant="outline">{metric.compute_strategy}</Badge>
          </div>
        </div>
        <div className="flex items-center gap-3">
          <ThresholdBadge
            value={latest}
            target={target}
            testid="metric-detail-badge"
          />
          {supportsManualInput && canSubmit ? (
            <ManualInputModal
              metricID={metric.id}
              metricName={metric.name}
              unit={metric.unit}
            />
          ) : null}
        </div>
      </header>

      <div className="grid grid-cols-1 gap-4 lg:grid-cols-3">
        <Card
          size="sm"
          className="lg:col-span-2"
          data-testid="metric-detail-chart"
        >
          <CardHeader>
            <CardTitle>Observations</CardTitle>
            <CardDescription>
              {latest
                ? `Latest ${formatValue(parseValue(latest), metric.unit)}`
                : "No observations in window"}
              {target?.target_value
                ? ` · target ${formatValue(
                    parseValue(target.target_value),
                    metric.unit,
                  )}`
                : ""}
            </CardDescription>
          </CardHeader>
          <CardContent className="px-4">
            {obsQuery.isLoading ? (
              <Skeleton className="h-[200px] w-full" />
            ) : (
              <LineChart
                observations={observations}
                target={target}
                testid="metric-detail-chart-svg"
              />
            )}
          </CardContent>
        </Card>

        <Card size="sm" data-testid="metric-detail-target">
          <CardHeader>
            <CardTitle>Target</CardTitle>
            <CardDescription>
              How the threshold badge is computed.
            </CardDescription>
          </CardHeader>
          <CardContent className="space-y-1.5 px-4 text-sm">
            {targetQuery.isLoading ? (
              <Skeleton className="h-24 w-full" />
            ) : target ? (
              <>
                <p>
                  <span className="text-muted-foreground">Target:</span>{" "}
                  {formatValue(parseValue(target.target_value), metric.unit)}
                </p>
                <p>
                  <span className="text-muted-foreground">Warning at:</span>{" "}
                  {formatValue(
                    parseValue(target.warning_threshold),
                    metric.unit,
                  )}
                </p>
                <p>
                  <span className="text-muted-foreground">Critical at:</span>{" "}
                  {formatValue(
                    parseValue(target.critical_threshold),
                    metric.unit,
                  )}
                </p>
                <p>
                  <span className="text-muted-foreground">Direction:</span>{" "}
                  <code className="font-mono">{target.direction}</code>
                </p>
                {target.notes ? (
                  <p className="text-xs text-muted-foreground">
                    {target.notes}
                  </p>
                ) : null}
              </>
            ) : (
              <p className="text-muted-foreground">
                No target set. The threshold badge defaults to green until a
                target is configured.
              </p>
            )}
          </CardContent>
        </Card>
      </div>

      <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
        <RelativesPanel
          title="Parents"
          metrics={detail.parents}
          empty="Top-of-cascade metric — no parents."
          testid="metric-detail-parents"
        />
        <RelativesPanel
          title="Children"
          metrics={detail.children}
          empty="Leaf metric — no children."
          testid="metric-detail-children"
        />
      </div>

      <Card size="sm" data-testid="metric-detail-audit-trail">
        <CardHeader>
          <CardTitle>Manual input audit trail</CardTitle>
          <CardDescription>
            Recent observations recorded via{" "}
            <code className="font-mono">
              POST /v1/metrics/{metric.id}/inputs
            </code>
            . Filtered to{" "}
            <code className="font-mono">source LIKE manual:%</code>.
          </CardDescription>
        </CardHeader>
        <CardContent className="px-4">
          {manualInputs.length === 0 ? (
            <p className="text-sm text-muted-foreground">
              No manual inputs in the current window.
            </p>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Value</TableHead>
                  <TableHead>Observed at</TableHead>
                  <TableHead>Source</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {manualInputs.map((o) => (
                  <TableRow key={o.id} data-testid={`audit-row-${o.id}`}>
                    <TableCell>
                      {formatValue(parseValue(o.numeric_value), metric.unit)}
                    </TableCell>
                    <TableCell>
                      {formatRelative(o.observed_at)}{" "}
                      <span className="text-xs text-muted-foreground">
                        ({o.observed_at})
                      </span>
                    </TableCell>
                    <TableCell>
                      <code className="font-mono text-xs">{o.source}</code>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
          {supportsManualInput && !canSubmit ? (
            <p className="mt-3 text-xs text-muted-foreground">
              Submitting a value requires the <code>admin</code> role.{" "}
              <Button
                variant="link"
                size="xs"
                disabled
                data-testid="manual-input-admin-only"
              >
                Submit value (admin only)
              </Button>
            </p>
          ) : null}
        </CardContent>
      </Card>
    </div>
  );
}

function latestValue(observations: Observation[]): string | undefined {
  if (observations.length === 0) return undefined;
  // Upstream returns DESC by observed_at; the first row is newest.
  return observations[0].numeric_value;
}

function RelativesPanel({
  title,
  metrics,
  empty,
  testid,
}: {
  title: string;
  metrics: { id: string; name: string; level: string }[];
  empty: string;
  testid: string;
}) {
  return (
    <Card size="sm" data-testid={testid}>
      <CardHeader>
        <CardTitle>{title}</CardTitle>
      </CardHeader>
      <CardContent className="px-4">
        {metrics.length === 0 ? (
          <p className="text-sm text-muted-foreground">{empty}</p>
        ) : (
          <ul className="space-y-1 text-sm">
            {metrics.map((m) => (
              <li key={m.id}>
                <Link
                  href={`/dashboards/metrics/${encodeURIComponent(m.id)}`}
                  className="hover:underline"
                >
                  {m.name}
                </Link>{" "}
                <span className="text-xs text-muted-foreground">
                  ({m.level})
                </span>
              </li>
            ))}
          </ul>
        )}
      </CardContent>
    </Card>
  );
}
