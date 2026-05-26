"use client";

// Slice 099 + 106 — /evidence list view.
//
// Slice 099 originally shipped a control-pill-driven UX with a "Pick a
// control" prompt as the empty-state, because the upstream
// `GET /v1/evidence` REQUIRED `control_id`. Slice 106 makes that param
// optional and adds four filter axes (kind, result, source_actor_type,
// source_actor_id) plus the `result` column on the wire shape. The page
// now defaults to the tenant-wide ledger window with five filter pills.
//
// Data source (slice 106 D1):
//   Row source is `evidenceWire` in
//   `internal/api/controldetail/handler.go` — the same wire shape on
//   both code paths (per-control + tenant-wide). The page binds to
//   `EvidenceRecord` from `web/lib/api.ts`.
//
// Constitutional invariants honored:
//   - Invariant 6 (tenant isolation): the BFF at /api/evidence forwards
//     the bearer cookie; the platform enforces tenant isolation via RLS
//     on the tenant_id predicate plus FORCE ROW LEVEL SECURITY. The UI
//     does NOT pass tenant_id.
//
// Anti-criteria honored (P0):
//   - P0-A1: NO invented columns — every column is derived from
//            `EvidenceRecord`. Slice 106 now surfaces `result` on the
//            wire, so the page renders the REAL `result` cell (the
//            slice-099 em-dash placeholder is gone).
//   - P0-A2: hash rendered as 8-character prefix ONLY; full hash on
//            copy-click.
//   - P0-A3: horizontal pill filter row ONLY — no left filter sidebar.
//            Per `<FilterPills>` from the shared shell.
//   - P0-A4: neutral test-* tokens in tests; no vendor token prefixes.

import { useQuery } from "@tanstack/react-query";
import { useRouter, useSearchParams } from "next/navigation";
import { Suspense, useCallback, useEffect, useMemo, useState } from "react";

import {
  CursorPagination,
  FilterPills,
  ListLoadingSkeleton,
  ListPage,
  ListTable,
  popCursor,
  pushCursor,
  type FilterPill,
  type ListColumn,
} from "@/components/list";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button, buttonVariants } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogPortal,
  DialogTitle,
} from "@/components/ui/dialog";
import {
  fetchAuditPeriods,
  fetchControlsList,
  fetchEvidenceList,
  fetchScopeCells,
  type AuditPeriodsListResponse,
  type ControlsListResponse,
  type EvidenceListResponse,
  type EvidenceRecord,
  type ScopeCellsListResponse,
} from "@/lib/api";

import {
  ALL,
  NONE,
  SCOPE_CELL_CAP,
  SOURCE_DELIM,
  buildControlOptions,
  buildKindOptions,
  buildResultOptions,
  buildScopeCellOptions,
  buildSinceOptions,
  buildSourceOptions,
  clearFilters,
  DEFAULT_FILTERS,
  isDefault,
  setFilter,
  sinceCutoff,
  toFetchOptions,
  type EvidenceFilters,
} from "./filters";
import {
  hashPrefix,
  ledgerSubtitleSuffix,
  observedAtLabel,
  prettyJSON,
  recordCountMeta,
  scopeLabel,
  sourceSummary,
} from "./format";
import {
  PUSH_CTA_HREF,
  PUSH_CTA_LABEL,
  PUSH_CTA_SUBTITLE_PREFIX,
} from "./push-cta";

// URL parameter names mirror the upstream + BFF FORWARD_PARAMS so the
// browser URL is a faithful echo of the request that the BFF will
// dispatch upstream. Bookmarkable + shareable.
//
// Slice 234 — `scope_cell_id` is a real upstream param (slice 234
// backend extension). The Since pill stores its preset *key* under
// `since_preset` so a "Last 7 days" selection survives a reload
// without re-resolving to a sliding RFC3339 cutoff each time the page
// renders; the resolved `since` query param is computed at submit time
// from the key.
const URL_KEYS: Record<keyof EvidenceFilters, string> = {
  controlId: "control_id",
  kind: "kind",
  result: "result",
  sourceActorType: "source_actor_type",
  sourceActorId: "source_actor_id",
  scopeCellId: "scope_cell_id",
  since: "since_preset",
};

