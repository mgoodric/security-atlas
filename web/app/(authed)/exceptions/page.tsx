"use client";

// Slice 177 — /exceptions list view.
//
// Surfaces the tenant-wide exception/waiver register, plus the slice 138
// Export buttons (CSV / JSON / XLSX) in the toolbar. Mirrors the slice
// 098 controls / slice 099 evidence / slice 101 policies pattern so the
// entity list-view shape stays predictable across `/controls`,
// `/evidence`, `/policies`, `/risks`, `/audits`, and now `/exceptions`.
//
// Data source resolution:
//   Row source is `exceptionWire` in
//   `internal/api/exceptions/handlers.go` (the SAME wire shape the slice
//   138 export handler materialises). The page binds to `Exception`
//   from `web/lib/api.ts`. Fetched via the BFF at `/api/exceptions`
//   which forwards the bearer cookie to upstream `/v1/exceptions`.
//
// Constitutional invariants honored:
//   - Invariant 6 (tenant isolation): the BFF at /api/exceptions
//     forwards the bearer cookie; the platform enforces tenant
//     isolation via RLS on `tenant_id` plus FORCE ROW LEVEL SECURITY.
//     The UI does NOT pass tenant_id.
//
// Anti-criteria honored (P0):
//   - P0-A-176-1: NO inline edit affordances. The exception lifecycle
//     (request / approve / deny / activate / expire) lives on the slice
//     022 control-detail page; this list page is READ-ONLY.
//   - P0-A-176-2: NO invented columns. Every column derives from
//     `exceptionWire`. Justification is shown truncated; the full text
//     is only available via the row drawer (or future detail page).
//   - Slice 138 P0-A-Ledger-3: justification is sensitive but in-scope
//     via RLS. The page does not log or persist it client-side.
//   - Neutral test-* tokens in tests; no vendor token prefixes.

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
  fetchExceptionsList,
  type Exception,
  type ExceptionsListResponse,
} from "@/lib/api/exceptions";
import {
  EXCEPTIONS_EXPORT_FORMATS,
  EXCEPTIONS_EXPORT_FORMAT_LABELS,
  buildExceptionsExportURL,
} from "@/lib/api/exceptions-export";

import {
  ALL,
  applyFilters,
  clearFilters,
  DEFAULT_FILTERS,
  isDefault,
  setFilter,
  toFetchOptions,
  uniqueControlIDs,
  type ExceptionFilters,
} from "./filters";

const FILTER_KEYS: (keyof ExceptionFilters)[] = ["status", "control_id"];

// Status values mirror `internal/exception/store.go` State* constants.
// Order is lifecycle order so the dropdown reads as a process:
// requested → approved → active → (denied | expired).
const STATUS_OPTIONS: { value: string; label: string }[] = [
  { value: ALL, label: "All statuses" },
  { value: "requested", label: "requested" },
  { value: "approved", label: "approved" },
  { value: "active", label: "active" },
  { value: "denied", label: "denied" },
  { value: "expired", label: "expired" },
];

function statusPillClass(status: string): string {
  switch (status) {
    case "active":
      return "bg-emerald-50 text-emerald-700 dark:bg-emerald-950 dark:text-emerald-300";
    case "requested":
    case "approved":
      return "bg-amber-50 text-amber-700 dark:bg-amber-950 dark:text-amber-300";
    case "denied":
      return "bg-rose-50 text-rose-700 dark:bg-rose-950 dark:text-rose-300";
    case "expired":
      return "bg-slate-100 text-slate-700 dark:bg-slate-800 dark:text-slate-300";
    default:
      return "bg-muted text-muted-foreground";
  }
}

/**
 * Truncate a justification string to a single-line preview. Sensitive
 * text per slice 138 P0-A-Ledger-3, so the cell intentionally caps at
 * ~80 chars; full text only via row drawer / detail page.
 */
function truncate(s: string, max = 80): string {
  if (s.length <= max) return s;
  return s.slice(0, max).trimEnd() + "…";
}

/**
 * Format an ISO timestamp as a short YYYY-MM-DD date label. Same
 * shorthand the slice 099 evidence list uses.
 */
function dateLabel(iso?: string | null): string {
  if (!iso) return "—";
  // Defensive — if upstream ever returns a malformed string, fall back
  // to the raw string rather than throwing.
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  return d.toISOString().slice(0, 10);
}

/**
 * Compute the integer day count between `requested_at` and
 * `expires_at`. Returns "—" for malformed dates so the column never
 * crashes the render. Derived (not invented) — both source fields are
 * required on the wire.
 */
function durationDays(requested: string, expires: string): string {
  const a = new Date(requested);
  const b = new Date(expires);
  if (Number.isNaN(a.getTime()) || Number.isNaN(b.getTime())) return "—";
  const ms = b.getTime() - a.getTime();
  const days = Math.round(ms / (24 * 60 * 60 * 1000));
  return String(days);
}

