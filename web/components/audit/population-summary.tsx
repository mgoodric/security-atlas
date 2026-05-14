// Slice 042 — population summary card (AC-3).
//
// Renders the row_count + time window for a population. The row_count is
// the platform's count of evidence records eligible under the frozen
// horizon — when the period is frozen, the platform already applied
// `observed_at <= frozen_at`, so this number is the auditor's bounded
// universe (invariant 10, enforced server-side).

import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import type { Population } from "@/lib/api/audit";

export function PopulationSummary({ population }: { population: Population }) {
  return (
    <Card data-testid="population-summary" size="sm">
      <CardHeader>
        <CardTitle className="text-sm">Population</CardTitle>
      </CardHeader>
      <CardContent className="grid gap-1 text-sm">
        <div className="flex justify-between gap-4">
          <span className="text-muted-foreground">Eligible records</span>
          <span
            data-testid="population-row-count"
            className="font-medium tabular-nums"
          >
            {population.row_count}
          </span>
        </div>
        <div className="flex justify-between gap-4">
          <span className="text-muted-foreground">Window start</span>
          <span className="tabular-nums">{population.time_window_start}</span>
        </div>
        <div className="flex justify-between gap-4">
          <span className="text-muted-foreground">Window end</span>
          <span className="tabular-nums">{population.time_window_end}</span>
        </div>
        {population.frozen_at ? (
          <div className="flex justify-between gap-4">
            <span className="text-muted-foreground">Frozen horizon</span>
            <span className="tabular-nums">{population.frozen_at}</span>
          </div>
        ) : null}
      </CardContent>
    </Card>
  );
}
