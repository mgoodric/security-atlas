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
import { Button } from "@/components/ui/button";
import {
  fetchControlsList,
  type AnchorWithState,
  type ControlsListResponse,
} from "@/lib/api";

import {
  ALL,
  applyFilters,
  clearFilters,
  DEFAULT_FILTERS,
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
];

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

  const anchorsQ = useQuery<ControlsListResponse>({
    queryKey: ["controls", "list"],
    queryFn: fetchControlsList,
  });

  // Convert the anchor wire payload into the join-ready row shape used
  // by the filter logic + table renderer. Slice 104 attaches a real
  // state cell per anchor (or `null` when the tenant has no control
  // instantiated for the anchor). The shape was split in slice 098 for
  // exactly this hand-off — no page-level rework needed.
  const rows: AnchorRow[] = useMemo(() => {
    const anchors: AnchorWithState[] = anchorsQ.data?.anchors ?? [];
    return anchors.map<AnchorRow>((a) => {
      const { state, ...anchor } = a;
      return {
        anchor,
        state: state
          ? {
              result: state.result,
              freshness_status: state.freshness_status,
              last_observed_at: state.last_observed_at,
            }
          : null,
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
  ];

  const actions = (
    <>
      <Button variant="outline" size="sm" disabled>
        Export CSV
      </Button>
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
            d="M9 3.75H6.912a2.25 2.25 0 00-2.15 1.588L2.35 13.177a2.25 2.25 0 00-.1.661V18a2.25 2.25 0 002.25 2.25h15a2.25 2.25 0 002.25-2.25v-4.162c0-.224-.034-.447-.1-.661L19.24 5.338a2.25 2.25 0 00-2.15-1.588H15M2.25 13.5h3.86a2.25 2.25 0 012.012 1.244l.256.512a2.25 2.25 0 002.013 1.244h3.218a2.25 2.25 0 002.013-1.244l.256-.512a2.25 2.25 0 012.013-1.244h3.859"
            strokeLinecap="round"
            strokeLinejoin="round"
          />
        </svg>
      }
      title="No controls match these filters"
      body="Try widening the framework, family, or state filters."
      cta={
        isDefault(filters)
          ? undefined
          : { label: "Clear filters", onClick: clearAll }
      }
    />
  );

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
