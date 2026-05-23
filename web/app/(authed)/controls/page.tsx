"use client";

// Slice 098 + 104 — /controls list view.
//
// Today `/controls` 404'd in the sidebar (audit finding F-4 in
// `Plans/canvas/13-ui-mockup-audit-2026-05-16.md`). This page ships the
// missing list view per the design captured in slice 093
// (`Plans/mockups/controls.html` + `Plans/canvas/12-ui-fill-in-design-
// decisions.md` §1/2/3/7/8).
//
// The page consumes the shared `web/components/list/*` shell — the
// reusable primitives that the next four list-view slices
// (099/100/101/102) will also consume.
//
// Data source resolution:
//   * Slice 098: shipped against `GET /v1/anchors` with state cells
//     rendered as `—` (no backend join existed).
//   * Slice 104 (this PR): the BFF now calls
//     `GET /v1/anchors?include=state`. State columns render real
//     result / freshness / last_observed_at when the tenant has a
//     control instantiated for the anchor; `—` for the null branch
//     (anchor in catalog, no tenant control). Per-row state fan-out
//     remains explicitly avoided — the join is one query, not 1,400.
//
// Constitutional invariants honored:
//   - Invariant 6 (tenant isolation): the BFF at /api/controls forwards
//     the bearer cookie to /v1/anchors?include=state; the platform
//     enforces tenant isolation via RLS. The UI does not pass tenant_id.
//
// Anti-criteria honored (P0):
//   - P0-A1: NO invented columns — every column is derived from
//     anchorWire (id, scf_id, family, name) or the slice-104 joined
//     state cell (result, freshness_status, last_observed_at). When
//     the tenant has no control for an anchor, the state cells render
//     `—` honestly.
//   - P0-A2: horizontal pill filter row ONLY — no left filter sidebar.
//   - P0-A3: skeleton rows ONLY (via `<ListLoadingSkeleton>`) — no
//     centered spinner.
//   - P0-A4: real placeholder data — no Lorem Ipsum.
//   - P0-A5: neutral test tokens only in tests.

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
import {
  fetchControlsList,
  fetchScopeCells,
  type AnchorWithState,
  type ControlsListResponse,
  type ScopeCellsListResponse,
} from "@/lib/api";
import {
  CONTROLS_EXPORT_FORMATS,
  CONTROLS_EXPORT_FORMAT_LABELS,
  buildControlsExportURL,
} from "@/lib/api/controls-export";
import {
  CONTROLS_HISTORY_EXPORT_FORMATS,
  CONTROLS_HISTORY_EXPORT_FORMAT_LABELS,
  buildControlsHistoryExportURL,
} from "@/lib/api/controls-history-export";

import {
  ALL,
  applyFilters,
  clearFilters,
  DEFAULT_FILTERS,
  FRAMEWORKS_JOIN_SEPARATOR,
  isDefault,
  setFilter,
  uniqueFamilies,
  type AnchorRow,
  type ControlFilters,
} from "./filters";

const FILTER_KEYS: (keyof ControlFilters)[] = [
  "framework",
  "family",
  "result",
  "freshness",
  // Slice 224 — scope cell filter. URL key `scope`, value is a cell uuid
  // or `ALL`. The BFF / upstream applies the actual intersection
  // (P0-224-2 — applicability_expr never reaches the browser).
  "scope",
];

// Slice 224 — cap the Scope pill at 50 entries per AC-5. Tenants with
// more cells get a banner indicating the cap; the dropdown still works
// for the first 50 cells (newest-first ordering from /v1/scopes/cells).
// A typeahead replacement for tenants exceeding the cap is deferred to
// a follow-on slice (per AC-5 + decision log D3).
const SCOPE_CELL_CAP = 50;

const RESULT_OPTIONS: { value: string; label: string }[] = [
  { value: ALL, label: "All states" },
  { value: "pass", label: "pass" },
  { value: "fail", label: "fail" },
  { value: "insufficient_evidence", label: "insufficient_evidence" },
  { value: "not_applicable", label: "not_applicable" },
];

