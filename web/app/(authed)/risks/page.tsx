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
//
// Slice 185 amendment — UI honesty (F-178-5 closure):
//   The row-click affordance was removed. Previously rows routed to
//   `/risks/hierarchy?focus=<id>` as a "no 404" stand-in for a
//   per-risk detail page, creating an honesty-gap (the row promised
//   a detail destination it could not deliver). Replaced by:
//     1. an explicit per-row "View in hierarchy" link in a new
//        `actions` column (AC-2 — preserves the prior workflow);
//     2. a banner above the table (AC-3 — "Per-risk detail page is
//        a future slice");
//     3. removal of `onRowClick` from the `<ListTable>` call site,
//        which makes the `ListTable` primitive drop the
//        `cursor-pointer` class automatically (AC-1).
//   Option B (ship `/risks/[id]/page.tsx`) stays as a separate
//   future slice per P0-185-1.

import { useQuery } from "@tanstack/react-query";
import Link from "next/link";
import { useRouter, useSearchParams } from "next/navigation";
import { Suspense, useMemo } from "react";

import {
  EmptyState,
  FilterPills,
  ListLoadingSkeleton,
  ListPage,
  ListPagination,
  ListTable,
  paginateRows,
  type FilterPill,
  type ListColumn,
} from "@/components/list";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { buttonVariants } from "@/components/ui/button";
import { fetchHierarchyOrgUnits, type OrgUnit } from "@/lib/api/risk-hierarchy";
import {
  fetchRisksList,
  type Risk,
  type RisksListResponse,
} from "@/lib/api/risks";
import {
  RISK_EXPORT_FORMATS,
  RISK_EXPORT_FORMAT_LABELS,
  buildRiskExportURL,
} from "@/lib/api/risks-export";

import { risksCountLabel } from "./count-label";
import {
  ALL,
  applyFilters,
  clearFilters,
  DEFAULT_FILTERS,
  formatResidualScore,
  residualClass,
  residualState,
  reviewDuePending,
  setFilter,
  severityBand,
  severityClasses,
  uniqueOwners,
  type RiskFilters,
} from "./filters";
import {
  DEFAULT_SORT,
  nextSortState,
  parseSortState,
  serializeSortState,
  sortRisks,
  type SortKey,
  type SortState,
} from "./sort";
import { SortableHeader } from "./sortable-header";

// Slice 244 — six filter keys. Order mirrors the mockup
// (Plans/_archive/mockups/risks.html lines 126-173) with one local deviation:
// the slice-100 Severity pill is a net positive over the mockup and is
// retained per anti-criterion P0-244-1. It sits between Org unit and
// Owner so it neighbours the related risk-scoring controls without
// disturbing the mockup-pill visual order.
const FILTER_KEYS: (keyof RiskFilters)[] = [
  "category",
  "treatment",
  "methodology",
  "org_unit",
  "severity",
  "owner",
];

// Slice 246 — page-size default per AC-3.
//
// Per anti-criterion P0-246-4 this constant lives at module scope so it
// is greppable; component code references `RISKS_PAGE_SIZE` rather than
// inlining `50`. A future slice that promotes pagination to server-side
// LIMIT/OFFSET will swap the constant for an API-derived value without
// touching the JSX.
const RISKS_PAGE_SIZE = 50;

// Slice 246 — URL query-string key for the 1-indexed page index. The
// `page` key is sibling to the filter keys above; the filter-change
// handlers below explicitly DROP it on every filter mutation (AC-5 —
// page index resets to 1 when a filter changes, preserving the user's
// mental model).
const PAGE_PARAM = "page";

// Slice 681 / ATLAS-039 — URL query-string key for the active column
// sort (shape `"<key>:<dir>"`, e.g. `severity:asc`). Sibling to the
// filter keys + the `page` key. A filter or sort change resets the page
// index to 1 (the user's mental model: re-ordering the set starts at the
// top). The default sort (severity desc) is treated as "no param" so the
// canonical register URL stays clean until a non-default sort is chosen.
const SORT_PARAM = "sort";

