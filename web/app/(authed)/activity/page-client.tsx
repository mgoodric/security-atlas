"use client";

// Slice 270 — /activity client island.
//
// AC-1 through AC-4 + AC-9. URL-driven filter state + TanStack
// `useInfiniteQuery` over the BFF + IntersectionObserver-driven infinite
// scroll capped at 10 auto-loaded pages, after which a manual "Load
// more" button takes over (mirrors slice 125 audit-log page DoS posture
// via the shared 1000-row backend cap).
//
// Filters live in the URL (`?from=...&to=...&actor=...&kind=foo,bar`)
// so back/forward buttons and shared links restore the same view.
//
// Slice 270 D3: this island is a near-duplicate of slice 125's
// `AuditLogPageClient` (`web/app/audit-log/page-client.tsx`). The
// differences are:
//
//   - BFF URL is `/api/activity` instead of `/api/audit-log/unified`.
//   - `router.replace` target is `/activity` instead of `/audit-log`.
//   - The actor filter accepts the literal `me` sentinel (slice 270
//     D5) which the BFF resolves server-side to the caller's user_id.
//     The input box shows `me` with a friendlier `(your activity)`
//     label so the operator does not see a UUID they did not type.
//   - No export bar — slice 270 does not ship export (out of scope).
//
// A follow-on slice can extract a shared `<UnifiedAuditTable>`
// component consumed by both pages; for slice 270 the duplication is
// intentional and bounded. When either page changes, the maintainer
// MUST update both — surface drift is a real risk and the duplicated
// shell carries this header as the gate.

import { useCallback, useEffect, useMemo, useRef, useState } from "react";

import { useInfiniteQuery } from "@tanstack/react-query";
import { useRouter, useSearchParams } from "next/navigation";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";

import {
  ACTIVITY_KINDS,
  ACTOR_ME_SENTINEL,
  ActivityEntry,
  ActivityFetchError,
  ActivityKind,
  ActivityListResponse,
  MAX_WINDOW_DAYS,
  fetchActivity,
  renderActorLabel,
} from "@/lib/api/activity";

// AUTO_LOAD_PAGE_LIMIT — after this many auto-loaded pages, switch to
// a manual "Load more" button so the operator must explicitly opt
// into more work. 10 pages × 1000 rows/page = 10,000 rows, mirroring
// slice 125.
const AUTO_LOAD_PAGE_LIMIT = 10;

// Default window: last 7 days.
function defaultFrom(): string {
  const d = new Date();
  d.setUTCDate(d.getUTCDate() - 7);
  return toRFC3339Day(d);
}
function defaultTo(): string {
  return toRFC3339Day(new Date());
}

function toRFC3339Day(d: Date): string {
  const yyyy = d.getUTCFullYear();
  const mm = String(d.getUTCMonth() + 1).padStart(2, "0");
  const dd = String(d.getUTCDate()).padStart(2, "0");
  return `${yyyy}-${mm}-${dd}T00:00:00.000Z`;
}

function htmlDateValue(rfc3339: string): string {
  return rfc3339.slice(0, 10);
}

function rangeIsValid(from: string, to: string): boolean {
  const f = Date.parse(from);
  const t = Date.parse(to);
  if (!Number.isFinite(f) || !Number.isFinite(t)) return false;
  if (t <= f) return false;
  const days = (t - f) / (24 * 60 * 60 * 1000);
  return days <= MAX_WINDOW_DAYS;
}

function formatLocal(s: string): string {
  try {
    return new Date(s).toLocaleString();
  } catch {
    return s;
  }
}

type Filters = {
  from: string;
  to: string;
  actor: string;
  kinds: ActivityKind[];
  // includeReads (slice 669) — when false (the default), the Activity feed
  // shows mutating/business events only and excludes the high-volume
  // `decision`/`read` internal telemetry. When true, the full ledger is
  // shown. URL-driven (`?include_reads=true`) so back/forward and shared
  // links restore the same view. This is a view filter only; the
  // underlying audit ledger is unchanged.
  includeReads: boolean;
};

