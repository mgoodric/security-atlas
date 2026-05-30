"use client";

// Slice 040 — evidence freshness panel.
//
// Binds to `GET /v1/evidence/freshness` via the dashboard BFF (slice
// 016 freshness read model). Renders the per-freshness-class
// distribution as fresh/stale bars and the tenant-wide total-stale
// count. Fully bound — no gap.

import { PanelCard, type PanelState } from "@/components/dashboard/panel-card";
import type { FreshnessReport } from "@/lib/api/dashboard";

function freshFraction(fresh: number, total: number): number {
  if (total <= 0) return 0;
  return Math.round((fresh / total) * 100);
}

// barColor maps a fresh-fraction percentage to its bar fill: green at
// 90%+, amber at 70-89%, destructive below 70%.
function barColor(pct: number): string {
  if (pct >= 90) return "h-full bg-emerald-500";
  if (pct >= 70) return "h-full bg-amber-500";
  return "h-full bg-destructive";
}

export function EvidenceFreshnessPanel({
  report,
  state,
}: {
  report: FreshnessReport | undefined;
  state: PanelState;
}) {
  return (
    <PanelCard
      title="Evidence freshness"
      description="Fresh vs stale evidence by freshness class"
      state={state}
      skeletonClassName="h-48 w-full"
      testid="evidence-freshness-panel"
    >
      {!report || report.buckets.length === 0 ? (
        <p
          className="py-6 text-sm text-muted-foreground"
          data-testid="evidence-freshness-empty"
        >
          No evidence freshness data is available yet.
        </p>
      ) : (
        <div className="space-y-3" data-testid="evidence-freshness-list">
          {report.buckets.map((bucket) => {
            const pct = freshFraction(bucket.fresh, bucket.total);
            return (
              <div key={bucket.freshness_class} data-testid="freshness-bucket">
                <div className="mb-1 flex items-baseline justify-between text-xs">
                  <span className="text-muted-foreground">
                    {bucket.freshness_class}
                  </span>
                  <span className="font-mono text-muted-foreground">
                    {bucket.fresh}/{bucket.total} fresh
                  </span>
                </div>
                <div className="h-1.5 overflow-hidden rounded-full bg-muted">
                  <div className={barColor(pct)} style={{ width: `${pct}%` }} />
                </div>
              </div>
            );
          })}
        </div>
      )}
      {report ? (
        <p
          className="mt-3 border-t border-foreground/5 pt-3 text-xs text-muted-foreground"
          data-testid="evidence-freshness-stale-total"
        >
          <span className="font-medium text-destructive">
            {report.total_stale}
          </span>{" "}
          of {report.total} evidence records are past their freshness window.
        </p>
      ) : null}
    </PanelCard>
  );
}
