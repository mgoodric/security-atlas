// #2106 — "Add action plan" on a risk page opens /action-plans/new with
// ?risk_id=<id>. The form starts with that risk linked. The id is only a
// starting selection: a value that is not a risk UUID is dropped here, and
// the platform still checks the risk is in the session's tenant when the
// plan is linked to it.

import { parsePastedId } from "@/lib/entity-picker";

export type ActionPlanPrefill = {
  riskIds: string[];
  // Where to go after the plan is created: back to the risk it was added
  // from, or (no prefill) the new plan's own page.
  returnTo: string | null;
};

export function prefillFromSearch(
  search: Record<string, string | string[] | undefined>,
): ActionPlanPrefill {
  const raw = search.risk_id;
  const value = Array.isArray(raw) ? raw[0] : raw;
  if (!value) return { riskIds: [], returnTo: null };
  const parsed = parsePastedId(["risk"], value);
  if (!parsed.ok) return { riskIds: [], returnTo: null };
  return {
    riskIds: [parsed.id],
    returnTo: `/risks/${encodeURIComponent(parsed.id)}`,
  };
}
