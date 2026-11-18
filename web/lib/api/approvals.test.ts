// #2119 — the approvals page's pure helpers: where each row links, and
// how rows group.

import { QueryClient, QueryObserver } from "@tanstack/react-query";
import { describe, expect, test, vi } from "vitest";

import {
  APPROVALS_QUERY_KEY,
  approvalHref,
  approvalsQuery,
  groupApprovals,
  invalidateApprovals,
  type ApprovalItem,
} from "./approvals";

function item(over: Partial<ApprovalItem>): ApprovalItem {
  return {
    type: "exception",
    id: "00000000-0000-0000-0000-000000002119",
    title: "t",
    requested_by: "u",
    self_approval: false,
    ...over,
  };
}

describe("approvalHref", () => {
  test("links each type to its approval action", () => {
    const id = "00000000-0000-0000-0000-000000002119";
    expect(approvalHref(item({ type: "exception", id }))).toBe(
      `/exceptions/${id}`,
    );
    expect(approvalHref(item({ type: "risk_acceptance", id }))).toBe(
      `/risks/${id}/acceptance/approve`,
    );
    expect(approvalHref(item({ type: "policy", id }))).toBe(`/policies/${id}`);
    expect(
      approvalHref(
        item({ type: "framework_scope", id, framework_version_id: "fv-1" }),
      ),
    ).toBe("/framework-scopes/fv-1");
  });

  test("a scope with no framework version falls back to the scopes list", () => {
    expect(approvalHref(item({ type: "framework_scope" }))).toBe(
      "/framework-scopes",
    );
  });

  test("encodes ids", () => {
    expect(approvalHref(item({ type: "policy", id: "a/b" }))).toBe(
      "/policies/a%2Fb",
    );
  });
});

describe("groupApprovals", () => {
  test("groups in page order and drops empty groups", () => {
    const groups = groupApprovals([
      item({ type: "policy", id: "p" }),
      item({ type: "exception", id: "e1" }),
      item({ type: "exception", id: "e2" }),
    ]);
    expect(groups.map((g) => [g.label, g.items.map((i) => i.id)])).toEqual([
      ["Exceptions", ["e1", "e2"]],
      ["Policies", ["p"]],
    ]);
  });

  test("nothing pending, no groups", () => {
    expect(groupApprovals([])).toEqual([]);
  });
});

// #2457 — a sign-in to another tenant in the same tab drops nothing, so
// the key itself keeps tenant A's list and badge away from tenant B.
describe("approvals query keyed by tenant", () => {
  const TENANT_A = "aaaaaaaa-0000-4000-8000-000000000001";
  const TENANT_B = "bbbbbbbb-0000-4000-8000-000000000002";

  function cache(qc: QueryClient, tenantId: string, total: number) {
    return qc.fetchQuery({
      queryKey: approvalsQuery(tenantId).queryKey,
      queryFn: async () => total,
    });
  }

  // Every result the badge would see for `tenantId`, from mount until the
  // fetch settles.
  async function observe(
    qc: QueryClient,
    tenantId: string | undefined,
    fetcher: () => Promise<number>,
  ) {
    const seen: (number | undefined)[] = [];
    const observer = new QueryObserver(qc, {
      ...approvalsQuery(tenantId),
      queryFn: fetcher,
      retry: false,
    });
    seen.push(observer.getCurrentResult().data);
    const unsubscribe = observer.subscribe((r) => seen.push(r.data));
    if (approvalsQuery(tenantId).enabled) {
      await vi.waitFor(() =>
        expect(observer.getCurrentResult().isSuccess).toBe(true),
      );
    }
    unsubscribe();
    return seen;
  }

  test("puts the tenant in the key, under the shared prefix", () => {
    expect(approvalsQuery(TENANT_A).queryKey).toEqual([
      ...APPROVALS_QUERY_KEY,
      TENANT_A,
    ]);
    expect(approvalsQuery(TENANT_A).queryKey).not.toEqual(
      approvalsQuery(TENANT_B).queryKey,
    );
  });

  test("never serves tenant A's list to a sign-in as tenant B", async () => {
    const qc = new QueryClient();
    await cache(qc, TENANT_A, 7);

    const fetchB = vi.fn(async () => 2);
    const seen = await observe(qc, TENANT_B, fetchB);

    expect(seen).not.toContain(7);
    expect(seen.at(-1)).toBe(2);
    expect(fetchB).toHaveBeenCalledTimes(1);
  });

  test("with the tenant unknown, reads nothing cached and fetches nothing", async () => {
    const qc = new QueryClient();
    await cache(qc, TENANT_A, 7);

    const fetcher = vi.fn(async () => 1);
    const seen = await observe(qc, undefined, fetcher);

    expect(seen.every((d) => d === undefined)).toBe(true);
    expect(fetcher).not.toHaveBeenCalled();
    expect(approvalsQuery("").enabled).toBe(false);
  });

  test("invalidateApprovals reaches every tenant's entry", async () => {
    const qc = new QueryClient();
    await cache(qc, TENANT_A, 1);
    await cache(qc, TENANT_B, 2);
    await invalidateApprovals(qc);
    for (const t of [TENANT_A, TENANT_B]) {
      expect(qc.getQueryState(approvalsQuery(t).queryKey)?.isInvalidated).toBe(
        true,
      );
    }
  });
});
