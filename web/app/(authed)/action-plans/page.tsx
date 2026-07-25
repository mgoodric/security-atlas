"use client";

// Slice 384 — /action-plans list view.
//
// Surfaces the tenant-wide ActionPlan register with a status filter
// (AC-22) and a paginated table (title / status / owner / due_date). Mirrors
// the slice-177 /exceptions list shape so the entity list-view stays
// predictable across the app.
//
// Data source: `planWire` in `internal/api/actionplans/handlers.go`, fetched
// via the BFF at `/api/action-plans` which forwards the bearer cookie to
// upstream `/v1/action-plans`. Tenant isolation is enforced by RLS at the DB
// layer (invariant 6); the UI never passes tenant_id.

import { useQuery } from "@tanstack/react-query";
import Link from "next/link";
import { useRouter, useSearchParams } from "next/navigation";
import { Suspense, useMemo } from "react";

import {
  EmptyState,
  FilterPills,
  ListLoadingSkeleton,
  ListPage,
  ListTable,
  type FilterPill,
  type ListColumn,
} from "@/components/list";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { buttonVariants } from "@/components/ui/button";
import {
  ACTION_PLAN_STATUSES,
  fetchActionPlansList,
  type ActionPlan,
  type ActionPlansListResponse,
} from "@/lib/api/action-plans";

import { dateLabel, statusLabel, statusPillClass } from "./status";

const ALL = "__all__";

const STATUS_OPTIONS = [
  { value: ALL, label: "All statuses" },
  ...ACTION_PLAN_STATUSES.map((s) => ({ value: s, label: statusLabel(s) })),
];

function ActionPlansPageInner() {
  const router = useRouter();
  const search = useSearchParams();

  const status = search.get("status") ?? ALL;

  const updateStatus = (value: string) => {
    const sp = new URLSearchParams(search.toString());
    if (value === ALL) sp.delete("status");
    else sp.set("status", value);
    router.replace(`/action-plans?${sp.toString()}`);
  };

  const fetchOpts = useMemo(
    () => (status !== ALL ? { status: status as ActionPlan["status"] } : {}),
    [status],
  );

  const plansQ = useQuery<ActionPlansListResponse>({
    queryKey: ["action-plans", "list", fetchOpts],
    queryFn: () => fetchActionPlansList(fetchOpts),
  });

  const rows: ActionPlan[] = useMemo(
    () => plansQ.data?.action_plans ?? [],
    [plansQ.data],
  );

  const pills: FilterPill[] = [
    { id: "status", label: "Status", value: status, options: STATUS_OPTIONS },
  ];

  const meta = (
    <span>
      Showing <span className="text-foreground font-medium">{rows.length}</span>{" "}
      action plan{rows.length === 1 ? "" : "s"}
    </span>
  );

  const columns: ListColumn<ActionPlan>[] = [
    {
      id: "title",
      header: "Title",
      cell: (row) => (
        <Link
          href={`/action-plans/${encodeURIComponent(row.id)}`}
          className="text-primary hover:underline"
          data-testid="action-plans-row-title"
          onClick={(e) => e.stopPropagation()}
        >
          {row.title}
        </Link>
      ),
    },
    {
      id: "status",
      header: "Status",
      cell: (row) => (
        <span
          className={
            "inline-flex items-center rounded-md px-1.5 py-0.5 text-[11px] font-medium " +
            statusPillClass(row.status)
          }
          data-testid="action-plans-row-status"
        >
          {statusLabel(row.status)}
        </span>
      ),
    },
    {
      id: "owner_id",
      header: "Owner",
      cell: (row) => (
        <span
          className="font-mono text-xs text-muted-foreground"
          title={row.owner_id}
          data-testid="action-plans-row-owner"
        >
          {row.owner_id.slice(0, 8)}…
        </span>
      ),
    },
    {
      id: "due_date",
      header: "Due",
      cell: (row) => (
        <span
          className="font-mono text-xs text-muted-foreground"
          data-testid="action-plans-row-due-date"
        >
          {dateLabel(row.due_date)}
        </span>
      ),
    },
  ];

  const actions = (
    <Link
      href="/action-plans/new"
      className={buttonVariants({ size: "sm" })}
      data-testid="action-plans-new-button"
    >
      New action plan
    </Link>
  );

  const emptyState = (
    <EmptyState
      icon={emptyIcon}
      title={
        status === ALL
          ? "No action plans yet"
          : "No action plans match this filter"
      }
      body={
        status === ALL
          ? "An action plan captures a forward-looking commitment to close a gap — owner, due date, and the risks and controls it touches. Create one when a finding needs a written remediation commitment."
          : "Try widening the status filter."
      }
      cta={
        status === ALL
          ? {
              label: "New action plan",
              onClick: () => router.push("/action-plans/new"),
            }
          : { label: "Clear filter", onClick: () => updateStatus(ALL) }
      }
    />
  );

  const subtitle =
    "Forward-looking remediation commitments · owner, due date, linked risks + controls";

  if (plansQ.isLoading) {
    return (
      <ListPage
        title="Action Plans"
        subtitle={subtitle}
        actions={actions}
        filterRow={
          <FilterPills pills={pills} onChange={() => {}} meta={meta} />
        }
      >
        <ListLoadingSkeleton />
      </ListPage>
    );
  }

  if (plansQ.isError) {
    return (
      <ListPage title="Action Plans" subtitle={subtitle} actions={actions}>
        <Alert variant="destructive" data-testid="action-plans-load-error">
          <AlertTitle>Could not load action plans</AlertTitle>
          <AlertDescription>{(plansQ.error as Error).message}</AlertDescription>
        </Alert>
      </ListPage>
    );
  }

  return (
    <ListPage
      title="Action Plans"
      subtitle={subtitle}
      actions={actions}
      filterRow={
        <FilterPills
          pills={pills}
          onChange={(_id, v) => updateStatus(v)}
          meta={meta}
        />
      }
    >
      <ListTable<ActionPlan>
        columns={columns}
        rows={rows}
        rowKey={(row) => row.id}
        onRowClick={(row) =>
          router.push(`/action-plans/${encodeURIComponent(row.id)}`)
        }
        emptyFallback={emptyState}
      />
    </ListPage>
  );
}

const emptyIcon = (
  <svg
    className="w-12 h-12 mx-auto"
    viewBox="0 0 24 24"
    fill="none"
    stroke="currentColor"
    strokeWidth="1.5"
    aria-hidden
  >
    <path
      d="M9 12l2 2 4-4m6 2a9 9 0 11-18 0 9 9 0 0118 0z"
      strokeLinecap="round"
      strokeLinejoin="round"
    />
  </svg>
);

export default function ActionPlansListPage() {
  return (
    <Suspense fallback={<ListLoadingSkeleton />}>
      <ActionPlansPageInner />
    </Suspense>
  );
}
