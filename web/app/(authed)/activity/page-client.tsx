"use client";

// Slice 270 — /activity client island. Since #2093 this is the one
// audit-log page; the slice-125 /audit-log island it duplicated is gone.
//
// URL-driven filter state + TanStack `useInfiniteQuery` over the BFF +
// IntersectionObserver-driven infinite scroll capped at 10 auto-loaded
// pages, after which a manual "Load more" button takes over (the shared
// 1000-row backend cap, slice 125 DoS posture).
//
// Filters live in the URL (`?from=...&to=...&actor=...&kind=foo,bar`)
// so back/forward buttons and shared links restore the same view; a
// link saved from the old /audit-log page carries the same keys and
// lands here through its redirect.
//
// Two views of the same rows (#2093), chosen by `?view=`:
//
//   - sentences (default): "<Person> approved exception …", grouped by
//     day, relative time, payload behind an expander. The sentence comes
//     from `lib/activity-sentences.ts` and uses only the row envelope.
//   - ledger (`?view=ledger`): the raw table — kind, target, IDs.
//
// `payload_json` is null on a row whose source the caller cannot read
// (#2379). The expander then says the details are not available; it
// never fetches them from anywhere else.
//
// Export (CSV / JSON / XLSX) goes through `/api/audit-log/export`, which
// admits fewer roles than `/v1/activity/unified`; the server shell
// passes `canExport` so the bar shows only to callers it admits.

import { useCallback, useEffect, useMemo, useRef, useState } from "react";

import Link from "next/link";
import { useInfiniteQuery, useQuery } from "@tanstack/react-query";
import { useRouter, useSearchParams } from "next/navigation";

import { Badge } from "@/components/ui/badge";
import { Button, buttonVariants } from "@/components/ui/button";
import { EntityPicker } from "@/components/ui/entity-picker";
import { Input } from "@/components/ui/input";
import { Person } from "@/components/ui/person";
import { Timestamp } from "@/components/ui/timestamp";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";

import { describeEvent, groupByDay } from "@/lib/activity-sentences";
import { enumLabel } from "@/lib/labels/enum-labels";
import {
  machineActorOptions,
  type EntityKind,
  type EntityOption,
} from "@/lib/entity-picker";
import {
  ACTIVITY_KINDS,
  ACTOR_ME_SENTINEL,
  ActivityEntry,
  ActivityFetchError,
  ActivityKind,
  ActivityListResponse,
  MAX_WINDOW_DAYS,
  fetchActivity,
  fetchActivityActors,
  renderActorLabel,
} from "@/lib/api/activity";
import {
  AUDIT_LOG_EXPORT_FORMATS,
  type AuditLogExportFormat,
  buildAuditLogExportURL,
} from "@/lib/api/audit-log";

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

type View = "sentences" | "ledger";

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
  view: View;
};

