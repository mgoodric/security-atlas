"use client";

// Slice 094 — agenda view (default).
//
// Vertical list of events grouped by month header. Each row: date +
// type icon (color dot) + title + linked entity. Overdue control events
// render with a red dot for visual urgency (AC-13a).
//
// Per-event links go to the canonical detail page:
//   audit       -> /audits/[id]   (placeholder until that page ships)
//   exception   -> /admin/exceptions/[id]   (placeholder)
//   policy      -> /policies/[id]   (placeholder)
//   control     -> /controls/[id]   (real page from slice 041)
//
// Anti-criterion P0-A5 compliance: links point to placeholders when the
// per-page slices haven't shipped; the user sees a friendly "page coming
// soon" rather than a hard 404. The calendar links update automatically
// when the page slices land.

import Link from "next/link";

import type { CalendarEvent } from "@/lib/api";

type Props = {
  events: CalendarEvent[];
  truncated: boolean;
};

const TYPE_COLOR: Record<string, string> = {
  audit: "bg-blue-500",
  exception: "bg-amber-500",
  policy: "bg-purple-500",
  control: "bg-emerald-500",
};

const TYPE_LABEL: Record<string, string> = {
  audit: "Audit",
  exception: "Exception",
  policy: "Policy",
  control: "Control review",
};

function linkFor(ev: CalendarEvent): string {
  switch (ev.type) {
    case "audit":
      return `/audits/${ev.related_entity_id}`;
    case "exception":
      return `/admin/exceptions/${ev.related_entity_id}`;
    case "policy":
      return `/policies/${ev.related_entity_id}`;
    case "control":
      return `/controls/${ev.related_entity_id}`;
    default:
      return "#";
  }
}

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
              return (
                <li key={`${ev.type}-${ev.id}`} className="p-3">
                  <Link
                    href={linkFor(ev)}
                    className="flex items-start gap-3 hover:bg-muted/50 -m-3 p-3 rounded-md transition-colors"
                  >
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
                  </Link>
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
