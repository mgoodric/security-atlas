"use client";

// Slice 040 — top risks aging panel (AC-3).
//
// Binds to `GET /v1/risks?treatment=mitigate` via the dashboard BFF.
// The mockup wants the table sorted by `residual × age-in-treatment`,
// but the ListRisks handler exposes no `sort` param and `residual_score`
// is an opaque JSON blob with no exposed age field — so this panel
// renders the returned rows in server order and prints an honest note
// that the residual/age ranking is a follow-up backend gap. It never
// fabricates a ranking (anti-criterion P0-1).

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
      description="Risks with treatment = mitigate · server order (residual/age ranking pending)"
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
      <p
        className="mt-3 border-t border-foreground/5 pt-3 text-xs text-muted-foreground"
        data-testid="top-risks-sort-gap"
      >
        Ranking by residual × age-in-treatment needs a server-side{" "}
        <span className="font-mono">sort=residual,age</span> capability on{" "}
        <span className="font-mono">GET /v1/risks</span> — not on main yet. Rows
        are shown in the API&apos;s server order until then.
      </p>
    </PanelCard>
  );
}
