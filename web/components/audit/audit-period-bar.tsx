// Slice 042 — AuditPeriod top-bar context (AC-1, AC-5).
//
// Renders the active AuditPeriod name, framework version, period window,
// and a frozen badge when `frozen_at` is set. The frozen badge is the
// auditor's visual confirmation of invariant 10: the workspace they are
// looking at is bounded to evidence with `observed_at <= frozen_at`.

import { Badge } from "@/components/ui/badge";
import type { AuditPeriod } from "@/lib/api/audit";

function fmtDate(iso: string): string {
  // period_start / period_end arrive as YYYY-MM-DD from slice 025.
  return iso;
}

export function AuditPeriodBar({ period }: { period: AuditPeriod }) {
  const frozen = Boolean(period.frozen_at);
  return (
    <div
      data-testid="audit-period-bar"
      className="flex flex-wrap items-center gap-x-4 gap-y-1 border-b bg-muted/30 px-4 py-2.5 sm:px-6"
    >
      <div className="flex items-center gap-2">
        <span className="text-sm font-semibold tracking-tight">
          {period.name}
        </span>
        {frozen ? (
          <Badge variant="secondary" data-testid="period-frozen-badge">
            frozen
          </Badge>
        ) : (
          <Badge variant="outline" data-testid="period-open-badge">
            open
          </Badge>
        )}
      </div>
      <span className="text-xs text-muted-foreground tabular-nums">
        {fmtDate(period.period_start)} — {fmtDate(period.period_end)}
      </span>
      {frozen && period.frozen_at ? (
        <span className="text-xs text-muted-foreground">
          evidence horizon: {period.frozen_at}
        </span>
      ) : (
        <span className="text-xs text-muted-foreground">
          live until frozen — sampling draws from current evidence
        </span>
      )}
    </div>
  );
}