const FRESHNESS_OPTIONS: { value: string; label: string }[] = [
  { value: ALL, label: "All" },
  { value: "fresh", label: "fresh" },
  { value: "stale", label: "stale" },
  { value: "expired", label: "expired" },
];

const FRAMEWORK_OPTIONS: { value: string; label: string }[] = [
  { value: ALL, label: "All frameworks" },
  { value: "soc2", label: "SOC 2" },
  { value: "iso27001", label: "ISO 27001" },
  { value: "nist_csf", label: "NIST CSF" },
  { value: "pci_dss", label: "PCI DSS" },
  { value: "hipaa", label: "HIPAA" },
  { value: "gdpr", label: "GDPR" },
];

function ControlsPageInner() {
  const router = useRouter();
  const search = useSearchParams();

  // URL-driven filter state — mirrors the slice 094 calendar pattern so
  // the filter set is shareable / bookmarkable. Default = ALL on every
  // pill.
  const filters: ControlFilters = useMemo(() => {
    const out = { ...DEFAULT_FILTERS };
    for (const k of FILTER_KEYS) {
      const v = search.get(k);
      if (v) out[k] = v;
    }
    return out;
  }, [search]);

  const updateFilter = (key: keyof ControlFilters, value: string) => {
    const next = setFilter(filters, key, value);
    const sp = new URLSearchParams(search.toString());
    if (next[key] === ALL) {
      sp.delete(key);
    } else {
      sp.set(key, next[key]);
    }
    router.replace(`/controls?${sp.toString()}`);
  };

  const clearAll = () => {
    const cleared = clearFilters();
    const sp = new URLSearchParams(search.toString());
    for (const k of FILTER_KEYS) {
      if (cleared[k] === ALL) sp.delete(k);
    }
    router.replace(`/controls?${sp.toString()}`);
  };

  // Slice 224 — refetch the controls list when the scope filter changes.
  // The narrowing is server-side; the queryKey carries the scope so
  // TanStack invalidates correctly. `ALL` is treated as no-filter
  // (fetchControlsList drops the query param entirely).
  const scopeArg = filters.scope === ALL ? undefined : filters.scope;
  const anchorsQ = useQuery<ControlsListResponse>({
    queryKey: ["controls", "list", scopeArg ?? "all"],
    queryFn: () => fetchControlsList(scopeArg),
  });

  // Slice 224 — the dropdown options for the Scope filter pill come
  // from the tenant's own scope cells (RLS-scoped to the caller's
  // tenant in /v1/scopes/cells, per slice 017). Failure to load this
  // list is non-fatal — the pill renders with just "All cells".
  const scopeCellsQ = useQuery<ScopeCellsListResponse>({
    queryKey: ["scope-cells"],
    queryFn: fetchScopeCells,
  });

  // Convert the anchor wire payload into the join-ready row shape used
  // by the filter logic + table renderer. Slice 104 attaches a real
  // state cell per anchor (or `null` when the tenant has no control
  // instantiated for the anchor). Slice 226 additionally threads the
  // per-anchor frameworks set (display abbreviations) through the row;
  // empty array when the anchor has no satisfaction edges.
  const rows: AnchorRow[] = useMemo(() => {
    const anchors: AnchorWithState[] = anchorsQ.data?.anchors ?? [];
    return anchors.map<AnchorRow>((a) => {
      const { state, frameworks, ...anchor } = a;
      return {
        anchor,
        state: state
          ? {
              result: state.result,
              freshness_status: state.freshness_status,
              last_observed_at: state.last_observed_at,
            }
          : null,
        // Slice 226: defensive default so older fixtures (e.g.
        // hand-rolled mocks in route.test.ts cases that predate the
        // backend extension) round-trip cleanly. The live BFF always
        // ships an array.
        frameworks: frameworks ?? [],
      };
    });
  }, [anchorsQ.data]);

  const visible = useMemo(() => applyFilters(rows, filters), [rows, filters]);
  const familyOptions: { value: string; label: string }[] = useMemo(() => {
    const families = uniqueFamilies(rows);
    return [
      { value: ALL, label: "All families" },
      ...families.map((f) => ({ value: f, label: f })),
    ];
  }, [rows]);

  // Slice 224 — render the Scope pill options from the tenant's cells.
  // Cap at 50 entries; surface a banner above the table when the
  // tenant has more cells than the cap (AC-5). The cell label falls
  // back to a deterministic key=value summary if the cell has no
  // explicit `label` text on the wire.
  const allScopeCells = scopeCellsQ.data?.cells ?? [];
  const cellsCapped = allScopeCells.length > SCOPE_CELL_CAP;
  const cappedScopeCells = cellsCapped
    ? allScopeCells.slice(0, SCOPE_CELL_CAP)
    : allScopeCells;
  const scopeOptions: { value: string; label: string }[] = useMemo(() => {
    const opts: { value: string; label: string }[] = [
      { value: ALL, label: "All cells" },
    ];
    for (const c of cappedScopeCells) {
      const fallback = Object.entries(c.dimensions)
        .sort(([a], [b]) => a.localeCompare(b))
        .map(([k, v]) => `${k}=${v}`)
        .join(" / ");
      opts.push({ value: c.id, label: c.label || fallback || c.id });
    }
    return opts;
  }, [cappedScopeCells]);

  const pills: FilterPill[] = [
    {
      id: "framework",
      label: "Framework",
      value: filters.framework,
      options: FRAMEWORK_OPTIONS,
    },
    {
      id: "family",
      label: "Family",
      value: filters.family,
      options: familyOptions,
    },
    {
      id: "result",
      label: "State",
      value: filters.result,
      options: RESULT_OPTIONS,
    },
    {
      id: "freshness",
      label: "Freshness",
      value: filters.freshness,
      options: FRESHNESS_OPTIONS,
    },
    // Slice 224 — fifth pill, scope cell filter. Options enumerate
    // the tenant's own cells (RLS-scoped). Selecting a cell sets
    // ?scope=<id> and the BFF / upstream applies the intersection.
    {
      id: "scope",
      label: "Scope",
      value: filters.scope,
      options: scopeOptions,
    },
  ];

  const meta = (
    <span>
      Showing{" "}
      <span className="text-foreground font-medium">{visible.length}</span> of{" "}
      <span className="font-mono">{rows.length}</span> SCF anchors
    </span>
  );

  const columns: ListColumn<AnchorRow>[] = [
    {
      id: "scf_id",
      header: "SCF anchor",
      cell: (row) => (
        <Link
          href={`/controls/${encodeURIComponent(row.anchor.id)}`}
          className="font-mono text-xs font-semibold text-primary hover:underline"
          data-testid="controls-row-scf-id"
          onClick={(e) => e.stopPropagation()}
        >
          {row.anchor.scf_id}
        </Link>
      ),
    },
    {
      id: "name",
      header: "Name",
      cell: (row) => (
        <Link
          href={`/controls/${encodeURIComponent(row.anchor.id)}`}
          className="text-foreground hover:text-primary"
          onClick={(e) => e.stopPropagation()}
        >
          {row.anchor.name}
        </Link>
      ),
    },
    {
      id: "family",
      header: "Family",
      cell: (row) => (
        <span className="text-muted-foreground">{row.anchor.family}</span>
      ),
    },
    {
      id: "result",
      header: "State",
      cell: (row) =>
        row.state ? (
          <span className="font-mono text-xs">{row.state.result}</span>
        ) : (
          <span className="text-muted-foreground">—</span>
        ),
    },
    {
      id: "freshness_status",
      header: "Freshness",
      cell: (row) =>
        row.state ? (
          <span className="text-xs text-muted-foreground">
            {row.state.freshness_status}
          </span>
        ) : (
          <span className="text-muted-foreground">—</span>
        ),
    },
    {
      id: "last_observed_at",
      header: "Last observed",
      cell: (row) =>
        row.state?.last_observed_at ? (
          <span className="font-mono text-xs text-muted-foreground">
            {row.state.last_observed_at}
          </span>
        ) : (
          <span className="text-muted-foreground">—</span>
        ),
    },
    // Slice 226 — Frameworks-per-row column. Right-aligned per the
    // mockup (`Plans/mockups/controls.html` line 197). Renders the
    // display abbreviations joined by `·`; empty set renders `—`
    // (AC-6). The abbreviation authority lives in the backend
    // (`internal/catalog/framework_codes.go`); the frontend is a pure
    // renderer (P0-226-2 — no slug→display map here).
    {
      id: "frameworks",
      header: "Frameworks",
      align: "right",
      cell: (row) =>
        row.frameworks.length > 0 ? (
          <span
            className="text-xs text-muted-foreground"
            data-testid="controls-row-frameworks"
          >
            {row.frameworks.join(FRAMEWORKS_JOIN_SEPARATOR)}
          </span>
        ) : (
          <span
            className="text-muted-foreground"
            data-testid="controls-row-frameworks-empty"
          >
            —
          </span>
        ),
    },
  ];

  // Slice 137: Export buttons (CSV / JSON / XLSX) wire to the BFF
  // proxy at `/api/controls/export?format=...`, which forwards to the
  // platform `GET /v1/controls/export` endpoint. Each link is an
  // `<a download>` so the browser honours the backend's
  // Content-Disposition filename; no client-side JS download flow.
  //
  // Slice 175: History export buttons (CSV / JSON / XLSX) wire to a
  // sibling BFF proxy at `/api/controls/history/export?format=...`,
  // which forwards to `GET /v1/controls/history/export`. Same link
  // shape — distinguished by the "Export History …" label and the
  // `controls-history-export-*` data-testid. The history export
  // returns 17 columns (slice 137's 15 + superseded_by + superseded_at)
  // covering every version of every bundle, active + superseded.
  const actions = (
    <>
      {CONTROLS_EXPORT_FORMATS.map((format) => (
        <a
          key={format}
          href={buildControlsExportURL(format)}
          download
          rel="noopener"
          className={buttonVariants({ variant: "outline", size: "sm" })}
          data-testid={`controls-export-${format}`}
        >
          Export {CONTROLS_EXPORT_FORMAT_LABELS[format]}
        </a>
      ))}
      {CONTROLS_HISTORY_EXPORT_FORMATS.map((format) => (
        <a
          key={`history-${format}`}
          href={buildControlsHistoryExportURL(format)}
          download
          rel="noopener"
          className={buttonVariants({ variant: "outline", size: "sm" })}
          data-testid={`controls-history-export-${format}`}
        >
          Export History {CONTROLS_HISTORY_EXPORT_FORMAT_LABELS[format]}
        </a>
      ))}
      <Button size="sm" disabled>
        New control
      </Button>
    </>
  );

  if (anchorsQ.isLoading) {
    return (
      <ListPage
        title="Controls"
        subtitle="SCF anchors evaluated against live evidence"
        actions={actions}
        filterRow={
          <FilterPills pills={pills} onChange={() => {}} meta={meta} />
        }
      >
        <ListLoadingSkeleton />
      </ListPage>
    );
  }

  if (anchorsQ.isError) {
    return (
      <ListPage
        title="Controls"
        subtitle="SCF anchors evaluated against live evidence"
        actions={actions}
      >
        <Alert variant="destructive" data-testid="controls-load-error">
          <AlertTitle>Could not load controls</AlertTitle>
          <AlertDescription>
            {(anchorsQ.error as Error).message}
          </AlertDescription>
        </Alert>
      </ListPage>
    );
  }

  // Slice 152 D1-b: distinguish truly-zero (the anchor catalog itself
  // returned zero rows — defensive; on main today anchors are catalog-
  // global so this only fires if the SCF importer has not been run) from
  // filter-narrowed empty. The truly-zero copy is HONEST about cause
  // (catalog not seeded) and offers documentation orientation, NOT a
  // vapor "use the SOC 2 starter kit" CTA — there is no in-app button
  // for kit ingestion on main and slice 152 does not invent one
  // (decisions log D-152-1 + ADR-0004).
  const isTrulyEmpty = rows.length === 0;
  const trulyEmptyState = (
    <EmptyState
      icon={emptyStateIcon}
      title="No controls in your tenant yet"
      body="The global SCF anchor catalog is empty in this deployment. Import a framework via the atlas CLI, or run the SCF importer to populate the catalog. See the user guide for the bootstrap walkthrough."
    />
  );
  const filterEmptyState = (
    <EmptyState
      icon={emptyStateIcon}
      title="No controls match these filters"
      body="Try widening the framework, family, or state filters."
      cta={
        isDefault(filters)
          ? undefined
          : { label: "Clear filters", onClick: clearAll }
      }
    />
  );
  const emptyState = isTrulyEmpty ? trulyEmptyState : filterEmptyState;

  return (
    <ListPage
      title="Controls"
      subtitle="SCF anchors evaluated against live evidence"
      actions={actions}
      filterRow={
        <FilterPills
          pills={pills}
          onChange={(id, v) => updateFilter(id as keyof ControlFilters, v)}
          meta={meta}
        />
      }
    >
      {cellsCapped ? (
        <Alert data-testid="controls-scope-cells-capped" className="mb-3">
          <AlertTitle>Scope filter capped at {SCOPE_CELL_CAP} cells</AlertTitle>
          <AlertDescription>
            Your tenant has {allScopeCells.length} scope cells; only the first{" "}
            {SCOPE_CELL_CAP} are listed in the Scope filter. A typeahead
            replacement for larger tenants is on the follow-on backlog.
          </AlertDescription>
        </Alert>
      ) : null}
      <ListTable<AnchorRow>
        columns={columns}
        rows={visible}
        rowKey={(row) => row.anchor.id}
        onRowClick={(row) =>
          router.push(`/controls/${encodeURIComponent(row.anchor.id)}`)
        }
        emptyFallback={emptyState}
      />
    </ListPage>
  );
}

