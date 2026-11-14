// #1831 — treatment badge for the dashboard's top-risks panel.
//
// Pure logic, split out of top-risks-panel.tsx so vitest (node env, no
// JSX) can cover it. An acceptance badge reflects its status: a proposed
// acceptance renders as `warning` with the label `accept (proposed)`, so
// a pending sign-off never reads the same as an approved one (#1746).

import type { DashboardRisk } from "@/lib/api/dashboard";
import { treatmentLabel } from "@/lib/api/risks";

export type TreatmentBadgeVariant =
  | "destructive"
  | "secondary"
  | "warning"
  | "outline";

type TreatmentFields = Pick<DashboardRisk, "treatment" | "acceptance_status">;

export function treatmentBadge(r: TreatmentFields): {
  label: string;
  variant: TreatmentBadgeVariant;
} {
  return { label: treatmentLabel(r), variant: treatmentVariant(r) };
}

function treatmentVariant(r: TreatmentFields): TreatmentBadgeVariant {
  if (r.treatment === "mitigate") return "destructive";
  if (r.treatment === "accept") {
    return r.acceptance_status === "approved" ? "secondary" : "warning";
  }
  return "outline";
}
