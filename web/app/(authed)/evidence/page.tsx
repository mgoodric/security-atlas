"use client";

// Slice 099 — /evidence list view.
//
// Today `/evidence` 404'd in the sidebar (audit finding F-4 in
// `Plans/canvas/13-ui-mockup-audit-2026-05-16.md`). This page ships the
// missing list view per the design captured in slice 093
// (`Plans/mockups/evidence.html` + `Plans/canvas/12-ui-fill-in-design-
// decisions.md` §2/3/7/8).
//
// The page consumes the shared `web/components/list/*` shell — the
// reusable primitives that slice 098 extracted and that the four
// remaining list-view slices (100/101/102 already shipped + 099 here)
// also consume.
//
// Data source resolution (slice 099 D1):
//   Row source is `evidenceWire` in
//   `internal/api/controldetail/handler.go` — the row shape the
//   upstream `GET /v1/evidence?control_id=` returns. The slice text
//   cites `recordWire` from `internal/api/evidence/http.go`, but that
//   is the PUSH wire shape; the existing GET shape is `evidenceWire`
//   (a strict subset). We bind to what the backend actually returns
//   today.
//
// Control-id is REQUIRED today (slice 099 D2):
//   The upstream handler at `internal/api/controldetail/handler.go`
//   Evidence requires `control_id`. The page renders the "Pick a
//   control" prompt as the empty state until the user selects one via
//   the Control filter pill. When a control is selected, the page
//   calls `fetchEvidenceList(controlId)` and renders the row list.
//   Spillover slice 106 files the backend extension to make
//   `control_id` optional + add `?kind=` / `?result=` for a true
//   tenant-wide ledger view.
//
// Constitutional invariants honored:
//   - Invariant 6 (tenant isolation): the BFF at /api/evidence
//     forwards the bearer cookie to /v1/evidence; the platform
//     enforces tenant isolation via RLS. The UI does not pass
//     tenant_id.
//
// Anti-criteria honored (P0):
//   - P0-A1: NO invented columns — every column is derived from
//            `evidenceWire`. The slice design doc §7 lists a `result`
//            column, but that field is NOT on `evidenceWire` today
//            (it lives only on the PUSH `recordWire`). We OMIT the
//            column rather than fabricate one. Slice 106 will surface
//            it on the GET shape and a follow-on can add the cell.
//   - P0-A2: hash rendered as 8-character prefix ONLY; full hash on
//            copy-click.
//   - P0-A3: horizontal pill filter row ONLY — no left filter sidebar.
//            Per `<FilterPills>` from the shared shell.
//   - P0-A4: neutral test-* tokens in tests; no vendor token prefixes.

import { useQuery } from "@tanstack/react-query";
import { useRouter, useSearchParams } from "next/navigation";
import { Suspense, useCallback, useEffect, useMemo, useState } from "react";

import {
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
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogPortal,
  DialogTitle,
} from "@/components/ui/dialog";
import {
  fetchControlsList,
  fetchEvidenceList,
  type ControlsListResponse,
  type EvidenceListResponse,
  type EvidenceRecord,
} from "@/lib/api";

import {
  buildControlOptions,
  clearFilters,
  DEFAULT_FILTERS,
  isNoneSelected,
  NONE,
  setFilter,
  type EvidenceFilters,
} from "./filters";
import {
  hashPrefix,
  observedAtLabel,
  prettyJSON,
  scopeLabel,
  sourceSummary,
} from "./format";

const URL_KEY = "control_id";