function ExceptionsPageInner() {
  const router = useRouter();
  const search = useSearchParams();

  // URL-driven filter state — mirrors the slice 098 / 099 / 101 pattern
  // so every active filter is shareable / bookmarkable.
  const filters: ExceptionFilters = useMemo(() => {
    const out = { ...DEFAULT_FILTERS };
    for (const k of FILTER_KEYS) {
      const v = search.get(k);
      if (v) out[k] = v;
    }
    return out;
  }, [search]);

  const updateFilter = (key: keyof ExceptionFilters, value: string) => {
    const next = setFilter(filters, key, value);
    const sp = new URLSearchParams(search.toString());
    if (next[key] === ALL) {
      sp.delete(key);
    } else {
      sp.set(key, next[key]);
    }
    router.replace(`/exceptions?${sp.toString()}`);
  };

  const clearAll = () => {
    const cleared = clearFilters();
    void cleared;
    router.replace(`/exceptions`);
  };

  // Exception list query. Server-side `status` filter narrows the row
  // set at the DB. `control_id` is passed through too; either may be
  // absent to yield the tenant-wide register.
  const fetchOpts = useMemo(() => toFetchOptions(filters), [filters]);
  const exceptionsQ = useQuery<ExceptionsListResponse>({
    queryKey: ["exceptions", "list", fetchOpts],
    queryFn: () => fetchExceptionsList(fetchOpts),
  });

  const rows: Exception[] = useMemo(
    () => exceptionsQ.data?.exceptions ?? [],
    [exceptionsQ.data],
  );

  // Client-side narrowing too — covers the case where the BFF returned
  // a superset (the upstream may ignore an unknown status), and lets
  // the filter pill respond without a roundtrip.
  const visible = useMemo(() => applyFilters(rows, filters), [rows, filters]);

  // Build the Control pill options from the rows that came back. We
  // intentionally derive these from the result set (not a global
  // anchor catalog) — that keeps the filter UX honest: "you can only
  // narrow to a control that has at least one exception".
  const controlOptions: { value: string; label: string }[] = useMemo(() => {
    const ids = uniqueControlIDs(rows);
    return [
      { value: ALL, label: "All controls" },
      ...ids.map((id) => ({ value: id, label: id })),
    ];
  }, [rows]);

  const pills: FilterPill[] = [
    {
      id: "status",
      label: "Status",
      value: filters.status,
      options: STATUS_OPTIONS,
    },
    {
      id: "control_id",
      label: "Control",
      value: filters.control_id,
      options: controlOptions,
    },
  ];

  const meta = (
    <span>
      Showing{" "}
      <span className="text-foreground font-medium">{visible.length}</span> of{" "}
      <span className="font-mono">{rows.length}</span> exception
      {rows.length === 1 ? "" : "s"}
    </span>
  );

  const columns: ListColumn<Exception>[] = [
    {
      id: "id",
      header: "ID",
      cell: (row) => (
        <span
          className="font-mono text-[11px] text-muted-foreground"
          title={row.id}
          data-testid="exceptions-row-id"
        >
          {row.id.slice(0, 8)}…
        </span>
      ),
    },
    {
      id: "control_id",
      header: "Control",
      cell: (row) => (
        <Link
          href={`/controls/${encodeURIComponent(row.control_id)}`}
          className="font-mono text-xs text-primary hover:underline"
          data-testid="exceptions-row-control-id"
          onClick={(e) => e.stopPropagation()}
        >
          {row.control_id.slice(0, 8)}…
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
          data-testid="exceptions-row-status"
        >
          {row.status}
        </span>
      ),
    },
    {
      id: "requested_by",
      header: "Requested by",
      cell: (row) => (
        <span
          className="font-mono text-xs text-muted-foreground"
          data-testid="exceptions-row-requested-by"
        >
          {row.requested_by}
        </span>
      ),
    },
    {
      id: "requested_at",
      header: "Requested",
      cell: (row) => (
        <span
          className="font-mono text-xs text-muted-foreground"
          data-testid="exceptions-row-requested-at"
        >
          {dateLabel(row.requested_at)}
        </span>
      ),
    },
    {
      id: "expires_at",
      header: "Expires",
      cell: (row) => (
        <span
          className="font-mono text-xs text-muted-foreground"
          data-testid="exceptions-row-expires-at"
        >
          {dateLabel(row.expires_at)}
        </span>
      ),
    },
    {
      id: "duration",
      header: "Days",
      cell: (row) => (
        <span
          className="font-mono text-xs text-muted-foreground"
          data-testid="exceptions-row-duration"
        >
          {durationDays(row.requested_at, row.expires_at)}
        </span>
      ),
    },
    {
      id: "justification",
      header: "Justification",
      cell: (row) => (
        <span
          className="text-xs text-foreground/80"
          title={row.justification}
          data-testid="exceptions-row-justification"
        >
          {truncate(row.justification)}
        </span>
      ),
    },
  ];

  // Slice 138 Export buttons — three links to the slice 138 exceptions
  // BFF (`/api/admin/exceptions/export?format=...`). Each is an `<a
  // download>` so the browser's native file-save dialog handles the
  // download; the BFF streams the platform response back unchanged.
  // Slice 138 P0-A-Ledger-3: justification IS included in the export
  // (the export handler treats RLS as the authority — operators with
  // tenant context have already proved they may see it).
  const actions = (
    <div
      className="flex items-center gap-1"
      data-testid="exceptions-export-buttons"
    >
      <span className="text-xs text-muted-foreground">Export:</span>
      {EXCEPTIONS_EXPORT_FORMATS.map((fmt) => (
        <a
          key={fmt}
          href={buildExceptionsExportURL(fmt)}
          download
          rel="noopener"
          className={buttonVariants({ variant: "outline", size: "sm" })}
          data-testid={`exceptions-export-${fmt}`}
        >
          {EXCEPTIONS_EXPORT_FORMAT_LABELS[fmt]}
        </a>
      ))}
    </div>
  );

  // Distinguish truly-empty (tenant has zero exceptions ever filed —
  // the green path for an early-stage SaaS) from filter-narrowed empty.
  // The truly-zero copy is the more common case at v1: most tenants
  // will not have filed an exception yet. Slice 098 D1-b precedent.
  const isTrulyEmpty = rows.length === 0 && isDefault(filters);

  const trulyEmptyState = (
    <EmptyState
      icon={exceptionsEmptyIcon}
      title="No exceptions filed yet"
      body="Exceptions/waivers capture a deliberate, time-bounded deviation from a control. File one from the control detail page when a finding cannot be remediated immediately."
    />
  );

  const filterEmptyState = (
    <EmptyState
      icon={exceptionsEmptyIcon}
      title="No exceptions match these filters"
      body="Try widening the status or control filters."
      cta={
        isDefault(filters)
          ? undefined
          : { label: "Clear filters", onClick: clearAll }
      }
    />
  );

  const emptyState = isTrulyEmpty ? trulyEmptyState : filterEmptyState;

  // ---- render ----

  if (exceptionsQ.isLoading) {
    return (
      <ListPage
        title="Exceptions"
        subtitle="Time-bounded waivers from active controls · approved deviations live here"
        actions={actions}
        filterRow={
          <FilterPills pills={pills} onChange={() => {}} meta={meta} />
        }
      >
        <ListLoadingSkeleton />
      </ListPage>
    );
  }

  if (exceptionsQ.isError) {
    return (
      <ListPage
        title="Exceptions"
        subtitle="Time-bounded waivers from active controls · approved deviations live here"
        actions={actions}
      >
        <Alert variant="destructive" data-testid="exceptions-load-error">
          <AlertTitle>Could not load exceptions</AlertTitle>
          <AlertDescription>
            {(exceptionsQ.error as Error).message}
          </AlertDescription>
        </Alert>
      </ListPage>
    );
  }

  return (
    <ListPage
      title="Exceptions"
      subtitle="Time-bounded waivers from active controls · approved deviations live here"
      actions={actions}
      filterRow={
        <FilterPills
          pills={pills}
          onChange={(id, v) => updateFilter(id as keyof ExceptionFilters, v)}
          meta={meta}
        />
      }
    >
      <ListTable<Exception>
        columns={columns}
        rows={visible}
        rowKey={(row) => row.id}
        onRowClick={(row) =>
          // Per P0-A-176-1 the list view is read-only; clicking a row
          // routes to the control detail page where the slice 022
          // lifecycle workflow (approve / deny / activate / expire)
          // lives. The drawer pattern would be nice but is deferred —
          // navigating the operator to the control where the
          // exception lives is the more honest interaction.
          router.push(`/controls/${encodeURIComponent(row.control_id)}`)
        }
        emptyFallback={emptyState}
      />
    </ListPage>
  );
}

