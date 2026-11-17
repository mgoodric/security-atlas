"use client";

// Slice 100 — /risks, the risk register.
//
// #2132: the register is filtered, sorted and paged on the server. The page
// reads its filters, sort, page and columns from the URL, sends them to
// `GET /v1/risks` through the BFF at /api/risks, and renders the one page
// it gets back. No filtering or sorting happens in the browser.
//
//   - Filters: status (open by default; closed and all on request), owner
//     (a person or unowned), treatment, category, review due (overdue, due
//     in 14 days, a date range), methodology and org unit. Each URL key is
//     the platform parameter of the same name.
//   - Sort: every column header; residual, highest first, by default. The
//     URL `sort` is the platform's `sort`.
//   - Columns: ID, Title, Category, Owner, Residual, Treatment, Review due
//     and Status by default, sized to fit 1316px without sideways scroll.
//     The column chooser adds methodology, level, org unit, themes and
//     created (`?cols=`).
//   - Rows open the risk.
//   - Saved views (#2133): the filters, sort, roll-ups toggle and columns,
//     saved per user under the `risks` surface of `/v1/saved-views` and
//     applied by putting them back on the URL.
//
// Constitutional invariants honored:
//   - Invariant 6 (tenant isolation): the BFF forwards the bearer cookie;
//     the platform enforces tenant isolation with RLS and an explicit
//     tenant_id. The UI never passes a tenant.

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import Link from "next/link";
import { useRouter, useSearchParams } from "next/navigation";
import {
  Suspense,
  useCallback,
  useEffect,
  useMemo,
  useState,
  type MouseEvent,
} from "react";

import {
  EmptyState,
  FilterPills,
  ListLoadingSkeleton,
  ListPage,
  ListPagination,
  ListTable,
  type FilterPill,
  type ListColumn,
} from "@/components/list";
import { Term } from "@/components/help/term";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { buttonVariants } from "@/components/ui/button";
import { Person, usePeopleDirectory } from "@/components/ui/person";
import { resolvePerson } from "@/lib/person";
import { APIError } from "@/lib/api/base";
import {
  createSavedView,
  deleteSavedView,
  listSavedViews,
  type ServerSavedView,
} from "@/lib/api/saved-views";
import { fetchHierarchyOrgUnits, type OrgUnit } from "@/lib/api/risk-hierarchy";
import {
  fetchRegisterPage,
  fetchRiskCategories,
  fetchRiskRegisterTotal,
  treatmentLabel,
  type Risk,
  type RiskCategory,
  type RisksListResponse,
} from "@/lib/api/risks";
import {
  RISK_EXPORT_FORMATS,
  RISK_EXPORT_FORMAT_LABELS,
  buildRiskExportURL,
} from "@/lib/api/risks-export";
import { enumLabel, enumOptions } from "@/lib/labels/enum-labels";
import { pageTitle } from "@/lib/page-titles";

import { SavedViewsBar } from "../controls/controls-toolbar";
import { risksCountLabel } from "./count-label";
import {
  categoryDisplay,
  categoryOptions,
  fairScore,
  formatDollars,
  formatResidualScore,
  isUnscored,
  patternNoLongerMet,
  residualClass,
  residualSeverity,
  residualState,
  reviewDuePending,
  reviewOverdue,
  severityBand,
  severityBandLabel,
  UNSCORED_LABEL,
} from "./filters";
import {
  ALL,
  clampedPage,
  COLUMNS_PARAM,
  DEFAULT_FILTERS,
  DEFAULT_STATUS,
  filtersFromParams,
  FILTER_KEYS,
  isDefaultFilters,
  OPTIONAL_COLUMN_LABELS,
  OPTIONAL_COLUMNS,
  parseColumns,
  registerQuery,
  shortRiskId,
  toggleColumn,
  type FilterKey,
  type OptionalColumn,
  type RegisterFilters,
} from "./server-query";
import {
  ROLLUPS_PARAM,
  SORT_PARAM,
  searchFromView,
  viewFromSearch,
} from "./saved-views";
import { SeverityPill } from "./severity-pill";
import {
  ariaSort,
  nextSortState,
  parseSortState,
  sortParam,
  type SortKey,
} from "./sort";
import { SortableHeader } from "./sortable-header";

