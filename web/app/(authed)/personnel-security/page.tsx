"use client";

// OE-664 — /personnel-security list view.
//
// Surfaces the tenant's onboarding/offboarding checklists with kind +
// status filters. Overdue offboarding is the visually prominent class:
// it sorts to the very top (status.ts checklistRank) and carries a rose
// "Overdue offboarding" badge — a departed person with open items is
// the highest-risk row on the page. Mirrors the slice-384 /action-plans
// list shape so the entity list-view stays predictable across the app.
//
// Data source: `checklistWire` in
// `internal/api/personnelsecurity/handlers.go` (OE-663), fetched via
// the BFF at `/api/personnel-security/checklists`. Tenant isolation is
// enforced by RLS at the DB layer (invariant 6); the UI never passes
// tenant_id.

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
  CHECKLIST_KINDS,
  CHECKLIST_STATUSES,
  fetchPersonnelChecklists,
  type Checklist,
  type ChecklistsListResponse,
} from "@/lib/api/personnel-security";

import {
  dateLabel,
  kindLabel,
  sortChecklists,
  statusBadgeClass,
  statusBadgeLabel,
} from "./status";

const ALL = "__all__";

const KIND_OPTIONS = [
  { value: ALL, label: "All kinds" },
  ...CHECKLIST_KINDS.map((k) => ({ value: k, label: kindLabel(k) })),
];

const STATUS_OPTIONS = [
  { value: ALL, label: "All statuses" },
  ...CHECKLIST_STATUSES.map((s) => ({
    value: s,
    label: s === "open" ? "Open" : "Completed",
  })),
];

function PersonnelSecurityPageInner() {
  const router = useRouter();
  const search = useSearchParams();

  const kind = search.get("kind") ?? ALL;
  const status = search.get("status") ?? ALL;

  const updateFilter = (id: string, value: string) => {
    const sp = new URLSearchParams(search.toString());
    if (value === ALL) sp.delete(id);
    else sp.set(id, value);
    router.replace(`/personnel-security?${sp.toString()}`);
  };

  const fetchOpts = useMemo(
    () => ({
      ...(kind !== ALL ? { kind: kind as Checklist["kind"] } : {}),
      ...(status !== ALL ? { status: status as Checklist["status"] } : {}),
    }),
    [kind, status],
  );

  const listQ = useQuery<ChecklistsListResponse>({
    queryKey: ["personnel-security", "list", fetchOpts],
    queryFn: () => fetchPersonnelChecklists(fetchOpts),
  });

  const now = useMemo(() => new Date(), []);

  const rows: Checklist[] = useMemo(
    () => sortChecklists(listQ.data?.checklists ?? [], now),
    [listQ.data, now],
  );

  const pills: FilterPill[] = [
    { id: "kind", label: "Kind", value: kind, options: KIND_OPTIONS },
    { id: "status", label: "Status", value: status, options: STATUS_OPTIONS },
  ];

  const meta = (
    <span>
      Showing <span className="text-foreground font-medium">{rows.length}</span>{" "}
      checklist{rows.length === 1 ? "" : "s"}
    </span>
  );

  const columns: ListColumn<Checklist>[] = [
    {
      id: "person",
      header: "Person",
      cell: (row) => (
        <Link
          href={`/personnel-security/${encodeURIComponent(row.id)}`}
          className="text-primary hover:underline"
          data-testid="personnel-row-person"
          onClick={(e) => e.stopPropagation()}
        >
          {row.person_display_name || row.person_external_id}
        </Link>
      ),
    },
    {
      id: "kind",
      header: "Kind",
      cell: (row) => (
        <span className="text-sm" data-testid="personnel-row-kind">
          {kindLabel(row.kind)}
        </span>
      ),
    },
    {
      id: "status",
      header: "Status",
      cell: (row) => (
        <span
          className={
            "inline-flex items-center rounded-md px-1.5 py-0.5 text-[11px] font-medium " +
            statusBadgeClass(row, now)
          }
          data-testid="personnel-row-status"
        >
          {statusBadgeLabel(row, now)}
        </span>
      ),
    },
    {
      id: "source",
      header: "Source",
      cell: (row) => (
        <span
          className="text-xs text-muted-foreground"
          data-testid="personnel-row-source"
        >
          {row.source}
        </span>
      ),
    },
    {
      id: "due_at",
      header: "Due",
      cell: (row) => (
        <span
          className="font-mono text-xs text-muted-foreground"
          data-testid="personnel-row-due"
        >
          {dateLabel(row.due_at)}
        </span>
      ),
    },
  ];

  const actions = (
    <Link
      href="/personnel-security/new"
      className={buttonVariants({ size: "sm" })}
      data-testid="personnel-new-button"
    >
      New checklist
    </Link>
  );

  const filtered = kind !== ALL || status !== ALL;

  const emptyState = (
    <EmptyState
      icon={emptyIcon}
      title={
        filtered
          ? "No checklists match these filters"
          : "No personnel checklists yet"
      }
      body={
        filtered
          ? "Try widening the kind or status filter."
          : "A checklist tracks the security tasks for one person's onboarding or offboarding — each completed item records evidence (CC1). Create one when a person joins or leaves."
      }
      cta={
        filtered
          ? {
              label: "Clear filters",
              onClick: () => router.replace("/personnel-security"),
            }
          : {
              label: "New checklist",
              onClick: () => router.push("/personnel-security/new"),
            }
      }
    />
  );

  const subtitle =
    "Onboarding / offboarding security checklists · overdue offboarding surfaces first";

  if (listQ.isLoading) {
    return (
      <ListPage
        title="Personnel Security"
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

  if (listQ.isError) {
    return (
      <ListPage
        title="Personnel Security"
        subtitle={subtitle}
        actions={actions}
      >
        <Alert variant="destructive" data-testid="personnel-load-error">
          <AlertTitle>Could not load checklists</AlertTitle>
          <AlertDescription>{(listQ.error as Error).message}</AlertDescription>
        </Alert>
      </ListPage>
    );
  }

  return (
    <ListPage
      title="Personnel Security"
      subtitle={subtitle}
      actions={actions}
      filterRow={
        <FilterPills pills={pills} onChange={updateFilter} meta={meta} />
      }
    >
      <ListTable<Checklist>
        columns={columns}
        rows={rows}
        rowKey={(row) => row.id}
        onRowClick={(row) =>
          router.push(`/personnel-security/${encodeURIComponent(row.id)}`)
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
      d="M16 7a4 4 0 11-8 0 4 4 0 018 0zM12 14a7 7 0 00-7 7h14a7 7 0 00-7-7z"
      strokeLinecap="round"
      strokeLinejoin="round"
    />
  </svg>
);

export default function PersonnelSecurityListPage() {
  return (
    <Suspense fallback={<ListLoadingSkeleton />}>
      <PersonnelSecurityPageInner />
    </Suspense>
  );
}
