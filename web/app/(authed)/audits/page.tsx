"use client";

// Slice 102 — /audits list view (the plural period index).
//
// Today `/audits` (plural) 404'd in the sidebar (audit finding F-4 in
// `Plans/canvas/13-ui-mockup-audit-2026-05-16.md`). This page ships the
// missing list view per the design captured in slice 093
// (`Plans/mockups/audits.html` + `Plans/canvas/12-ui-fill-in-design-
// decisions.md` §2/3/6/7/8).
//
// Disambiguation per design doc §6:
//   /audits           (plural)   — this page. Period index. List of
//                                  audit_periods (the lifecycle artifact).
//   /audit/[controlId] (singular) — slice 042. Per-control auditor walk-
//                                   through inside one open/frozen period.
// Different routes, different files, different goals. No collision.
//
// Row source resolution (slice 102 D1):
//   periodWire from `internal/api/auditperiods/handlers.go` via
//   `GET /v1/audit-periods` (canonical per design doc §7). Tenant-scoped
//   at the platform via the bearer-derived tenant context + RLS.
//
// Constitutional invariants honored:
//   - Invariant 6 (tenant isolation): the BFF at /api/audits forwards
//     the bearer cookie to /v1/audit-periods; the platform enforces
//     tenant isolation via RLS. The UI does not pass tenant_id.
//   - Invariant 10 (audit-period freezing): frozen periods are visually
//     distinct (lock icon + sky pill + tooltip). The list itself is
//     read-only — editing frozen periods requires the period-detail
//     page's unfreeze workflow (out of scope per P0-A2).
//
// Anti-criteria honored (P0):
//   - P0-A1: NO collision with /audit/[controlId] (different file,
//            different route segment — Next.js routes /audits and
//            /audit/[id] independently).
//   - P0-A2: NO editing frozen periods from the list (read-only render).
//   - P0-A3: NO period-create UI — the "New audit period" CTA is a
//            placeholder link to the existing admin flow.
//   - P0-A4: NO invented columns — every column is derived from
//            periodWire (name, framework_version_id, period_start,
//            period_end, status, frozen_at, frozen_by, created_by).
//            Mockup shows a "Sample size" column but periodWire does
//            NOT carry it — we OMIT the column rather than invent.
//   - P0-A5: neutral test-* tokens only in tests.

import { useQuery } from "@tanstack/react-query";
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
import { Button } from "@/components/ui/button";
import {
  fetchAuditPeriods,
  type AuditPeriod,
  type AuditPeriodsListResponse,
} from "@/lib/api";

import {
  ALL,
  applyFilters,
  clearFilters,
  DEFAULT_FILTERS,
  isDefault,
  setFilter,
  uniqueYears,
  type AuditFilters,
} from "./filters";
import {
  daysUntilEnd,
  daysUntilEndLabel,
  frozenMetaLabel,
  frozenTooltip,
  isFrozen,
  isInProgressUrgent,
  periodRangeLabel,
  statusDotClass,
  statusPillClass,
} from "./format";

const FILTER_KEYS: (keyof AuditFilters)[] = ["framework", "status", "year"];

// The status pill option set enumerates the broader forward-looking
// status vocabulary from the slice text. The DB CHECK constraint only
// allows {'open','frozen'} in v1; the additional options become live
// when the backend lifts the constraint, with no page rework.
const STATUS_OPTIONS: { value: string; label: string }[] = [
  { value: ALL, label: "All statuses" },
  { value: "open", label: "open" },
  { value: "in_progress", label: "in_progress" },
  { value: "frozen", label: "frozen" },
  { value: "closed", label: "closed" },
  { value: "planned", label: "planned" },
];

// Framework pill is a no-op in v1 (periodWire has only the UUID — no
// label endpoint exists yet). The pill still renders so the UI shape
// stays stable across slices.
const FRAMEWORK_OPTIONS: { value: string; label: string }[] = [
  { value: ALL, label: "All frameworks" },
  { value: "soc2", label: "SOC 2" },
  { value: "iso27001", label: "ISO 27001" },
  { value: "nist_csf", label: "NIST CSF" },
  { value: "pci_dss", label: "PCI DSS" },
  { value: "hipaa", label: "HIPAA" },
  { value: "gdpr", label: "GDPR" },
];

// Small lock SVG. Heroicons mini lock-closed. Inlined to avoid pulling
// a new dependency for one icon.
function LockIcon({ className }: { className?: string }) {
  return (
    <svg
      aria-hidden
      className={className}
      viewBox="0 0 20 20"
      fill="currentColor"
      data-testid="audits-row-lock-icon"
    >
      <path
        fillRule="evenodd"
        d="M10 1a4.5 4.5 0 00-4.5 4.5V9H5a2 2 0 00-2 2v6a2 2 0 002 2h10a2 2 0 002-2v-6a2 2 0 00-2-2h-.5V5.5A4.5 4.5 0 0010 1zm3 8V5.5a3 3 0 10-6 0V9h6z"
        clipRule="evenodd"
      />
    </svg>
  );
}

