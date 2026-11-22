"use client";

// #2104 — the History section on the risk page: every field change,
// category move and placement move, newest first, with who made it, when,
// and the before and after values. Read from GET /api/risks/{id}/audit-log.
//
// #2138: who made a change shows as a name through <Person> ("System" for a
// `system:` actor), and an id-valued field (owner, approver, control, org
// unit, child risk) shows the person's name or the item's title. The
// control, org unit and risk lists are fetched only when an entry names
// one; an id no list knows reads "Unknown control", "Unknown org unit" or
// "Unknown risk", never the id.

import { useQuery } from "@tanstack/react-query";

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Person } from "@/components/ui/person";
import { Skeleton } from "@/components/ui/skeleton";
import { fetchTenantControlsList } from "@/lib/api/controls-list";
import { fetchHierarchyOrgUnits } from "@/lib/api/risk-hierarchy";
import { fetchRiskAuditLog, fetchRisksListWithRollUps } from "@/lib/api/risks";

import {
  historyActionLabel,
  historyChanges,
  type HistoryChange,
} from "./history-format";

export function RiskHistory({ riskId }: { riskId: string }) {
  const { data, isLoading, error } = useQuery({
    queryKey: ["risk-audit-log", riskId],
    queryFn: () => fetchRiskAuditLog(riskId),
  });

  const changes = (data?.entries ?? []).map((e) => historyChanges(e));
  const needs = (ref: HistoryChange["ref"]) =>
    changes.some((cs) => cs.some((c) => c.ref === ref));

  // The same query the Linked controls section runs, so it is shared.
  const controls = useQuery({
    queryKey: ["entity-picker", "controls"],
    queryFn: fetchTenantControlsList,
    staleTime: 30_000,
    enabled: needs("control"),
  });
  const orgUnits = useQuery({
    queryKey: ["risks", "org-units"],
    queryFn: fetchHierarchyOrgUnits,
    staleTime: 60_000,
    enabled: needs("org_unit"),
  });

  // #2958: a child risk named by a hierarchy link or unlink.
  const risks = useQuery({
    queryKey: ["risk-history", "risks"],
    queryFn: fetchRisksListWithRollUps,
    staleTime: 30_000,
    enabled: needs("risk"),
  });

  const titleOf = (ref: HistoryChange["ref"], v: string) => {
    if (v === "none") return v;
    if (ref === "risk") {
      const r = (risks.data?.risks ?? []).find((x) => x.id === v);
      return r?.title ?? (risks.isLoading ? "…" : "Unknown risk");
    }
    if (ref === "control") {
      const c = (controls.data ?? []).find((x) => x.id === v);
      return c?.title ?? (controls.isLoading ? "…" : "Unknown control");
    }
    const u = (orgUnits.data ?? []).find((x) => x.id === v);
    return u?.name ?? (orgUnits.isLoading ? "…" : "Unknown org unit");
  };

  const value = (c: HistoryChange, v: string) =>
    c.ref === "person" && v !== "none" ? (
      <Person id={v} />
    ) : c.ref ? (
      titleOf(c.ref, v)
    ) : (
      v
    );

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
            {data.entries.map((e, i) => (
              <li
                key={e.id}
                className="border-l-2 border-muted pl-4"
                data-testid="risk-history-entry"
              >
                <div className="flex flex-wrap items-baseline gap-x-2 text-sm">
                  <span className="font-medium">
                    {historyActionLabel(e.action)}
                  </span>
                  <span
                    className="text-muted-foreground"
                    data-testid="risk-history-actor"
                  >
                    by <Person id={e.actor} />
                  </span>
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
                  {changes[i].map((c) => (
                    <li key={c.field} data-testid="risk-history-change">
                      <span className="text-muted-foreground">{c.field}: </span>
                      <span className="line-through decoration-muted-foreground/60">
                        {value(c, c.before)}
                      </span>
                      {" → "}
                      <span>{value(c, c.after)}</span>
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