export function ActivityPageClient({ canExport }: { canExport: boolean }) {
  const router = useRouter();
  const searchParams = useSearchParams();

  const filters = useMemo<Filters>(
    () => parseFilters(searchParams),
    [searchParams],
  );

  const queryEnabled = rangeIsValid(filters.from, filters.to);

  // The view is not part of the query: switching it re-renders the
  // rows already loaded.
  const { view, ...queryFilters } = filters;
  const query = useInfiniteQuery<
    ActivityListResponse,
    ActivityFetchError,
    { pages: ActivityListResponse[]; pageParams: (string | undefined)[] },
    readonly unknown[],
    string | undefined
  >({
    queryKey: ["activity", queryFilters],
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
      if (next.view === "ledger") params.set("view", "ledger");
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
  const applyActor = (actor: string) => {
    replaceWith({ ...filters, actor: actor.trim() });
  };
  const clearActor = () => {
    replaceWith({ ...filters, actor: "" });
  };
  const setView = (v: View) => {
    replaceWith({ ...filters, view: v });
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

      <div className="flex flex-wrap items-center justify-between gap-2">
        <ViewToggle view={view} onChange={setView} />
        {canExport ? (
          <ExportBar filters={filters} disabled={!queryEnabled} />
        ) : null}
      </div>

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

      {view === "ledger" ? (
        <LedgerTable rows={rows} loading={query.isLoading} />
      ) : (
        <SentenceList rows={rows} loading={query.isLoading} />
      )}

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
  const view: View =
    searchParams.get("view") === "ledger" ? "ledger" : "sentences";

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

  return { from, to, actor, kinds, includeReads, view };
}

type FilterBarProps = {
  filters: Filters;
  onActorApply: (actor: string) => void;
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
  windowValid,
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
            actorWindow={{
              from: filters.from,
              to: filters.to,
              includeReads: filters.includeReads,
            }}
            windowValid={windowValid}
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
                {enumLabel("ActivityKind", k)}
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

// ActorFilterInput picks the actor by name (#2086): a person, an API key,
// or "You". Picking one applies the filter; a pasted user or API key ID
// is checked on entry and applied when valid.
//
// #2414: the machine and system actors in the window's activity are
// listed too ("System", "OAuth client", the process or client ID as the
// hint), from GET /api/activity/actors. A deep-linked machine actor
// (`?actor=system`, `?actor=oauth_client:<id>`) is listed even when the
// window holds none of its rows, so it keeps its label and survives a
// blur. Free text for other values is not accepted.
//
// Slice 270 D5: "You" is the literal `me` sentinel the BFF resolves
// server-side to the caller's user_id, so the operator never sees a UUID
// they did not type.
const ACTOR_KINDS: readonly EntityKind[] = ["user", "credential"];
const ACTOR_ME_OPTION: readonly EntityOption[] = [
  { value: ACTOR_ME_SENTINEL, label: "You", hint: "Your activity" },
];

function ActorFilterInput({
  initial,
  actorWindow,
  windowValid,
  onApply,
  onClear,
  hasCommittedValue,
}: {
  initial: string;
  actorWindow: { from: string; to: string; includeReads: boolean };
  windowValid: boolean;
  onApply: (actor: string) => void;
  onClear: () => void;
  hasCommittedValue: boolean;
}) {
  const isMeSentinel = initial === ACTOR_ME_SENTINEL;
  const machineActors = useQuery({
    queryKey: [
      "activity-actors",
      actorWindow.from,
      actorWindow.to,
      actorWindow.includeReads,
    ],
    enabled: windowValid,
    queryFn: ({ signal }) => fetchActivityActors(actorWindow, signal),
    staleTime: 60_000,
    retry: false,
  });
  const actorOptions = useMemo<readonly EntityOption[]>(
    () => [
      ...ACTOR_ME_OPTION,
      ...machineActorOptions([...(machineActors.data ?? []), initial]),
    ],
    [machineActors.data, initial],
  );
  return (
    <div>
      <label
        htmlFor="activity-actor"
        className="block text-xs font-medium text-muted-foreground"
      >
        Actor
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
        <div className="min-w-0 flex-1">
          <EntityPicker
            kinds={ACTOR_KINDS}
            id="activity-actor"
            testId="activity-actor"
            placeholder="Search people, API keys and system actors, or paste an ID"
            value={initial}
            extraOptions={actorOptions}
            onChange={(actor) => (actor ? onApply(actor) : onClear())}
          />
        </div>
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

// ----- view toggle ---------------------------------------------------

function ViewToggle({
  view,
  onChange,
}: {
  view: View;
  onChange: (v: View) => void;
}) {
  const option = (v: View, label: string) => (
    <button
      type="button"
      data-testid={`activity-view-${v}`}
      aria-pressed={view === v}
      onClick={() => onChange(v)}
      className={
        "rounded-full border px-3 py-1 text-xs transition-colors " +
        (view === v
          ? "border-primary bg-primary text-primary-foreground"
          : "border-input bg-background hover:bg-muted")
      }
    >
      {label}
    </button>
  );
  return (
    <div
      role="group"
      aria-label="View"
      data-testid="activity-view-toggle"
      className="flex items-center gap-2"
    >
      {option("sentences", "Events")}
      {option("ledger", "Raw ledger")}
    </div>
  );
}

// ----- export --------------------------------------------------------
//
// Slice 135: CSV / JSON / XLSX of the current filter set. Each format
// is an <a download> at the BFF export URL; the filename is
// server-authored. The export endpoint returns every row in the window
// (it takes no include_reads filter), as it did on /audit-log.

const EXPORT_FORMAT_LABELS: Record<AuditLogExportFormat, string> = {
  csv: "CSV",
  json: "JSON",
  xlsx: "XLSX",
};

function ExportBar({
  filters,
  disabled,
}: {
  filters: Filters;
  disabled: boolean;
}) {
  return (
    <div
      data-testid="activity-export-bar"
      className="flex flex-wrap items-center gap-2"
    >
      <span className="text-xs font-medium text-muted-foreground">
        Export current filter:
      </span>
      {AUDIT_LOG_EXPORT_FORMATS.map((format) => {
        if (disabled) {
          return (
            <Button
              key={format}
              type="button"
              size="sm"
              variant="outline"
              disabled
              data-testid={`activity-export-${format}`}
            >
              {EXPORT_FORMAT_LABELS[format]}
            </Button>
          );
        }
        const href = buildAuditLogExportURL(format, {
          from: filters.from,
          to: filters.to,
          actor: filters.actor,
          kinds: filters.kinds,
        });
        return (
          <a
            key={format}
            href={href}
            download
            rel="noopener"
            className={buttonVariants({ variant: "outline", size: "sm" })}
            data-testid={`activity-export-${format}`}
          >
            {EXPORT_FORMAT_LABELS[format]}
          </a>
        );
      })}
    </div>
  );
}

// ----- shared row states ----------------------------------------------

function EmptyOrLoading({
  loading,
  empty,
}: {
  loading: boolean;
  empty: boolean;
}) {
  if (loading && empty) {
    return (
      <div
        data-testid="activity-loading"
        className="rounded-md border bg-card p-6 text-center text-sm text-muted-foreground"
      >
        Loading…
      </div>
    );
  }
  return (
    <div
      data-testid="activity-empty"
      className="rounded-md border bg-card p-6 text-center text-sm text-muted-foreground"
    >
      No activity entries match these filters.
    </div>
  );
}

function rowKey(row: ActivityEntry): string {
  return `${row.kind}-${row.row_id}`;
}

// PayloadView renders the row's payload, or says it is not available
// when the backend withheld it (#2379: null for a source the caller
// cannot read).
function PayloadView({ row, id }: { row: ActivityEntry; id: string }) {
  if (row.payload_json === null || row.payload_json === undefined) {
    return (
      <p
        id={id}
        data-testid="activity-payload-unavailable"
        className="text-xs text-muted-foreground"
      >
        Details not available.
      </p>
    );
  }
  return (
    <pre
      id={id}
      tabIndex={0}
      aria-label="Event payload"
      className="max-h-80 overflow-auto rounded bg-foreground/5 p-3 text-xs"
    >
      {safeStringify(row.payload_json)}
    </pre>
  );
}

function ExpandToggle({
  open,
  controls,
  onToggle,
}: {
  open: boolean;
  controls: string;
  onToggle: () => void;
}) {
  // A real button: aria-expanded is not allowed on a bare span (axe
  // aria-allowed-attr). A ledger row click toggles too, so the button
  // stops its own click from toggling twice.
  return (
    <Button
      type="button"
      variant="ghost"
      size="icon-xs"
      data-testid="activity-row-toggle"
      aria-expanded={open}
      aria-controls={open ? controls : undefined}
      aria-label={open ? "Collapse details" : "Expand details"}
      onClick={(e) => {
        e.stopPropagation();
        onToggle();
      }}
    >
      {open ? "▾" : "▸"}
    </Button>
  );
}

// ----- sentences -----------------------------------------------------

function SentenceList({
  rows,
  loading,
}: {
  rows: ActivityEntry[];
  loading: boolean;
}) {
  if (rows.length === 0) {
    return <EmptyOrLoading loading={loading} empty />;
  }
  const groups = groupByDay(rows);
  return (
    <div data-testid="activity-sentences" className="space-y-4">
      {groups.map((g) => (
        <section
          key={g.key}
          data-testid="activity-day"
          aria-labelledby={`activity-day-${g.key}`}
          className="rounded-xl border bg-card"
        >
          <h2
            id={`activity-day-${g.key}`}
            data-testid="activity-day-heading"
            className="border-b px-4 py-2 text-xs font-medium uppercase tracking-wider text-muted-foreground"
          >
            {g.label}
          </h2>
          <ul className="divide-y">
            {g.rows.map((row) => (
              <SentenceRow key={rowKey(row)} row={row} />
            ))}
          </ul>
        </section>
      ))}
    </div>
  );
}

function SentenceRow({ row }: { row: ActivityEntry }) {
  const [open, setOpen] = useState(false);
  const s = describeEvent(row);
  const payloadId = `activity-payload-${rowKey(row)}`;
  return (
    <li data-testid="activity-row" data-kind={row.kind} className="px-4 py-2">
      <div className="flex items-start gap-2">
        <ExpandToggle
          open={open}
          controls={payloadId}
          onToggle={() => setOpen((v) => !v)}
        />
        <p data-testid="activity-sentence" className="flex-1 text-sm">
          <Person id={row.actor_id} className="font-medium" /> {s.phrase}
          {s.noun ? ` ${s.noun}` : ""}
          {s.targetId ? (
            <>
              {" "}
              {s.href ? (
                <Link
                  href={s.href}
                  className="font-mono text-xs underline underline-offset-2"
                  data-testid="activity-sentence-target"
                >
                  {shortId(s.targetId)}
                </Link>
              ) : (
                <span
                  className="font-mono text-xs"
                  title={s.targetId}
                  data-testid="activity-sentence-target"
                >
                  {shortId(s.targetId)}
                </span>
              )}
            </>
          ) : null}
          {s.suffix ? ` ${s.suffix}` : ""}
        </p>
        <Timestamp
          value={row.occurred_at}
          className="shrink-0 text-xs text-muted-foreground"
        />
      </div>
      {open ? (
        <div data-testid="activity-row-payload" className="mt-2 pl-8">
          <PayloadView row={row} id={payloadId} />
        </div>
      ) : null}
    </li>
  );
}

// shortId keeps a UUID-length id readable in a sentence; the full id is
// in the raw ledger and the tooltip.
function shortId(id: string): string {
  return id.length > 12 ? `${id.slice(0, 8)}…` : id;
}

// ----- raw ledger ----------------------------------------------------

function LedgerTable({
  rows,
  loading,
}: {
  rows: ActivityEntry[];
  loading: boolean;
}) {
  if (rows.length === 0) {
    return <EmptyOrLoading loading={loading} empty />;
  }
  return (
    <div
      data-testid="activity-table-wrap"
      className="overflow-x-auto rounded-xl border bg-card"
    >
      <Table>
        <TableHeader>
          <TableRow className="bg-muted/50 text-[11px] uppercase tracking-wider text-muted-foreground">
            <TableHead className="w-8">
              <span className="sr-only">Details</span>
            </TableHead>
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
            <LedgerRow key={rowKey(row)} row={row} />
          ))}
        </TableBody>
      </Table>
    </div>
  );
}

function LedgerRow({ row }: { row: ActivityEntry }) {
  const [open, setOpen] = useState(false);
  const payloadId = `activity-ledger-payload-${rowKey(row)}`;
  return (
    <>
      <TableRow
        data-testid="activity-ledger-row"
        className="cursor-pointer hover:bg-muted/40"
        onClick={() => setOpen((v) => !v)}
      >
        <TableCell className="w-8 text-center text-muted-foreground">
          <ExpandToggle
            open={open}
            controls={payloadId}
            onToggle={() => setOpen((v) => !v)}
          />
        </TableCell>
        <TableCell>
          <Timestamp value={row.occurred_at} />
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
            {enumLabel("ActivityKind", row.kind)}
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
            <PayloadView row={row} id={payloadId} />
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
