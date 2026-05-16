"use client";

// Slice 097 — cascade-tree explorer (AC-5/6/7/8).
//
// Fetches `GET /v1/metrics/cascade?level=board&depth=3` once, then
// reassembles the flat node list into a parent → child tree per
// `reassembleCascade`. Each node is rendered with an indent-and-rule
// guide (decision D1) and clicks navigate to the per-metric detail
// page. If the upstream sets `X-Cascade-Truncated: true` (via the
// `truncated: true` field in the JSON envelope), a "depth limit
// reached" hint surfaces beneath the tree.

import { useQuery } from "@tanstack/react-query";
import Link from "next/link";

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Card, CardContent } from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";
import {
  type CascadeTreeNode,
  fetchCascade,
  fetchMetricsCatalog,
  type Metric,
  reassembleCascade,
} from "@/lib/api/metrics";

export function CascadeTree({
  rootMetricID,
  testid,
}: {
  rootMetricID?: string;
  testid?: string;
}) {
  const cascadeQuery = useQuery({
    queryKey: ["metrics-cascade", "board", 3],
    queryFn: () => fetchCascade("board", 3),
    staleTime: 60_000,
  });

  // Hydrate every metric_id in the tree to its catalog row so we can
  // render `name` and `unit` per node. This is one batch call rather
  // than per-node detail fetches.
  const catalogQuery = useQuery({
    queryKey: ["metrics-catalog", "all"],
    queryFn: () => fetchMetricsCatalog(),
    staleTime: 5 * 60_000,
  });

  if (cascadeQuery.isLoading || catalogQuery.isLoading) {
    return (
      <Card size="sm" data-testid={testid}>
        <CardContent className="px-4">
          <Skeleton className="h-32 w-full" />
        </CardContent>
      </Card>
    );
  }
  if (cascadeQuery.isError || catalogQuery.isError) {
    return (
      <Card size="sm" data-testid={testid}>
        <CardContent className="px-4">
          <Alert variant="destructive">
            <AlertTitle>Could not load the cascade</AlertTitle>
            <AlertDescription>
              {(cascadeQuery.error ?? catalogQuery.error)?.toString() ??
                "Unknown error"}
            </AlertDescription>
          </Alert>
        </CardContent>
      </Card>
    );
  }

  const cascade = cascadeQuery.data!;
  const catalog = catalogQuery.data!;
  const byID = new Map(catalog.map((m) => [m.id, m]));

  let roots = reassembleCascade(cascade.nodes);
  // If a specific root is passed in, scope the tree to that subtree.
  if (rootMetricID) {
    const sub = findSubtree(roots, rootMetricID);
    roots = sub ? [sub] : [];
  }

  if (roots.length === 0) {
    return (
      <Card size="sm" data-testid={testid}>
        <CardContent className="px-4 text-sm text-muted-foreground">
          No descendants — this metric has no children in the cascade.
        </CardContent>
      </Card>
    );
  }

  return (
    <Card size="sm" data-testid={testid}>
      <CardContent className="space-y-1 px-4">
        {roots.map((root) => (
          <TreeRow key={root.metric_id} node={root} byID={byID} />
        ))}
        {cascade.truncated ? (
          <Alert data-testid={`${testid ?? "cascade-tree"}-truncated`}>
            <AlertTitle>Depth limit reached</AlertTitle>
            <AlertDescription>
              The cascade is deeper than the {cascade.depth}-level cap. Open a
              specific metric to walk its subtree.
            </AlertDescription>
          </Alert>
        ) : null}
      </CardContent>
    </Card>
  );
}

function TreeRow({
  node,
  byID,
}: {
  node: CascadeTreeNode;
  byID: Map<string, Metric>;
}) {
  const metric = byID.get(node.metric_id);
  return (
    <div
      data-testid={`cascade-row-${node.metric_id}`}
      data-depth={node.depth}
      style={{ marginLeft: `${node.depth * 20}px` }}
      className="border-l border-border pl-3"
    >
      <Link
        href={`/dashboards/metrics/${encodeURIComponent(node.metric_id)}`}
        className="flex items-baseline gap-2 py-1 text-sm hover:underline"
      >
        <span className="font-medium">{metric?.name ?? node.metric_id}</span>
        {metric?.level ? (
          <span className="text-xs text-muted-foreground">
            ({metric.level})
          </span>
        ) : null}
      </Link>
      {node.children.map((c) => (
        <TreeRow key={`${c.metric_id}-${c.depth}`} node={c} byID={byID} />
      ))}
    </div>
  );
}

function findSubtree(
  roots: CascadeTreeNode[],
  id: string,
): CascadeTreeNode | null {
  for (const r of roots) {
    if (r.metric_id === id) return r;
    const inner = findSubtree(r.children, id);
    if (inner) return inner;
  }
  return null;
}
