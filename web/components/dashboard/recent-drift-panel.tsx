"use client";

// Slice 040 — recent drift panel (AC-4).
//
// Binds to `GET /v1/controls/drift?since=7d` via the dashboard BFF
// (slice 016 drift read model). #2063: the headline is gains and losses
// counted from the two lists below it ("+3 started passing · 1 stopped
// passing"), each count linking to its list. The bare net delta is gone:
// beside a loss list it read as a contradiction. Fully bound — no gap.

import Link from "next/link";

import { PanelCard, type PanelState } from "@/components/dashboard/panel-card";
import { summarizeDrift } from "@/components/dashboard/recent-drift-summary";
import type { DriftReport } from "@/lib/api/dashboard";

export function RecentDriftPanel({
  report,
  state,
}: {
  report: DriftReport | undefined;
  state: PanelState;
}) {
  const summary = report ? summarizeDrift(report) : null;
  const gains = report?.started_passing ?? [];
  const losses = report?.flipped_out ?? [];

  return (
    <PanelCard
      title="Recent drift · last 7 days"
      description="Controls that started or stopped passing"
      action={
        summary?.label ? (
          <span
            className="font-mono text-xs text-muted-foreground"
            data-testid="drift-summary"
          >
            <a href="#recent-drift-gains" className="hover:underline">
              +{summary.gains} started passing
            </a>
            {" · "}
            <a href="#recent-drift-losses" className="hover:underline">
              {summary.losses} stopped passing
            </a>
          </span>
        ) : null
      }
      state={state}
      skeletonClassName="h-40 w-full"
      testid="recent-drift-panel"
    >
      {!report || !summary?.label ? (
        <p
          className="py-6 text-sm text-muted-foreground"
          data-testid="recent-drift-empty"
        >
          No controls started or stopped passing in the last 7 days
          {report ? ` (window ${report.since} → ${report.through})` : ""}.
        </p>
      ) : (
        <>
          {losses.length > 0 ? (
            <ul
              id="recent-drift-losses"
              className="divide-y divide-foreground/5"
              data-testid="recent-drift-list"
            >
              {losses.map((row) => (
                <li
                  key={row.control_id}
                  data-testid="recent-drift-row"
                  className="py-3 text-sm"
                >
                  <div className="flex items-center justify-between">
                    <Link
                      href={`/controls/${encodeURIComponent(row.control_id)}`}
                      className="font-medium hover:underline"
                    >
                      {row.control_id.slice(0, 8)}
                    </Link>
                    <span className="font-mono text-xs text-destructive">
                      {row.current_result}
                    </span>
                  </div>
                  <div className="mt-1 text-xs text-muted-foreground">
                    last passing{" "}
                    <span className="font-mono">{row.last_passing}</span>
                  </div>
                </li>
              ))}
            </ul>
          ) : null}
          {gains.length > 0 ? (
            <ul
              id="recent-drift-gains"
              className="divide-y divide-foreground/5"
              data-testid="recent-drift-gain-list"
            >
              {gains.map((row) => (
                <li
                  key={row.control_id}
                  data-testid="recent-drift-gain-row"
                  className="flex items-center justify-between py-3 text-sm"
                >
                  <Link
                    href={`/controls/${encodeURIComponent(row.control_id)}`}
                    className="font-medium hover:underline"
                  >
                    {row.control_id.slice(0, 8)}
                  </Link>
                  <span className="font-mono text-xs text-muted-foreground">
                    started passing
                  </span>
                </li>
              ))}
            </ul>
          ) : null}
        </>
      )}
    </PanelCard>
  );
}