// Slice 680 / ATLAS-038 — column-header clarity copy.
//
// The "Severity" and "Residual" columns are INDEPENDENT axes, and the
// audit flagged that they read as inconsistent (the same residual maps
// to different severities across seeded risks). They are NOT a scoring
// bug — per canvas §6.2 they measure different things:
//
//   - INHERENT SEVERITY = likelihood × impact on the 5×5 grid, BEFORE
//     any control mitigation (the risk's raw exposure).
//   - RESIDUAL = inherent × (1 − control_effectiveness) — the exposure
//     AFTER the linked controls' measured effectiveness, normalized to
//     0..1.
//
// Two risks with the same inherent severity legitimately carry
// different residuals (different controls); two risks with the same
// residual legitimately carry different inherent severities. The
// headers now name the axis explicitly and a native `title` tooltip
// spells out the relationship so the columns no longer read as a
// scoring inconsistency. Copy/label only — the scoring model is
// unchanged (anti-criterion).
const SEVERITY_HEADER_TOOLTIP =
  "Inherent severity: likelihood × impact on the 5×5 grid, before any control mitigation. Independent of the residual column.";
const RESIDUAL_HEADER_TOOLTIP =
  "Residual: inherent severity reduced by the linked controls' measured effectiveness (0..1). Two risks with the same inherent severity can have different residuals, and vice versa.";

// Slice 680 / ATLAS-029 — pending-evaluation affordance copy.
//
// A newly-created risk has no residual_score or review_due_at until the
// evaluator backfills them. The old cell rendered a bare "—", which
// reads as broken. We render an explicit "Pending evaluation" label
// (with a tooltip) instead so the empty state reads as "awaiting the
// evaluator", not "missing data".
const PENDING_EVAL_LABEL = "Pending evaluation";
const PENDING_EVAL_TOOLTIP =
  "Awaiting the risk evaluator. Inherent severity computes immediately; the residual and review-due date are backfilled once controls are evaluated.";

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

// Slice 244 — wire-enum-backed option lists.
//
// Decision D1 (see docs/audit-log/244-decisions.md): the mockup
// labels for the Category pill (Operational / Compliance /
// Third-party / Strategic) do not match the wire enum `risk_category`,
// which is the CIA-Privacy-axis seven-value enum below. The wire is
// the source of truth — exact-string match (AC-3) against any mockup
// label would never hit because no risk row carries those strings.
// The mockup is non-canonical and the discrepancy is captured as a
// follow-up item in the slice's decision log.
const CATEGORY_OPTIONS: { value: string; label: string }[] = [
  { value: ALL, label: "All categories" },
  { value: "confidentiality", label: "confidentiality" },
  { value: "integrity", label: "integrity" },
  { value: "availability", label: "availability" },
  { value: "privacy", label: "privacy" },
  { value: "regulatory", label: "regulatory" },
  { value: "operational", label: "operational" },
  { value: "financial", label: "financial" },
];