// One page of the register. The platform caps a page at 500.
const RISKS_PAGE_SIZE = 50;

// URL keys beside the filters: the 1-indexed page, the sort, and the
// roll-ups (#2060) and archived-categories (#1817) toggles.
const PAGE_PARAM = "page";
const ARCHIVED_PARAM = "archived";

const RESIDUAL_HEADER_TOOLTIP =
  "Residual: inherent severity reduced by the linked controls' measured effectiveness, banded like inherent (Low 1–7, Medium 8–14, High 15–25). FAIR risks are in dollars and sort after the 5×5 risks. Hover a value for the inherent severity.";

const PENDING_EVAL_LABEL = "Pending evaluation";
const PENDING_EVAL_TOOLTIP =
  "Awaiting the risk evaluator. Inherent severity computes immediately; the residual is backfilled once controls are evaluated.";

const STATUS_OPTIONS = [
  { value: "open", label: "Open" },
  { value: "closed", label: "Closed" },
  { value: ALL, label: "Open and closed" },
];

const TREATMENT_OPTIONS = [
  { value: ALL, label: "All treatments" },
  ...enumOptions("RiskTreatment"),
];

const METHODOLOGY_OPTIONS = [
  { value: ALL, label: "All methodologies" },
  ...enumOptions("RiskMethodology"),
];

const REVIEW_DUE_OPTIONS = [
  { value: ALL, label: "Any date" },
  { value: "overdue", label: "Overdue" },
  { value: "due_soon", label: "Due in 14 days" },
];

const LEVEL_LABELS: Record<string, string> = {
  team: "Team",
  org: "Org",
  company: "Company",
};

// A link inside a clickable row: let it navigate on its own, without the
// row's click also pushing the risk.
function stopRowClick(e: MouseEvent) {
  e.stopPropagation();
}

