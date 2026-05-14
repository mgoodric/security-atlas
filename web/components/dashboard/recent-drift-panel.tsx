"use client";

// Slice 040 — recent drift panel (AC-4).
//
// Binds to `GET /v1/controls/drift?since=7d` via the dashboard BFF
// (slice 016 drift read model). Renders the controls that flipped OUT
// of passing in the window, each with its last-passing date and current
// result, plus the signed delta over the window. Fully bound — no gap.

import Link from "next/link";

import { PanelCard, type PanelState } from "@/components/dashboard/panel-card";
import type { DriftReport } from "@/lib/api";

export function RecentDriftPanel({
  report,
  state,
}: {
  report: DriftReport | undefined;
  state: PanelState;
}) {
  const delta = report?.delta ?? 0;
  const deltaLabel = delta > 0 ? `+${delta}` : String(delta);

  return (
    <PanelCard
      title="Recent drift · last 7 days"
      description="Controls that flipped out of passing"
      action={
        report ? (
          <span
            className="font-mono text-xs text-muted-foreground"
            data-testid="drift-delta"
          >
            delta {deltaLabel}
          </span>
        ) : null
      }
      state={state}
      skeletonClassName="h-40 w-full"
      testid="recent-drift-panel"
    >
      {!report || report.flipped_out.length === 0 ? (
        <p
          className="py-6 text-sm text-muted-foreground"
          data-testid="recent-drift-empty"
        >
          No controls flipped out of passing in the last 7 days
          {report ? ` (window ${report.since} → ${report.through})` : ""}.
        </p>
      ) : (
        <ul
          className="divide-y divide-foreground/5"
          data-testid="recent-drift-list"
        >
          {report.flipped_out.map((row) => (
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
      )}
    </PanelCard>
  );
}
