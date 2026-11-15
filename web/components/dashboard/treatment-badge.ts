// #1831 — treatment badge for the dashboard's top-risks panel.
//
// Pure logic, split out of top-risks-panel.tsx so vitest (node env, no
// JSX) can cover it. An acceptance badge reflects its status: a proposed
// acceptance renders as `warning` with the label `accept (proposed)`, so
// a pending sign-off never reads the same as an approved one (#1746).

import type { DashboardRisk } from "@/lib/api/dashboard";
import { treatmentLabel } from "@/lib/api/risks";

// #2090: a treatment is a category, not an alarm, so mitigate takes the
// `progress` tone (work is under way), never `destructive`. Red is kept
// for irreversible actions.
export type TreatmentBadgeVariant =
  | "progress" // mitigate
  | "secondary" // accept (approved)
  | "warning" // accept (proposed or no status)
  | "outline"; // every other treatment

type TreatmentFields = Pick<DashboardRisk, "treatment" | "acceptance_status">;

export function treatmentBadge(r: TreatmentFields): {
  label: string;
  variant: TreatmentBadgeVariant;
} {
  return { label: treatmentLabel(r), variant: treatmentVariant(r) };
}

function treatmentVariant(r: TreatmentFields): TreatmentBadgeVariant {
  if (r.treatment === "mitigate") return "progress";
  if (r.treatment === "accept") {
    return r.acceptance_status === "approved" ? "secondary" : "warning";
  }
  return "outline";
}
