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
  ALL,
  NONE,
  buildControlOptions,
  buildKindOptions,
  buildResultOptions,
  clearFilters,
  DEFAULT_FILTERS,
  isDefault,
  setFilter,
  toFetchOptions,
  type EvidenceFilters,
} from "./filters";
import {
  hashPrefix,
  observedAtLabel,
  prettyJSON,
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
const URL_KEYS: Record<keyof EvidenceFilters, string> = {
  controlId: "control_id",
  kind: "kind",
  result: "result",
  sourceActorType: "source_actor_type",
  sourceActorId: "source_actor_id",
};

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
    return out;
  }, [search]);

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
    router.replace(`/evidence?${sp.toString()}`);
  };

  const clearAll = () => {
    const cleared = clearFilters();
    void cleared;
    router.replace(`/evidence`);
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

  // Evidence ledger query — slice 106 always runs (no more gating on a
  // control_id presence). The filter translator drops sentinel values
  // so the URL query string only carries narrowing predicates.
  const fetchOpts = useMemo(() => toFetchOptions(filters), [filters]);
  const evidenceQ = useQuery<EvidenceListResponse>({
    queryKey: ["evidence", "list", fetchOpts],
    queryFn: () => fetchEvidenceList(fetchOpts),
  });
  const records: EvidenceRecord[] = useMemo(
    () => evidenceQ.data?.evidence ?? [],
    [evidenceQ.data],
  );
  const kindOptions = useMemo(
    () => buildKindOptions(records.map((r) => r.evidence_kind ?? "")),
    [records],
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
  ];

  const meta = (
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
        subtitle={subtitleNode}
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
        subtitle={subtitleNode}
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
