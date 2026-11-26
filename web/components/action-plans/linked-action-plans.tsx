"use client";

// Slice 384 — read-only "Linked Action Plans" section, shared by the
// /risks/[id] (AC-25) and /controls/[id] (AC-26) detail pages. Fetches the
// linked plans for the given target via the BFF and renders a compact,
// read-only list. No link/unlink affordances here — linkage is managed from
// the action-plan create form + detail page; this section is a drill-in.
// #2106: with addHref, the section offers "Add action plan", which opens
// the create form pre-linked to the target.

import { useQuery } from "@tanstack/react-query";
import Link from "next/link";

import {
  planStatusLabel,
  statusPillClass,
} from "@/app/(authed)/action-plans/status";
import {
  fetchActionPlansForControl,
  fetchActionPlansForRisk,
  type ActionPlanRef,
  type ActionPlansRefListResponse,
} from "@/lib/api/action-plans";

type Props = {
  target: "risk" | "control";
  targetId: string;
  // The create form, pre-linked to the target; absent, no add link.
  addHref?: string;
};

export function LinkedActionPlans({ target, targetId, addHref }: Props) {
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
      <div className="flex items-center justify-between gap-2">
        <h2 className="text-sm font-medium">Linked Action Plans</h2>
        {addHref ? (
          <Link
            href={addHref}
            className="text-sm text-primary hover:underline"
            data-testid="linked-action-plans-add"
          >
            Add action plan
          </Link>
        ) : null}
      </div>
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
            <li
              key={p.id}
              className="flex items-center gap-2 text-sm"
              data-testid="linked-action-plan"
            >
              <Link
                href={`/action-plans/${encodeURIComponent(p.id)}`}
                className="text-primary hover:underline"
              >
                {p.title}
              </Link>
              <span
                className={
                  "inline-flex items-center rounded-md px-1.5 py-0.5 text-[11px] font-medium " +
                  statusPillClass(p.status)
                }
                data-testid="linked-action-plan-status"
              >
                {planStatusLabel(p.status, p.controls_failing)}
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
