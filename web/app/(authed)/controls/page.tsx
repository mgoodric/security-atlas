"use client";

// Slice 098 + 104 — /controls list view.
//
// Today `/controls` 404'd in the sidebar (audit finding F-4 in
// `Plans/canvas/13-ui-mockup-audit-2026-05-16.md`). This page ships the
// missing list view per the design captured in slice 093
// (`Plans/_archive/mockups/controls.html` + `Plans/canvas/12-ui-fill-in-design-
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

import {
  keepPreviousData,
  useMutation,
  useQuery,
  useQueryClient,
} from "@tanstack/react-query";
import Link from "next/link";
import { useRouter, useSearchParams } from "next/navigation";
import { Suspense, useCallback, useEffect, useMemo, useState } from "react";

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
import { DemoBadge } from "@/components/demo-badge";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { buttonVariants } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Person } from "@/components/ui/person";
import {
  fetchCatalogCounts,
  fetchControlsList,
  fetchMyControlsPage,
  fetchMyControlsTotal,
  fetchScopeCells,
  type ControlsListResponse,
  type ControlsWithStateResponse,
  type ScopeCellsListResponse,
} from "@/lib/api/controls-list";
import {
  createSavedView,
  deleteSavedView,
  listSavedViews,
  type ServerSavedView,
} from "@/lib/api/saved-views";
import { bulkAssignOwner } from "@/lib/api/controls-bulk";
import { getMe } from "@/lib/api/me";
import { APIError } from "@/lib/api/base";
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
  ANCHOR_FIT_OPTIONS,
  anchorFitMarker,
  clearFilters,
  DEFAULT_FILTERS,
  FILTER_KEYS,
  FRAMEWORKS_JOIN_SEPARATOR,
  isDefault,
  setFilter,
  type AnchorRow,
  type ControlFilters,
} from "./filters";
import { controlsCountLabel } from "./count-label";
import {
  ariaSort,
  clampedPage,
  myControlsQuery,
  nextSort,
  normalizeFilters,
  pageOffset,
  SORT_KEY_BY_COLUMN,
} from "./server-query";
import {
  controlHref,
  controlsViewSummary,
  HAS_CONTROL_OPTIONS,
  HAS_CONTROL_PARAM,
  parseControlsView,
  parseHasControl,
  rowsFromAnchors,
  rowsFromControls,
  VIEW_OPTIONS,
  VIEW_PARAM,
} from "./view";
import {
  BulkAssignMessage,
  SavedViewsBar,
  SelectionBar,
} from "./controls-toolbar";
import {
  isOverCap,
  selectAllState,
  toggleSelectAll,
  toggleSelection,
} from "./selection";
import {
  findView,
  migrateLocalViewsToServer,
  sanitizeFilters,
  type SavedView,
} from "./saved-views";
import { enumLabel, enumOptions } from "@/lib/labels/enum-labels";
import { pageTitle } from "@/lib/page-titles";
import { Term } from "@/components/help/term";
import { Timestamp } from "@/components/ui/timestamp";

// #2131 — the filter keys (`FILTER_KEYS`, from ./filters) are the URL keys
// and the `GET /v1/controls?include=state` parameters, one for one; the
// platform applies every one of them. Only "My controls" offers the
// framework, family, state, freshness, anchor fit and owner filters and
// the column sort; the SCF catalog view filters by scope and has-control.

// #2131 — the filters "My controls" offers and the catalog does not.
const MINE_ONLY_KEYS: (keyof ControlFilters)[] = [
  "framework",
  "family",
  "result",
  "freshness",
  "anchor_fit",
  "owner",
  "sort",
];

// Slice 224 — cap the Scope pill at 50 entries per AC-5. Tenants with
// more cells get a banner indicating the cap; the dropdown still works
// for the first 50 cells (newest-first ordering from /v1/scopes/cells).
// A typeahead replacement for tenants exceeding the cap is deferred to
// a follow-on slice (per AC-5 + decision log D3).
const SCOPE_CELL_CAP = 50;

// Slice 227 — page-size default per AC-2. Reuses the shared
// `<ListPagination>` primitive shipped by slice 246.
//
// Per anti-criterion P0-227 (greppability — same posture slice 246 took
// for /risks) this constant lives at module scope so it is greppable;
// component code references `CONTROLS_PAGE_SIZE` rather than inlining
// `50`. The value matches `RISKS_PAGE_SIZE` so the "scan a screenful"
// budget stays consistent across list-view pages; future divergence
// across pages is fine — the constant lives next to the page that owns
// it.
const CONTROLS_PAGE_SIZE = 50;

// Slice 745 — how long the bulk-assign confirmation stays on screen
// before auto-dismissing, in milliseconds. The message renders in a
// persistent region above the table (independent of the selection bar)
// so the operator actually sees it after a successful bulk-assign clears
// the selection; it then auto-dismisses so a stale confirmation does not
// linger across unrelated actions. The next bulk-assign attempt also
// clears it eagerly (onAssignToMe nulls it before the request), so a
// fresh attempt never shows the previous result. Greppable at module
// scope per the slice-227 CONTROLS_PAGE_SIZE precedent.
const BULK_ASSIGN_MESSAGE_TTL_MS = 6000;

