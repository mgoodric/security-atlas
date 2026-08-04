// OE-664 — shared presentation helpers for the /personnel-security
// surfaces (list, detail). Pure functions so they are unit-testable
// without rendering (the slice-384 action-plans status.ts precedent).
//
// The load-bearing logic here is overdue-offboarding prominence: an
// open offboarding checklist past its due date is the highest-risk row
// on the page (a departed person may still hold access), so it sorts
// first and carries the strongest badge.

import type { Checklist } from "@/lib/api/personnel-security";

/** True when the checklist is open and its due date has passed. */
export function isOverdue(
  c: Pick<Checklist, "status" | "due_at">,
  now: Date,
): boolean {
  if (c.status !== "open") return false;
  const due = new Date(c.due_at);
  if (Number.isNaN(due.getTime())) return false;
  return due.getTime() < now.getTime();
}

/**
 * Sort rank for the list page. Lower ranks render first:
 *   0 — overdue offboarding (highest risk: departed person, open items)
 *   1 — overdue onboarding
 *   2 — open, not yet due
 *   3 — completed
 */
export function checklistRank(
  c: Pick<Checklist, "kind" | "status" | "due_at">,
  now: Date,
): number {
  if (c.status === "completed") return 3;
  if (!isOverdue(c, now)) return 2;
  return c.kind === "offboarding" ? 0 : 1;
}

/**
 * Sort checklists for the list page: rank ascending (overdue
 * offboarding at the very top), then due_at ascending within a rank
 * (most-overdue / soonest-due first). Pure — returns a new array.
 */
export function sortChecklists(rows: Checklist[], now: Date): Checklist[] {
  return [...rows].sort((a, b) => {
    const rank = checklistRank(a, now) - checklistRank(b, now);
    if (rank !== 0) return rank;
    return new Date(a.due_at).getTime() - new Date(b.due_at).getTime();
  });
}

/** Badge label for the status pill; overdue rows say so explicitly. */
export function statusBadgeLabel(
  c: Pick<Checklist, "kind" | "status" | "due_at">,
  now: Date,
): string {
  if (c.status === "completed") return "Completed";
  if (!isOverdue(c, now)) return "Open";
  return c.kind === "offboarding" ? "Overdue offboarding" : "Overdue";
}

/** Tailwind class for the status pill. Overdue offboarding is rose. */
export function statusBadgeClass(
  c: Pick<Checklist, "kind" | "status" | "due_at">,
  now: Date,
): string {
  if (c.status === "completed") {
    return "bg-emerald-50 text-emerald-700 dark:bg-emerald-950 dark:text-emerald-300";
  }
  if (!isOverdue(c, now)) {
    return "bg-slate-100 text-slate-700 dark:bg-slate-800 dark:text-slate-300";
  }
  return c.kind === "offboarding"
    ? "bg-rose-100 text-rose-700 dark:bg-rose-950 dark:text-rose-300"
    : "bg-amber-50 text-amber-700 dark:bg-amber-950 dark:text-amber-300";
}

/** Human label for a workflow kind; falls back to the raw value. */
export function kindLabel(kind: string): string {
  switch (kind) {
    case "onboarding":
      return "Onboarding";
    case "offboarding":
      return "Offboarding";
    default:
      return kind;
  }
}

/** Format an ISO date/timestamp as YYYY-MM-DD; "—" for empty/malformed. */
export function dateLabel(iso?: string | null): string {
  if (!iso) return "—";
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  return d.toISOString().slice(0, 10);
}

/** "3 of 12 items complete" progress line for a checklist's items. */
export function itemProgress(items: { completed_at?: string }[]): string {
  const done = items.filter((i) => Boolean(i.completed_at)).length;
  return `${done} of ${items.length} item${
    items.length === 1 ? "" : "s"
  } complete`;
}
