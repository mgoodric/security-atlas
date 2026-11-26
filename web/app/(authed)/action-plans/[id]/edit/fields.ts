// #2108 — the edit form's pure helpers: the stored due date as a form
// value, and the PATCH body of only the changed fields.

import type { ActionPlan, UpdateActionPlanInput } from "@/lib/api/action-plans";

/** YYYY-MM-DD of a stored due date, "" for none. */
export function dayOf(iso?: string): string {
  return iso ? iso.slice(0, 10) : "";
}

/** The changed fields, as the PATCH body. */
export function changedFields(
  plan: ActionPlan,
  v: { title: string; description: string; ownerId: string; dueDate: string },
): UpdateActionPlanInput {
  const body: UpdateActionPlanInput = {};
  if (v.title.trim() !== plan.title) body.title = v.title.trim();
  if (v.description.trim() !== (plan.description ?? ""))
    body.description = v.description.trim();
  if (v.ownerId.trim() !== plan.owner_id) body.owner_id = v.ownerId.trim();
  if (v.dueDate !== dayOf(plan.due_date)) {
    if (v.dueDate) body.due_date = `${v.dueDate}T00:00:00Z`;
    else body.clear_due_date = true;
  }
  return body;
}
