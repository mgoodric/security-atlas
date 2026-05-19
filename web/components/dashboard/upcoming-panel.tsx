"use client";

// Slice 040 — upcoming items panel — REBOUND by slice 157.
//
// Binds to `GET /v1/upcoming` via the dashboard BFF (slice 066 AC-4
// unified rollup). Each row carries `{due_date, category, title,
// resource_type, resource_id}` across four categories: exception,
// policy_ack, vendor_review, audit_period.
//
// Slice 040 originally wired this to `/v1/exceptions/expiring?within=30d`
// (the only real source on main at slice-040 time) and surfaced the
// unified-rollup gap as a labelled `upcoming-gap` footer. Slice 066
// shipped the rollup endpoint, and slice 157 closes the loop by
// re-pointing the panel onto it — dropping both the `ExpiringExceptions`
// types and the gap footer. See
// `docs/audit-log/157-dashboard-upcoming-and-top-risks-decisions.md`.

import { PanelCard, type PanelState } from "@/components/dashboard/panel-card";
import { Badge } from "@/components/ui/badge";
import type { UpcomingResponse } from "@/lib/api";

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

// categoryVariant maps a rollup category onto the shadcn Badge
// variants. Exception is destructive (it's the only category that
// represents an open accepted-risk window); the other three are neutral
// "outline" to keep visual weight even across the feed.
function categoryVariant(
  category: string,
): "destructive" | "secondary" | "outline" {
  if (category === "exception") return "destructive";
  if (category === "audit_period") return "secondary";
  return "outline";
}

// categoryLabel renders the snake_case category name as a short
// human-readable label.
function categoryLabel(category: string): string {
  switch (category) {
    case "exception":
      return "Exception";
    case "policy_ack":
      return "Policy ack";
    case "vendor_review":
      return "Vendor review";
    case "audit_period":
      return "Audit period";
    default:
      return category;
  }
}

export function UpcomingPanel({
  report,
  state,
}: {
  report: UpcomingResponse | undefined;
  state: PanelState;
}) {
  return (
    <PanelCard
      title="Upcoming · next 30 days"
      description="Expiring exceptions, policy acknowledgments, vendor reviews, audit milestones"
      state={state}
      skeletonClassName="h-40 w-full"
      testid="upcoming-panel"
    >
      {!report || report.upcoming.length === 0 ? (
        <p
          className="py-6 text-sm text-muted-foreground"
          data-testid="upcoming-empty"
        >
          Nothing due in the next 30 days.
        </p>
      ) : (
        <ul
          className="divide-y divide-foreground/5"
          data-testid="upcoming-list"
        >
          {report.upcoming.map((item, idx) => {
            const { month, day } = dateParts(item.due_date);
            const left = daysUntil(item.due_date);
            return (
              <li
                key={`${item.category}:${item.resource_id}:${idx}`}
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
                      variant={categoryVariant(item.category)}
                      data-testid="upcoming-row-category"
                    >
                      {categoryLabel(item.category)}
                    </Badge>
                    <span
                      className="truncate font-medium"
                      data-testid="upcoming-row-title"
                    >
                      {item.title}
                    </span>
                  </div>
                  <div className="mt-0.5 truncate font-mono text-xs text-muted-foreground">
                    {item.resource_type}
                    {item.resource_id
                      ? ` · ${item.resource_id.slice(0, 12)}`
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
