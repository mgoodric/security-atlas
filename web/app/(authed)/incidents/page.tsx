"use client";

import { useQueries, useQuery } from "@tanstack/react-query";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { Suspense, useMemo } from "react";

import {
  EmptyState,
  ListLoadingSkeleton,
  ListPage,
  ListTable,
  type ListColumn,
} from "@/components/list";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { buttonVariants } from "@/components/ui/button";
import {
  fetchIncidentsList,
  fetchIncidentDetail,
  linkIDs,
  type Incident,
  type IncidentsListResponse,
} from "@/lib/api/incidents";

import {
  affectedSystemsSummary,
  dateTimeLabel,
  SEVERITY_LABELS,
  STATUS_LABELS,
} from "./display";

function IncidentsPageInner() {
  const router = useRouter();
  const incidentsQ = useQuery<IncidentsListResponse>({
    queryKey: ["incidents", "list"],
    queryFn: fetchIncidentsList,
  });

  const rows = useMemo(
    () => incidentsQ.data?.incidents ?? [],
    [incidentsQ.data],
  );
  const linkQueries = useQueries({
    queries: rows.map((row) => ({
      queryKey: ["incidents", "detail-links", row.id],
      queryFn: () => fetchIncidentDetail(row.id),
      staleTime: 30_000,
    })),
  });
  const linkCountByID = useMemo(() => {
    const out = new Map<string, string>();
    rows.forEach((row, index) => {
      const detail = linkQueries[index]?.data?.incident;
      if (!detail) {
        out.set(row.id, "-");
        return;
      }
      out.set(row.id, linkCountLabel(detail.links));
    });
    return out;
  }, [linkQueries, rows]);

  const columns: ListColumn<Incident>[] = [
    {
      id: "title",
      header: "Incident",
      cell: (row) => (
        <div className="min-w-52">
          <div className="font-medium" data-testid="incidents-row-title">
            {row.title}
          </div>
          <div className="font-mono text-[11px] text-muted-foreground">
            {row.id.slice(0, 8)}...
          </div>
        </div>
      ),
    },
    {
      id: "status",
      header: "Status",
      cell: (row) => (
        <Badge
          variant={statusVariant(row.status)}
          data-testid="incidents-row-status"
        >
          {STATUS_LABELS[row.status]}
        </Badge>
      ),
    },
    {
      id: "severity",
      header: "Severity",
      cell: (row) => (
        <Badge
          variant={severityVariant(row.severity)}
          data-testid="incidents-row-severity"
        >
          {SEVERITY_LABELS[row.severity]}
        </Badge>
      ),
    },
    {
      id: "detected_at",
      header: "Detected",
      cell: (row) => (
        <span
          className="font-mono text-xs text-muted-foreground"
          data-testid="incidents-row-detected-at"
        >
          {dateTimeLabel(row.detected_at)}
        </span>
      ),
    },
    {
      id: "affected_systems",
      header: "Affected systems",
      cell: (row) => (
        <span className="text-xs" data-testid="incidents-row-affected-systems">
          {affectedSystemsSummary(row.affected_systems)}
        </span>
      ),
    },
    {
      id: "links",
      header: "Linked",
      align: "right",
      cell: (row) => {
        const counts = linkCountByID.get(row.id) ?? "-";
        return (
          <span
            className="font-mono text-xs text-muted-foreground"
            data-testid="incidents-row-linked-counts"
          >
            {counts}
          </span>
        );
      },
    },
  ];

  const actions = (
    <Link
      href="/incidents/new"
      className={buttonVariants({ size: "sm" })}
      data-testid="incidents-new-link"
    >
      Log incident
    </Link>
  );

  if (incidentsQ.isLoading) {
    return (
      <ListPage
        title="Incidents"
        subtitle="Operational security incidents, lifecycle state, linked evidence, and postmortems"
        actions={actions}
      >
        <ListLoadingSkeleton />
      </ListPage>
    );
  }

  if (incidentsQ.isError) {
    return (
      <ListPage
        title="Incidents"
        subtitle="Operational security incidents, lifecycle state, linked evidence, and postmortems"
        actions={actions}
      >
        <Alert variant="destructive" data-testid="incidents-load-error">
          <AlertTitle>Could not load incidents</AlertTitle>
          <AlertDescription>
            {(incidentsQ.error as Error).message}
          </AlertDescription>
        </Alert>
      </ListPage>
    );
  }

  return (
    <ListPage
      title="Incidents"
      subtitle="Operational security incidents, lifecycle state, linked evidence, and postmortems"
      actions={actions}
      titleAdornment={
        <span
          className="text-sm text-muted-foreground"
          aria-label="Incident count"
        >
          {rows.length} open/closed
        </span>
      }
    >
      <ListTable
        columns={columns}
        rows={rows}
        rowKey={(row) => row.id}
        onRowClick={(row) =>
          router.push(`/incidents/${encodeURIComponent(row.id)}`)
        }
        mobileMode="cards"
        emptyFallback={
          <EmptyState
            title="No incidents logged yet"
            body="Security incidents logged through the register will appear here with status, severity, affected systems, links, and postmortem state."
          />
        }
      />
    </ListPage>
  );
}

function linkCountLabel(links: Parameters<typeof linkIDs>[0]): string {
  const controls = linkIDs(links, "controls").length;
  const risks = linkIDs(links, "risks").length;
  const vendors = linkIDs(links, "vendors").length;
  const evidence = linkIDs(links, "evidence").length;
  return `${controls}c ${risks}r ${vendors}v ${evidence}e`;
}

function statusVariant(
  status: Incident["status"],
): "secondary" | "warning" | "progress" | "pass" {
  switch (status) {
    case "detected":
      return "warning";
    case "triaged":
    case "contained":
    case "resolved":
      return "progress";
    case "closed":
      return "pass";
    default:
      return "secondary";
  }
}

function severityVariant(
  severity: Incident["severity"],
): "secondary" | "warning" | "critical" | "destructive" {
  switch (severity) {
    case "p0":
      return "destructive";
    case "p1":
      return "critical";
    case "p2":
      return "warning";
    case "p3":
      return "secondary";
  }
}

export default function IncidentsPage() {
  return (
    <Suspense fallback={<ListLoadingSkeleton />}>
      <IncidentsPageInner />
    </Suspense>
  );
}
