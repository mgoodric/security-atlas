"use client";

// #1795 — what a revision in review changes against the activated revision:
// the applicability decisions added, removed, re-decided or re-justified,
// and the predicate before and after. The approver reads this instead of
// the whole decision list.

import { useQuery } from "@tanstack/react-query";

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
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
  ApplicabilityDiff,
  CHANGE_LABELS,
  describeSide,
  summarizeChanges,
} from "@/lib/api/applicability";
import { APIError } from "@/lib/api/base";
import type { FrameworkScope } from "@/lib/api/framework-scopes";

async function fetchDiff(id: string): Promise<ApplicabilityDiff> {
  const res = await fetch(
    `/api/framework-scopes/${encodeURIComponent(id)}/applicability/diff`,
  );
  if (!res.ok) {
    throw new APIError(res.status, `${res.status} ${res.statusText}`);
  }
  return (await res.json()) as ApplicabilityDiff;
}

export function ReviewDiff({ scope }: { scope: FrameworkScope }) {
  const q = useQuery({
    queryKey: ["framework-scope-diff", scope.id, scope.updated_at],
    queryFn: () => fetchDiff(scope.id),
  });

  return (
    <Card data-testid={`review-diff-${scope.id}`}>
      <CardHeader>
        <CardTitle>Changes in review: {scope.name}</CardTitle>
        <CardDescription>
          {q.data?.baseline
            ? `Compared with the activated revision "${q.data.baseline.name}".`
            : "No revision is activated yet, so every decision is new."}
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-4">
        {q.isLoading && <Skeleton className="h-24 w-full" />}
        {q.error && (
          <Alert variant="destructive">
            <AlertTitle>Could not load the changes</AlertTitle>
            <AlertDescription>{(q.error as Error).message}</AlertDescription>
          </Alert>
        )}
        {q.data && <DiffBody diff={q.data} />}
      </CardContent>
    </Card>
  );
}

function DiffBody({ diff }: { diff: ApplicabilityDiff }) {
  const counts = summarizeChanges(diff.changes);
  return (
    <>
      <div
        className="flex flex-wrap gap-2 text-xs"
        data-testid="review-diff-counts"
      >
        {(Object.keys(counts) as (keyof typeof counts)[]).map((k) => (
          <Badge
            key={k}
            variant="outline"
            data-testid={`review-diff-count-${k}`}
          >
            {CHANGE_LABELS[k]}: {counts[k]}
          </Badge>
        ))}
      </div>

      <div data-testid="review-diff-predicate">
        <h3 className="mb-1 text-sm font-medium">Predicate</h3>
        {diff.predicate.changed ? (
          <div className="grid gap-2 md:grid-cols-2">
            <PredicateBlock label="Activated" value={diff.predicate.before} />
            <PredicateBlock label="In review" value={diff.predicate.after} />
          </div>
        ) : (
          <p className="text-sm text-muted-foreground">
            {diff.baseline
              ? "Unchanged."
              : "No activated predicate to compare."}
          </p>
        )}
      </div>

      {diff.changes.length === 0 ? (
        <p className="text-sm text-muted-foreground">
          No applicability decisions changed.
        </p>
      ) : (
        <Table data-testid="review-diff-changes">
          <TableHeader>
            <TableRow>
              <TableHead className="w-28">Requirement</TableHead>
              <TableHead className="w-44">Change</TableHead>
              <TableHead>Before</TableHead>
              <TableHead>After</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {diff.changes.map((c) => (
              <TableRow
                key={c.requirement_id}
                data-testid={`review-diff-row-${c.code}`}
                data-change={c.change}
              >
                <TableCell className="font-mono text-xs" title={c.title}>
                  {c.code}
                </TableCell>
                <TableCell>
                  <Badge variant="secondary">{CHANGE_LABELS[c.change]}</Badge>
                </TableCell>
                <TableCell className="text-xs text-muted-foreground">
                  {describeSide(c.before)}
                </TableCell>
                <TableCell className="text-xs">
                  {describeSide(c.after)}
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
    </>
  );
}

function PredicateBlock({ label, value }: { label: string; value: unknown }) {
  return (
    <div>
      <div className="mb-1 text-xs text-muted-foreground">{label}</div>
      <pre className="overflow-x-auto rounded border bg-muted/40 p-2 font-mono text-xs">
        {JSON.stringify(value, null, 2)}
      </pre>
    </div>
  );
}
