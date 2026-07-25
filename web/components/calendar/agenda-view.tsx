"use client";

// Slice 094 — agenda view (default).
// Slice 183 — `linkFor` extracted to `./link-for` and switched to a
// tagged-union return so exception / policy events render as a
// non-linked span with a tooltip instead of an `<a>` that 404s.
//
// Vertical list of events grouped by month header. Each row: date +
// type icon (color dot) + title + linked entity. Overdue control events
// render with a red dot for visual urgency (AC-13a).
//
// Per-event link disposition (see `./link-for.ts` for the source of
// truth):
//   audit     -> /audits/[id]            (link; placeholder page —
//                                          calendar dead-link follow-on
//                                          is slice 184's responsibility
//                                          on the audits-list side; the
//                                          calendar branch stays a link
//                                          until that detail page ships)
//   exception -> static span + tooltip   (slice 183 AC-2; no detail
//                                          route exists)
//   policy    -> static span + tooltip   (slice 183 AC-3; no detail
//                                          route exists)
//   control   -> /controls/[id]          (real page from slice 041)
//
// Anti-criterion P0-A5 compliance preserved: where a per-page slice has
// not shipped, the calendar surface is an honest non-link rather than
// a 404 anchor.

import Link from "next/link";

import type { CalendarEvent } from "@/lib/api/calendar";

import { linkFor } from "./link-for";

type Props = {
  events: CalendarEvent[];
  truncated: boolean;
};

const TYPE_COLOR: Record<string, string> = {
  audit: "bg-blue-500",
  exception: "bg-amber-500",
  policy: "bg-purple-500",
  vendor: "bg-rose-500",
  control: "bg-emerald-500",
};

const TYPE_LABEL: Record<string, string> = {
  audit: "Audit",
  exception: "Exception",
  policy: "Policy",
  vendor: "Vendor review",
  control: "Control review",
};

function monthKey(iso: string): string {
  const d = new Date(iso);
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, "0")}`;
}

function monthLabel(key: string): string {
  const [y, m] = key.split("-");
  const d = new Date(Number(y), Number(m) - 1, 1);
  return d.toLocaleDateString(undefined, { year: "numeric", month: "long" });
}

function dayLabel(iso: string): string {
  const d = new Date(iso);
  return d.toLocaleDateString(undefined, {
    weekday: "short",
    month: "short",
    day: "numeric",
  });
}

function cadenceLabel(ev: CalendarEvent): string {
  if (ev.type !== "control") return "";
  const cadence = ev.cadence ?? "";
  const status = ev.status;
  if (status === "overdue") {
    return `${cadence ? `${cadence} review · ` : ""}overdue`;
  }
  if (status === "due-soon") {
    return `${cadence ? `${cadence} review · ` : ""}due soon`;
  }
  return cadence ? `${cadence} review` : "";
}

export function AgendaView({ events, truncated }: Props) {
  if (events.length === 0) {
    return (
      <div className="rounded-md border bg-card p-8 text-center">
        <p className="text-sm font-medium">
          No compliance events in the next 90 days.
        </p>
        <p className="mt-2 text-sm text-muted-foreground">
          Add an audit period, populate policy review dates, or check exception
          expirations to populate this view.
        </p>
      </div>
    );
  }

  // Group by YYYY-MM. Events come back sorted by starts_at ASC, so the
  // group order falls out naturally.
  const groups = new Map<string, CalendarEvent[]>();
  for (const ev of events) {
    const k = monthKey(ev.starts_at);
    const arr = groups.get(k) ?? [];
    arr.push(ev);
    groups.set(k, arr);
  }

  return (
    <div className="space-y-6">
      {Array.from(groups.entries()).map(([key, evs]) => (
        <section key={key}>
          <h2 className="mb-3 text-sm font-semibold text-muted-foreground">
            {monthLabel(key)}
          </h2>
          <ul className="divide-y rounded-md border bg-card">
            {evs.map((ev) => {
              const isOverdue =
                ev.type === "control" && ev.status === "overdue";
              const dotClass = isOverdue
                ? "bg-red-500"
                : TYPE_COLOR[ev.type] ?? "bg-muted";
              const target = linkFor(ev);
              const rowBody = (
                <>
                  <span
                    aria-label={
                      isOverdue ? "Overdue" : TYPE_LABEL[ev.type] ?? ev.type
                    }
                    className={`mt-1.5 inline-block h-2.5 w-2.5 shrink-0 rounded-full ${dotClass}`}
                  />
                  <div className="flex-1 min-w-0">
                    <div className="flex flex-wrap items-baseline gap-x-2">
                      <span className="text-xs uppercase tracking-wide text-muted-foreground">
                        {TYPE_LABEL[ev.type] ?? ev.type}
                      </span>
                      <span className="text-xs text-muted-foreground">
                        · {dayLabel(ev.starts_at)}
                      </span>
                    </div>
                    <p className="mt-0.5 text-sm font-medium truncate">
                      {ev.title}
                    </p>
                    {cadenceLabel(ev) && (
                      <p className="mt-0.5 text-xs text-muted-foreground">
                        {cadenceLabel(ev)}
                      </p>
                    )}
                  </div>
                </>
              );
              return (
                <li key={`${ev.type}-${ev.id}`} className="p-3">
                  {target.kind === "link" ? (
                    <Link
                      href={target.href}
                      className="flex items-start gap-3 hover:bg-muted/50 -m-3 p-3 rounded-md transition-colors"
                    >
                      {rowBody}
                    </Link>
                  ) : (
                    // Slice 183 — no `data-testid` here on purpose:
                    // adding one would trip the slice 178 HONESTY-GAP
                    // heuristic (unexpected testid not in the manifest's
                    // expected/allowed sets). The non-link element is
                    // structurally a `<span>` so the dead-anchor
                    // heuristic ignores it; the tooltip + aria-label
                    // carries the disclosure copy.
                    <span
                      title={target.reason}
                      aria-label={target.reason}
                      className="flex items-start gap-3 -m-3 p-3 rounded-md cursor-help"
                    >
                      {rowBody}
                    </span>
                  )}
                </li>
              );
            })}
          </ul>
        </section>
      ))}
      {truncated && (
        <p className="text-xs text-muted-foreground">
          Showing the first 500 events. Narrow the filter or shorten the date
          window to see more.
        </p>
      )}
    </div>
  );
}