// Shared empty-state icon (a sticky-note-with-strikethrough hero —
// reads as "waiver", "permission slip", or "documented deviation").
const exceptionsEmptyIcon = (
  <svg
    className="w-12 h-12 mx-auto"
    viewBox="0 0 24 24"
    fill="none"
    stroke="currentColor"
    strokeWidth="1.5"
    aria-hidden
  >
    <path
      d="M9 12.75L11.25 15 15 9.75M21 12c0 1.268-.63 2.39-1.593 3.068a3.745 3.745 0 01-1.043 3.296 3.745 3.745 0 01-3.296 1.043A3.745 3.745 0 0112 21c-1.268 0-2.39-.63-3.068-1.593a3.746 3.746 0 01-3.296-1.043 3.745 3.745 0 01-1.043-3.296A3.745 3.745 0 013 12c0-1.268.63-2.39 1.593-3.068a3.745 3.745 0 011.043-3.296 3.746 3.746 0 013.296-1.043A3.746 3.746 0 0112 3c1.268 0 2.39.63 3.068 1.593a3.746 3.746 0 013.296 1.043 3.746 3.746 0 011.043 3.296A3.745 3.745 0 0121 12z"
      strokeLinecap="round"
      strokeLinejoin="round"
    />
  </svg>
);

export default function ExceptionsListPage() {
  // useSearchParams must be wrapped in Suspense in App Router (Next 16
  // strict-mode requirement). The fallback is the same skeleton the
  // query-pending state shows, so the perceived layout is stable.
  return (
    <Suspense fallback={<ListLoadingSkeleton />}>
      <ExceptionsPageInner />
    </Suspense>
  );
}
