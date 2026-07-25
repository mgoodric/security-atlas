"use client";

// Slice 384 — read-only "Linked Action Plans" section, shared by the
// /risks/[id] (AC-25) and /controls/[id] (AC-26) detail pages. Fetches the
// linked plans for the given target via the BFF and renders a compact,
// read-only list. No link/unlink affordances here — linkage is managed from
// the action-plan create form + detail page; this section is a drill-in.

import { useQuery } from "@tanstack/react-query";
import Link from "next/link";

import {
  fetchActionPlansForControl,
  fetchActionPlansForRisk,
  type ActionPlanRef,
  type ActionPlansRefListResponse,
} from "@/lib/api/action-plans";

type Props = {
  target: "risk" | "control";
  targetId: string;
};

function pillClass(status: string): string {
  switch (status) {
    case "verified":
      return "bg-emerald-50 text-emerald-700 dark:bg-emerald-950 dark:text-emerald-300";
    case "completed":
      return "bg-sky-50 text-sky-700 dark:bg-sky-950 dark:text-sky-300";
    case "in_progress":
      return "bg-amber-50 text-amber-700 dark:bg-amber-950 dark:text-amber-300";
    case "blocked":
      return "bg-rose-50 text-rose-700 dark:bg-rose-950 dark:text-rose-300";
    default:
      return "bg-slate-100 text-slate-700 dark:bg-slate-800 dark:text-slate-300";
  }
}

export function LinkedActionPlans({ target, targetId }: Props) {
  const q = useQuery<ActionPlansRefListResponse>({
    queryKey: ["action-plans", "for", target, targetId],
    queryFn: () =>
      target === "risk"
        ? fetchActionPlansForRisk(targetId)
        : fetchActionPlansForControl(targetId),
    enabled: Boolean(targetId),
  });

  const rows: ActionPlanRef[] = q.data?.action_plans ?? [];

  return (
    <section className="space-y-2" data-testid="linked-action-plans">
      <h2 className="text-sm font-medium">Linked Action Plans</h2>
      {q.isLoading ? (
        <p className="text-sm text-muted-foreground">Loading…</p>
      ) : q.isError ? (
        <p
          className="text-sm text-rose-600"
          data-testid="linked-action-plans-error"
        >
          Could not load action plans.
        </p>
      ) : rows.length === 0 ? (
        <p
          className="text-sm text-muted-foreground"
          data-testid="linked-action-plans-empty"
        >
          No action plans linked to this {target}.
        </p>
      ) : (
        <ul className="space-y-1" data-testid="linked-action-plans-list">
          {rows.map((p) => (
            <li key={p.id} className="flex items-center gap-2 text-sm">
              <Link
                href={`/action-plans/${encodeURIComponent(p.id)}`}
                className="text-primary hover:underline"
              >
                {p.title}
              </Link>
              <span
                className={
                  "inline-flex items-center rounded-md px-1.5 py-0.5 text-[11px] font-medium " +
                  pillClass(p.status)
                }
              >
                {p.status}
              </span>
              {p.due_date ? (
                <span className="text-xs text-muted-foreground">
                  due {p.due_date}
                </span>
              ) : null}
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}
