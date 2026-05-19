"use client";

// Slice 100 — /risks list view.
//
// Today `/risks` 404'd in the sidebar (audit finding F-4 in
// `Plans/canvas/13-ui-mockup-audit-2026-05-16.md`). This page ships the
// missing flat list AND addresses audit F-3 by removing
// `/risks/hierarchy` from the top-nav (the canonical default is the
// flat list; the hierarchy stays reachable via the page-header
// `Hierarchy view ->` link per design doc §5).
//
// The page consumes the shared `web/components/list/*` shell from slice
// 098 — the reusable primitives that the other list-view slices
// (099/101/102) also consume.
//
// Data source resolution (slice 100):
//   `GET /v1/risks` (slice 019 + slice 067 hierarchy/severity fields)
//   is the row source. Per AC-3 the visible filter set narrows to
//   three (treatment + severity + owner); the additional pills shown
//   in the mockup (category/methodology/org_unit) stay deferrable.
//
// Constitutional invariants honored:
//   - Invariant 6 (tenant isolation): the BFF at /api/risks forwards
//     the bearer cookie to /v1/risks; the platform enforces tenant
//     isolation via RLS. The UI does not pass tenant_id.
//
// Anti-criteria honored (P0):
//   - P0-A1: ZERO content edits to /risks/hierarchy beyond the
//     `List view ->` page-header link.
//   - P0-A2: read-only list — Add first risk CTA links to the dedicated
//     risk-create UI at `/risks/new` (slice 105). The placeholder
//     `/admin` link from slice 100's original ship was lifted when
//     slice 105 landed.
//   - P0-A3: NO invented columns — every column derives from `riskWire`
//     (id, title, category, treatment, treatment_owner, residual_score,
//     severity, review_due_at).
//   - P0-A4: neutral test-* tokens.

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
import { Button, buttonVariants } from "@/components/ui/button";
import { fetchRisksList, type Risk, type RisksListResponse } from "@/lib/api";
import {
  RISK_EXPORT_FORMATS,
  RISK_EXPORT_FORMAT_LABELS,
  buildRiskExportURL,
} from "@/lib/api/risks-export";

import {
  ALL,
  applyFilters,
  clearFilters,
  DEFAULT_FILTERS,
  formatResidualScore,
  isDefault,
  residualClass,
  setFilter,
  severityBand,
  severityClasses,
  uniqueOwners,
  type RiskFilters,
} from "./filters";

const FILTER_KEYS: (keyof RiskFilters)[] = ["treatment", "severity", "owner"];

const TREATMENT_OPTIONS: { value: string; label: string }[] = [
  { value: ALL, label: "All treatments" },
  { value: "mitigate", label: "mitigate" },
  { value: "transfer", label: "transfer" },
  { value: "accept", label: "accept" },
  { value: "avoid", label: "avoid" },
];

const SEVERITY_OPTIONS: { value: string; label: string }[] = [
  { value: ALL, label: "All severity" },
  { value: "high", label: "high (>=15)" },
  { value: "medium", label: "medium (8-14)" },
  { value: "low", label: "low (1-7)" },
  { value: "none", label: "none (0)" },
];