// Slice 237 — the cursor URL param is owned by the page (not by the
// filter module) because cursors are pagination state, not filter state.
// The current cursor lives in the URL so a deep-link is shareable
// (anti-criterion P0-237-2 only forbids persisting the STACK; the
// current cursor IS shareable per the spec narrative). The stack is
// in-memory only.
const CURSOR_PARAM = "cursor";

function EvidencePageInner() {
  const router = useRouter();
  const search = useSearchParams();

  // URL-driven filter state — mirrors the slice 098 / 102 pattern so
  // every active filter is shareable / bookmarkable.
  const filters: EvidenceFilters = useMemo(() => {
    const out = { ...DEFAULT_FILTERS };
    const cid = search.get(URL_KEYS.controlId);
    if (cid) out.controlId = cid;
    const k = search.get(URL_KEYS.kind);
    if (k) out.kind = k;
    const r = search.get(URL_KEYS.result);
    if (r) out.result = r;
    const sat = search.get(URL_KEYS.sourceActorType);
    if (sat) out.sourceActorType = sat;
    const sai = search.get(URL_KEYS.sourceActorId);
    if (sai) out.sourceActorId = sai;
    // Slice 234.
    const sc = search.get(URL_KEYS.scopeCellId);
    if (sc) out.scopeCellId = sc;
    const sp = search.get(URL_KEYS.since);
    if (sp) out.since = sp;
    return out;
  }, [search]);

  // Slice 237 — pagination state. The current cursor (the keyset token
  // identifying the start of the currently-rendered page) lives in the
  // URL for shareable deep-links. The cursor STACK (the history of
  // cursors the operator paged THROUGH on the way here) lives in React
  // state only — never persisted, never URL-encoded — per
  // anti-criterion P0-237-2.
  const urlCursor = search.get(CURSOR_PARAM) ?? "";
  const [cursorStack, setCursorStack] = useState<string[]>([]);

  const updateFilter = (key: keyof EvidenceFilters, value: string) => {
    const next = setFilter(filters, key, value);
    const sp = new URLSearchParams(search.toString());
    const urlKey = URL_KEYS[key];
    const sentinel = key === "controlId" ? NONE : ALL;
    if (next[key] === sentinel) {
      sp.delete(urlKey);
    } else {
      sp.set(urlKey, next[key]);
    }
    // Slice 237 — any filter mutation resets pagination: the current
    // cursor is keyset-bound to the OLD filter window, so keeping it
    // would yield a non-deterministic page slice. Clear both surfaces:
    // the URL cursor AND the in-memory stack.
    sp.delete(CURSOR_PARAM);
    setCursorStack([]);
    router.replace(`/evidence?${sp.toString()}`);
  };

  // Slice 234 — the Source pill's value is the composite `type|id`
  // tuple (or `ALL`). On change we update BOTH URL params atomically
  // so the round-trip stays consistent. A composite key keeps the pill
  // honest: the operator selects one observed tuple, the page binds
  // exactly that tuple, no cross-product surprises.
  const updateSourceFilter = (value: string) => {
    const sp = new URLSearchParams(search.toString());
    if (value === ALL) {
      sp.delete(URL_KEYS.sourceActorType);
      sp.delete(URL_KEYS.sourceActorId);
    } else {
      const [t, id] = value.split(SOURCE_DELIM);
      if (t) sp.set(URL_KEYS.sourceActorType, t);
      else sp.delete(URL_KEYS.sourceActorType);
      if (id) sp.set(URL_KEYS.sourceActorId, id);
      else sp.delete(URL_KEYS.sourceActorId);
    }
    // Slice 237 — same reset semantics as `updateFilter` above.
    sp.delete(CURSOR_PARAM);
    setCursorStack([]);
    router.replace(`/evidence?${sp.toString()}`);
  };

  // Dispatcher: every pill's onChange routes through here so the Source
  // pill's composite handling stays local to the page (filters.ts is
  // wire-shape-agnostic).
  const onPillChange = (id: string, value: string) => {
    if (id === "source") {
      updateSourceFilter(value);
      return;
    }
    updateFilter(id as keyof EvidenceFilters, value);
  };

  const clearAll = () => {
    const cleared = clearFilters();
    void cleared;
    // Slice 237 — clearing filters also clears the pagination cursor
    // (the URL is rewritten to `/evidence` with no query string) and
    // the in-memory stack.
    setCursorStack([]);
    router.replace(`/evidence`);
  };

  // Slice 237 — pagination mutators. Both routes through `router.replace`
  // so the browser's back/forward stack does not fill up with cursor
  // transitions (the operator can still navigate away from the page and
  // come back via the app shell).
  const goNext = (nextCur: string) => {
    if (!nextCur) return;
    setCursorStack((s) => pushCursor(s, urlCursor));
    const sp = new URLSearchParams(search.toString());
    sp.set(CURSOR_PARAM, nextCur);
    router.replace(`/evidence?${sp.toString()}`);
  };
  const goPrevious = () => {
    const { popped, rest } = popCursor(cursorStack);
    setCursorStack(rest);
    const sp = new URLSearchParams(search.toString());
    // `popped === ""` means we paged back to the first (no-cursor) page.
    // `popped === undefined` means the stack was empty: the operator
    // landed here via a deep-link with a URL cursor; clicking Previous
    // returns to the unparameterized first page (spec AC-5).
    if (popped === undefined || popped === "") {
      sp.delete(CURSOR_PARAM);
    } else {
      sp.set(CURSOR_PARAM, popped);
    }
    router.replace(`/evidence?${sp.toString()}`);
  };

  // Anchor catalog — drives the Control pill option list. The fetch
  // reuses the slice 098 endpoint so we don't duplicate the wire.
  const anchorsQ = useQuery<ControlsListResponse>({
    queryKey: ["controls", "list"],
    queryFn: () => fetchControlsList(),
  });
  const anchors = useMemo(() => anchorsQ.data?.anchors ?? [], [anchorsQ.data]);
  const controlOptions = useMemo(() => buildControlOptions(anchors), [anchors]);
  const resultOptions = useMemo(() => buildResultOptions(), []);

  // Slice 234 — scope cells drive the Scope pill option list. Failure
  // to load is non-fatal: the pill still renders with just "All cells".
  // Reuses the slice 224 BFF + query key so the catalog is shared with
  // /controls (TanStack caches it under the same key).
  const scopeCellsQ = useQuery<ScopeCellsListResponse>({
    queryKey: ["scope-cells"],
    queryFn: fetchScopeCells,
  });
  const scopeCells = useMemo(
    () => scopeCellsQ.data?.cells ?? [],
    [scopeCellsQ.data],
  );
  const scopeOptions = useMemo(
    () => buildScopeCellOptions(scopeCells),
    [scopeCells],
  );
  const scopeCellsCapped = scopeCells.length > SCOPE_CELL_CAP;

  // Slice 234 — `nowAtMount` is captured once per mount so the
  // "active audit period" and the Since cutoff computations stay pure
  // for this render lifecycle (React purity rule:
  // `react-hooks/purity` rejects `Date.now()` inside a useMemo). A
  // sliding window still re-resolves on next mount; for an open page,
  // the window is effectively pinned to the moment the operator
  // opened it — the desirable shape for an audit-period operator
  // workflow (the window does not silently shift while the operator
  // is reading the table).
  const [nowAtMount] = useState<Date>(() => new Date());

  // Slice 234 — audit periods drive the "Audit period (current)" Since
  // option. We pick the active period as `status === 'open'` whose
  // [period_start, period_end] contains today's date. Failure to load
  // is non-fatal: the option still renders with the generic label and
  // resolves to undefined cutoff (the upstream default 30-day window
  // takes over).
  const auditPeriodsQ = useQuery<AuditPeriodsListResponse>({
    queryKey: ["audit-periods", "list"],
    queryFn: fetchAuditPeriods,
  });
  const activeAuditPeriod = useMemo(() => {
    const periods = auditPeriodsQ.data?.audit_periods ?? [];
    const nowMs = nowAtMount.getTime();
    return (
      periods.find((p) => {
        if (p.status !== "open") return false;
        const start = Date.parse(p.period_start);
        const end = Date.parse(p.period_end);
        return (
          Number.isFinite(start) &&
          Number.isFinite(end) &&
          start <= nowMs &&
          nowMs <= end
        );
      }) ?? null
    );
  }, [auditPeriodsQ.data, nowAtMount]);
  const sinceOptions = useMemo(
    () => buildSinceOptions(activeAuditPeriod?.name),
    [activeAuditPeriod],
  );

  // Slice 234 — resolve the Since preset key to an RFC3339 cutoff.
  // Pinned to `nowAtMount` for the same purity reason as above.
  const resolvedSince = useMemo(() => {
    if (filters.since === ALL) return undefined;
    return sinceCutoff(
      filters.since,
      nowAtMount,
      activeAuditPeriod?.period_start,
    );
  }, [filters.since, activeAuditPeriod, nowAtMount]);

  // Evidence ledger query — slice 106 always runs (no more gating on a
  // control_id presence). The filter translator drops sentinel values
  // so the URL query string only carries narrowing predicates.
  //
  // Slice 237 — when the URL carries a `?cursor=…` value, the page
  // forwards it as the `cursor` fetch option so the upstream returns
  // the keyset-paginated page that starts AT that cursor. An empty URL
  // cursor (the default state) omits the field entirely so the
  // TanStack cache treats first-page-via-undefined and first-page-via-
  // empty-string as the same entry.
  const fetchOpts = useMemo(() => {
    const base = toFetchOptions(filters, resolvedSince);
    if (urlCursor) {
      return { ...base, cursor: urlCursor };
    }
    return base;
  }, [filters, resolvedSince, urlCursor]);
  const evidenceQ = useQuery<EvidenceListResponse>({
    queryKey: ["evidence", "list", fetchOpts],
    queryFn: () => fetchEvidenceList(fetchOpts),
  });
  const records: EvidenceRecord[] = useMemo(
    () => evidenceQ.data?.evidence ?? [],
    [evidenceQ.data],
  );
  // Slice 236 — tenant-wide ledger total, surfaced on the wire by the
  // upstream handler. `undefined` while the query is in flight; the
  // meta + subtitle branches treat that as "skip the of-M suffix".
  const ledgerTotal: number | undefined = evidenceQ.data?.total;
  // Slice 237 — the upstream's keyset cursor for the NEXT page. Empty
  // string ("" or undefined) means "no more pages" — drives the Next
  // button's disabled state. Previous is enabled when EITHER the
  // in-memory stack has entries OR the URL carries a cursor (deep-link
  // case — clicking Previous returns to the unparameterized first page
  // per spec AC-5).
  const upstreamNextCursor: string = evidenceQ.data?.next_cursor ?? "";
  const hasNextPage = upstreamNextCursor !== "";
  const hasPreviousPage = cursorStack.length > 0 || urlCursor !== "";
  const kindOptions = useMemo(
    () => buildKindOptions(records.map((r) => r.evidence_kind ?? "")),
    [records],
  );
  // Slice 234 — Source pill options derived from the observed
  // (actor_type, actor_id) tuples on the current page of evidence
  // rows. Same provenance shape as the table cell renderer; no
  // invented values (P0 — "Options come from observed values only").
  const sourceOptions = useMemo(
    () =>
      buildSourceOptions(
        records.map((r) => ({
          actor_type:
            typeof r.source?.actor_type === "string"
              ? r.source.actor_type
              : undefined,
          actor_id:
            typeof r.source?.actor_id === "string"
              ? r.source.actor_id
              : undefined,
        })),
      ),
    [records],
  );
  // Slice 234 — the Source pill's current value is the composite
  // `type|id` (or ALL when neither is narrowing). When the URL carries
  // only one of the two params, we still surface the pill as "narrowed"
  // so the operator sees the state is non-default — but the dropdown
  // value falls back to ALL because no observed tuple matches a half-
  // narrowed state.
  const sourcePillValue =
    filters.sourceActorType !== ALL && filters.sourceActorId !== ALL
      ? `${filters.sourceActorType}${SOURCE_DELIM}${filters.sourceActorId}`
      : ALL;

  // Row drawer state — clicking a row opens an inline Dialog showing
  // the full record JSON pretty-printed (slice 099 AC-7, decision D3).
  const [drawerRecord, setDrawerRecord] = useState<EvidenceRecord | null>(null);
  const openDrawer = useCallback((row: EvidenceRecord) => {
    setDrawerRecord(row);
  }, []);
  const closeDrawer = useCallback(() => setDrawerRecord(null), []);

  // Hash-copy feedback. Tracks the recently-copied content_hash so the
  // cell can show a brief "Copied!" affordance.
  const [copiedHash, setCopiedHash] = useState<string | null>(null);
  useEffect(() => {
    if (!copiedHash) return;
    const t = setTimeout(() => setCopiedHash(null), 1500);
    return () => clearTimeout(t);
  }, [copiedHash]);

  const handleHashClick = useCallback(
    async (hash: string, e: React.MouseEvent) => {
      e.stopPropagation();
      try {
        if (navigator?.clipboard?.writeText) {
          await navigator.clipboard.writeText(hash);
        }
        setCopiedHash(hash);
      } catch {
        // Clipboard API unavailable (insecure context / blocked).
        // Fall through silently — the user can still triple-click the
        // cell text to select-and-copy manually.
      }
    },
    [],
  );

  const pills: FilterPill[] = [
    {
      id: "controlId",
      label: "Control",
      value: filters.controlId === NONE ? NONE : filters.controlId,
      options: controlOptions,
    },
    {
      id: "kind",
      label: "Kind",
      value: filters.kind,
      options: kindOptions,
    },
    {
      id: "result",
      label: "Result",
      value: filters.result,
      options: resultOptions,
    },
    // Slice 234 — three new pills bring the row to the six-pill mockup
    // parity (Plans/mockups/evidence.html lines 125-184). Source binds
    // both source_actor_* params atomically (composite key handled by
    // updateSourceFilter); Scope binds scope_cell_id; Since maps a
    // preset key to an RFC3339 cutoff client-side.
    {
      id: "source",
      label: "Source",
      value: sourcePillValue,
      options: sourceOptions,
    },
    {
      id: "scopeCellId",
      label: "Scope",
      value: filters.scopeCellId,
      options: scopeOptions,
    },
    {
      id: "since",
      label: "Since",
      value: filters.since,
      options: sinceOptions,
    },
  ];

  // Slice 236 — the meta line surfaces the filtered window count (N) AND
  // the tenant-wide ledger total (M) so the operator can distinguish
  // "filters narrowed to zero" from "ledger is empty tenant-wide".
  // `ledgerTotal` is undefined until the query lands; fall back to the
  // old-shape "Showing N records" so the loading state doesn't lie about
  // the ledger being empty. Once the query resolves, the recordCountMeta
  // formatter takes over (the empty-ledger branch renders "No records in
  // ledger yet").
  const meta =
    ledgerTotal === undefined ? (
      <span data-testid="evidence-record-count-meta">
        Showing{" "}
        <span className="text-foreground font-medium">{records.length}</span>{" "}
        record
        {records.length === 1 ? "" : "s"}
      </span>
    ) : (
      <span data-testid="evidence-record-count-meta">
        {recordCountMeta(records.length, ledgerTotal)}
      </span>
    );

  const columns: ListColumn<EvidenceRecord>[] = [
    {
      id: "observed_at",
      header: "Observed",
      cell: (row) => (
        <span
          className="font-mono text-xs text-muted-foreground"
          data-testid="evidence-row-observed-at"
        >
          {observedAtLabel(row.observed_at)}
        </span>
      ),
    },
    {
      id: "evidence_kind",
      header: "Evidence kind",
      cell: (row) => (
        <span
          className="font-mono text-xs"
          data-testid="evidence-row-evidence-kind"
        >
          {row.evidence_kind ?? "—"}
        </span>
      ),
    },
    {
      id: "result",
      header: "Result",
      cell: (row) => (
        <span
          className="font-mono text-xs"
          data-testid="evidence-row-result"
          title={row.result}
        >
          {row.result}
        </span>
      ),
    },
    {
      id: "source",
      header: "Source",
      cell: (row) => (
        <span className="text-xs" data-testid="evidence-row-source">
          {sourceSummary(row.source)}
        </span>
      ),
    },
    {
      id: "scope",
      header: "Scope",
      cell: (row) => (
        <span
          className="font-mono text-xs text-muted-foreground"
          title={row.scope_cell ?? undefined}
          data-testid="evidence-row-scope"
        >
          {scopeLabel(row.scope_cell)}
        </span>
      ),
    },
    {
      id: "hash",
      header: "Hash",
      cell: (row) => {
        const isCopied = copiedHash === row.content_hash;
        return (
          <button
            type="button"
            className="font-mono text-[10px] text-muted-foreground hover:text-primary cursor-pointer"
            onClick={(e) => handleHashClick(row.content_hash, e)}
            title={isCopied ? "Copied!" : `Click to copy: ${row.content_hash}`}
            data-testid="evidence-row-hash"
          >
            {isCopied ? "Copied!" : `${hashPrefix(row.content_hash)}…`}
          </button>
        );
      },
    },
  ];

  // Slice 138 — three Export links to the slice 138 evidence BFF
  // (`/api/admin/evidence/export?format=...`). Each is an `<a>` so the
  // browser's native file-save dialog handles the download; the BFF
  // streams the platform response back unchanged. Per slice 138
  // P0-A-Ledger-1, the canonical column set EXCLUDES payload — operators
  // who need payload introspection use the evidence-detail page (RLS-
  // protected read), not bulk export.
  const actions = (
    <>
      <div
        className="flex items-center gap-1"
        data-testid="evidence-export-buttons"
      >
        <span className="text-xs text-muted-foreground">Export:</span>
        {(["csv", "json", "xlsx"] as const).map((fmt) => (
          <a
            key={fmt}
            href={`/api/admin/evidence/export?format=${fmt}`}
            className={buttonVariants({ variant: "outline", size: "sm" })}
            data-testid={`evidence-export-${fmt}`}
          >
            {fmt.toUpperCase()}
          </a>
        ))}
      </div>
      {/* Slice 233 — primary CTA points at the canonical CLI push doc
          rather than rendering as a permanently-disabled `<Button>`.
          The in-app Push dialog (Option B in the slice 233 spec) is a
          deferred follow-on; until it ships, "Push evidence →" is the
          truthful affordance for the operator who wants to push their
          first record. Opens in a new tab so the operator does not
          lose their filtered ledger view. */}
      <a
        href={PUSH_CTA_HREF}
        target="_blank"
        rel="noreferrer"
        className={buttonVariants({ size: "sm" })}
        data-testid="evidence-push-cta"
      >
        {PUSH_CTA_LABEL}
      </a>
    </>
  );

  // Slice 233 — subtitle gains a second sentence directing operators
  // to the canonical CLI push doc. The trailing "Push evidence →" is
  // anchored to the same destination as the page-level CTA so both
  // surfaces point at the same place; the inline link uses the same
  // `data-testid="evidence-push-cta-inline"` shape so a Playwright
  // selector can disambiguate it from the action-bar CTA.
  //
  // Slice 236 — the subtitle now ALSO surfaces a constant ledger-context
  // suffix (`append-only · M records`) independent of filter state, so
  // the operator has a tenant-wide signal even with narrowing filters
  // applied. The suffix is rendered via `<span>` rather than inline
  // text so a Playwright selector can target it specifically. When the
  // ledger is empty (`total === 0`) the suffix collapses to empty —
  // the meta row's "No records in ledger yet" carries the operator
  // signal in that case (mockup parity: `Plans/mockups/evidence.html`
  // line 111).
  const ledgerSuffix =
    ledgerTotal === undefined ? "" : ledgerSubtitleSuffix(ledgerTotal);
  const subtitleNode = (
    <>
      Append-only · ingestion separated from evaluation · point-in-time replay
      always possible. {PUSH_CTA_SUBTITLE_PREFIX}
      <a
        href={PUSH_CTA_HREF}
        target="_blank"
        rel="noreferrer"
        className="underline hover:text-foreground"
        data-testid="evidence-push-cta-inline"
      >
        {PUSH_CTA_LABEL}
      </a>
      {ledgerSuffix ? (
        <>
          {" · "}
          <span data-testid="evidence-ledger-subtitle-suffix">
            {ledgerSuffix}
          </span>
        </>
      ) : null}
    </>
  );

  // True-empty state: a filter is in play (or not) and the upstream
  // returned zero rows. Surfaces TWO actions: "Clear filters" + "Set
  // up a connector →". Custom block (the shared `<EmptyState>` shell
  // only takes one CTA).
  const noRecordsEmptyState = (
    <div
      data-testid="list-empty-state"
      className="rounded-xl border bg-card py-16 px-6 text-center"
    >
      <div className="mx-auto mb-3 text-muted-foreground">
        <EvidenceLedgerIcon />
      </div>
      <div
        className="text-sm font-semibold text-foreground mb-1"
        data-testid="evidence-empty-title"
      >
        No evidence records match these filters
      </div>
      <div className="text-xs text-muted-foreground mb-4">
        Try a wider time window, clear filters, or push a record via CLI or
        connector.
      </div>
      <div className="flex items-center gap-2 justify-center">
        <Button
          variant="outline"
          size="sm"
          onClick={clearAll}
          disabled={isDefault(filters)}
          data-testid="evidence-empty-clear"
        >
          Clear filters
        </Button>
        <Button
          size="sm"
          onClick={() => router.push("/admin/credentials")}
          data-testid="evidence-empty-connector"
        >
          Set up a connector →
        </Button>
      </div>
    </div>
  );

  // ---- render ----

  // Anchor list still loading: render the skeleton (we need anchors
  // to populate the Control pill, so we wait for them first).
  if (anchorsQ.isLoading) {
    return (
      <ListPage
        title="Evidence ledger"
        subtitle={subtitleNode}
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
        title="Evidence ledger"
        subtitle={subtitleNode}
        actions={actions}
      >
        <Alert variant="destructive" data-testid="evidence-load-error">
          <AlertTitle>Could not load controls</AlertTitle>
          <AlertDescription>
            {(anchorsQ.error as Error).message}
          </AlertDescription>
        </Alert>
      </ListPage>
    );
  }

  // Evidence query in flight.
  if (evidenceQ.isLoading) {
    return (
      <ListPage
        title="Evidence ledger"
        subtitle={subtitleNode}
        actions={actions}
        filterRow={
          <FilterPills pills={pills} onChange={onPillChange} meta={meta} />
        }
      >
        <ListLoadingSkeleton />
      </ListPage>
    );
  }

  if (evidenceQ.isError) {
    return (
      <ListPage
        title="Evidence ledger"
        subtitle={subtitleNode}
        actions={actions}
        filterRow={
          <FilterPills pills={pills} onChange={onPillChange} meta={meta} />
        }
      >
        <Alert variant="destructive" data-testid="evidence-load-error">
          <AlertTitle>Could not load evidence</AlertTitle>
          <AlertDescription>
            {(evidenceQ.error as Error).message}
          </AlertDescription>
        </Alert>
      </ListPage>
    );
  }

  return (
    <>
      <ListPage
        title="Evidence ledger"
        subtitle={subtitleNode}
        actions={actions}
        filterRow={
          <FilterPills pills={pills} onChange={onPillChange} meta={meta} />
        }
      >
        {/* Slice 234 — Scope filter cell-cap banner (mirrors the slice
            224 banner above the controls table). The banner fires when
            the tenant has more cells than `SCOPE_CELL_CAP`; the
            dropdown still works for the first 50 cells. */}
        {scopeCellsCapped ? (
          <Alert data-testid="evidence-scope-cells-capped" className="mb-3">
            <AlertTitle>
              Scope filter capped at {SCOPE_CELL_CAP} cells
            </AlertTitle>
            <AlertDescription>
              Your tenant has {scopeCells.length} scope cells; only the first{" "}
              {SCOPE_CELL_CAP} are listed in the Scope filter. A typeahead
              replacement is tracked as a follow-on.
            </AlertDescription>
          </Alert>
        ) : null}
        <ListTable<EvidenceRecord>
          columns={columns}
          rows={records}
          rowKey={(row) => row.evidence_id}
          onRowClick={openDrawer}
          emptyFallback={noRecordsEmptyState}
          // Slice 281 — collapse to a card stack at `< md`. The
          // evidence ledger renders 6 columns (observed / kind /
          // result / source / scope / hash) which horizontal-scrolls
          // at 375px. The card variant stacks the provenance label/
          // value pairs vertically so the operator can scan the
          // ledger one-handed on mobile. The row-click `openDrawer`
          // handler still fires (the card carries the same handler)
          // so the full-record dialog continues to work. Desktop UX
          // is unchanged at `≥ md` (P0-281-1).
          mobileMode="cards"
        />
        {/* Slice 237 — cursor-paginated footer. Matches the mockup at
            Plans/mockups/evidence.html lines 266-272. Renders only when
            the current page has rows (slice 246 D3 convention: empty
            sets surface the EmptyState above instead). The Previous
            stack lives in `cursorStack` (in-memory, page-scoped). */}
        {records.length > 0 ? (
          <CursorPagination
            recordCount={records.length}
            hasNext={hasNextPage}
            hasPrevious={hasPreviousPage}
            onNext={() => goNext(upstreamNextCursor)}
            onPrevious={goPrevious}
            testIdPrefix="evidence-pagination"
          />
        ) : null}
      </ListPage>

      {/* Row-detail drawer — opens on row click, shows the full record
          JSON pretty-printed. Per slice 099 AC-7 / decision D3: simpler
          than a per-record page stub, no orphan route. */}
      <Dialog
        open={drawerRecord !== null}
        onOpenChange={(open) => {
          if (!open) closeDrawer();
        }}
      >
        <DialogPortal>
          <DialogContent
            className="max-w-3xl"
            data-testid="evidence-row-drawer"
          >
            <DialogHeader>
              <DialogTitle>Evidence record</DialogTitle>
              <DialogDescription>
                {drawerRecord
                  ? `${drawerRecord.evidence_kind ?? "(no kind)"} · observed ${
                      drawerRecord.observed_at
                    }`
                  : ""}
              </DialogDescription>
            </DialogHeader>
            {drawerRecord ? (
              <div className="mt-2 space-y-3">
                <div>
                  <div className="text-[11px] uppercase tracking-wider text-muted-foreground mb-1">
                    Full content hash
                  </div>
                  <div
                    className="font-mono text-xs break-all"
                    data-testid="evidence-drawer-full-hash"
                  >
                    {drawerRecord.content_hash}
                  </div>
                </div>
                <div>
                  <div className="text-[11px] uppercase tracking-wider text-muted-foreground mb-1">
                    Result
                  </div>
                  <div
                    className="font-mono text-xs"
                    data-testid="evidence-drawer-result"
                  >
                    {drawerRecord.result}
                  </div>
                </div>
                <div>
                  <div className="text-[11px] uppercase tracking-wider text-muted-foreground mb-1">
                    Scope cell
                  </div>
                  <div className="font-mono text-xs break-all">
                    {drawerRecord.scope_cell ?? "—"}
                  </div>
                </div>
                <div>
                  <div className="text-[11px] uppercase tracking-wider text-muted-foreground mb-1">
                    Source provenance
                  </div>
                  <pre
                    className="font-mono text-xs bg-muted/40 rounded p-3 overflow-x-auto"
                    data-testid="evidence-drawer-source-json"
                  >
                    {prettyJSON(drawerRecord.source)}
                  </pre>
                </div>
              </div>
            ) : null}
          </DialogContent>
        </DialogPortal>
      </Dialog>
    </>
  );
}

function EvidenceLedgerIcon() {
  return (
    <svg
      className="w-12 h-12 mx-auto"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.5"
      aria-hidden
    >
      <path
        d="M19.5 14.25v-2.625a3.375 3.375 0 00-3.375-3.375h-1.5A1.125 1.125 0 0113.5 7.125v-1.5a3.375 3.375 0 00-3.375-3.375H8.25m0 12.75h7.5m-7.5 3H12M10.5 2.25H5.625c-.621 0-1.125.504-1.125 1.125v17.25c0 .621.504 1.125 1.125 1.125h12.75c.621 0 1.125-.504 1.125-1.125V11.25a9 9 0 00-9-9z"
        strokeLinecap="round"
        strokeLinejoin="round"
      />
    </svg>
  );
}

export default function EvidenceListPage() {
  // useSearchParams must be wrapped in Suspense in App Router (Next 16
  // strict-mode requirement). The fallback is the same skeleton the
  // query-pending state shows, so the perceived layout is stable.
  return (
    <Suspense fallback={<ListLoadingSkeleton />}>
      <EvidencePageInner />
    </Suspense>
  );
}
