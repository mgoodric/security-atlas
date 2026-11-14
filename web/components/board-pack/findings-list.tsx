// Slice 043 — open findings list (per board-pack.html §04 placement; the
// mockup folds findings into operational, but slice 032 has a dedicated
// `open_findings` section — render it on its own card to match the
// canonical SectionKeys order).

import { cn } from "@/lib/utils";

import { TONE_CHIP } from "./tone";

export type FindingRow = {
  evaluation_id: string;
  control_id: string;
  scope_cell_id: string;
  evaluated_at: string;
  freshness_status: string;
};

const freshnessTone: Record<string, string> = {
  fresh: TONE_CHIP.pass,
  stale: TONE_CHIP.warning,
  expired: TONE_CHIP.critical,
  missing: TONE_CHIP.neutral,
};

export function FindingsList({
  findings,
  count,
}: {
  findings: FindingRow[];
  count: number;
}) {
  if (!findings || findings.length === 0) {
    return (
      <p
        className="rounded-md border border-dashed border-border p-4 text-sm text-muted-foreground"
        data-testid="findings-empty"
      >
        No open findings as of period end.
      </p>
    );
  }
  return (
    <div data-testid="findings-list">
      <p className="mb-3 text-sm text-muted-foreground">
        {count} open finding{count === 1 ? "" : "s"} as of period end. A finding
        is a failing control evaluation (slice 032 decision D4).
      </p>
      <ul className="divide-y divide-border rounded-lg border border-border">
        {findings.map((f) => (
          <li key={f.evaluation_id} className="flex items-center gap-3 p-3">
            <span className="flex-1 truncate font-mono text-xs text-foreground">
              {f.control_id}
            </span>
            <span
              className={cn(
                "inline-flex items-center rounded border px-2 py-0.5 text-[11px] font-medium",
                freshnessTone[f.freshness_status] ?? TONE_CHIP.neutral,
              )}
            >
              {f.freshness_status}
            </span>
            <span className="font-mono text-[11px] text-muted-foreground">
              {f.evaluated_at}
            </span>
          </li>
        ))}
      </ul>
    </div>
  );
}