export function ActivityPageClient() {
  const router = useRouter();
  const searchParams = useSearchParams();

  const filters = useMemo<Filters>(
    () => parseFilters(searchParams),
    [searchParams],
  );

  const queryEnabled = rangeIsValid(filters.from, filters.to);

  const query = useInfiniteQuery<
    ActivityListResponse,
    ActivityFetchError,
    { pages: ActivityListResponse[]; pageParams: (string | undefined)[] },
    readonly unknown[],
    string | undefined
  >({
    queryKey: ["activity", filters],
    enabled: queryEnabled,
    initialPageParam: undefined,
    queryFn: async ({ pageParam }) => {
      return fetchActivity({
        from: filters.from,
        to: filters.to,
        actor: filters.actor,
        kinds: filters.kinds,
        cursor: pageParam,
        includeReads: filters.includeReads,
      });
    },
    getNextPageParam: (last) => last.next_cursor || undefined,
    staleTime: 60_000,
  });

  const pages = query.data?.pages ?? [];
  const rows: ActivityEntry[] = pages.flatMap((p) => p.entries);
  const totalLoaded = rows.length;
  const autoLoadExhausted = pages.length >= AUTO_LOAD_PAGE_LIMIT;

  const sentinelRef = useRef<HTMLDivElement | null>(null);
  useEffect(() => {
    if (!sentinelRef.current) return;
    if (!query.hasNextPage) return;
    if (query.isFetchingNextPage) return;
    if (autoLoadExhausted) return;

    const el = sentinelRef.current;
    const observer = new IntersectionObserver(
      (entries) => {
        for (const entry of entries) {
          if (entry.isIntersecting) {
            query.fetchNextPage();
          }
        }
      },
      { rootMargin: "200px" },
    );
    observer.observe(el);
    return () => observer.disconnect();
  }, [query.hasNextPage, query.isFetchingNextPage, autoLoadExhausted, query]);

  const replaceWith = useCallback(
    (next: Filters) => {
      const params = new URLSearchParams();
      params.set("from", next.from);
      params.set("to", next.to);
      if (next.actor) params.set("actor", next.actor);
      if (next.kinds.length > 0) params.set("kind", next.kinds.join(","));
      if (next.includeReads) params.set("include_reads", "true");
      router.replace(`/activity?${params.toString()}`);
    },
    [router],
  );

  const setFrom = (from: string) => {
    const next = {
      ...filters,
      from: toRFC3339Day(new Date(`${from}T00:00:00Z`)),
    };
    replaceWith(next);
  };
  const setTo = (to: string) => {
    const next = { ...filters, to: toRFC3339Day(new Date(`${to}T00:00:00Z`)) };
    replaceWith(next);
  };
  const toggleKind = (k: ActivityKind) => {
    const next = filters.kinds.includes(k)
      ? { ...filters, kinds: filters.kinds.filter((x) => x !== k) }
      : { ...filters, kinds: [...filters.kinds, k] };
    replaceWith(next);
  };
  const toggleIncludeReads = () => {
    replaceWith({ ...filters, includeReads: !filters.includeReads });
  };
  const applyActor = (draft: string) => {
    replaceWith({ ...filters, actor: draft.trim() });
  };
  const clearActor = () => {
    replaceWith({ ...filters, actor: "" });
  };

  const error = query.error;

  return (
    <div data-testid="activity-page" className="space-y-4">
      <FilterBar
        filters={filters}
        onActorApply={applyActor}
        onActorClear={clearActor}
        onFromChange={setFrom}
        onToChange={setTo}
        onKindToggle={toggleKind}
        onIncludeReadsToggle={toggleIncludeReads}
        windowValid={queryEnabled}
      />

      {!queryEnabled ? (
        <div
          data-testid="activity-window-error"
          className="rounded-md border border-destructive/40 bg-destructive/10 p-3 text-sm text-destructive"
        >
          Window invalid: pick a `to` strictly after `from`, and keep the range
          at or under {MAX_WINDOW_DAYS} days.
        </div>
      ) : null}

      {error ? (
        <div
          data-testid="activity-fetch-error"
          className="rounded-md border border-destructive/40 bg-destructive/10 p-3 text-sm text-destructive"
        >
          Failed to load activity entries: {error.message}
        </div>
      ) : null}

      <RowsTable rows={rows} loading={query.isLoading} />

      <div className="flex items-center justify-between text-xs text-muted-foreground">
        <span data-testid="activity-row-count">
          {totalLoaded} {totalLoaded === 1 ? "row" : "rows"} loaded
          {query.hasNextPage ? " (more available)" : ""}
        </span>

        {query.hasNextPage && !autoLoadExhausted ? (
          <div ref={sentinelRef} data-testid="activity-sentinel" aria-hidden />
        ) : null}

        {query.hasNextPage && autoLoadExhausted ? (
          <Button
            data-testid="activity-load-more"
            variant="outline"
            size="sm"
            disabled={query.isFetchingNextPage}
            onClick={() => query.fetchNextPage()}
          >
            {query.isFetchingNextPage ? "Loading…" : "Load more"}
          </Button>
        ) : null}
      </div>
    </div>
  );
}

