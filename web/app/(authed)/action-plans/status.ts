// Slice 384 — shared status presentation helpers for the /action-plans
// surfaces (list, detail, linked-plans sections). Pure functions so they are
// unit-testable without rendering (slice-353 Q-2 pure-Go-first spirit,
// applied to the TS side).

import {
  ACTION_PLAN_STATUS_LABELS,
  type ActionPlanStatus,
} from "@/lib/api/action-plans";

/** Tailwind class for the status pill, by lifecycle state. */
export function statusPillClass(status: string): string {
  switch (status) {
    case "verified":
      return "bg-emerald-50 text-emerald-700 dark:bg-emerald-950 dark:text-emerald-300";
    case "completed":
      return "bg-sky-50 text-sky-700 dark:bg-sky-950 dark:text-sky-300";
    case "in_progress":
      return "bg-amber-50 text-amber-700 dark:bg-amber-950 dark:text-amber-300";
    case "blocked":
      return "bg-rose-50 text-rose-700 dark:bg-rose-950 dark:text-rose-300";
    case "draft":
      return "bg-slate-100 text-slate-700 dark:bg-slate-800 dark:text-slate-300";
    default:
      return "bg-muted text-muted-foreground";
  }
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
