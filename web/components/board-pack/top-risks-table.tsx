// Slice 043 — top risks aging table (per Plans/_archive/mockups/board-pack.html §02).
//
// Renders the slice-032 RiskAging rows as a table with treatment chip,
// residual severity, and aging days. Data only — no fabrication.

import { cn } from "@/lib/utils";

export type RiskAgingRow = {
  id: string;
  title: string;
  category: string;
  treatment: string;
  residual_severity: number;
  age_days: number;
};

const treatmentTone: Record<string, string> = {
  mitigate: "bg-rose-50 text-rose-700",
  accept: "bg-sky-50 text-sky-700",
  transfer: "bg-violet-50 text-violet-700",
  avoid: "bg-amber-50 text-amber-700",
};

export function TopRisksTable({ risks }: { risks: RiskAgingRow[] }) {
  if (!risks || risks.length === 0) {
    return (
      <p
        className="rounded-md border border-dashed border-slate-200 p-4 text-sm text-slate-500"
        data-testid="top-risks-empty"
      >
        No top risks aging in this period.
      </p>
    );
  }
  return (
    <table className="w-full text-sm" data-testid="top-risks-table">
      <thead className="border-b border-slate-200 text-[11px] uppercase tracking-wider text-slate-500">
        <tr>
          <th className="pb-2 pr-3 text-left font-medium">Risk</th>
          <th className="pb-2 pr-3 text-left font-medium">Treatment</th>
          <th className="pb-2 pr-3 text-right font-medium">Residual</th>
          <th className="pb-2 text-right font-medium">Open</th>
        </tr>
      </thead>
      <tbody className="divide-y divide-slate-100">
        {risks.map((risk) => (
          <tr key={risk.id}>
            <td className="py-3 pr-3">
              <div className="font-medium text-slate-900">{risk.title}</div>
              <div className="font-mono text-[11px] text-slate-400">
                {risk.id}
                {risk.category ? ` · ${risk.category}` : ""}
              </div>
            </td>
            <td className="py-3 pr-3">
              <span
                className={cn(
                  "inline-flex items-center rounded-md px-2 py-0.5 text-[11px] font-medium",
                  treatmentTone[risk.treatment] ??
                    "bg-slate-100 text-slate-700",
                )}
              >
                {risk.treatment}
              </span>
            </td>
            <td className="py-3 pr-3 text-right font-mono text-slate-700">
              {risk.residual_severity.toFixed(2)}
            </td>
            <td className="py-3 text-right font-mono text-slate-600">
              {risk.age_days} d
            </td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}