function parseFilters(searchParams: URLSearchParams): Filters {
  const fromRaw = searchParams.get("from");
  const toRaw = searchParams.get("to");
  const actor = searchParams.get("actor") ?? "";
  const kindRaw = searchParams.get("kind") ?? "";
  // Slice 669: read-telemetry is excluded by default; only the explicit
  // `include_reads=true` opts back into the full ledger.
  const includeReads = searchParams.get("include_reads") === "true";

  const from =
    fromRaw && Number.isFinite(Date.parse(fromRaw)) ? fromRaw : defaultFrom();
  const to = toRaw && Number.isFinite(Date.parse(toRaw)) ? toRaw : defaultTo();

  const kinds: ActivityKind[] = kindRaw
    ? kindRaw
        .split(",")
        .map((s) => s.trim())
        .filter((s): s is ActivityKind =>
          (ACTIVITY_KINDS as readonly string[]).includes(s),
        )
    : [];

  return { from, to, actor, kinds, includeReads };
}

type FilterBarProps = {
  filters: Filters;
  onActorApply: (draft: string) => void;
  onActorClear: () => void;
  onFromChange: (v: string) => void;
  onToChange: (v: string) => void;
  onKindToggle: (k: ActivityKind) => void;
  onIncludeReadsToggle: () => void;
  windowValid: boolean;
};

function FilterBar({
  filters,
  onActorApply,
  onActorClear,
  onFromChange,
  onToChange,
  onKindToggle,
  onIncludeReadsToggle,
}: FilterBarProps) {
  return (
    <div
      data-testid="activity-filters"
      className="space-y-3 rounded-xl border bg-card p-3"
    >
      <div className="grid gap-3 sm:grid-cols-4">
        <div>
          <label
            htmlFor="activity-from"
            className="block text-xs font-medium text-muted-foreground"
          >
            From (UTC day)
          </label>
          <Input
            id="activity-from"
            data-testid="activity-from"
            type="date"
            value={htmlDateValue(filters.from)}
            onChange={(e) => onFromChange(e.target.value)}
          />
        </div>
        <div>
          <label
            htmlFor="activity-to"
            className="block text-xs font-medium text-muted-foreground"
          >
            To (UTC day)
          </label>
          <Input
            id="activity-to"
            data-testid="activity-to"
            type="date"
            value={htmlDateValue(filters.to)}
            onChange={(e) => onToChange(e.target.value)}
          />
        </div>
        <div className="sm:col-span-2">
          <ActorFilterInput
            key={filters.actor}
            initial={filters.actor}
            onApply={onActorApply}
            onClear={onActorClear}
            hasCommittedValue={Boolean(filters.actor)}
          />
        </div>
      </div>

      <div>
        <div className="text-xs font-medium text-muted-foreground">
          Kinds (any-of)
        </div>
        <div
          data-testid="activity-kind-chips"
          className="mt-1 flex flex-wrap gap-2"
        >
          {ACTIVITY_KINDS.map((k) => {
            const active = filters.kinds.includes(k);
            return (
              <button
                key={k}
                type="button"
                data-testid={`activity-kind-chip-${k}`}
                aria-pressed={active}
                onClick={() => onKindToggle(k)}
                className={
                  "rounded-full border px-3 py-1 text-xs transition-colors " +
                  (active
                    ? "border-primary bg-primary text-primary-foreground"
                    : "border-input bg-background hover:bg-muted")
                }
              >
                {k}
              </button>
            );
          })}
        </div>
      </div>

      <div className="flex items-center justify-between border-t pt-3">
        <div>
          <div className="text-xs font-medium text-muted-foreground">
            Read-telemetry
          </div>
          <p className="text-xs text-muted-foreground">
            High-volume internal read events are hidden by default so business
            events stay visible. The full audit ledger is unchanged.
          </p>
        </div>
        <button
          type="button"
          data-testid="activity-include-reads-toggle"
          aria-pressed={filters.includeReads}
          onClick={onIncludeReadsToggle}
          className={
            "rounded-full border px-3 py-1 text-xs transition-colors " +
            (filters.includeReads
              ? "border-primary bg-primary text-primary-foreground"
              : "border-input bg-background hover:bg-muted")
          }
        >
          {filters.includeReads
            ? "Showing read-telemetry"
            : "Show read-telemetry"}
        </button>
      </div>
    </div>
  );
}