function AuditsPageInner() {
  const router = useRouter();
  const search = useSearchParams();

  // URL-driven filter state — mirrors the slice 098 controls pattern so
  // the filter set is shareable / bookmarkable. Default = ALL on every
  // pill.
  const filters: AuditFilters = useMemo(() => {
    const out = { ...DEFAULT_FILTERS };
    for (const k of FILTER_KEYS) {
      const v = search.get(k);
      if (v) out[k] = v;
    }
    return out;
  }, [search]);

  const updateFilter = (key: keyof AuditFilters, value: string) => {
    const next = setFilter(filters, key, value);
    const sp = new URLSearchParams(search.toString());
    if (next[key] === ALL) {
      sp.delete(key);
    } else {
      sp.set(key, next[key]);
    }
    router.replace(`/audits?${sp.toString()}`);
  };

  const clearAll = () => {
    const cleared = clearFilters();
    const sp = new URLSearchParams(search.toString());
    for (const k of FILTER_KEYS) {
      if (cleared[k] === ALL) sp.delete(k);
    }
    router.replace(`/audits?${sp.toString()}`);
  };

  const periodsQ = useQuery<AuditPeriodsListResponse>({
    queryKey: ["audits", "list"],
    queryFn: fetchAuditPeriods,
  });

  const periods: AuditPeriod[] = useMemo(
    () => periodsQ.data?.audit_periods ?? [],
    [periodsQ.data],
  );

  const visible = useMemo(
    () => applyFilters(periods, filters),
    [periods, filters],
  );

  const yearOptions: { value: string; label: string }[] = useMemo(() => {
    const years = uniqueYears(periods);
    return [
      { value: ALL, label: "All years" },
      ...years.map((y) => ({ value: y, label: y })),
    ];
  }, [periods]);

  const pills: FilterPill[] = [
    {
      id: "framework",
      label: "Framework",
      value: filters.framework,
      options: FRAMEWORK_OPTIONS,
    },
    {
      id: "status",
      label: "Status",
      value: filters.status,
      options: STATUS_OPTIONS,
    },
    {
      id: "year",
      label: "Year",
      value: filters.year,
      options: yearOptions,
    },
  ];

  const meta = (
    <span>
      Showing{" "}
      <span className="text-foreground font-medium">{visible.length}</span> of{" "}
      <span className="font-mono">{periods.length}</span> periods
    </span>
  );

  const columns: ListColumn<AuditPeriod>[] = [
    {
      id: "name",
      header: "Name",
      cell: (p) => (
        <span
          className="text-foreground font-medium"
          data-testid="audits-row-name"
        >
          {p.name}
        </span>
      ),
    },
    {
      id: "framework_version",
      header: "Framework version",
      cell: (p) => (
        // Framework label endpoint does not exist yet. Render the UUID
        // verbatim in mono so the user can copy it (e.g. for support
        // tickets). A spillover slice will file the dedicated label
        // endpoint and this cell will render a friendly name then.
        <span
          className="font-mono text-xs text-muted-foreground"
          title={p.framework_version_id}
          data-testid="audits-row-framework-version"
        >
          {p.framework_version_id.slice(0, 8)}…
        </span>
      ),
    },
    {
      id: "period",
      header: "Period",
      cell: (p) => (
        <span
          className="font-mono text-xs text-muted-foreground"
          data-testid="audits-row-period"
        >
          {periodRangeLabel(p)}
        </span>
      ),
    },
    {
      id: "status",
      header: "Status",
      cell: (p) => {
        const urgent = isInProgressUrgent(p);
        const days = daysUntilEnd(p);
        return (
          <span
            className="inline-flex items-center gap-1.5"
            data-testid="audits-row-status-cell"
          >
            <span
              className={`inline-flex items-center gap-1.5 px-2 py-0.5 text-[11px] font-medium rounded-md ${statusPillClass(
                p.status,
              )}`}
              data-testid="audits-row-status-pill"
            >
              <span
                className={`w-1.5 h-1.5 rounded-full ${statusDotClass(
                  p.status,
                )}`}
              />
              {p.status}
            </span>
            {isFrozen(p) ? (
              <span
                title={frozenTooltip(p)}
                className="text-sky-700"
                data-testid="audits-row-lock"
              >
                <LockIcon className="w-3.5 h-3.5" />
              </span>
            ) : null}
            {urgent ? (
              <span
                title={`ends in ${days}d — start fieldwork soon`}
                className="inline-flex items-center"
                data-testid="audits-row-urgent-cue"
              >
                <span className="w-1.5 h-1.5 rounded-full bg-amber-500 animate-pulse" />
                <span className="ml-1 text-[11px] text-amber-700">
                  {daysUntilEndLabel(days)}
                </span>
              </span>
            ) : null}
          </span>
        );
      },
    },
    {
      id: "frozen",
      header: "Frozen",
      cell: (p) =>
        isFrozen(p) ? (
          <span
            className="font-mono text-xs text-muted-foreground"
            data-testid="audits-row-frozen-meta"
          >
            {frozenMetaLabel(p)}
          </span>
        ) : (
          <span
            className="text-xs text-muted-foreground italic"
            data-testid="audits-row-frozen-meta-empty"
          >
            —
          </span>
        ),
    },
    {
      id: "created_by",
      header: "Created by",
      align: "right",
      cell: (p) => (
        <span
          className="text-xs text-muted-foreground"
          data-testid="audits-row-created-by"
        >
          {p.created_by}
        </span>
      ),
    },
  ];

  const actions = (
    <>
      <Button variant="outline" size="sm" disabled>
        Export OSCAL bundle
      </Button>
      {/* P0-A3: this CTA is a placeholder link — the period-create flow
          lives elsewhere (admin UI) and is out of scope for this slice. */}
      <Button size="sm" disabled data-testid="audits-create-cta">
        New audit period
      </Button>
    </>
  );

  if (periodsQ.isLoading) {
    return (
      <ListPage
        title="Audit periods"
        subtitle="Period-level index — open a period for the per-control walk-through"
        actions={actions}
        filterRow={
          <FilterPills pills={pills} onChange={() => {}} meta={meta} />
        }
      >
        <ListLoadingSkeleton
          columns={["w-40", "w-24", "w-36", "w-20", "w-24", "w-20"]}
        />
      </ListPage>
    );
  }

  if (periodsQ.isError) {
    return (
      <ListPage
        title="Audit periods"
        subtitle="Period-level index — open a period for the per-control walk-through"
        actions={actions}
      >
        <Alert variant="destructive" data-testid="audits-load-error">
          <AlertTitle>Could not load audit periods</AlertTitle>
          <AlertDescription>
            {(periodsQ.error as Error).message}
          </AlertDescription>
        </Alert>
      </ListPage>
    );
  }

  // Empty-state copy diverges by reason:
  //   - true zero-state (no periods at all) → design doc §2 audits row:
  //     "No audit periods yet" + Create CTA.
  //   - filter-induced empty → "No periods match these filters" + Clear.
  const emptyState =
    periods.length === 0 ? (
      <EmptyState
        icon={
          <svg
            className="w-12 h-12 mx-auto"
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            strokeWidth="1.5"
            aria-hidden
          >
            <path
              d="M6.75 3v2.25M17.25 3v2.25M3 18.75V7.5a2.25 2.25 0 012.25-2.25h13.5A2.25 2.25 0 0121 7.5v11.25m-18 0A2.25 2.25 0 005.25 21h13.5A2.25 2.25 0 0021 18.75m-18 0v-7.5A2.25 2.25 0 015.25 9h13.5A2.25 2.25 0 0121 11.25v7.5"
              strokeLinecap="round"
              strokeLinejoin="round"
            />
          </svg>
        }
        title="No audit periods yet"
        body="Create your first period when you start an external audit — sample populations will draw from evidence captured during the period."
        cta={{
          label: "Create audit period",
          // P0-A3: no in-list create UI. Sends the user to the
          // existing admin flow placeholder; when slice 042's period
          // create form lands (or an admin route), update the href.
          onClick: () => router.push("/admin"),
        }}
      />
    ) : (
      <EmptyState
        icon={
          <svg
            className="w-12 h-12 mx-auto"
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            strokeWidth="1.5"
            aria-hidden
          >
            <path
              d="M6.75 3v2.25M17.25 3v2.25M3 18.75V7.5a2.25 2.25 0 012.25-2.25h13.5A2.25 2.25 0 0121 7.5v11.25"
              strokeLinecap="round"
              strokeLinejoin="round"
            />
          </svg>
        }
        title="No periods match these filters"
        body="Try widening the framework, status, or year filters."
        cta={
          isDefault(filters)
            ? undefined
            : { label: "Clear filters", onClick: clearAll }
        }
      />
    );

  return (
    <ListPage
      title="Audit periods"
      subtitle="Period-level index — open a period for the per-control walk-through"
      actions={actions}
      filterRow={
        <FilterPills
          pills={pills}
          onChange={(id, v) => updateFilter(id as keyof AuditFilters, v)}
          meta={meta}
        />
      }
    >
      <ListTable<AuditPeriod>
        columns={columns}
        rows={visible}
        rowKey={(p) => p.id}
        // AC-7: row click navigates to a per-period detail page. The
        // route is a placeholder — the per-period detail page is a
        // future slice. Today this routes to /audits/{id} which 404s
        // with the standard Next.js not-found UI; that is the correct
        // placeholder behavior per the slice text ("placeholder OR
        // drawer"). When the detail slice lands, no page change here.
        onRowClick={(p) => router.push(`/audits/${encodeURIComponent(p.id)}`)}
        emptyFallback={emptyState}
      />
    </ListPage>
  );
}

export default function AuditsListPage() {
  // useSearchParams must be wrapped in Suspense in App Router (Next 16
  // strict-mode requirement). The fallback is the same skeleton the
  // query-pending state shows, so the perceived layout is stable.
  return (
    <Suspense
      fallback={
        <ListLoadingSkeleton
          columns={["w-40", "w-24", "w-36", "w-20", "w-24", "w-20"]}
        />
      }
    >
      <AuditsPageInner />
    </Suspense>
  );
}