// Slice 227 — URL query-string key for the 1-indexed page index. The
// `page` key is sibling to the filter keys above; the filter-change
// handlers below explicitly DROP it on every filter mutation (AC-8 —
// page index resets to 1 when a filter changes, preserving the user's
// mental model).
const PAGE_PARAM = "page";

const RESULT_OPTIONS: { value: string; label: string }[] = [
  { value: ALL, label: "All states" },
  ...enumOptions("EvaluationState"),
];

// Slice <NNN> — map the control evaluation `result` enum (the real wire
// values from `AnchorState.result`, enumerated by RESULT_OPTIONS above)
// onto the semantic Badge variants introduced with the PR-#1427 tokens.
// The design canvas's table card (web/.claude-design/components/table/)
// uses example labels (Compliant / In review / Gap); the REAL enum is
// pass / fail / inconclusive / excepted / na (#2131: this map used to
// name `insufficient_evidence` and `not_applicable`, which the rollup
// never produces), so the mapping is by semantic intent, not by the
// card's display strings:
//   pass                  -> pass     (green  — control passing)
//   fail                  -> critical (red    — the card's "Gap")
//   inconclusive          -> warning  (amber  — needs attention, not a hard fail)
//   na                    -> secondary (neutral — out of scope, no signal hue)
//   excepted              -> info     (blue   — waived by an active exception;
//                                      #2112: never the pass hue, never "pass")
// `progress` from the badge family has no member in the control-result
// enum, so it is intentionally unused here (it exists for other surfaces
// and the design preview).
type StatusBadgeVariant =
  | "pass"
  | "info"
  | "critical"
  | "warning"
  | "secondary"
  | "outline";

const RESULT_BADGE_VARIANT: Record<string, StatusBadgeVariant> = {
  pass: "pass",
  fail: "critical",
  inconclusive: "warning",
  na: "secondary",
  excepted: "info",
};

// #2112, #2083 — display text for a result. `excepted` reads "Excepted",
// so a waived control can never be mistaken for a passing one; the raw
// value stays on the data-result attribute.
function resultLabel(result: string): string {
  return enumLabel("EvaluationState", result);
}

function resultBadgeVariant(result: string): StatusBadgeVariant {
  // Unknown / future result values fall back to a neutral outline pill
  // rather than mis-signalling a semantic color.
  return RESULT_BADGE_VARIANT[result] ?? "outline";
}

