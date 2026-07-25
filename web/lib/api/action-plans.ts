// Slice 384 — ActionPlan client. Mirrors the slice-177 exceptions client
// shape (lib/api/exceptions.ts): browser-side fetchers that hit the BFF at
// `/api/action-plans[...]`, which forwards the bearer cookie to upstream
// `/v1/action-plans[...]`. The bearer never reaches the browser.
//
// Row source: `planWire` in `internal/api/actionplans/handlers.go`. Tenant
// isolation is enforced by RLS at the DB layer (invariant 6); the UI never
// passes tenant_id.

import { APIError } from "./base";

/**
 * ActionPlan lifecycle status. Mirrors the constants in
 * `internal/actionplan/statemachine.go`:
 *   draft → in_progress → blocked → completed → verified
 */
export type ActionPlanStatus =
  | "draft"
  | "in_progress"
  | "blocked"
  | "completed"
  | "verified";

export const ACTION_PLAN_STATUSES: ActionPlanStatus[] = [
  "draft",
  "in_progress",
  "blocked",
  "completed",
  "verified",
];

/** Human labels for the lifecycle statuses. */
export const ACTION_PLAN_STATUS_LABELS: Record<ActionPlanStatus, string> = {
  draft: "Draft",
  in_progress: "In progress",
  blocked: "Blocked",
  completed: "Completed",
  verified: "Verified",
};

/** ActionPlan is the canonical wire shape. Mirrors `planWire`. */
export type ActionPlan = {
  id: string;
  title: string;
  description: string;
  triggering_event: string;
  owner_id: string;
  due_date?: string;
  status: ActionPlanStatus;
  audit_period_id?: string;
  created_at: string;
  updated_at: string;
};

/** One M2M link row. */
export type ActionPlanLink = {
  target_id: string;
  linked_at: string;
  linked_by: string;
};

export type ActionPlanLinkage = {
  risks: ActionPlanLink[];
  controls: ActionPlanLink[];
};

export type ActionPlansListResponse = {
  action_plans: ActionPlan[];
  count: number;
  next_cursor?: string;
};

export type ActionPlanDetailResponse = {
  action_plan: ActionPlan;
  linkage: ActionPlanLinkage;
};

/** Compact reference row used by the "Linked Action Plans" sections. */
export type ActionPlanRef = {
  id: string;
  title: string;
  status: ActionPlanStatus;
  due_date?: string;
};

export type ActionPlansRefListResponse = {
  action_plans: ActionPlanRef[];
  count: number;
};

export type ActionPlansListFilters = {
  status?: ActionPlanStatus | "";
  limit?: number;
  cursor?: string;
};

async function parse<T>(res: Response): Promise<T> {
  if (!res.ok) {
    let msg = `${res.status} ${res.statusText}`;
    try {
      const j = (await res.json()) as { error?: string };
      if (j.error) msg = j.error;
    } catch {
      /* body not JSON — keep the status line */
    }
    throw new APIError(res.status, msg);
  }
  return (await res.json()) as T;
}

/** List the tenant's action plans (RLS-scoped). */
export async function fetchActionPlansList(
  filters: ActionPlansListFilters = {},
): Promise<ActionPlansListResponse> {
  const qs = new URLSearchParams();
  if (filters.status) qs.set("status", filters.status);
  if (filters.limit) qs.set("limit", String(filters.limit));
  if (filters.cursor) qs.set("cursor", filters.cursor);
  const url = qs.toString()
    ? `/api/action-plans?${qs.toString()}`
    : `/api/action-plans`;
  return parse<ActionPlansListResponse>(await fetch(url));
}

/** Fetch one action plan plus its linkage. */
export async function fetchActionPlan(
  id: string,
): Promise<ActionPlanDetailResponse> {
  return parse<ActionPlanDetailResponse>(
    await fetch(`/api/action-plans/${encodeURIComponent(id)}`),
  );
}

/** The shape POSTed to create an action plan. */
export type CreateActionPlanInput = {
  title: string;
  description?: string;
  triggering_event?: string;
  owner_id: string;
  due_date?: string | null;
  risk_ids?: string[];
  control_ids?: string[];
};

/**
 * Create an action plan, then link the selected risks + controls. The
 * create + link calls are separate upstream endpoints; this helper
 * sequences them and returns the created plan id. A link failure after
 * create surfaces as an APIError (the plan exists; the caller can retry
 * linkage from the detail page).
 */
export async function createActionPlan(
  input: CreateActionPlanInput,
): Promise<{ id: string }> {
  const created = await parse<{ action_plan: ActionPlan }>(
    await fetch(`/api/action-plans`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        title: input.title,
        description: input.description ?? "",
        triggering_event: input.triggering_event ?? "",
        owner_id: input.owner_id,
        due_date: input.due_date ?? null,
      }),
    }),
  );
  const id = created.action_plan.id;
  for (const riskId of input.risk_ids ?? []) {
    await parse(
      await fetch(
        `/api/action-plans/${encodeURIComponent(id)}/risks/${encodeURIComponent(
          riskId,
        )}`,
        { method: "POST" },
      ),
    );
  }
  for (const controlId of input.control_ids ?? []) {
    await parse(
      await fetch(
        `/api/action-plans/${encodeURIComponent(
          id,
        )}/controls/${encodeURIComponent(controlId)}`,
        { method: "POST" },
      ),
    );
  }
  return { id };
}

/** Fetch the action plans linked to a risk (AC-25 read-only section). */
export async function fetchActionPlansForRisk(
  riskId: string,
): Promise<ActionPlansRefListResponse> {
  return parse<ActionPlansRefListResponse>(
    await fetch(`/api/risks/${encodeURIComponent(riskId)}/action-plans`),
  );
}

/** Fetch the action plans linked to a control (AC-26 read-only section). */
export async function fetchActionPlansForControl(
  controlId: string,
): Promise<ActionPlansRefListResponse> {
  return parse<ActionPlansRefListResponse>(
    await fetch(`/api/controls/${encodeURIComponent(controlId)}/action-plans`),
  );
}
