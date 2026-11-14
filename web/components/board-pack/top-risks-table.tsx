// Slice 043 — top risks aging table (per Plans/_archive/mockups/board-pack.html §02).
//
// Renders the slice-032 RiskAging rows as a table with treatment chip,
// residual severity, and aging days. Data only — no fabrication.

import {
  formatSeverity,
  severityBand,
  severityBandLabel,
} from "@/app/(authed)/risks/filters";
import { cn } from "@/lib/utils";

import { TONE_CHIP } from "./tone";

export type RiskAgingRow = {
  id: string;
  title: string;
  category: string;
  treatment: string;
  residual_severity: number;
  age_days: number;
};

// Keyed on the bare treatment: the board label carries an acceptance
// status suffix ("accept (proposed)", #1746), stripped before lookup.
const treatmentTone: Record<string, string> = {
  untreated: TONE_CHIP.neutral,
  mitigate: TONE_CHIP.critical,
  accept: TONE_CHIP.info,
  transfer: TONE_CHIP.progress,
  avoid: TONE_CHIP.warning,
};

export function TopRisksTable({ risks }: { risks: RiskAgingRow[] }) {
  if (!risks || risks.length === 0) {
    return (
      <p
        className="rounded-md border border-dashed border-border p-4 text-sm text-muted-foreground"
        data-testid="top-risks-empty"
      >
        No top risks aging in this period.
      </p>
    );
  }
  return (
    <table className="w-full text-sm" data-testid="top-risks-table">
      <thead className="border-b border-border text-[11px] uppercase tracking-wider text-muted-foreground">
        <tr>
          <th className="pb-2 pr-3 text-left font-medium">Risk</th>
          <th className="pb-2 pr-3 text-left font-medium">Treatment</th>
          <th className="pb-2 pr-3 text-right font-medium">Residual</th>
          <th className="pb-2 text-right font-medium">Open</th>
        </tr>
      </thead>
      <tbody className="divide-y divide-border">
        {risks.map((risk) => (
          <tr key={risk.id}>
            <td className="py-3 pr-3">
              <div className="font-medium text-foreground">{risk.title}</div>
              <div className="font-mono text-[11px] text-muted-foreground">
                {risk.id}
                {risk.category ? ` · ${risk.category}` : ""}
              </div>
            </td>
            <td className="py-3 pr-3">
              <span
                className={cn(
                  "inline-flex items-center rounded-md border px-2 py-0.5 text-[11px] font-medium",
                  treatmentTone[risk.treatment.split(" ")[0]] ??
                    TONE_CHIP.neutral,
                )}
              >
                {risk.treatment}
              </span>
            </td>
            <td className="py-3 pr-3 text-right font-mono text-foreground">
              {/* #2058: the board generator's residual_severity is on the
                  1..25 scale; show it with the register's band label. */}
              <span data-testid="top-risks-residual">
                {formatSeverity(risk.residual_severity)}
              </span>{" "}
              <span
                className="text-[11px] text-muted-foreground"
                data-testid="top-risks-residual-band"
              >
                {severityBandLabel(severityBand(risk.residual_severity))}
              </span>
            </td>
            <td className="py-3 text-right font-mono text-muted-foreground">
              {risk.age_days} d
            </td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}