const FRESHNESS_OPTIONS: { value: string; label: string }[] = [
  { value: ALL, label: "All" },
  ...enumOptions("FreshnessState"),
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

// Slice 468 — convert a server saved-view (flat string-map filters) to the
// page's typed SavedView. sanitizeFilters narrows the map to the slice-224
// ControlFilters shape (unknown keys dropped, missing keys -> ALL) so a row
// hand-edited or written under a future key-set degrades gracefully.
function toSavedView(v: ServerSavedView): SavedView {
  return { id: v.id, name: v.name, filters: sanitizeFilters(v.filters) };
}

// serializeFilters projects the typed ControlFilters to the flat string map
// the server persists, dropping ALL-valued (inactive) keys so the stored
// payload is just the active criteria (matches the server's own
// sanitizeControlFilters allow-list + empty-value drop).
function serializeFilters(filters: ControlFilters): Record<string, string> {
  const out: Record<string, string> = {};
  for (const k of FILTER_KEYS) {
    if (filters[k] !== ALL) out[k] = filters[k];
  }
  return out;
}

function ControlsPageInner() {
  const router = useRouter();
  const search = useSearchParams();

  // URL-driven filter state — mirrors the slice 094 calendar pattern so
  // the filter set is shareable / bookmarkable. Default = ALL on every
  // pill.
  //
  // #2131: each value is checked against what the platform accepts, so a
  // hand-edited URL widens the list rather than failing it with a 400.
  const filters: ControlFilters = useMemo(() => {
    const out = { ...DEFAULT_FILTERS };
    for (const k of FILTER_KEYS) {
      const v = search.get(k);
      if (v) out[k] = v;
    }
    return normalizeFilters(out);
  }, [search]);

  // Slice 227 — current page index. URL is the source of truth so the
  // page is bookmarkable and survives refresh. Invalid / missing /
  // negative values fall back to 1; the rendering math in
  // `paginationBounds` further clamps an out-of-range page to the
  // last available page so a stale bookmark survives a catalog that
  // shrunk between visits.
  const currentPage: number = useMemo(() => {
    const raw = search.get(PAGE_PARAM);
    if (!raw) return 1;
    const parsed = Number.parseInt(raw, 10);
    if (!Number.isFinite(parsed) || parsed < 1) return 1;
    return parsed;
  }, [search]);

  // Issue #1741 — which list the page shows. The default is the tenant's
  // own controls; the SCF catalog is the opt-in `?view=catalog`, where the
  // server-side `?has_control=` filter applies.
  const view = parseControlsView(search.get(VIEW_PARAM));
  const hasControlRaw = search.get(HAS_CONTROL_PARAM);
  const hasControl =
    view === "catalog" ? parseHasControl(hasControlRaw) : undefined;

  // Switching view or the has-control filter changes the fetched set, so
  // both reset the page index, like a filter change (slice 227 AC-8).
  const updateParam = (key: string, value: string | null) => {
    const sp = new URLSearchParams(search.toString());
    if (value === null || value === ALL) {
      sp.delete(key);
    } else {
      sp.set(key, value);
    }
    // Catalog anchors carry no fit, owner or per-control state, so the
    // "My controls" filters and sort are dropped on a view switch (issue
    // #1736, #2131).
    if (key === VIEW_PARAM) {
      sp.delete(HAS_CONTROL_PARAM);
      for (const k of MINE_ONLY_KEYS) sp.delete(k);
    }
    sp.delete(PAGE_PARAM);
    router.replace(`/controls?${sp.toString()}`);
  };

  const updateFilter = (key: keyof ControlFilters, value: string) => {
    const next = setFilter(filters, key, value);
    const sp = new URLSearchParams(search.toString());
    if (next[key] === ALL) {
      sp.delete(key);
    } else {
      sp.set(key, next[key]);
    }
    // Slice 227 AC-8: filter changes reset the page index to 1. The URL
    // key is dropped (page=1 is the default and need not be serialised)
    // so the URL stays clean when no pagination is in play.
    sp.delete(PAGE_PARAM);
    router.replace(`/controls?${sp.toString()}`);
  };

  const clearAll = () => {
    const cleared = clearFilters();
    const sp = new URLSearchParams(search.toString());
    for (const k of FILTER_KEYS) {
      if (cleared[k] === ALL) sp.delete(k);
    }
    // Slice 227 AC-8: clearing filters also resets pagination.
    sp.delete(PAGE_PARAM);
    router.replace(`/controls?${sp.toString()}`);
  };

  // Slice 227 — page-change handler. Writes the new 1-indexed page to
  // the URL; page=1 is omitted so the canonical URL of the first page
  // matches the no-pagination URL exactly.
  const goToPage = (page: number) => {
    const sp = new URLSearchParams(search.toString());
    if (page <= 1) {
      sp.delete(PAGE_PARAM);
    } else {
      sp.set(PAGE_PARAM, String(page));
    }
    router.replace(`/controls?${sp.toString()}`);
  };

  // #2131 — both views ask the server for one page. "My controls" sends
  // every filter and the sort to `GET /v1/controls?include=state`, which
  // filters, sorts and pages in SQL; the SCF catalog view sends its scope
  // and has-control filters to `GET /v1/anchors?include=state`. The
  // queryKey carries the whole request, so a filter, sort or page change
  // refetches. Only the active view's query runs.
  const scopeArg = filters.scope === ALL ? undefined : filters.scope;
  const myControlsParams = useMemo(
    () => myControlsQuery(filters, currentPage, CONTROLS_PAGE_SIZE),
    [filters, currentPage],
  );
  const myControlsQ = useQuery<Partial<ControlsWithStateResponse>>({
    queryKey: ["controls", "mine", myControlsParams],
    queryFn: () => fetchMyControlsPage(myControlsParams),
    enabled: view === "mine",
    placeholderData: keepPreviousData,
  });
  const anchorsQ = useQuery<ControlsListResponse>({
    queryKey: [
      "controls",
      "list",
      scopeArg ?? "all",
      String(hasControl),
      currentPage,
    ],
    queryFn: () =>
      fetchControlsList(
        scopeArg,
        hasControl,
        CONTROLS_PAGE_SIZE,
        pageOffset(currentPage, CONTROLS_PAGE_SIZE),
      ),
    enabled: view === "catalog",
    placeholderData: keepPreviousData,
  });
  // The catalog size for the "My controls" header. Issue #1832: a
  // one-row page; only the totals are read.
  const catalogCountsQ = useQuery<ControlsListResponse>({
    queryKey: ["controls", "catalog-counts"],
    queryFn: fetchCatalogCounts,
    enabled: view === "mine",
  });
  // The unfiltered control count for the "N of M controls" header. The
  // sidebar badge reads the same count under the same key (#1832).
  const myTotalQ = useQuery<number>({
    queryKey: ["sidebar", "controls-count"],
    queryFn: fetchMyControlsTotal,
    enabled: view === "mine",
  });
  const listQ = view === "mine" ? myControlsQ : anchorsQ;

  // Slice 224 — the dropdown options for the Scope filter pill come
  // from the tenant's own scope cells (RLS-scoped to the caller's
  // tenant in /v1/scopes/cells, per slice 017). Failure to load this
  // list is non-fatal — the pill renders with just "All cells".
  const scopeCellsQ = useQuery<ScopeCellsListResponse>({
    queryKey: ["scope-cells"],
    queryFn: fetchScopeCells,
  });

  // Convert the wire page into the row shape the table renders. Slice 104
  // attaches a real state cell per row (or `null` when there is no
  // evaluation); slice 226 threads the frameworks set through.
  //
  // Issue #1741 — the mapping per view lives in `./view`: control rows are
  // keyed and linked by control id; catalog rows by anchor id, linking to
  // the tenant control behind the anchor when there is one.
  //
  // #2131 — `rows` is the one page the server returned, already filtered
  // and sorted; the page does not narrow or reorder it.
  const rows: AnchorRow[] = useMemo(
    () =>
      view === "mine"
        ? rowsFromControls(myControlsQ.data?.controls ?? [])
        : rowsFromAnchors(anchorsQ.data?.anchors ?? []),
    [view, myControlsQ.data, anchorsQ.data],
  );
  // The filtered total the server counted, which the pagination footer
  // pages over.
  const filteredTotal =
    view === "mine"
      ? myControlsQ.data?.total ?? rows.length
      : anchorsQ.data?.total ?? rows.length;

  // A page past the end (a stale bookmark, or a filter that shrank the
  // set) moves to the last page that has rows.
  useEffect(() => {
    if (!listQ.data || listQ.isPlaceholderData) return;
    const last = clampedPage(currentPage, CONTROLS_PAGE_SIZE, filteredTotal);
    if (last !== null) goToPage(last);
    // goToPage is rebuilt each render from the current search params.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [listQ.data, listQ.isPlaceholderData, currentPage, filteredTotal]);

  // Slice 448 — multi-select state. A Set of control ids. The pure set
  // math lives in `./selection`; this holds the Set in React state.
  //
  // #2131: the page holds one server page of rows, so the selection
  // outlives a page change (select on page 1, then on page 2, then assign)
  // and is not pruned against the rows on screen. Bulk-assign re-checks
  // every id on the server (AC-11), so an id that has since gone is
  // refused there, not trusted here.
  const [selected, setSelected] = useState<ReadonlySet<string>>(
    () => new Set<string>(),
  );

  // The ids on this page. Select-all-in-view selects these.
  //
  // Issue #1741 — selection holds CONTROL ids, since bulk-assign posts
  // them; a catalog anchor without a tenant control is not selectable.
  const visibleIds = useMemo(
    () => rows.flatMap((row) => (row.controlId ? [row.controlId] : [])),
    [rows],
  );

  const headerSelectState = selectAllState(visibleIds, selected);
  const selectionOverCap = isOverCap(selected);

  const onToggleRow = useCallback((id: string) => {
    setSelected((prev) => toggleSelection(prev, id));
  }, []);

  const onToggleSelectAll = useCallback(() => {
    setSelected((prev) => toggleSelectAll(visibleIds, prev));
  }, [visibleIds]);

  const onClearSelection = useCallback(() => {
    setSelected(new Set<string>());
  }, []);

  // Slice 468 — WORKING bulk-assign-owner action. v1 assigns the selected
  // set to the CURRENT USER ("assign to me" — the dominant triage case the
  // slice-448 narrative names; a richer assign-to-any-user picker is a
  // follow-on, decisions log 468 D4). The owner id is resolved from
  // `/v1/me`; the upstream re-checks role + tenant PER ITEM (AC-11) and
  // caps the set server-side. On success the selection clears; the message
  // surfaces inline.
  const [assignMessage, setAssignMessage] = useState<{
    kind: "ok" | "error";
    text: string;
  } | null>(null);
  const queryClient = useQueryClient();
  const bulkAssignMutation = useMutation({
    mutationFn: async (controlIds: string[]) => {
      const me = await getMe();
      return bulkAssignOwner(controlIds, me.user_id);
    },
    onSuccess: (res) => {
      setAssignMessage({
        kind: "ok",
        text: `Assigned ${res.assigned} control${
          res.assigned === 1 ? "" : "s"
        } to you.`,
      });
      setSelected(new Set<string>());
      // #2128: the Owner column reads the assignment back.
      void queryClient.invalidateQueries({ queryKey: ["controls", "mine"] });
    },
    onError: (err) => {
      const text =
        err instanceof APIError
          ? err.message
          : "Could not assign the selected controls. Try again.";
      setAssignMessage({ kind: "error", text });
    },
  });
  const onAssignToMe = useCallback(() => {
    setAssignMessage(null);
    bulkAssignMutation.mutate(Array.from(selected));
  }, [bulkAssignMutation, selected]);

  // Slice 745 — auto-dismiss the bulk-assign confirmation after a delay so
  // a stale "Assigned N controls to you." does not linger across unrelated
  // actions. The message lives in the persistent `BulkAssignMessage` region
  // (above the table, independent of `selected.size`), so unlike the old
  // inline span it survives the selection-clear and is actually observable;
  // this timer is the natural counterpart that retires it. Re-armed each
  // time a new message is set; cleared on unmount or before the next
  // message. A null message (the initial / just-cleared state) schedules
  // nothing.
  useEffect(() => {
    if (!assignMessage) return;
    const handle = window.setTimeout(
      () => setAssignMessage(null),
      BULK_ASSIGN_MESSAGE_TTL_MS,
    );
    return () => window.clearTimeout(handle);
  }, [assignMessage]);

  // Slice 468 — saved filter-views are now SERVER-BACKED (was client-side
  // localStorage in slice 448). Persistence moved to the (tenant, user)-
  // scoped `/v1/saved-views` store via the BFF; the SavedViewStore seam
  // slice 448 built made this a one-place swap. The server returns a flat
  // filter map; `toSavedView` narrows it back to the typed ControlFilters
  // via the slice-448 `sanitizeFilters` (defense-in-depth on read).
  const [activeViewId, setActiveViewId] = useState<string>("");

  const savedViewsQ = useQuery<SavedView[]>({
    queryKey: ["saved-views", "controls"],
    queryFn: async () => {
      // AC-467-3 — one-time migration of any views a user saved CLIENT-SIDE
      // during the slice-448 interim. On first server-backed load we upload
      // each local view (best-effort; duplicate-name 409s are swallowed),
      // then clear the localStorage key so the migration runs once. The
      // server is the source of truth thereafter (decisions log 468 D5).
      if (typeof window !== "undefined") {
        await migrateLocalViewsToServer(window.localStorage, createSavedView);
      }
      const server = await listSavedViews();
      return server.map(toSavedView);
    },
  });
  const savedViews = useMemo(() => savedViewsQ.data ?? [], [savedViewsQ.data]);

  const saveViewMutation = useMutation({
    mutationFn: (name: string) =>
      createSavedView(name, serializeFilters(filters)),
    onSuccess: (created) => {
      void queryClient.invalidateQueries({
        queryKey: ["saved-views", "controls"],
      });
      setActiveViewId(created.id);
    },
  });

  const deleteViewMutation = useMutation({
    mutationFn: (id: string) => deleteSavedView(id),
    onSuccess: (_void, id) => {
      void queryClient.invalidateQueries({
        queryKey: ["saved-views", "controls"],
      });
      if (activeViewId === id) setActiveViewId("");
    },
  });

  // Apply a saved view's filter state to the URL (the URL is the source
  // of truth for filters — slice 224/227). Drops the page param so the
  // restored view starts at page 1.
  const onLoadView = useCallback(
    (id: string) => {
      setActiveViewId(id);
      if (id === "") {
        // "None" — clear filters back to default.
        clearAll();
        return;
      }
      const saved = findView(savedViews, id);
      if (!saved) return;
      // #2131: the saved filters become the URL, which is the query the
      // page sends to the server.
      const sp = new URLSearchParams();
      for (const k of FILTER_KEYS) {
        if (view === "catalog" && MINE_ONLY_KEYS.includes(k)) continue;
        if (saved.filters[k] !== ALL) sp.set(k, saved.filters[k]);
      }
      // Issue #1741 — a saved view restores filters within the view the
      // operator is on; it does not switch between controls and catalog.
      // The catalog applies only the scope.
      if (view === "catalog") sp.set(VIEW_PARAM, view);
      router.replace(`/controls?${sp.toString()}`);
    },
    // clearAll + router are stable across renders for this page.
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [savedViews, router, view],
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
        // Map the upstream status to the slice-448 inline messages.
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

  const onDeleteView = useCallback(
    (id: string) => {
      deleteViewMutation.mutate(id);
    },
    [deleteViewMutation],
  );

  // #2131 — the family options are every family of the tenant's controls,
  // which the server sends with each page; the page's own rows are only
  // one page. A family in the URL that is not in the list (a typo, or a
  // family since removed) stays selectable so the pill shows what applies.
  const serverFamilies = myControlsQ.data?.families;
  const familyOptions: { value: string; label: string }[] = useMemo(() => {
    const families = [...(serverFamilies ?? [])];
    if (filters.family !== ALL && !families.includes(filters.family)) {
      families.push(filters.family);
    }
    return [
      { value: ALL, label: "All families" },
      ...families.map((f) => ({ value: f, label: f })),
    ];
  }, [serverFamilies, filters.family]);

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

  // #2131 — the owner pill. A user id in the URL (a dashboard "mine"
  // link, or a saved view) shows as "A person"; the pill itself offers the
  // two cases a triage pass needs.
  const ownerOptions: { value: string; label: string }[] = [
    { value: ALL, label: "Anyone" },
    { value: "none", label: "Unowned" },
    ...(filters.owner !== ALL && filters.owner !== "none"
      ? [{ value: filters.owner, label: "A person" }]
      : []),
  ];

  // #2131 — "My controls" filters on every pill below in SQL. The SCF
  // catalog view lists anchors, which carry no owner, fit or per-control
  // state, so it keeps the view, has-control and scope pills only.
  const minePills: FilterPill[] =
    view === "mine"
      ? [
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
          {
            id: "owner",
            label: "Owner",
            value: filters.owner,
            options: ownerOptions,
          },
        ]
      : [];

  const pills: FilterPill[] = [
    // Issue #1741 — view toggle: the tenant's controls (default) or the
    // SCF catalog.
    {
      id: VIEW_PARAM,
      label: "View",
      value: view,
      options: VIEW_OPTIONS,
    },
    // Issue #1741 — catalog only: anchors with / without a tenant
    // control, filtered server-side ahead of paging.
    ...(view === "catalog"
      ? [
          {
            id: HAS_CONTROL_PARAM,
            label: "Has control",
            value: hasControl === undefined ? ALL : String(hasControl),
            options: HAS_CONTROL_OPTIONS,
          },
        ]
      : []),
    ...minePills,
    // Slice 224 — scope cell filter. Options enumerate the tenant's own
    // cells (RLS-scoped). Selecting a cell sets ?scope=<id> and the BFF /
    // upstream applies the intersection.
    {
      id: "scope",
      label: "Scope",
      value: filters.scope,
      options: scopeOptions,
    },
    // Issue #1736 — how well each control's SCF anchor fits it.
    ...(view === "mine"
      ? [
          {
            id: "anchor_fit",
            label: "Anchor fit",
            value: filters.anchor_fit,
            options: ANCHOR_FIT_OPTIONS,
          },
        ]
      : []),
  ];

  // Slice 666 — header count label. The header is a COUNT of the
  // filtered catalog ("42 of 53 SCF anchors" / "53 SCF anchors"), NOT a
  // "Showing M–N" range. The verb "Showing" belongs exclusively to the
  // pagination footer (`<ListPagination>`); sharing it produced the
  // ATLAS-007 contradiction (header "Showing 53 of 53" vs footer
  // "Showing 1–50 of 53"). The filtered total drives the header (AC-3)
  // and is the same number the footer paginates over, so the two now
  // read consistently. Copy/semantics only — page size + counts are
  // unchanged (anti-criterion). See
  // `docs/audit-log/666-controls-count-semantics-decisions.md`.
  //
  // Issue #1741 — the noun follows the view, and a second line states the
  // tenant's controls against the catalog size from the server's totals
  // (the sidebar badge counts the same controls).
  const rowNoun = view === "mine" ? "controls" : "SCF anchors";
  //
  // #2131 — both numbers are the server's: the filtered total the list
  // pages over, and the unfiltered count (the tenant's controls, or the
  // whole catalog).
  const unfilteredTotal =
    view === "mine"
      ? myTotalQ.data ?? filteredTotal
      : anchorsQ.data?.catalog_total ?? filteredTotal;
  const countLabel = controlsCountLabel(
    filteredTotal,
    unfilteredTotal,
    rowNoun,
  );
  const viewSummary = controlsViewSummary(view, {
    controls: myTotalQ.data,
    catalogTotal:
      view === "mine"
        ? catalogCountsQ.data?.catalog_total
        : anchorsQ.data?.catalog_total,
    withControlTotal: anchorsQ.data?.with_control_total,
  });
  const meta = (
    <span className="inline-flex flex-wrap items-baseline gap-x-3">
      <span data-testid="controls-count-label">
        {countLabel.isFiltered ? (
          <>
            <span className="text-foreground font-medium">
              {countLabel.filtered}
            </span>{" "}
            of <span className="font-mono">{countLabel.total}</span> {rowNoun}
          </>
        ) : (
          <>
            <span className="text-foreground font-medium">
              {countLabel.total}
            </span>{" "}
            {rowNoun}
          </>
        )}
      </span>
      {viewSummary ? (
        <span
          className="text-xs text-muted-foreground"
          data-testid="controls-view-summary"
        >
          {viewSummary}
        </span>
      ) : null}
    </span>
  );

  const columns: ListColumn<AnchorRow>[] = [
    // Slice 448 — multi-select column. Header is the select-all-in-view
    // tri-state checkbox; each row carries a per-row checkbox. The
    // checkbox click stops propagation so it never triggers the row's
    // navigate-to-detail onRowClick.
    {
      id: "select",
      header: (
        <span
          className="inline-flex"
          // Stop the header click from sorting/navigating (defensive —
          // the header has no click handler today).
          onClick={(e) => e.stopPropagation()}
        >
          <Checkbox
            aria-label="Select all controls in view"
            data-testid="controls-select-all"
            checked={headerSelectState === "all"}
            indeterminate={headerSelectState === "some"}
            onCheckedChange={() => onToggleSelectAll()}
          />
        </span>
      ),
      cell: (row) => {
        const controlId = row.controlId;
        if (!controlId) return null;
        return (
          <span className="inline-flex" onClick={(e) => e.stopPropagation()}>
            <Checkbox
              aria-label={`Select control ${row.anchor.scf_id}`}
              data-testid="controls-row-select"
              checked={selected.has(controlId)}
              onCheckedChange={() => onToggleRow(controlId)}
            />
          </span>
        );
      },
      className: "w-8",
    },
    {
      id: "scf_id",
      header: "SCF anchor",
      // Issue #1741 — links open the tenant control (by control id); a
      // catalog anchor with no control renders as plain text.
      cell: (row) => {
        const href = controlHref(row);
        return href ? (
          <Link
            href={href}
            className="font-mono text-xs font-semibold text-primary hover:underline"
            data-testid="controls-row-scf-id"
            onClick={(e) => e.stopPropagation()}
          >
            {row.anchor.scf_id || "—"}
          </Link>
        ) : (
          <span
            className="font-mono text-xs text-muted-foreground"
            data-testid="controls-row-scf-id-unlinked"
          >
            {row.anchor.scf_id}
          </span>
        );
      },
    },
    {
      id: "name",
      header: "Name",
      cell: (row) => {
        const href = controlHref(row);
        // Issue #1736 — a non-exact anchor fit carries a marker; its
        // tooltip is the fit note.
        const fit = anchorFitMarker(row);
        const marker = fit ? (
          <Badge
            variant="outline"
            className="ml-2"
            title={fit.title}
            data-testid="controls-row-anchor-fit"
            data-anchor-fit={row.anchorFit}
          >
            {fit.label}
          </Badge>
        ) : null;
        return href ? (
          <>
            <Link
              href={href}
              className="text-foreground hover:text-primary"
              onClick={(e) => e.stopPropagation()}
            >
              {row.anchor.name}
            </Link>
            {row.demo ? <DemoBadge testId="controls-row-demo" /> : null}
            {marker}
          </>
        ) : (
          <span className="text-muted-foreground">{row.anchor.name}</span>
        );
      },
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
      // Slice <NNN> — semantic status pill using the Badge variants wired
      // from the PR-#1427 tokens. The variant is chosen by the real
      // evaluation `result` enum (see resultBadgeVariant); a 6px LED dot
      // (inheriting the deep same-hue text via bg-current) mirrors the
      // design canvas's table card. The variant text color is the deep
      // token on its soft tint — >=4.5:1 (WCAG AA). The raw enum value
      // remains the visible text, so screen readers and existing filters
      // read the same token.
      cell: (row) =>
        row.state ? (
          <Badge
            variant={resultBadgeVariant(row.state.result)}
            data-testid="controls-row-state"
            data-result={row.state.result}
          >
            <span
              aria-hidden="true"
              className="size-1.5 shrink-0 rounded-full bg-current"
            />
            {resultLabel(row.state.result)}
          </Badge>
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
            {enumLabel("FreshnessState", row.state.freshness_status)}
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
          <Timestamp
            value={row.state.last_observed_at}
            className="text-xs text-muted-foreground"
          />
        ) : (
          <span className="text-muted-foreground">—</span>
        ),
    },
    // #2128 — the assigned owner (a person). Catalog anchor rows have no
    // control, so the column is on the "My controls" view only.
    ...(view === "mine"
      ? [
          {
            id: "owner",
            header: "Owner",
            cell: (row: AnchorRow) => (
              <Person
                id={row.ownerUserId}
                className="text-xs"
                testId="controls-row-owner"
              />
            ),
          },
        ]
      : []),
    // Slice 226 — Frameworks-per-row column. Right-aligned per the
    // mockup (`Plans/_archive/mockups/controls.html` line 197). Renders the
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

  // #2131 — on "My controls" every displayed column sorts on the server.
  // A header click cycles ascending, descending, then the default order
  // (failing first, then stalest evidence); the sort rides in the URL
  // with the filters, so it is bookmarkable and saved with a view.
  const tableColumns: ListColumn<AnchorRow>[] =
    view === "mine"
      ? columns.map((col) => {
          const key = SORT_KEY_BY_COLUMN[col.id];
          if (!key || typeof col.header !== "string") return col;
          const state = ariaSort(filters.sort, key);
          const label = col.header;
          return {
            ...col,
            ariaSort: state,
            // The cards show the plain label; the sort button is the
            // table header's alone.
            cardLabel: label,
            header: (
              <button
                type="button"
                className="inline-flex items-center gap-1 uppercase tracking-wider hover:text-foreground"
                data-testid={`controls-sort-${key}`}
                data-sort={state}
                title={`Sort by ${label}`}
                onClick={() =>
                  updateFilter("sort", nextSort(filters.sort, key))
                }
              >
                {label}
                <span aria-hidden="true">
                  {state === "ascending"
                    ? "▲"
                    : state === "descending"
                      ? "▼"
                      : ""}
                </span>
              </button>
            ),
          };
        })
      : columns;

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
      {/* #1785 — upload a control sheet; the upload opens the batch's
          review page. */}
      <Link
        href="/controls/imports"
        className={buttonVariants({ variant: "outline", size: "sm" })}
        data-testid="controls-import"
      >
        Import controls
      </Link>
    </>
  );

  const subtitle =
    view === "mine" ? (
      <>
        Your tenant&apos;s controls <Term id="evaluation">evaluated</Term>{" "}
        against live evidence
      </>
    ) : (
      <>
        The SCF <Term id="anchor">anchor</Term> catalog, with the controls your
        tenant has on it
      </>
    );

  if (listQ.isLoading) {
    return (
      <ListPage
        title={pageTitle("/controls")}
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
        title={pageTitle("/controls")}
        subtitle={subtitle}
        actions={actions}
      >
        <Alert variant="destructive" data-testid="controls-load-error">
          <AlertTitle>Could not load controls</AlertTitle>
          <AlertDescription>{(listQ.error as Error).message}</AlertDescription>
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
  //
  // Issue #1741 — in the default view, "truly empty" means the tenant has
  // no controls (the catalog may well be loaded), so the copy says so and
  // offers the catalog view instead of blaming the importer.
  //
  // #2131 — "truly empty" is the server's unfiltered count reading zero;
  // until that count loads, an empty filtered page is a filter result.
  const isTrulyEmpty =
    unfilteredTotal === 0 &&
    (isDefault(filters) || view === "catalog" || myTotalQ.data === 0);
  const trulyEmptyState =
    view === "mine" ? (
      <EmptyState
        icon={emptyStateIcon}
        title="No controls in your tenant yet"
        body="Upload a control bundle with the atlas CLI to add your first control. The SCF catalog view lists every anchor a control can map to."
        cta={{
          label: "Browse the SCF catalog",
          onClick: () => updateParam(VIEW_PARAM, "catalog"),
        }}
      />
    ) : (
      <EmptyState
        icon={emptyStateIcon}
        title="No SCF anchors to show"
        body="The global SCF anchor catalog is empty in this deployment, or no anchor matches the Has control filter. Import a framework via the atlas CLI, or run the SCF importer to populate the catalog. See the user guide for the bootstrap walkthrough."
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
      title={pageTitle("/controls")}
      subtitle={subtitle}
      actions={actions}
      filterRow={
        <FilterPills
          pills={pills}
          onChange={(id, v) =>
            id === VIEW_PARAM || id === HAS_CONTROL_PARAM
              ? updateParam(id, v)
              : updateFilter(id as keyof ControlFilters, v)
          }
          meta={meta}
        />
      }
    >
      {/* Slice 448 — saved filter-views bar (always shown) + selection
          bar (shown only when a selection exists). The save affordance
          is enabled only when at least one filter is active (saving the
          all-default filter set is meaningless). */}
      <SavedViewsBar
        views={savedViews}
        activeViewId={activeViewId}
        canSave={!isDefault(filters)}
        onLoadView={onLoadView}
        onSaveView={onSaveView}
        onDeleteView={onDeleteView}
      />
      {selected.size > 0 ? (
        <SelectionBar
          selectedCount={selected.size}
          overCap={selectionOverCap}
          onClear={onClearSelection}
          onAssignToMe={onAssignToMe}
          assigning={bulkAssignMutation.isPending}
        />
      ) : null}
      {/* Slice 745 — bulk-assign confirmation in a PERSISTENT region,
          independent of `selected.size`. The success handler clears the
          selection in the same batched update that sets the message; when
          the message lived inside the selection bar (gated on
          `selected.size > 0`) it unmounted in the same render it was set,
          so the operator never saw it. Rendering it here, outside the
          gated bar, lets the confirmation survive the selection-clear and
          be observable (re-enables slice 743's quarantined message
          assertion). Auto-dismisses via BULK_ASSIGN_MESSAGE_TTL_MS. */}
      <BulkAssignMessage message={assignMessage} />
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
        columns={tableColumns}
        rows={rows}
        rowKey={(row) => row.key}
        onRowClick={(row) => {
          const href = controlHref(row);
          if (href) router.push(href);
        }}
        emptyFallback={emptyState}
        // Slice 281 — collapse to a card stack at `< md`. Seven-column
        // anchor table horizontal-scrolls at 375px; the card variant
        // surfaces the primary affordance (SCF anchor + Name link) at
        // the top of each card so the row is one-tap. Desktop UX is
        // unchanged at `≥ md` (P0-281-1).
        mobileMode="cards"
      />
      {/* Slice 227 — pagination footer. Rendered ONLY when there is at
          least one row in the filtered set; an empty result delegates
          to the `emptyFallback` above and a stand-alone pagination
          chrome would be honesty-confusing (Previous / Next clicks
          would no-op). On a single-page result the footer still renders
          so the user gets the truth-telling "Showing N of N" summary;
          both Previous and Next are disabled and clearly read as such.
          Matches the slice 246 /risks composition (decisions log D3). */}
      {filteredTotal > 0 ? (
        <ListPagination
          currentPage={currentPage}
          pageSize={CONTROLS_PAGE_SIZE}
          totalCount={filteredTotal}
          onPageChange={goToPage}
          testIdPrefix="controls-pagination"
        />
      ) : null}
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