// Slice 152: shared empty-state icon node lifted out of the page body
// so both the truly-zero and filter-narrowed empty-states render with
// the same heroicon (slice 098 §2 — "centered illustration" pattern).
const emptyStateIcon = (
  <svg
    className="w-12 h-12 mx-auto"
    viewBox="0 0 24 24"
    fill="none"
    stroke="currentColor"
    strokeWidth="1.5"
    aria-hidden
  >
    <path
      d="M9 3.75H6.912a2.25 2.25 0 00-2.15 1.588L2.35 13.177a2.25 2.25 0 00-.1.661V18a2.25 2.25 0 002.25 2.25h15a2.25 2.25 0 002.25-2.25v-4.162c0-.224-.034-.447-.1-.661L19.24 5.338a2.25 2.25 0 00-2.15-1.588H15M2.25 13.5h3.86a2.25 2.25 0 012.012 1.244l.256.512a2.25 2.25 0 002.013 1.244h3.218a2.25 2.25 0 002.013-1.244l.256-.512a2.25 2.25 0 012.013-1.244h3.859"
      strokeLinecap="round"
      strokeLinejoin="round"
    />
  </svg>
);

export default function ControlsListPage() {
  // useSearchParams must be wrapped in Suspense in App Router (Next 16
  // strict-mode requirement). The fallback is the same skeleton the
  // query-pending state shows, so the perceived layout is stable.
  return (
    <Suspense fallback={<ListLoadingSkeleton />}>
      <ControlsPageInner />
    </Suspense>
  );
}
