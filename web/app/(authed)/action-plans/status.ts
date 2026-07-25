// Slice 384 — shared status presentation helpers for the /action-plans
// surfaces (list, detail, linked-plans sections). Pure functions so they are
// unit-testable without rendering (slice-353 Q-2 pure-Go-first spirit,
// applied to the TS side).

import {
  ACTION_PLAN_STATUS_LABELS,
  type ActionPlanStatus,
} from "@/lib/api/action-plans";
import { actionPlanStatusVariant } from "@/lib/status-variants";

/** Semantic badge variant for the status pill, by lifecycle state. */
export function statusPillVariant(status: string) {
  return actionPlanStatusVariant(status);
}

/** Human label for a status; falls back to the raw value. */
export function statusLabel(status: string): string {
  return ACTION_PLAN_STATUS_LABELS[status as ActionPlanStatus] ?? status;
}

/** Format an ISO date/timestamp as YYYY-MM-DD; "—" for empty/malformed. */
export function dateLabel(iso?: string | null): string {
  if (!iso) return "—";
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  return d.toISOString().slice(0, 10);
}
