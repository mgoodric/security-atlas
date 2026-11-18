"use client";

// Slice 040 — top risks panel — REBOUND by slice 157, and by #2135.
//
// #2135: a compact card of the 5 open risks with the highest residual,
// roll-ups left out, each with its residual band, owner and treatment.
// It binds to `GET /v1/risks?status=open&include_rollups=false
// &sort=-residual&limit=5` (TOP_RISKS_QUERY) via the dashboard BFF, so
// the order is the register's own residual ranking (5x5, then FAIR, then
// unscored; #2475/#2528) and every narrowing is the platform's. Slice
// 157 had pointed it at treatment=mitigate with the residual,age sort.

import Link from "next/link";

import {
  formatResidualScore,
  residualSeverity,
  severityBand,
  severityBandLabel,
  severityClasses,
} from "@/app/(authed)/risks/filters";
import { PanelCard, type PanelState } from "@/components/dashboard/panel-card";
import { treatmentBadge } from "@/components/dashboard/treatment-badge";
import { Badge } from "@/components/ui/badge";
import { Person } from "@/components/ui/person";
import type { DashboardRisk } from "@/lib/api/dashboard";

// ResidualBand shows the residual and its band. A FAIR residual is a
// dollar exposure with no 5x5 band; an unscored risk shows a dash.
function ResidualBand({ risk }: { risk: DashboardRisk }) {
  const formatted = formatResidualScore(risk.residual_score, risk.methodology);
  const n = residualSeverity(risk.residual_score);
  if (risk.methodology === "fair" || n == null) {
    return (
      <span className="font-mono text-xs" data-testid="top-risk-residual">
        {formatted}
      </span>
    );
  }
  const band = severityBand(n);
  return (
    <span className="inline-flex items-center gap-1.5">
      <span
        className={`inline-flex h-6 min-w-6 items-center justify-center rounded px-1 text-[11px] font-semibold ${severityClasses(
          band,
        )}`}
        data-testid="top-risk-residual"
      >
        {formatted}
      </span>
      <span
        className="text-xs text-muted-foreground"
        data-testid="top-risk-band"
      >
        {severityBandLabel(band)}
      </span>
    </span>
  );
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
      title="Top risks"
      description="The 5 open risks with the highest residual"
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
          No open risks.
        </p>
      ) : (
        <ul
          className="divide-y divide-foreground/5"
          data-testid="top-risks-list"
        >
          {risks.map((risk) => {
            const badge = treatmentBadge(risk);
            return (
              <li
                key={risk.id}
                data-testid="top-risk-row"
                data-id={risk.id}
                className="grid grid-cols-12 items-center gap-3 py-2.5 text-sm"
              >
                <Link
                  href={`/risks/${encodeURIComponent(risk.id)}`}
                  className="col-span-5 truncate font-medium hover:underline"
                  data-testid="top-risk-title"
                >
                  {risk.title}
                </Link>
                <div className="col-span-2">
                  <ResidualBand risk={risk} />
                </div>
                <div className="col-span-2">
                  <Badge
                    variant={badge.variant}
                    data-testid="top-risk-treatment"
                  >
                    {badge.label}
                  </Badge>
                </div>
                <div
                  className="col-span-3 truncate text-xs text-muted-foreground"
                  data-testid="top-risk-owner"
                >
                  {risk.owner_user_id ? (
                    <Person id={risk.owner_user_id} />
                  ) : risk.owner_unlinked ? (
                    `${risk.owner_unlinked} (unlinked)`
                  ) : (
                    "No owner"
                  )}
                </div>
              </li>
            );
          })}
        </ul>
      )}
    </PanelCard>
  );
}
