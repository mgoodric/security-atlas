"use client";

// Slice 040 — upcoming items panel (AC-5).
//
// Binds to `GET /v1/exceptions/expiring?within=30d` via the dashboard
// BFF (slice 028 exception lifecycle). The mockup's "Upcoming · next 30
// days" panel also wants board-report-due, access-review, and
// questionnaire-due rows — but there is no unified upcoming-rollup
// endpoint on main. Exceptions-expiring is the one real source and is
// bound; the other categories are surfaced as an honest labelled gap
// rather than fabricated rows (anti-criterion P0-1).

import { PanelCard, type PanelState } from "@/components/dashboard/panel-card";
import type { ExpiringExceptionsResponse } from "@/lib/api";

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

export function UpcomingPanel({
  report,
  state,
}: {
  report: ExpiringExceptionsResponse | undefined;
  state: PanelState;
}) {
  return (
    <PanelCard
      title="Upcoming · next 30 days"
      description="Expiring exceptions"
      state={state}
      skeletonClassName="h-40 w-full"
      testid="upcoming-panel"
    >
      {!report || report.exceptions.length === 0 ? (
        <p
          className="py-6 text-sm text-muted-foreground"
          data-testid="upcoming-empty"
        >
          No exceptions expire in the next 30 days.
        </p>
      ) : (
        <ul
          className="divide-y divide-foreground/5"
          data-testid="upcoming-list"
        >
          {report.exceptions.map((exc) => {
            const { month, day } = dateParts(exc.expires_at);
            const left = daysUntil(exc.expires_at);
            return (
              <li
                key={exc.id}
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
                  <div className="font-medium">
                    Exception {exc.id.slice(0, 8)} expires
                  </div>
                  <div className="truncate text-xs text-muted-foreground">
                    {exc.justification || "no justification recorded"}
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
      <p
        className="mt-3 border-t border-foreground/5 pt-3 text-xs text-muted-foreground"
        data-testid="upcoming-gap"
      >
        Board-report-due, access-review, and questionnaire-due items need a
        unified upcoming-rollup endpoint — not on main yet. Only expiring
        exceptions are shown until then.
      </p>
    </PanelCard>
  );
}
