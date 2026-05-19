"use client";

// Slice 040 — top risks aging panel — REBOUND by slice 157.
//
// Binds to `GET /v1/risks?treatment=mitigate&sort=residual,age` via the
// dashboard BFF (slice 066 AC-3 — ListRisks gained the residual,age
// server-side sort).
//
// Slice 040 originally wired this to the unsorted treatment=mitigate
// list and rendered rows in the API's server order with a labelled
// `top-risks-sort-gap` footer noting the residual,age ranking was a
// follow-up backend gap. Slice 066 shipped the ranking; slice 157
// closes the loop by re-pointing this panel onto it. See
// `docs/audit-log/157-dashboard-upcoming-and-top-risks-decisions.md`.

import Link from "next/link";

import { PanelCard, type PanelState } from "@/components/dashboard/panel-card";
import { Badge } from "@/components/ui/badge";
import type { DashboardRisk } from "@/lib/api";

function treatmentVariant(
  treatment: string,
): "destructive" | "secondary" | "outline" {
  if (treatment === "mitigate") return "destructive";
  if (treatment === "accept") return "secondary";
  return "outline";
}

export function TopRisksPanel({
  risks,
  state,
}: {
  risks: DashboardRisk[] | undefined;
  state: PanelState;
}) {
  return (
    <PanelCard
      title="Top risks · in treatment"
      description="Risks with treatment = mitigate · ranked by residual score, then age in treatment"
      action={
        <Link href="/risks" className="text-xs text-primary hover:underline">
          View register →
        </Link>
      }
      state={state}
      skeletonClassName="h-48 w-full"
      testid="top-risks-panel"
    >
      {!risks || risks.length === 0 ? (
        <p
          className="py-6 text-sm text-muted-foreground"
          data-testid="top-risks-empty"
        >
          No risks are currently in the mitigate treatment state.
        </p>
      ) : (
        <ul
          className="divide-y divide-foreground/5"
          data-testid="top-risks-list"
        >
          {risks.map((risk) => (
            <li
              key={risk.id}
              data-testid="top-risk-row"
              className="grid grid-cols-12 items-center gap-3 py-3 text-sm"
            >
              <div className="col-span-7">
                <div className="font-medium">{risk.title}</div>
                <div className="mt-0.5 font-mono text-xs text-muted-foreground">
                  {risk.id.slice(0, 8)} · {risk.methodology || "—"}
                </div>
              </div>
              <div className="col-span-2">
                <Badge variant={treatmentVariant(risk.treatment)}>
                  {risk.treatment}
                </Badge>
              </div>
              <div className="col-span-3 truncate text-xs text-muted-foreground">
                {risk.treatment_owner || "unassigned"}
              </div>
            </li>
          ))}
        </ul>
      )}
    </PanelCard>
  );
}
