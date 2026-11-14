"use client";

// Slice 040 — upcoming items panel — REBOUND by slice 157, then by #2130.
//
// "Coming up" reads the compliance calendar (GET /v1/calendar via the
// calendar BFF) for the next 14 days: `fetchDashboardComingUp` asks for
// [today, today + 14d). The calendar is the one list of what is due
// (#2130), so the panel shows the same items, with the same status, as
// /calendar. Each row is one calendar event: its type badge, title, the
// related entity and the days until it is due.
//
// History: slice 040 bound this panel to `/v1/exceptions/expiring`;
// slice 157 re-pointed it at the slice-066 upcoming rollup, which #2130
// retired.

import { PanelCard, type PanelState } from "@/components/dashboard/panel-card";
import { Badge } from "@/components/ui/badge";
import type { CalendarResponse } from "@/lib/api/calendar";
import { COMING_UP_DAYS } from "@/lib/api/dashboard";

function daysUntil(iso: string): number {
  const then = new Date(iso).getTime();
  const now = Date.now();
  return Math.ceil((then - now) / (1000 * 60 * 60 * 24));
}

function dateParts(iso: string): { month: string; day: string } {
  const d = new Date(iso);
  return {
    month: d.toLocaleString("en-US", { month: "short", timeZone: "UTC" }),
    day: String(d.getUTCDate()).padStart(2, "0"),
  };
}

// typeVariant maps a calendar event onto the shadcn Badge variants. An
// overdue item or an expiring exception (an open accepted-risk window)
// is destructive; an audit milestone is secondary; the rest are neutral
// "outline" to keep visual weight even across the feed.
function typeVariant(
  type: string,
  status: string,
): "destructive" | "secondary" | "outline" {
  if (status === "overdue" || type === "exception") return "destructive";
  if (type === "audit") return "secondary";
  return "outline";
}

// typeLabel renders the calendar event type as a short label.
function typeLabel(type: string): string {
  switch (type) {
    case "audit":
      return "Audit period";
    case "exception":
      return "Exception";
    case "policy":
      return "Policy review";
    case "vendor":
      return "Vendor";
    case "control":
      return "Control review";
    case "risk":
      return "Risk review";
    case "action_plan":
      return "Action plan";
    case "offboarding":
      return "Offboarding";
    default:
      return type;
  }
}

export function UpcomingPanel({
  report,
  state,
}: {
  report: CalendarResponse | undefined;
  state: PanelState;
}) {
  return (
    <PanelCard
      title={`Coming up · next ${COMING_UP_DAYS} days`}
      description="From the compliance calendar: reviews, due dates, expiring exceptions, audit milestones"
      state={state}
      skeletonClassName="h-40 w-full"
      testid="upcoming-panel"
    >
      {!report || report.events.length === 0 ? (
        <p
          className="py-6 text-sm text-muted-foreground"
          data-testid="upcoming-empty"
        >
          Nothing due in the next {COMING_UP_DAYS} days.
        </p>
      ) : (
        <ul
          className="divide-y divide-foreground/5"
          data-testid="upcoming-list"
        >
          {report.events.map((ev, idx) => {
            const { month, day } = dateParts(ev.starts_at);
            const left = daysUntil(ev.starts_at);
            return (
              <li
                key={`${ev.type}:${ev.id}:${idx}`}
                data-testid="upcoming-row"
                className="flex items-center gap-3 py-3 text-sm"
              >
                <div className="w-10 shrink-0 text-center">
                  <div className="text-[10px] font-medium tracking-wider text-muted-foreground uppercase">
                    {month}
                  </div>
                  <div className="text-lg leading-none font-semibold">
                    {day}
                  </div>
                </div>
                <div className="min-w-0 flex-1">
                  <div className="flex items-center gap-2">
                    <Badge
                      variant={typeVariant(ev.type, ev.status)}
                      data-testid="upcoming-row-category"
                    >
                      {typeLabel(ev.type)}
                    </Badge>
                    <span
                      className="truncate font-medium"
                      data-testid="upcoming-row-title"
                    >
                      {ev.title}
                    </span>
                  </div>
                  <div className="mt-0.5 truncate font-mono text-xs text-muted-foreground">
                    {ev.related_entity_kind}
                    {ev.related_entity_id
                      ? ` · ${ev.related_entity_id.slice(0, 12)}`
                      : ""}
                  </div>
                </div>
                <span className="shrink-0 font-mono text-xs text-muted-foreground">
                  {left}d
                </span>
              </li>
            );
          })}
        </ul>
      )}
    </PanelCard>
  );
}
