// #2119 — the approvals page's pure helpers: where each row links, and
// how rows group.

import { describe, expect, test } from "vitest";

import { approvalHref, groupApprovals, type ApprovalItem } from "./approvals";

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