function EvidencePageInner() {
  const router = useRouter();
  const search = useSearchParams();

  // URL-driven filter state — mirrors the slice 098 / 102 pattern so
  // the selected control is shareable / bookmarkable. Default = NONE
  // (no control selected → "Pick a control" prompt).
  const filters: EvidenceFilters = useMemo(() => {
    const out = { ...DEFAULT_FILTERS };
    const v = search.get(URL_KEY);
    if (v) out.controlId = v;
    return out;
  }, [search]);

  const updateFilter = (key: keyof EvidenceFilters, value: string) => {
    const next = setFilter(filters, key, value);
    const sp = new URLSearchParams(search.toString());
    if (next[key] === NONE) {
      sp.delete(URL_KEY);
    } else {
      sp.set(URL_KEY, next[key]);
    }
    router.replace(`/evidence?${sp.toString()}`);
  };

  const clearAll = () => {
    const cleared = clearFilters();
    const sp = new URLSearchParams(search.toString());
    if (cleared.controlId === NONE) sp.delete(URL_KEY);
    router.replace(`/evidence?${sp.toString()}`);
  };

  // Anchor catalog — drives the Control pill option list. The fetch
  // reuses the slice 098 endpoint so we don't duplicate the wire.
  const anchorsQ = useQuery<ControlsListResponse>({
    queryKey: ["controls", "list"],
    queryFn: fetchControlsList,
  });
  const anchors = useMemo(() => anchorsQ.data?.anchors ?? [], [anchorsQ.data]);
  const controlOptions = useMemo(() => buildControlOptions(anchors), [anchors]);

  // Evidence ledger query — gated on a real control_id. When the
  // user hasn't selected one yet, we don't fire the fetch (the
  // upstream would 400). The page renders the "pick a control"
  // prompt instead.
  const evidenceQ = useQuery<EvidenceListResponse>({
    queryKey: ["evidence", "list", filters.controlId],
    queryFn: () => fetchEvidenceList(filters.controlId),
    enabled: !isNoneSelected(filters),
  });
  const records: EvidenceRecord[] = useMemo(
    () => evidenceQ.data?.evidence ?? [],
    [evidenceQ.data],
  );

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
      value: filters.controlId,
      options: controlOptions,
    },
  ];

  const meta = isNoneSelected(filters) ? (
    <span>Select a control to load its evidence ledger</span>
  ) : (
    <span>
      Showing{" "}
      <span className="text-foreground font-medium">{records.length}</span>{" "}
      record
      {records.length === 1 ? "" : "s"}
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
      id: "control_id",
      header: "Control",
      cell: () => (
        <span
          className="font-mono text-xs text-muted-foreground"
          title={filters.controlId}
          data-testid="evidence-row-control-id"
        >
          {filters.controlId.slice(0, 8)}…
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

  const actions = (
    <>
      <Button variant="outline" size="sm" disabled>
        Export JSONL
      </Button>
      <Button size="sm" disabled>
        Push evidence
      </Button>
    </>
  );

  // Custom no-control-selected prompt (we use a custom block instead
  // of <EmptyState> because we want two CTAs — and the shared shell
  // <EmptyState> intentionally only takes one CTA so we can't extend
  // it without modifying the slice 098 shell).
  const pickControlPrompt = (
    <div
      data-testid="list-empty-state"
      className="rounded-xl border bg-card py-16 px-6 text-center"
    >
      <div className="mx-auto mb-3 text-muted-foreground">
        <EvidenceLedgerIcon />
      </div>
      <div
        className="text-sm font-semibold text-foreground mb-1"
        data-testid="evidence-pick-control-title"
      >
        Pick a control to see its evidence ledger
      </div>
      <div className="text-xs text-muted-foreground mb-4">
        Evidence records are scoped to a control today. Choose one from the
        Control filter above to load its ledger window.
      </div>
    </div>
  );

  // True-empty state: a control IS selected, but the upstream
  // returned zero rows. Per design doc §2 + slice 099 AC-5, this
  // empty state surfaces TWO actions: "Clear filters" + "Set up a
  // connector →". The shared `<EmptyState>` shell only takes one CTA,
  // so we render a custom block (same shape, just two buttons).
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
        Try a wider time window, or push a record via CLI or connector.
      </div>
      <div className="flex items-center gap-2 justify-center">
        <Button
          variant="outline"
          size="sm"
          onClick={clearAll}
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
        subtitle="Append-only · ingestion separated from evaluation · point-in-time replay always possible"
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
        subtitle="Append-only · ingestion separated from evaluation · point-in-time replay always possible"
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

  // No control selected → "pick a control" prompt.
  if (isNoneSelected(filters)) {
    return (
      <ListPage
        title="Evidence ledger"
        subtitle="Append-only · ingestion separated from evaluation · point-in-time replay always possible"
        actions={actions}
        filterRow={
          <FilterPills
            pills={pills}
            onChange={(id, v) => updateFilter(id as keyof EvidenceFilters, v)}
            meta={meta}
          />
        }
      >
        {pickControlPrompt}
      </ListPage>
    );
  }

  // Control selected — evidence query is in flight.
  if (evidenceQ.isLoading) {
    return (
      <ListPage
        title="Evidence ledger"
        subtitle="Append-only · ingestion separated from evaluation · point-in-time replay always possible"
        actions={actions}
        filterRow={
          <FilterPills
            pills={pills}
            onChange={(id, v) => updateFilter(id as keyof EvidenceFilters, v)}
            meta={meta}
          />
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
        subtitle="Append-only · ingestion separated from evaluation · point-in-time replay always possible"
        actions={actions}
        filterRow={
          <FilterPills
            pills={pills}
            onChange={(id, v) => updateFilter(id as keyof EvidenceFilters, v)}
            meta={meta}
          />
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
        subtitle="Append-only · ingestion separated from evaluation · point-in-time replay always possible"
        actions={actions}
        filterRow={
          <FilterPills
            pills={pills}
            onChange={(id, v) => updateFilter(id as keyof EvidenceFilters, v)}
            meta={meta}
          />
        }
      >
        <ListTable<EvidenceRecord>
          columns={columns}
          rows={records}
          rowKey={(row) => row.evidence_id}
          onRowClick={openDrawer}
          emptyFallback={noRecordsEmptyState}
        />
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
