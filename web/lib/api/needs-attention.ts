// #2135 — the needs-attention list (GET /v1/needs-attention, #2134).
//
// The platform ranks, filters by view (`for=me|all`), narrows by type and
// pages; the dashboard asks for the top 10 and /needs-attention for more.
// Nothing here re-filters: authz and module visibility are the server's.

import { apiFetch, bffControlFetch } from "./_shared";

export type NeedsAttentionView = "me" | "all";

export type NeedsAttentionBand =
  | "failing"
  | "approval"
  | "overdue"
  | "due_soon"
  | "ownership_gap";

// The item types a `?type=` filter may name (needsattention.ItemTypes).
export const NEEDS_ATTENTION_TYPES = [
  "control",
  "policy",
  "risk",
  "vendor",
  "action_plan",
  "exception",
  "risk_acceptance",
  "framework_scope",
] as const;

export type NeedsAttentionType = (typeof NEEDS_ATTENTION_TYPES)[number];

export type NeedsAttentionItem = {
  band: NeedsAttentionBand;
  type: string;
  id: string;
  title: string;
  link: string;
  action: string;
  due_at?: string;
  since?: string;
  owner?: { user_id?: string; name?: string; role?: string };
};

export type NeedsAttentionResponse = {
  for: NeedsAttentionView;
  items: NeedsAttentionItem[];
  total: number;
  counts: Record<NeedsAttentionBand, number>;
  type_counts: Record<string, number>;
  limit: number;
  offset: number;
};

export type NeedsAttentionParams = {
  // Omitted: the server picks (me, or all for a user who owns nothing
  // and may see everyone). The response's `for` says which it served.
  view?: NeedsAttentionView;
  types?: readonly string[];
  limit?: number;
  offset?: number;
};

export const DASHBOARD_NEEDS_ATTENTION_LIMIT = 10;
export const NEEDS_ATTENTION_PAGE_SIZE = 25;

const TYPE_LABELS: Record<string, string> = {
  control: "Control",
  policy: "Policy",
  risk: "Risk",
  vendor: "Vendor",
  action_plan: "Action plan",
  exception: "Exception",
  risk_acceptance: "Risk acceptance",
  framework_scope: "Framework scope",
};

const BAND_LABELS: Record<NeedsAttentionBand, string> = {
  failing: "Failing",
  approval: "Approval",
  overdue: "Overdue",
  due_soon: "Due soon",
  ownership_gap: "No owner",
};

export function needsAttentionTypeLabel(type: string): string {
  return TYPE_LABELS[type] ?? type.replace(/_/g, " ");
}

export function needsAttentionBandLabel(band: string): string {
  return BAND_LABELS[band as NeedsAttentionBand] ?? band.replace(/_/g, " ");
}

// needsAttentionQuery builds the query string both the BFF and the
// platform read. Unknown keys are never added.
export function needsAttentionQuery(p: NeedsAttentionParams): string {
  const q = new URLSearchParams();
  if (p.view) q.set("for", p.view);
  if (p.types && p.types.length > 0) q.set("type", p.types.join(","));
  if (p.limit != null) q.set("limit", String(p.limit));
  if (p.offset) q.set("offset", String(p.offset));
  const s = q.toString();
  return s ? `?${s}` : "";
}

// ----- server-side fn (called by the BFF route) -----

export async function getNeedsAttention(
  bearer: string,
  search: string,
): Promise<NeedsAttentionResponse> {
  const res = await apiFetch(`/v1/needs-attention${search}`, bearer);
  return (await res.json()) as NeedsAttentionResponse;
}

// ----- browser-side fn (hits the BFF) -----

export function fetchNeedsAttention(
  p: NeedsAttentionParams,
): Promise<NeedsAttentionResponse> {
  return bffControlFetch<NeedsAttentionResponse>(
    `/api/needs-attention${needsAttentionQuery(p)}`,
  );
}
