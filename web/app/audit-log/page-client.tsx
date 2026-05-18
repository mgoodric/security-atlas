"use client";

// Slice 125 — /audit-log client island.
//
// AC-1 through AC-4. URL-driven filter state + TanStack `useInfiniteQuery`
// over the BFF + IntersectionObserver-driven infinite scroll capped at
// 10 auto-loaded pages, after which a manual "Load more" button takes over
// (P0-A3 / DoS posture per the slice threat model). Each row expands to
// show its raw `payload_json` blob.
//
// Filters live in the URL (`?from=...&to=...&actor=...&kind=foo,bar`)
// so back/forward buttons and shared links restore the same view.
//
// `actor_name` is unresolved on the wire (see decisions log D1). Until the
// slice-124 endpoint extension lands, the table shows the first 8 chars of
// `actor_id`. Hovering displays the full UUID.

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
  AUDIT_LOG_KINDS,
  AuditLogFetchError,
  AuditLogKind,
  MAX_WINDOW_DAYS,
  UnifiedEntry,
  UnifiedListResponse,
  fetchUnifiedAuditLog,
} from "@/lib/api/audit-log";

// AUTO_LOAD_PAGE_LIMIT — after this many auto-loaded pages, switch to a
// manual "Load more" button so the operator must explicitly opt into more
// work. 10 pages × 1000 rows/page = 10,000 rows, which matches the threat
// model's stated ceiling.
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

// toRFC3339Day collapses a Date to a YYYY-MM-DDT00:00:00.000Z string so the
// date-picker's native `<input type="date">` round-trips cleanly. The
// platform parses both this and full-precision RFC3339.
function toRFC3339Day(d: Date): string {
  const yyyy = d.getUTCFullYear();
  const mm = String(d.getUTCMonth() + 1).padStart(2, "0");
  const dd = String(d.getUTCDate()).padStart(2, "0");
  return `${yyyy}-${mm}-${dd}T00:00:00.000Z`;
}

// htmlDateValue extracts the YYYY-MM-DD portion of an RFC3339 string so the
// native `<input type="date">` accepts it.
function htmlDateValue(rfc3339: string): string {
  return rfc3339.slice(0, 10);
}

// rangeIsValid asserts the from/to window the operator selected satisfies
// the backend's 90-day cap. The backend rejects with 400 on violation; this
// shadows that check for fast UI feedback.
function rangeIsValid(from: string, to: string): boolean {
  const f = Date.parse(from);
  const t = Date.parse(to);
  if (!Number.isFinite(f) || !Number.isFinite(t)) return false;
  if (t <= f) return false;
  const days = (t - f) / (24 * 60 * 60 * 1000);
  return days <= MAX_WINDOW_DAYS;
}

// formatUTC formats an RFC3339 timestamp as a local-time string, with the
// UTC value exposed as a tooltip via <time dateTime>.
function formatLocal(s: string): string {
  try {
    return new Date(s).toLocaleString();
  } catch {
    return s;
  }
}

function truncateActor(id: string): string {
  if (!id) return "(none)";
  if (id.length <= 8) return id;
  return `${id.slice(0, 8)}…`;
}

type Filters = {
  from: string;
  to: string;
  actor: string;
  kinds: AuditLogKind[];
};