function RisksPageInner() {
  const router = useRouter();
  const search = useSearchParams();

  // URL-driven filter state — mirrors the slice 098 controls pattern so
  // the filter set is shareable / bookmarkable. Default = ALL on every
  // pill.
  const filters: RiskFilters = useMemo(() => {
    const out = { ...DEFAULT_FILTERS };
    for (const k of FILTER_KEYS) {
      const v = search.get(k);
      if (v) out[k] = v;
    }
    return out;
  }, [search]);

  const updateFilter = (key: keyof RiskFilters, value: string) => {
    const next = setFilter(filters, key, value);
    const sp = new URLSearchParams(search.toString());
    if (next[key] === ALL) {
      sp.delete(key);
    } else {
      sp.set(key, next[key]);
    }
    router.replace(`/risks?${sp.toString()}`);
  };

  const clearAll = () => {
    const cleared = clearFilters();
    const sp = new URLSearchParams(search.toString());
    for (const k of FILTER_KEYS) {
      if (cleared[k] === ALL) sp.delete(k);
    }
    router.replace(`/risks?${sp.toString()}`);
  };

  const risksQ = useQuery<RisksListResponse>({
    queryKey: ["risks", "list"],
    queryFn: fetchRisksList,
  });

  const rows: Risk[] = useMemo(() => risksQ.data?.risks ?? [], [risksQ.data]);

  const visible = useMemo(() => applyFilters(rows, filters), [rows, filters]);

  const ownerOptions: { value: string; label: string }[] = useMemo(() => {
    const owners = uniqueOwners(rows);
    return [
      { value: ALL, label: "All owners" },
      ...owners.map((o) => ({
        value: o,
        label: o === "unassigned" ? "unassigned" : o,
      })),
    ];
  }, [rows]);

  const pills: FilterPill[] = [
    {
      id: "treatment",
      label: "Treatment",
      value: filters.treatment,
      options: TREATMENT_OPTIONS,
    },
    {
      id: "severity",
      label: "Severity",
      value: filters.severity,
      options: SEVERITY_OPTIONS,
    },
    {
      id: "owner",
      label: "Owner",
      value: filters.owner,
      options: ownerOptions,
    },
  ];

  const meta = (
    <span>
      Showing{" "}
      <span className="text-foreground font-medium">{visible.length}</span> of{" "}
      <span className="font-mono">{rows.length}</span> risks
    </span>
  );

  const columns: ListColumn<Risk>[] = [
    {
      id: "id",
      header: "ID",
      cell: (row) => (
        <span
          className="font-mono text-xs text-muted-foreground"
          data-testid="risks-row-id"
        >
          {row.id.slice(0, 8)}
        </span>
      ),
    },
    {
      id: "title",
      header: "Title",
      cell: (row) => (
        <span className="text-foreground" data-testid="risks-row-title">
          {row.title}
        </span>
      ),
    },
    {
      id: "category",
      header: "Category",
      cell: (row) => (
        <span className="text-xs text-muted-foreground">{row.category}</span>
      ),
    },
    {
      id: "treatment",
      header: "Treatment",
      cell: (row) => (
        <span
          className="inline-flex items-center px-2 py-0.5 text-[11px] font-medium rounded-md bg-muted text-foreground"
          data-testid="risks-row-treatment"
        >
          {row.treatment}
        </span>
      ),
    },
    {
      id: "treatment_owner",
      header: "Owner",
      cell: (row) => {
        const owner = row.treatment_owner.trim();
        if (owner === "") {
          return (
            <span className="text-xs italic text-muted-foreground">
              unassigned
            </span>
          );
        }
        return <span className="text-xs text-muted-foreground">{owner}</span>;
      },
    },
    {
      id: "residual_score",
      header: "Residual",
      cell: (row) => {
        const formatted = formatResidualScore(row.residual_score);
        return (
          <span
            className={`font-mono text-xs ${residualClass(formatted)}`}
            data-testid="risks-row-residual"
          >
            {formatted}
          </span>
        );
      },
    },
    {
      id: "severity",
      header: "Severity",
      cell: (row) => {
        const band = severityBand(row.severity);
        return (
          <span
            className={`inline-flex items-center justify-center w-6 h-6 text-[11px] font-semibold rounded ${severityClasses(
              band,
            )}`}
            data-testid="risks-row-severity"
          >
            {row.severity}
          </span>
        );
      },
    },
    {
      id: "review_due_at",
      header: "Review due",
      cell: (row) =>
        row.review_due_at ? (
          <span className="text-xs text-muted-foreground">
            {row.review_due_at.slice(0, 10)}
          </span>
        ) : (
          <span className="text-xs text-muted-foreground">—</span>
        ),
    },
  ];

  // AC-6: Page-header `Hierarchy view ->` link on /risks navigates to
  // /risks/hierarchy. The reciprocal `List view ->` link on the
  // hierarchy page is wired in a sibling edit.
  //
  // Slice 136: Export buttons (CSV / JSON / XLSX) wire to the BFF
  // proxy at `/api/risks/export?format=...`, which forwards to the
  // platform `GET /v1/risks/export` endpoint. Each link is an
  // `<a download>` so the browser honours the backend's
  // Content-Disposition filename; no client-side JS download flow.
  const actions = (
    <>
      <Link
        href="/risks/hierarchy"
        data-testid="risks-hierarchy-link"
        className={buttonVariants({ variant: "outline", size: "sm" })}
      >
        Hierarchy view →
      </Link>
      {RISK_EXPORT_FORMATS.map((format) => (
        <a
          key={format}
          href={buildRiskExportURL(format)}
          download
          rel="noopener"
          className={buttonVariants({ variant: "outline", size: "sm" })}
          data-testid={`risks-export-${format}`}
        >
          Export {RISK_EXPORT_FORMAT_LABELS[format]}
        </a>
      ))}
      <Button size="sm" disabled>
        New risk
      </Button>
    </>
  );

  const subtitle = (
    <>
      Flat list of all risks · for the org-tree view see{" "}
      <Link href="/risks/hierarchy" className="text-primary hover:underline">
        Risk hierarchy
      </Link>
    </>
  );

  if (risksQ.isLoading) {
    return (
      <ListPage
        title="Risk register"
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

  if (risksQ.isError) {
    return (
      <ListPage title="Risk register" subtitle={subtitle} actions={actions}>
        <Alert variant="destructive" data-testid="risks-load-error">
          <AlertTitle>Could not load risks</AlertTitle>
          <AlertDescription>{(risksQ.error as Error).message}</AlertDescription>
        </Alert>
      </ListPage>
    );
  }

  // AC-4: empty-state copy "No risks logged yet" with `Add first risk`
  // primary CTA (per design doc §2 — true zero-state). Most installs
  // start with zero risks; the CTA routes to the dedicated risk-create
  // form at `/risks/new` (slice 105). When filters narrow to zero
  // results on a populated tenant, the CTA changes to `Clear filters`.
  const isFilterEmpty = rows.length > 0 && visible.length === 0;
  const emptyState = (
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
            d="M12 9v3.75m-9.303 3.376c-.866 1.5.217 3.374 1.948 3.374h14.71c1.73 0 2.813-1.874 1.948-3.374L13.949 3.378c-.866-1.5-3.032-1.5-3.898 0L2.697 16.126zM12 15.75h.007v.008H12v-.008z"
            strokeLinecap="round"
            strokeLinejoin="round"
          />
        </svg>
      }
      title={
        isFilterEmpty ? "No risks match these filters" : "No risks logged yet"
      }
      body={
        isFilterEmpty
          ? "Try widening the treatment, severity, or owner filters."
          : "Start a register with one or two known operational risks — you can refine methodology later."
      }
      cta={
        isFilterEmpty
          ? { label: "Clear filters", onClick: clearAll }
          : {
              // True zero-state CTA — routes to the dedicated
              // risk-create form added by slice 105.
              label: "Add first risk",
              onClick: () => router.push("/risks/new"),
            }
      }
    />
  );

  // AC-7: Row click navigates to a per-risk detail page placeholder.
  // The slice-019 backend exposes `GET /v1/risks/{id}` already; a
  // dedicated per-risk detail route lives in a future slice. Today
  // we route to `/risks/hierarchy?focus=<id>` so the existing
  // hierarchy view at least scopes the user toward that risk without
  // a 404. When the per-risk detail page ships, this single line
  // flips to `router.push(/risks/${id})` without touching the
  // table layer.
  const onRowClick = (row: Risk) => {
    if (isDefault(filters) && visible.length === 0) return;
    router.push(`/risks/hierarchy?focus=${encodeURIComponent(row.id)}`);
  };

  return (
    <ListPage
      title="Risk register"
      subtitle={subtitle}
      actions={actions}
      filterRow={
        <FilterPills
          pills={pills}
          onChange={(id, v) => updateFilter(id as keyof RiskFilters, v)}
          meta={meta}
        />
      }
    >
      <ListTable<Risk>
        columns={columns}
        rows={visible}
        rowKey={(row) => row.id}
        onRowClick={onRowClick}
        emptyFallback={emptyState}
      />
    </ListPage>
  );
}

export default function RisksListPage() {
  // useSearchParams must be wrapped in Suspense in App Router (Next 16
  // strict-mode requirement). The fallback is the same skeleton the
  // query-pending state shows, so the perceived layout is stable.
  return (
    <Suspense fallback={<ListLoadingSkeleton />}>
      <RisksPageInner />
    </Suspense>
  );
}