// ActorFilterInput owns its own draft so the parent can re-render on
// every keystroke without forcing a refetch. Parent commits via
// onApply(draft) when the user presses Enter or clicks Apply.
//
// Slice 270 D5: the literal string `me` is a sentinel the BFF resolves
// server-side to the caller's user_id. When the URL value is `me`, the
// input renders the friendlier "(your activity)" label next to the
// input so the operator does not see a UUID they did not type.
function ActorFilterInput({
  initial,
  onApply,
  onClear,
  hasCommittedValue,
}: {
  initial: string;
  onApply: (draft: string) => void;
  onClear: () => void;
  hasCommittedValue: boolean;
}) {
  const [draft, setDraft] = useState<string>(initial);
  const isMeSentinel = initial === ACTOR_ME_SENTINEL;
  return (
    <div>
      <label
        htmlFor="activity-actor"
        className="block text-xs font-medium text-muted-foreground"
      >
        Actor (UUID, credential id, or `me`)
        {isMeSentinel ? (
          <span
            className="ml-2 text-foreground"
            data-testid="activity-actor-me-label"
          >
            (your activity)
          </span>
        ) : null}
      </label>
      <div className="flex gap-2">
        <Input
          id="activity-actor"
          data-testid="activity-actor"
          type="text"
          value={draft}
          onChange={(e) => setDraft(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter") onApply(draft);
          }}
          placeholder="me, 00000000-0000-0000-0000-000000001111, key_..."
        />
        <Button
          type="button"
          variant="outline"
          size="sm"
          data-testid="activity-actor-apply"
          onClick={() => onApply(draft)}
        >
          Apply
        </Button>
        {hasCommittedValue ? (
          <Button
            type="button"
            variant="ghost"
            size="sm"
            onClick={onClear}
            data-testid="activity-actor-clear"
          >
            Clear
          </Button>
        ) : null}
      </div>
    </div>
  );
}

function RowsTable({
  rows,
  loading,
}: {
  rows: ActivityEntry[];
  loading: boolean;
}) {
  if (loading && rows.length === 0) {
    return (
      <div
        data-testid="activity-loading"
        className="rounded-md border bg-card p-6 text-center text-sm text-muted-foreground"
      >
        Loading…
      </div>
    );
  }
  if (rows.length === 0) {
    return (
      <div
        data-testid="activity-empty"
        className="rounded-md border bg-card p-6 text-center text-sm text-muted-foreground"
      >
        No activity entries match these filters.
      </div>
    );
  }
  return (
    <div
      data-testid="activity-table-wrap"
      className="overflow-x-auto rounded-xl border bg-card"
    >
      <Table>
        <TableHeader>
          <TableRow className="bg-muted/50 text-[11px] uppercase tracking-wider text-muted-foreground">
            <TableHead className="w-8" />
            <TableHead>Occurred</TableHead>
            <TableHead>Actor</TableHead>
            <TableHead>Kind</TableHead>
            <TableHead>Target type</TableHead>
            <TableHead>Target id</TableHead>
            <TableHead>Action</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {rows.map((row) => (
            <Row key={`${row.kind}-${row.row_id}`} row={row} />
          ))}
        </TableBody>
      </Table>
    </div>
  );
}

function Row({ row }: { row: ActivityEntry }) {
  const [open, setOpen] = useState(false);
  const utcLabel = row.occurred_at;
  return (
    <>
      <TableRow
        data-testid="activity-row"
        className="cursor-pointer hover:bg-muted/40"
        onClick={() => setOpen((v) => !v)}
      >
        <TableCell className="w-8 text-center text-muted-foreground">
          <span
            data-testid="activity-row-toggle"
            aria-expanded={open}
            aria-label={open ? "Collapse payload" : "Expand payload"}
          >
            {open ? "▾" : "▸"}
          </span>
        </TableCell>
        <TableCell>
          <time dateTime={utcLabel} title={`${utcLabel} (UTC)`}>
            {formatLocal(row.occurred_at)}
          </time>
        </TableCell>
        <TableCell>
          <span
            title={row.actor_id || "(empty)"}
            className={
              row.actor_name && row.actor_name.length > 0
                ? "text-sm"
                : "font-mono text-xs"
            }
            data-testid="activity-row-actor"
          >
            {renderActorLabel(row)}
          </span>
        </TableCell>
        <TableCell>
          <Badge variant="secondary" data-testid="activity-row-kind">
            {row.kind}
          </Badge>
        </TableCell>
        <TableCell>
          <code className="text-xs">{row.target_type}</code>
        </TableCell>
        <TableCell>
          <span className="font-mono text-xs" title={row.target_id}>
            {row.target_id}
          </span>
        </TableCell>
        <TableCell>
          <code className="text-xs">{row.action}</code>
        </TableCell>
      </TableRow>
      {open ? (
        <TableRow data-testid="activity-row-payload">
          <TableCell />
          <TableCell colSpan={6}>
            <pre className="max-h-80 overflow-auto rounded bg-foreground/5 p-3 text-xs">
              {safeStringify(row.payload_json)}
            </pre>
          </TableCell>
        </TableRow>
      ) : null}
    </>
  );
}

function safeStringify(v: unknown): string {
  try {
    return JSON.stringify(v, null, 2);
  } catch {
    return String(v);
  }
}
