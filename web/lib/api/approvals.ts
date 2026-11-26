// #2119 — what is waiting for the caller's approval.
//
// Row source: `Response` in `internal/api/approvals/handler.go`. The
// platform lists exactly what each type's approve handler would accept
// from the caller (requested exceptions, proposed risk acceptances,
// policies under review, framework scopes in review) and counts them per
// type, so the sidebar badge and the /approvals page read one number.
// A caller who cannot approve gets an empty list, not an error.
//
// The page and the badge share approvalsQuery, so a refetch on either
// updates both, and every approval flow invalidates it.
//
// #2457: the list is keyed by the session's tenant (the #2399 pattern),
// so a sign-in to another tenant in the same tab never shows the previous
// tenant's list or badge. With the tenant unknown the query does not run.

import type { QueryClient } from "@tanstack/react-query";

import { APIError } from "./base";
import { upstreamErrorMessage } from "./_shared";

export type ApprovalType =
  | "exception"
  | "risk_acceptance"
  | "policy"
  | "framework_scope";

/** The page's group order, with each group's heading. */
export const APPROVAL_GROUPS: readonly { type: ApprovalType; label: string }[] =
  [
    { type: "exception", label: "Exceptions" },
    { type: "risk_acceptance", label: "Risk acceptances" },
    { type: "policy", label: "Policies" },
    { type: "framework_scope", label: "Framework scopes" },
  ];

export type ApprovalItem = {
  type: ApprovalType;
  id: string;
  title: string;
  requested_by: string;
  requested_at?: string;
  /** The caller's own request, listed because self-approval is allowed. */
  self_approval: boolean;
  /** Set on a framework scope: its page is keyed by framework version. */
  framework_version_id?: string;
};

export type ApprovalsResponse = {
  items: ApprovalItem[];
  counts: Record<ApprovalType, number>;
  total: number;
  /** True when a type has more than the platform lists (100 per type). */
  truncated: boolean;
};

// The prefix every tenant's list sits under.
export const APPROVALS_QUERY_KEY = ["approvals", "pending"] as const;

/**
 * The approvals query for one tenant. `enabled` is false while the tenant
 * is unknown: no entry is read or fetched without a tenant in its key.
 */
export function approvalsQuery(tenantId: string | undefined) {
  return {
    queryKey: [...APPROVALS_QUERY_KEY, tenantId ?? ""],
    enabled: tenantId !== undefined && tenantId !== "",
  };
}

/**
 * invalidateApprovals refreshes the page and the badge after a transition.
 * It matches every tenant's entry by prefix.
 */
export function invalidateApprovals(qc: QueryClient): Promise<void> {
  return qc.invalidateQueries({ queryKey: APPROVALS_QUERY_KEY });
}

/**
 * approvalHref is where a row's approval action lives: the exception and
 * the policy approve inline on their pages, a risk acceptance on its own
 * approve form, a framework scope on its framework version's page.
 */
export function approvalHref(item: ApprovalItem): string {
  const id = encodeURIComponent(item.id);
  switch (item.type) {
    case "exception":
      return `/exceptions/${id}`;
    case "risk_acceptance":
      return `/risks/${id}/acceptance/approve`;
    case "policy":
      return `/policies/${id}`;
    case "framework_scope":
      return item.framework_version_id
        ? `/framework-scopes/${encodeURIComponent(item.framework_version_id)}`
        : "/framework-scopes";
  }
}

/** groupApprovals splits items into the page's groups, dropping empty ones. */
export function groupApprovals(
  items: readonly ApprovalItem[],
): { type: ApprovalType; label: string; items: ApprovalItem[] }[] {
  return APPROVAL_GROUPS.map((g) => ({
    ...g,
    items: items.filter((i) => i.type === g.type),
  })).filter((g) => g.items.length > 0);
}

export async function fetchApprovals(): Promise<ApprovalsResponse> {
  const res = await fetch("/api/approvals");
  if (!res.ok) {
    const msg = await upstreamErrorMessage(res);
    throw new APIError(res.status, msg);
  }
  return (await res.json()) as ApprovalsResponse;
}