function RisksPageInner() {
  const router = useRouter();
  const search = useSearchParams();

  const filters: RegisterFilters = useMemo(
    () => filtersFromParams(search),
    [search],
  );
  const sortState = useMemo(
    () => parseSortState(search.get(SORT_PARAM)),
    [search],
  );
  const columnsChosen = useMemo(
    () => parseColumns(search.get(COLUMNS_PARAM)),
    [search],
  );
  const currentPage: number = useMemo(() => {
    const parsed = Number.parseInt(search.get(PAGE_PARAM) ?? "", 10);
    return Number.isFinite(parsed) && parsed >= 1 ? parsed : 1;
  }, [search]);
  const showRollUps = search.get(ROLLUPS_PARAM) === "1";
  const showArchived = search.get(ARCHIVED_PARAM) === "1";

  // Every URL change goes through here. A change to what the list holds
  // (a filter, the sort, a toggle) resets the page to 1.
  const replace = (edit: (sp: URLSearchParams) => void, resetPage = true) => {
    const sp = new URLSearchParams(search.toString());
    edit(sp);
    if (resetPage) sp.delete(PAGE_PARAM);
    const qs = sp.toString();
    router.replace(qs ? `/risks?${qs}` : "/risks");
  };

  const updateFilter = (key: FilterKey, value: string) =>
    replace((sp) => {
      if (value === DEFAULT_FILTERS[key] || value === "") sp.delete(key);
      else sp.set(key, value);
    });

  const clearAll = () =>
    replace((sp) => {
      for (const k of FILTER_KEYS) sp.delete(k);
    });

  const updateSort = (clicked: SortKey) =>
    replace((sp) => {
      const next = sortParam(nextSortState(sortState, clicked));
      if (next) sp.set(SORT_PARAM, next);
      else sp.delete(SORT_PARAM);
    });

  const toggleFlag = (param: string, on: boolean) =>
    replace((sp) => {
      if (on) sp.delete(param);
      else sp.set(param, "1");
    });

  const toggleOptionalColumn = (col: OptionalColumn) =>
    replace((sp) => {
      const next = toggleColumn(columnsChosen, col);
      if (next) sp.set(COLUMNS_PARAM, next);
      else sp.delete(COLUMNS_PARAM);
    }, false);

  // #2133 — saved views. The query key is the surface's own, apart from
  // the controls list's.
  const queryClient = useQueryClient();
  const [activeViewId, setActiveViewId] = useState("");
  const savedViewsQ = useQuery<ServerSavedView[]>({
    queryKey: ["saved-views", "risks"],
    queryFn: () => listSavedViews("risks"),
  });
  const savedViews = useMemo(() => savedViewsQ.data ?? [], [savedViewsQ.data]);
  const currentView = useMemo(() => viewFromSearch(search), [search]);

  const saveViewMutation = useMutation({
    mutationFn: (name: string) => createSavedView(name, currentView, "risks"),
    onSuccess: (created) => {
      void queryClient.invalidateQueries({
        queryKey: ["saved-views", "risks"],
      });
      setActiveViewId(created.id);
    },
  });
  const deleteViewMutation = useMutation({
    mutationFn: (id: string) => deleteSavedView(id),
    onSuccess: (_void, id) => {
      void queryClient.invalidateQueries({
        queryKey: ["saved-views", "risks"],
      });
      if (activeViewId === id) setActiveViewId("");
    },
  });

  const onLoadView = useCallback(
    (id: string) => {
      setActiveViewId(id);
      const saved = savedViews.find((v) => v.id === id);
      const qs = saved ? searchFromView(saved.filters) : "";
      router.replace(qs ? `/risks?${qs}` : "/risks");
    },
    [savedViews, router],
  );

  const onSaveView = useCallback(
    async (
      name: string,
    ): Promise<{ ok: true } | { ok: false; message: string }> => {
      const trimmed = name.trim();
      if (trimmed.length === 0) {
        return { ok: false, message: "Enter a name for this view." };
      }
      try {
        await saveViewMutation.mutateAsync(trimmed);
        return { ok: true };
      } catch (err) {
        const message =
          err instanceof APIError && err.status === 409
            ? "A view with that name already exists."
            : err instanceof APIError && err.status === 422
              ? "Saved-view limit reached — delete one before saving another."
              : "Could not save the view. Try again.";
        return { ok: false, message };
      }
    },
    [saveViewMutation],
  );

  const goToPage = (page: number) =>
    replace((sp) => {
      if (page <= 1) sp.delete(PAGE_PARAM);
      else sp.set(PAGE_PARAM, String(page));
    }, false);

  // #1817 — the organisation's categories, archived included, so a risk
  // on an archived category still reads by name with its archived mark.
  const categoriesQ = useQuery<RiskCategory[]>({
    queryKey: ["risks", "categories", "with-archived"],
    queryFn: () => fetchRiskCategories(true),
    staleTime: 60_000,
  });
  const categories = useMemo(() => categoriesQ.data ?? [], [categoriesQ.data]);

  const orgUnitsQ = useQuery<OrgUnit[]>({
    queryKey: ["risks", "org-units"],
    queryFn: fetchHierarchyOrgUnits,
    staleTime: 5 * 60 * 1000,
  });
  const orgUnitName = useMemo(() => {
    const m = new Map<string, string>();
    for (const u of orgUnitsQ.data ?? []) m.set(u.id, u.name);
    return m;
  }, [orgUnitsQ.data]);

  const query = useMemo(
    () =>
      registerQuery({
        filters,
        sort: search.get(SORT_PARAM),
        rollups: showRollUps,
        page: currentPage,
        pageSize: RISKS_PAGE_SIZE,
      }),
    [filters, search, showRollUps, currentPage],
  );

  const risksQ = useQuery<RisksListResponse>({
    queryKey: ["risks", "list", query],
    queryFn: () => fetchRegisterPage(query),
    placeholderData: (prev) => prev,
  });

  // The register's unfiltered size, for "N of M": the platform's total for
  // the default register (open risks, no roll-ups), off a one-row page
  // (#2490). Its own key: the sidebar badge counts something else.
  const registerSizeQ = useQuery<number>({
    queryKey: ["risks", "register-size"],
    queryFn: fetchRiskRegisterTotal,
    staleTime: 60_000,
    retry: false,
  });

  const rows: Risk[] = useMemo(() => risksQ.data?.risks ?? [], [risksQ.data]);
  const total = risksQ.data?.total ?? rows.length;

  // A stale bookmark past the end of a shrunken set lands on the last
  // page that has rows.
  useEffect(() => {
    if (!risksQ.data || risksQ.isPlaceholderData) return;
    const clamp = clampedPage(currentPage, RISKS_PAGE_SIZE, total);
    if (clamp !== null && clamp !== currentPage) goToPage(clamp);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [risksQ.data, risksQ.isPlaceholderData, currentPage, total]);

  // #2129: an owner is a user, shown by name. The pill offers anyone,
  // unowned and every person in the directory; an owner in the URL the
  // directory does not know still shows.
  const people = usePeopleDirectory();
  const ownerOptions = useMemo(() => {
    const users = Array.from(people.data?.users.keys() ?? [])
      .map((id) => ({ value: id, label: resolvePerson(id, people.data).text }))
      .sort((a, b) => a.label.localeCompare(b.label));
    const opts = [
      { value: ALL, label: "Anyone" },
      { value: "none", label: "Unowned" },
      ...users,
    ];
    if (filters.owner !== ALL && !opts.some((o) => o.value === filters.owner)) {
      opts.push({ value: filters.owner, label: "A person" });
    }
    return opts;
  }, [people.data, filters.owner]);

  const orgUnitOptions = useMemo(() => {
    const units = (orgUnitsQ.data ?? [])
      .map((u) => ({ value: u.id, label: u.name }))
      .sort((a, b) => a.label.localeCompare(b.label));
    const opts = [{ value: ALL, label: "All units" }, ...units];
    if (
      filters.org_unit !== ALL &&
      !opts.some((o) => o.value === filters.org_unit)
    ) {
      opts.push({ value: filters.org_unit, label: "A unit" });
    }
    return opts;
  }, [orgUnitsQ.data, filters.org_unit]);

  const pills: FilterPill[] = [
    {
      id: "status",
      label: "Status",
      value: filters.status,
      options: STATUS_OPTIONS,
    },
    {
      id: "owner",
      label: "Owner",
      value: filters.owner,
      options: ownerOptions,
    },
    {
      id: "treatment",
      label: "Treatment",
      value: filters.treatment,
      options: TREATMENT_OPTIONS,
    },
    {
      id: "category",
      label: "Category",
      value: filters.category,
      options: categoryOptions(categories, showArchived, filters.category),
    },
    {
      id: "review_due",
      label: "Review due",
      value: filters.review_due,
      options: REVIEW_DUE_OPTIONS,
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
  ];

  const filtered =
    !isDefaultFilters(filters) ||
    showRollUps ||
    filters.status !== DEFAULT_STATUS;
  const registerSize = registerSizeQ.data ?? null;
  const countLabel =
    filtered && registerSize !== null && total <= registerSize
      ? risksCountLabel(total, registerSize)
      : risksCountLabel(total, total);

  const meta = (
    <span className="inline-flex flex-wrap items-center gap-3">
      <span data-testid="risks-count-label" title={countLabel.text}>
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
      <label className="inline-flex items-center gap-1.5 text-xs text-muted-foreground">
        Due from
        <input
          type="date"
          value={filters.review_due_from === ALL ? "" : filters.review_due_from}
          onChange={(e) => updateFilter("review_due_from", e.target.value)}
          className="rounded border bg-background px-1 py-0.5 text-xs"
          data-testid="risks-review-due-from"
        />
      </label>
      <label className="inline-flex items-center gap-1.5 text-xs text-muted-foreground">
        to
        <input
          type="date"
          value={filters.review_due_to === ALL ? "" : filters.review_due_to}
          onChange={(e) => updateFilter("review_due_to", e.target.value)}
          className="rounded border bg-background px-1 py-0.5 text-xs"
          data-testid="risks-review-due-to"
        />
      </label>
      <label className="inline-flex items-center gap-1.5 text-xs text-muted-foreground">
        <input
          type="checkbox"
          checked={showRollUps}
          onChange={() => toggleFlag(ROLLUPS_PARAM, showRollUps)}
          data-testid="risks-show-rollups"
        />
        Show roll-ups
      </label>
      <label className="inline-flex items-center gap-1.5 text-xs text-muted-foreground">
        <input
          type="checkbox"
          checked={showArchived}
          onChange={() => toggleFlag(ARCHIVED_PARAM, showArchived)}
          data-testid="risks-show-archived"
        />
        Show archived categories
      </label>
      <details className="relative" data-testid="risks-column-chooser">
        <summary className="cursor-pointer select-none rounded border px-2 py-0.5 text-xs">
          Columns
        </summary>
        <div className="absolute right-0 z-20 mt-1 w-44 rounded-md border bg-popover p-2 shadow-md">
          {OPTIONAL_COLUMNS.map((col) => (
            <label
              key={col}
              className="flex items-center gap-2 py-0.5 text-xs text-foreground"
            >
              <input
                type="checkbox"
                checked={columnsChosen.includes(col)}
                onChange={() => toggleOptionalColumn(col)}
                data-testid={`risks-column-toggle-${col}`}
              />
              {OPTIONAL_COLUMN_LABELS[col]}
            </label>
          ))}
        </div>
      </details>
    </span>
  );

  const header = (key: SortKey, label: string, title?: string) => (
    <SortableHeader
      sortKey={key}
      label={label}
      title={title}
      state={sortState}
      onSort={updateSort}
    />
  );

  // shadcn cells never wrap; the text columns must, or a long title or
  // owner pushes the table past 1316px.
  const nowrap = "whitespace-nowrap";
  const wrap = "whitespace-normal break-words";

  const defaultColumns: ListColumn<Risk>[] = [
    {
      id: "id",
      header: header("id", "ID"),
      ariaSort: ariaSort(sortState, "id"),
      className: nowrap,
      cell: (row) => (
        <Link
          href={`/risks/${encodeURIComponent(row.id)}`}
          onClick={stopRowClick}
          className="font-mono text-xs text-muted-foreground hover:underline"
          title={row.id}
          data-testid="risks-row-id"
        >
          {shortRiskId(row.id)}
        </Link>
      ),
    },
    {
      id: "title",
      header: header("title", "Title"),
      ariaSort: ariaSort(sortState, "title"),
      className: `${wrap} min-w-[14rem]`,
      cell: (row) => (
        <span className="inline-flex flex-wrap items-center gap-x-2 gap-y-0.5">
          <Link
            href={`/risks/${encodeURIComponent(row.id)}`}
            onClick={stopRowClick}
            className="text-primary hover:underline"
            data-testid="risks-row-title"
          >
            {row.title}
          </Link>
          {patternNoLongerMet(row.inherent_score) ? (
            <span
              className="rounded border px-1.5 py-0.5 text-[11px] text-muted-foreground"
              data-testid="risks-row-pattern-lapsed"
              title="Rule-generated meta-risk with 0 child risks in the current window. The engine never closes it; review it."
            >
              Pattern no longer met
            </span>
          ) : null}
          {/* #2060: a roll-up only shows with the toggle on. The marker
              links to the children it summarises on the detail page. */}
          {row.child_count ? (
            <Link
              href={`/risks/${encodeURIComponent(row.id)}#children`}
              onClick={stopRowClick}
              className="rounded border px-1.5 py-0.5 text-[11px] text-muted-foreground hover:underline"
              data-testid="risks-row-rollup"
              title="Roll-up of other risks in the register. It is left out of the count unless roll-ups are shown."
            >
              Roll-up · {row.child_count}{" "}
              {row.child_count === 1 ? "child" : "children"}
            </Link>
          ) : null}
        </span>
      ),
    },
    {
      id: "category",
      header: header("category", "Category"),
      ariaSort: ariaSort(sortState, "category"),
      className: wrap,
      cell: (row) => {
        const cat = categoryDisplay(row, categories);
        return (
          <span
            className="text-xs text-muted-foreground"
            data-testid="risks-row-category"
          >
            {cat.name}
            {cat.archived ? (
              <span
                className="ml-1.5 rounded border px-1 py-0.5 text-[10px]"
                data-testid="risks-row-category-archived"
                title="This category is archived. It stays on the risks that have it but takes no new risks."
              >
                archived
              </span>
            ) : null}
          </span>
        );
      },
    },
    {
      id: "owner",
      header: header("owner", "Owner"),
      ariaSort: ariaSort(sortState, "owner"),
      className: `${wrap} min-w-[7rem]`,
      cell: (row) => {
        if (row.owner_user_id) {
          return (
            <Person
              id={row.owner_user_id}
              className="text-xs text-muted-foreground"
              testId="risks-row-owner"
            />
          );
        }
        const unlinked = (row.owner_unlinked ?? "").trim();
        if (unlinked === "") {
          return (
            <span
              className="text-xs italic text-muted-foreground"
              data-testid="risks-row-owner-none"
            >
              Unowned
            </span>
          );
        }
        return (
          <span
            className="text-xs text-muted-foreground"
            title="Owner (unlinked): no user matched this text. Edit the risk to pick one."
            data-testid="risks-row-owner-unlinked"
          >
            {unlinked} <span className="italic">(unlinked)</span>
          </span>
        );
      },
    },
    {
      id: "residual",
      header: (
        <SortableHeader
          sortKey="residual"
          term={{
            node: <Term id="residual-risk">Residual</Term>,
            text: "Residual",
          }}
          label=""
          title={RESIDUAL_HEADER_TOOLTIP}
          state={sortState}
          onSort={updateSort}
        />
      ),
      ariaSort: ariaSort(sortState, "residual"),
      className: nowrap,
      cell: (row) => <ResidualCell row={row} />,
    },
    {
      id: "treatment",
      header: header("treatment", "Treatment"),
      ariaSort: ariaSort(sortState, "treatment"),
      className: nowrap,
      cell: (row) => (
        <span
          className="inline-flex items-center px-2 py-0.5 text-[11px] font-medium rounded-md bg-muted text-foreground"
          data-testid="risks-row-treatment"
        >
          {treatmentLabel(row)}
        </span>
      ),
    },
    {
      id: "review_due",
      header: header("review_due", "Review due"),
      ariaSort: ariaSort(sortState, "review_due"),
      className: nowrap,
      cell: (row) => {
        // #2059: every risk gets a review date at creation, so a missing
        // one is a data gap and reads as "Not set".
        if (reviewDuePending(row.review_due_at)) {
          return (
            <span
              className="text-xs italic text-muted-foreground"
              data-testid="risks-row-review-due-missing"
            >
              Not set
            </span>
          );
        }
        return (
          <span className="inline-flex items-center gap-1.5">
            <span
              className="font-mono text-xs text-muted-foreground"
              data-testid="risks-row-review-due"
            >
              {row.review_due_at!.slice(0, 10)}
            </span>
            {reviewOverdue(row.review_due_at) ? (
              <span
                className="rounded px-1.5 py-0.5 text-[10px] font-medium bg-red-50 text-red-700"
                data-testid="risks-row-review-overdue"
              >
                Overdue
              </span>
            ) : null}
          </span>
        );
      },
    },
    {
      id: "status",
      header: header("status", "Status"),
      ariaSort: ariaSort(sortState, "status"),
      className: nowrap,
      cell: (row) =>
        row.status === "closed" ? (
          <span
            className="rounded border px-1.5 py-0.5 text-[11px] text-muted-foreground"
            data-testid="risks-row-closed"
            title="Closed risk. It is left out of the register unless the Status filter shows closed risks."
          >
            Closed
          </span>
        ) : (
          <span
            className="text-xs text-foreground"
            data-testid="risks-row-open"
          >
            Open
          </span>
        ),
    },
  ];

  const optionalColumns: Record<OptionalColumn, ListColumn<Risk>> = {
    methodology: {
      id: "methodology",
      header: header("methodology", "Methodology"),
      ariaSort: ariaSort(sortState, "methodology"),
      cell: (row) => (
        <span
          className="text-xs text-muted-foreground"
          data-testid="risks-row-methodology"
        >
          {enumLabel("RiskMethodology", row.methodology)}
        </span>
      ),
    },
    level: {
      id: "level",
      header: header("level", "Level"),
      ariaSort: ariaSort(sortState, "level"),
      cell: (row) => (
        <span
          className="text-xs text-muted-foreground"
          data-testid="risks-row-level"
        >
          {row.level ? LEVEL_LABELS[row.level] ?? row.level : "—"}
        </span>
      ),
    },
    org_unit: {
      id: "org_unit",
      header: header("org_unit", "Org unit"),
      ariaSort: ariaSort(sortState, "org_unit"),
      cell: (row) => (
        <span
          className="text-xs text-muted-foreground"
          data-testid="risks-row-org-unit"
        >
          {row.org_unit_id ? orgUnitName.get(row.org_unit_id) ?? "—" : "—"}
        </span>
      ),
    },
    themes: {
      id: "themes",
      header: header("themes", "Themes"),
      ariaSort: ariaSort(sortState, "themes"),
      cell: (row) => (
        <span
          className="text-xs text-muted-foreground"
          data-testid="risks-row-themes"
        >
          {row.themes.length ? row.themes.join(", ") : "—"}
        </span>
      ),
    },
    created_at: {
      id: "created_at",
      header: header("created_at", "Created"),
      ariaSort: ariaSort(sortState, "created_at"),
      className: nowrap,
      cell: (row) => (
        <span
          className="font-mono text-xs text-muted-foreground"
          data-testid="risks-row-created"
        >
          {row.created_at.slice(0, 10)}
        </span>
      ),
    },
  };

  const columns = [
    ...defaultColumns,
    ...columnsChosen.map((c) => optionalColumns[c]),
  ];

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

  const filterRow = (
    <>
      <SavedViewsBar
        views={savedViews}
        activeViewId={activeViewId}
        canSave={Object.keys(currentView).length > 0}
        onLoadView={onLoadView}
        onSaveView={onSaveView}
        onDeleteView={(id) => deleteViewMutation.mutate(id)}
        testIdPrefix="risks"
      />
      <FilterPills
        pills={pills}
        onChange={(id, v) => updateFilter(id as FilterKey, v)}
        meta={meta}
      />
    </>
  );

  if (risksQ.isLoading) {
    return (
      <ListPage
        title={pageTitle("/risks")}
        subtitle={subtitle}
        actions={actions}
        filterRow={filterRow}
      >
        <ListLoadingSkeleton />
      </ListPage>
    );
  }

  if (risksQ.isError) {
    return (
      <ListPage
        title={pageTitle("/risks")}
        subtitle={subtitle}
        actions={actions}
        filterRow={filterRow}
      >
        <Alert variant="destructive" data-testid="risks-load-error">
          <AlertTitle>Could not load risks</AlertTitle>
          <AlertDescription>{(risksQ.error as Error).message}</AlertDescription>
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
            d="M12 9v3.75m-9.303 3.376c-.866 1.5.217 3.374 1.948 3.374h14.71c1.73 0 2.813-1.874 1.948-3.374L13.949 3.378c-.866-1.5-3.032-1.5-3.898 0L2.697 16.126zM12 15.75h.007v.008H12v-.008z"
            strokeLinecap="round"
            strokeLinejoin="round"
          />
        </svg>
      }
      title={filtered ? "No risks match these filters" : "No risks logged yet"}
      body={
        filtered
          ? "Try widening the status, owner, treatment, category, review due, methodology or org unit filters."
          : "Start a register with one or two known operational risks — you can refine methodology later."
      }
      cta={
        filtered
          ? { label: "Clear filters", onClick: clearAll }
          : {
              label: "Add first risk",
              onClick: () => router.push("/risks/new"),
            }
      }
    />
  );

  return (
    <ListPage
      title={pageTitle("/risks")}
      subtitle={subtitle}
      actions={actions}
      filterRow={filterRow}
    >
      <ListTable<Risk>
        columns={columns}
        rows={rows}
        rowKey={(row) => row.id}
        onRowClick={(row) =>
          router.push(`/risks/${encodeURIComponent(row.id)}`)
        }
        emptyFallback={emptyState}
        mobileMode="cards"
      />
      {total > 0 ? (
        <ListPagination
          currentPage={currentPage}
          pageSize={RISKS_PAGE_SIZE}
          totalCount={total}
          onPageChange={goToPage}
          testIdPrefix="risks-pagination"
        />
      ) : null}
    </ListPage>
  );
}

// The residual cell (#2132): the band, with the inherent severity on
// hover. A 5x5 residual shows its 1..25 value and band; a FAIR residual is
// dollars and carries no band; a risk not yet evaluated says so.
function ResidualCell({ row }: { row: Risk }) {
  if (isUnscored(row.inherent_score)) {
    // #2511: logged before it was assessed. Nothing to evaluate yet.
    return (
      <span
        className="text-xs italic text-muted-foreground"
        title="This risk has not been scored yet. Edit it to add an inherent score."
        data-testid="risks-row-residual-unscored"
      >
        {UNSCORED_LABEL}
      </span>
    );
  }
  const inherentFair =
    row.methodology === "fair" ? fairScore(row.inherent_score) : null;
  const inherentText = inherentFair
    ? `Inherent: ${formatDollars(inherentFair.annualized_loss_exposure)} (FAIR)`
    : `Inherent: ${row.severity} (${severityBandLabel(
        severityBand(row.severity),
      )})`;

  if (residualState(row.residual_score) === "pending") {
    return (
      <span
        className="text-xs italic text-muted-foreground"
        title={`${PENDING_EVAL_TOOLTIP} ${inherentText}.`}
        data-testid="risks-row-residual-pending"
      >
        {PENDING_EVAL_LABEL}
      </span>
    );
  }
  const formatted = formatResidualScore(row.residual_score, row.methodology);
  const n = residualSeverity(row.residual_score);
  if (row.methodology === "fair" || n == null) {
    return (
      <span
        className="font-mono text-xs text-foreground"
        title={inherentText}
        data-testid="risks-row-residual"
      >
        {formatted}
      </span>
    );
  }
  return (
    <SeverityPill
      value={formatted}
      band={severityBand(n)}
      className={residualClass(formatted)}
      testId="risks-row-residual"
      title={inherentText}
    />
  );
}

export default function RisksListPage() {
  // useSearchParams must be wrapped in Suspense in App Router (Next 16
  // strict-mode requirement).
  return (
    <Suspense fallback={<ListLoadingSkeleton />}>
      <RisksPageInner />
    </Suspense>
  );
}
