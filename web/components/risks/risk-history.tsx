"use client";

// #2104 — the History section on the risk page: every field change,
// category move and placement move, newest first, with who made it, when,
// and the before and after values. Read from GET /api/risks/{id}/audit-log.

import { useQuery } from "@tanstack/react-query";

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";
import { fetchRiskAuditLog } from "@/lib/api/risks";

import { historyActionLabel, historyChanges } from "./history-format";

export function RiskHistory({ riskId }: { riskId: string }) {
  const { data, isLoading, error } = useQuery({
    queryKey: ["risk-audit-log", riskId],
    queryFn: () => fetchRiskAuditLog(riskId),
  });

  return (
    <Card data-testid="risk-history">
      <CardHeader className="border-b">
        <CardTitle>History</CardTitle>
      </CardHeader>
      <CardContent>
        {isLoading ? <Skeleton className="h-16 w-full" /> : null}
        {error ? (
          <Alert variant="destructive" data-testid="risk-history-error">
            <AlertTitle>Could not load history</AlertTitle>
            <AlertDescription>{(error as Error).message}</AlertDescription>
          </Alert>
        ) : null}
        {data && data.entries.length === 0 ? (
          <p
            className="text-sm text-muted-foreground"
            data-testid="risk-history-empty"
          >
            No changes since this risk was created.
          </p>
        ) : null}
        {data?.truncated ? (
          <p
            className="mb-3 text-sm text-muted-foreground"
            data-testid="risk-history-truncated"
          >
            Showing the latest {data.entries.length} changes. Older changes are
            kept but not listed here.
          </p>
        ) : null}
        {data && data.entries.length > 0 ? (
          <ol className="space-y-3">
            {data.entries.map((e) => (
              <li
                key={e.id}
                className="border-l-2 border-muted pl-4"
                data-testid="risk-history-entry"
              >
                <div className="flex flex-wrap items-baseline gap-x-2 text-sm">
                  <span className="font-medium">
                    {historyActionLabel(e.action)}
                  </span>
                  <span className="text-muted-foreground">by {e.actor}</span>
                  <time
                    className="font-mono text-xs text-muted-foreground"
                    dateTime={e.created_at}
                  >
                    {new Date(e.created_at)
                      .toISOString()
                      .replace("T", " ")
                      .slice(0, 16)}{" "}
                    UTC
                  </time>
                </div>
                <ul className="mt-1 space-y-0.5 text-sm">
                  {historyChanges(e).map((c) => (
                    <li key={c.field} data-testid="risk-history-change">
                      <span className="text-muted-foreground">{c.field}: </span>
                      <span className="line-through decoration-muted-foreground/60">
                        {c.before}
                      </span>
                      {" → "}
                      <span>{c.after}</span>
                    </li>
                  ))}
                </ul>
              </li>
            ))}
          </ol>
        ) : null}
      </CardContent>
    </Card>
  );
}