export function AuditLogPageClient() {
  const router = useRouter();
  const searchParams = useSearchParams();

  const filters = useMemo<Filters>(
    () => parseFilters(searchParams),
    [searchParams],
  );

  // The actor input draft is owned by a child component keyed on the URL's
  // actor value (`ActorFilterInput key={filters.actor}`). When the URL
  // changes (back/forward, share-link load), the child remounts with the
  // URL-derived initial value — no effect, no ref-during-render, no
  // `react-hooks/set-state-in-effect` violation.

  const queryEnabled = rangeIsValid(filters.from, filters.to);

  // AC-4: cursor-paginated infinite scroll. `pageParam` is the opaque
  // base64 cursor returned by the previous page; `undefined` for page 1.
  const query = useInfiniteQuery<
    UnifiedListResponse,
    AuditLogFetchError,
    { pages: UnifiedListResponse[]; pageParams: (string | undefined)[] },
    readonly unknown[],
    string | undefined
  >({
    queryKey: ["audit-log", filters],
    enabled: queryEnabled,
    initialPageParam: undefined,
    queryFn: async ({ pageParam }) => {
      return fetchUnifiedAuditLog({
        from: filters.from,
        to: filters.to,
        actor: filters.actor,
        kinds: filters.kinds,
        cursor: pageParam,
      });
    },
    getNextPageParam: (last) => last.next_cursor || undefined,
    // Stale time aggressive enough that the page doesn't re-fetch on
    // every focus event while the operator is typing.
    staleTime: 60_000,
  });

  const pages = query.data?.pages ?? [];
  const rows: UnifiedEntry[] = pages.flatMap((p) => p.entries);
  const totalLoaded = rows.length;
  const autoLoadExhausted = pages.length >= AUTO_LOAD_PAGE_LIMIT;

  // AC-4: auto-load when the sentinel scrolls into view, but stop at the
  // page-count ceiling. After that, only a manual button click loads more.
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

  // ----- URL state mutators -----

  const replaceWith = useCallback(
    (next: Filters) => {
      const params = new URLSearchParams();
      params.set("from", next.from);
      params.set("to", next.to);
      if (next.actor) params.set("actor", next.actor);
      if (next.kinds.length > 0) params.set("kind", next.kinds.join(","));
      router.replace(`/audit-log?${params.toString()}`);
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
  const toggleKind = (k: AuditLogKind) => {
    const next = filters.kinds.includes(k)
      ? { ...filters, kinds: filters.kinds.filter((x) => x !== k) }
      : { ...filters, kinds: [...filters.kinds, k] };
    replaceWith(next);
  };
  const applyActor = (draft: string) => {
    replaceWith({ ...filters, actor: draft.trim() });
  };
  const clearActor = () => {
    replaceWith({ ...filters, actor: "" });
  };

  const error = query.error;

  return (
    <div data-testid="audit-log-page" className="space-y-4">
      <FilterBar
        filters={filters}
        onActorApply={applyActor}
        onActorClear={clearActor}
        onFromChange={setFrom}
        onToChange={setTo}
        onKindToggle={toggleKind}
        windowValid={queryEnabled}
      />

      {!queryEnabled ? (
        <div
          data-testid="audit-log-window-error"
          className="rounded-md border border-destructive/40 bg-destructive/10 p-3 text-sm text-destructive"
        >
          Window invalid: pick a `to` strictly after `from`, and keep the range
          at or under {MAX_WINDOW_DAYS} days.
        </div>
      ) : null}

      {error ? (
        <div
          data-testid="audit-log-fetch-error"
          className="rounded-md border border-destructive/40 bg-destructive/10 p-3 text-sm text-destructive"
        >
          Failed to load audit-log entries: {error.message}
        </div>
      ) : null}

      <RowsTable rows={rows} loading={query.isLoading} />

      <div className="flex items-center justify-between text-xs text-muted-foreground">
        <span data-testid="audit-log-row-count">
          {totalLoaded} {totalLoaded === 1 ? "row" : "rows"} loaded
          {query.hasNextPage ? " (more available)" : ""}
        </span>

        {/* AC-4: sentinel for IntersectionObserver auto-load. */}
        {query.hasNextPage && !autoLoadExhausted ? (
          <div ref={sentinelRef} data-testid="audit-log-sentinel" aria-hidden />
        ) : null}

        {/* After the auto-load ceiling, render the manual button. */}
        {query.hasNextPage && autoLoadExhausted ? (
          <Button
            data-testid="audit-log-load-more"
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

// ----- parseFilters --------------------------------------------------

function parseFilters(searchParams: URLSearchParams): Filters {
  const fromRaw = searchParams.get("from");
  const toRaw = searchParams.get("to");
  const actor = searchParams.get("actor") ?? "";
  const kindRaw = searchParams.get("kind") ?? "";

  const from =
    fromRaw && Number.isFinite(Date.parse(fromRaw)) ? fromRaw : defaultFrom();
  const to = toRaw && Number.isFinite(Date.parse(toRaw)) ? toRaw : defaultTo();

  const kinds: AuditLogKind[] = kindRaw
    ? kindRaw
        .split(",")
        .map((s) => s.trim())
        .filter((s): s is AuditLogKind =>
          (AUDIT_LOG_KINDS as readonly string[]).includes(s),
        )
    : [];

  return { from, to, actor, kinds };
}

// ----- FilterBar -----------------------------------------------------

type FilterBarProps = {
  filters: Filters;
  onActorApply: (draft: string) => void;
  onActorClear: () => void;
  onFromChange: (v: string) => void;
  onToChange: (v: string) => void;
  onKindToggle: (k: AuditLogKind) => void;
  windowValid: boolean;
};

function FilterBar({
  filters,
  onActorApply,
  onActorClear,
  onFromChange,
  onToChange,
  onKindToggle,
}: FilterBarProps) {
  return (
    <div
      data-testid="audit-log-filters"
      className="space-y-3 rounded-xl border bg-card p-3"
    >
      <div className="grid gap-3 sm:grid-cols-4">
        <div>
          <label
            htmlFor="audit-log-from"
            className="block text-xs font-medium text-muted-foreground"
          >
            From (UTC day)
          </label>
          <Input
            id="audit-log-from"
            data-testid="audit-log-from"
            type="date"
            value={htmlDateValue(filters.from)}
            onChange={(e) => onFromChange(e.target.value)}
          />
        </div>
        <div>
          <label
            htmlFor="audit-log-to"
            className="block text-xs font-medium text-muted-foreground"
          >
            To (UTC day)
          </label>
          <Input
            id="audit-log-to"
            data-testid="audit-log-to"
            type="date"
            value={htmlDateValue(filters.to)}
            onChange={(e) => onToChange(e.target.value)}
          />
        </div>
        <div className="sm:col-span-2">
          <ActorFilterInput
            // Remount the child on URL changes so the internally-owned
            // draft re-initializes from the new URL value (back/forward).
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
          data-testid="audit-log-kind-chips"
          className="mt-1 flex flex-wrap gap-2"
        >
          {AUDIT_LOG_KINDS.map((k) => {
            const active = filters.kinds.includes(k);
            return (
              <button
                key={k}
                type="button"
                data-testid={`audit-log-kind-chip-${k}`}
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
    </div>
  );
}

// ----- ActorFilterInput ---------------------------------------------

// Owns its own draft so the parent can re-render on every keystroke
// without forcing a refetch. Parent commits via onApply(draft) when the
// user presses Enter or clicks Apply.
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
  return (
    <div>
      <label
        htmlFor="audit-log-actor"
        className="block text-xs font-medium text-muted-foreground"
      >
        Actor (UUID or credential id)
      </label>
      <div className="flex gap-2">
        <Input
          id="audit-log-actor"
          data-testid="audit-log-actor"
          type="text"
          value={draft}
          onChange={(e) => setDraft(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter") onApply(draft);
          }}
          placeholder="00000000-0000-0000-0000-000000001111"
        />
        <Button
          type="button"
          variant="outline"
          size="sm"
          data-testid="audit-log-actor-apply"
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
            data-testid="audit-log-actor-clear"
          >
            Clear
          </Button>
        ) : null}
      </div>
    </div>
  );
}

// ----- RowsTable -----------------------------------------------------

function RowsTable({
  rows,
  loading,
}: {
  rows: UnifiedEntry[];
  loading: boolean;
}) {
  if (loading && rows.length === 0) {
    return (
      <div
        data-testid="audit-log-loading"
        className="rounded-md border bg-card p-6 text-center text-sm text-muted-foreground"
      >
        Loading…
      </div>
    );
  }
  if (rows.length === 0) {
    return (
      <div
        data-testid="audit-log-empty"
        className="rounded-md border bg-card p-6 text-center text-sm text-muted-foreground"
      >
        No audit-log entries match these filters.
      </div>
    );
  }
  return (
    <div
      data-testid="audit-log-table-wrap"
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

function Row({ row }: { row: UnifiedEntry }) {
  const [open, setOpen] = useState(false);
  const utcLabel = row.occurred_at;
  return (
    <>
      <TableRow
        data-testid="audit-log-row"
        className="cursor-pointer hover:bg-muted/40"
        onClick={() => setOpen((v) => !v)}
      >
        <TableCell className="w-8 text-center text-muted-foreground">
          <span
            data-testid="audit-log-row-toggle"
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
            className="font-mono text-xs"
            data-testid="audit-log-row-actor"
          >
            {truncateActor(row.actor_id)}
          </span>
        </TableCell>
        <TableCell>
          <Badge variant="secondary" data-testid="audit-log-row-kind">
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
        <TableRow data-testid="audit-log-row-payload">
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