// Decision D2: the mockup shows `five_by_five` but the wire enum
// `risk_methodology` carries `qualitative_5x5` for the same concept.
// All five wire values are exposed.
const METHODOLOGY_OPTIONS: { value: string; label: string }[] = [
  { value: ALL, label: "All methodologies" },
  { value: "nist_800_30", label: "nist_800_30" },
  { value: "fair", label: "fair" },
  { value: "cis_ram", label: "cis_ram" },
  { value: "iso_27005", label: "iso_27005" },
  { value: "qualitative_5x5", label: "qualitative_5x5" },
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

  // Slice 246 — current page index. URL is the source of truth so the
  // page is bookmarkable and survives refresh. Invalid / missing /
  // negative values fall back to 1; the rendering math in
  // `paginationBounds` further clamps an out-of-range page to the
  // last available page so a stale bookmark survives a register that
  // shrunk between visits.
  const currentPage: number = useMemo(() => {
    const raw = search.get(PAGE_PARAM);
    if (!raw) return 1;
    const parsed = Number.parseInt(raw, 10);
    if (!Number.isFinite(parsed) || parsed < 1) return 1;
    return parsed;
  }, [search]);

  // Slice 681 — active column sort, URL-driven so it is bookmarkable and
  // survives refresh (same posture as the filter + page state). A
  // missing / malformed param resolves to DEFAULT_SORT (severity desc).
  const sortState: SortState = useMemo(
    () => parseSortState(search.get(SORT_PARAM)),
    [search],
  );

  // Slice 681 — header-click handler. Toggles the clicked column's
  // direction (or switches to it, descending-first) and writes the
  // result to the URL. The default sort is dropped from the URL so the
  // canonical register URL has no `sort` key; any other sort serialises
  // to `?sort=<key>:<dir>`. Re-sorting resets the page index to 1.
  const updateSort = (clicked: SortKey) => {
    const next = nextSortState(sortState, clicked);
    const sp = new URLSearchParams(search.toString());
    if (next.key === DEFAULT_SORT.key && next.dir === DEFAULT_SORT.dir) {
      sp.delete(SORT_PARAM);
    } else {
      sp.set(SORT_PARAM, serializeSortState(next));
    }
    sp.delete(PAGE_PARAM);
    router.replace(`/risks?${sp.toString()}`);
  };

  const updateFilter = (key: keyof RiskFilters, value: string) => {
    const next = setFilter(filters, key, value);
    const sp = new URLSearchParams(search.toString());
    if (next[key] === ALL) {
      sp.delete(key);
    } else {
      sp.set(key, next[key]);
    }
    // AC-5: filter changes reset the page index to 1. The URL key is
    // dropped (page=1 is the default and need not be serialised) so
    // the URL stays clean when no pagination is in play.
    sp.delete(PAGE_PARAM);
    router.replace(`/risks?${sp.toString()}`);
  };

  const clearAll = () => {
    const cleared = clearFilters();
    const sp = new URLSearchParams(search.toString());
    for (const k of FILTER_KEYS) {
      if (cleared[k] === ALL) sp.delete(k);
    }
    // AC-2 / AC-5: clearing filters also resets pagination.
    sp.delete(PAGE_PARAM);
    router.replace(`/risks?${sp.toString()}`);
  };

  // Slice 246 — page-change handler. Writes the new 1-indexed page to
  // the URL; page=1 is omitted so the canonical URL of the first page
  // matches the no-pagination URL exactly.
  const goToPage = (page: number) => {
    const sp = new URLSearchParams(search.toString());
    if (page <= 1) {
      sp.delete(PAGE_PARAM);
    } else {
      sp.set(PAGE_PARAM, String(page));
    }
    router.replace(`/risks?${sp.toString()}`);
  };

  const risksQ = useQuery<RisksListResponse>({
    queryKey: ["risks", "list"],
    queryFn: fetchRisksList,
  });

  // Slice 244 — Org unit pill options need org-unit *names*, not just
  // the `org_unit_id` UUIDs the risk rows carry. Reuse the existing
  // browser-side fetcher (`fetchHierarchyOrgUnits`) so we do NOT
  // introduce a new BFF route (anti-criterion P0-244-3). Stale time of
  // 5 minutes — org unit data rarely changes during a session and a
  // refetch on every nav is wasteful.
  const orgUnitsQ = useQuery<OrgUnit[]>({
    queryKey: ["risks", "org-units"],
    queryFn: fetchHierarchyOrgUnits,
    staleTime: 5 * 60 * 1000,
  });

  const rows: Risk[] = useMemo(() => risksQ.data?.risks ?? [], [risksQ.data]);

  const filtered = useMemo(() => applyFilters(rows, filters), [rows, filters]);

  // Slice 681 / ATLAS-039 — apply the active column sort AFTER filtering
  // and BEFORE pagination, so the page slice the table renders is the
  // correctly-ordered window. `sortRisks` is pure (returns a new array)
  // so the memoized `filtered` array is never mutated.
  const visible = useMemo(
    () => sortRisks(filtered, sortState),
    [filtered, sortState],
  );

  // Slice 246 — client-side page slice over the filtered set. Per
  // P0-246-1 the v1 wire `GET /v1/risks` ships the full list; the
  // table consumes the per-page slice rather than the full `visible`
  // array. The pagination footer below the table emits page-change
  // events through `goToPage` which round-trip through the URL.
  const pagedRows = useMemo(
    () => paginateRows(visible, currentPage, RISKS_PAGE_SIZE),
    [visible, currentPage],
  );

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

  // Slice 244 — Org unit options derive from the unique `org_unit_id`
  // set on the loaded rows (same pattern as `ownerOptions`), then join
  // client-side to OrgUnit names so the pill displays "Platform"
  // rather than a bare UUID. Rows with no `org_unit_id` are skipped —
  // the spec does not call for an "unassigned" org-unit bucket, and
  // the filter is by exact `org_unit_id` against the wire.
  const orgUnitOptions: { value: string; label: string }[] = useMemo(() => {
    const ids = new Set<string>();
    for (const r of rows) {
      if (r.org_unit_id) ids.add(r.org_unit_id);
    }
    const nameById = new Map<string, string>();
    for (const u of orgUnitsQ.data ?? []) {
      nameById.set(u.id, u.name);
    }
    const sorted = Array.from(ids)
      .map((id) => ({ id, name: nameById.get(id) ?? id }))
      .sort((a, b) => a.name.localeCompare(b.name));
    return [
      { value: ALL, label: "All units" },
      ...sorted.map(({ id, name }) => ({ value: id, label: name })),
    ];
  }, [rows, orgUnitsQ.data]);

  const pills: FilterPill[] = [
    {
      id: "category",
      label: "Category",
      value: filters.category,
      options: CATEGORY_OPTIONS,
    },
    {
      id: "treatment",
      label: "Treatment",
      value: filters.treatment,
      options: TREATMENT_OPTIONS,
    },
    {
      id: "methodology",
      label: "Methodology",
      value: filters.methodology,
      options: METHODOLOGY_OPTIONS,
    },
    {
      id: "org_unit",
      label: "Org unit",
      value: filters.org_unit,
      options: orgUnitOptions,
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

  // Slice 684 — header count semantics fix (mirrors slice 666 on
  // /controls). Previously the header read "Showing {visible} of {rows}
  // risks" while the shared `<ListPagination>` footer reads "Showing M–N
  // of TOTAL" — both used the verb "Showing" but meant different things,
  // so read together the header implied all rows were on screen while
  // the footer paginated only the first page. The header now drops the
  // verb and renders a plain COUNT of the filtered register; the footer
  // keeps sole ownership of the page-range phrasing. The filtered count
  // drives the header (AC-3) and is the same number the footer
  // paginates over, so the two now read consistently. Copy/semantics
  // only — page size + counts are unchanged (anti-criterion). See
  // `docs/audit-log/684-risks-count-semantics-decisions.md`.
  const countLabel = risksCountLabel(visible.length, rows.length);
  const meta = (
    <span data-testid="risks-count-label">
      {countLabel.isFiltered ? (
        <>
          <span className="text-foreground font-medium">
            {countLabel.filtered}
          </span>{" "}
          of <span className="font-mono">{countLabel.total}</span> risks
        </>
      ) : (
        <>
          <span className="text-foreground font-medium">
            {countLabel.total}
          </span>{" "}
          risks
        </>
      )}
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
      // Slice 681 / ATLAS-039 (AC-2): the title is now a link to the
      // read-only per-risk detail at `/risks/{id}`. Slice 185 removed the
      // old implicit row-click (it dishonestly routed to the hierarchy);
      // this restores a drill-in that truthfully advertises its
      // destination, now that the detail route exists (decisions log D2).
      cell: (row) => (
        <Link
          href={`/risks/${encodeURIComponent(row.id)}`}
          className="text-primary hover:underline"
          data-testid="risks-row-title"
        >
          {row.title}
        </Link>
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
      // Slice 680 / ATLAS-038: name the axis ("after controls") + tooltip
      // so it doesn't read as inconsistent with the inherent-severity
      // column. Independent axes (canvas §6.2).
      // Slice 681 / ATLAS-039: the header is now a sortable button. The
      // slice-680 axis-disambiguation tooltip moves to the button's
      // native `title`; pending (un-scored) rows sort to the end.
      header: (
        <SortableHeader
          sortKey="residual"
          label="Residual (after controls)"
          title={RESIDUAL_HEADER_TOOLTIP}
          state={sortState}
          onSort={updateSort}
        />
      ),
      cell: (row) => {
        // Slice 680 / ATLAS-029: a brand-new risk has no residual yet —
        // render an explicit "Pending evaluation" affordance rather than
        // a bare "—" that reads as broken.
        if (residualState(row.residual_score) === "pending") {
          return (
            <span
              className="text-xs italic text-muted-foreground"
              title={PENDING_EVAL_TOOLTIP}
              data-testid="risks-row-residual-pending"
            >
              {PENDING_EVAL_LABEL}
            </span>
          );
        }
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
      // Slice 680 / ATLAS-038: "Inherent severity" names the axis (before
      // controls) so it doesn't read as inconsistent with the residual
      // (after controls) column. Independent axes (canvas §6.2).
      // Slice 681 / ATLAS-039: sortable; this is the DEFAULT sort
      // (descending — worst exposure first).
      header: (
        <SortableHeader
          sortKey="severity"
          label="Inherent severity"
          title={SEVERITY_HEADER_TOOLTIP}
          state={sortState}
          onSort={updateSort}
        />
      ),
      cell: (row) => {
        const band = severityBand(row.severity);
        return (
          <span
            className={`inline-flex items-center justify-center w-6 h-6 text-[11px] font-semibold rounded ${severityClasses(
              band,
            )}`}
            data-testid="risks-row-severity"
            title="Inherent severity (likelihood × impact, before controls)"
          >
            {row.severity}
          </span>
        );
      },
    },
    {
      id: "review_due_at",
      // Slice 681 / ATLAS-039: sortable by review-due date. Pending
      // (un-dated, newly-created) rows sort to the end in both
      // directions so "next review soonest" never surfaces an
      // un-evaluated risk first.
      header: (
        <SortableHeader
          sortKey="review_due"
          label="Review due"
          state={sortState}
          onSort={updateSort}
        />
      ),
      cell: (row) =>
        // Slice 680 / ATLAS-029: an unset review-due date on a new risk is
        // "Pending evaluation", not a bare "—". A real date renders as-is.
        reviewDuePending(row.review_due_at) ? (
          <span
            className="text-xs italic text-muted-foreground"
            title={PENDING_EVAL_TOOLTIP}
            data-testid="risks-row-review-due-pending"
          >
            {PENDING_EVAL_LABEL}
          </span>
        ) : (
          <span className="text-xs text-muted-foreground">
            {row.review_due_at!.slice(0, 10)}
          </span>
        ),
    },
    // Slice 185 (AC-2): explicit per-row "View in hierarchy" link
    // replaces the implicit row-click affordance. The link preserves
    // the existing `?focus=<id>` workflow (P0-185-2) so users who
    // relied on the row-click reaching the hierarchy view still have
    // a one-click path; the difference is that the affordance now
    // truthfully advertises its destination instead of pretending to
    // be a row-as-detail link.
    {
      id: "actions",
      header: "",
      align: "right",
      cell: (row) => (
        <Link
          href={`/risks/hierarchy?focus=${encodeURIComponent(row.id)}`}
          data-testid="risks-row-hierarchy-link"
          className="text-xs text-primary hover:underline"
        >
          View in hierarchy →
        </Link>
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
      {/* Slice 247 — honesty fix. Previously rendered as
          `<Button size="sm" disabled>` with no tooltip / banner / route,
          even though `/risks/new` exists (slice 105) and the empty-state
          CTA already routes there. Same shadcn Button shape via
          `buttonVariants({ size: "sm" })`, wrapped in a Next `<Link>` to
          `/risks/new`. Matches the /vendors page header pattern. */}
      <Link
        href="/risks/new"
        data-testid="risks-new-link"
        className={buttonVariants({ size: "sm" })}
      >
        New risk
      </Link>
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
          ? "Try widening the category, treatment, methodology, org unit, severity, or owner filters."
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

  // Slice 185 (AC-1, AC-3): the row-click affordance is intentionally
  // ABSENT. The previous implementation routed `onRowClick` to
  // `/risks/hierarchy?focus=<id>` as a "no 404" stand-in for a
  // per-risk detail page that does not yet exist. That created an
  // honesty-gap: the row advertised "click to view risk detail" but
  // delivered the hierarchy view. The fix is to remove the row-level
  // affordance entirely; the explicit per-row "View in hierarchy"
  // link (AC-2, see the `actions` column above) preserves the
  // existing workflow. The future per-risk detail page is a separate
  // slice (Option B in slice 185's spec); when it ships, this page
  // gains a per-row link to `/risks/${id}` without re-introducing
  // row-as-link semantics.

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
      {/* Slice 681 (AC-2): the slice-185 "Per-risk detail page is a
          future slice" banner is REMOVED — the read-only detail route
          now exists (`/risks/{id}`) and the title links to it. The
          per-row "View in hierarchy" link is retained for the org-tree
          scoping workflow (it sits alongside, not instead of, the
          title drill-in). See decisions log D2. */}
      <ListTable<Risk>
        columns={columns}
        rows={pagedRows}
        rowKey={(row) => row.id}
        emptyFallback={emptyState}
        // Slice 281 — collapse to a card stack at `< md`. The risks
        // table carries 9 columns (id / title / category / treatment
        // / owner / residual / severity / review_due / actions); it
        // horizontal-scrolls badly at 375px and the per-row "View in
        // hierarchy" link (slice 185 AC-2) is unreachable without
        // scrolling. The card variant surfaces every column inline as
        // label/value pairs. Desktop UX is unchanged at `≥ md`
        // (P0-281-1).
        mobileMode="cards"
      />
      {/* Slice 246 — pagination footer. Rendered ONLY when there is at
          least one row in the filtered set; an empty result delegates
          to the `emptyFallback` above and a stand-alone pagination
          chrome would be honesty-confusing (Previous / Next clicks
          would no-op). On a single-page result the footer still
          renders so the user gets the truth-telling "Showing N of N"
          summary; both Previous and Next are disabled and clearly
          read as such. */}
      {visible.length > 0 ? (
        <ListPagination
          currentPage={currentPage}
          pageSize={RISKS_PAGE_SIZE}
          totalCount={visible.length}
          onPageChange={goToPage}
          testIdPrefix="risks-pagination"
        />
      ) : null}
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
