import { describe, expect, test } from "vitest";

import type { EdgeRemapProposal, RemapProposal } from "@/lib/api/remap";
import {
  edgesFor,
  isReleaseLabel,
  isSCFCode,
  matchesAnchorQuery,
  needsExplicitAnchor,
  pendingByClass,
} from "./remap-review";

function proposal(over: Partial<RemapProposal>): RemapProposal {
  return {
    id: "p",
    control_id: "c",
    bundle_id: "b",
    title: "t",
    legacy_scf_id: "IAC-01",
    legacy_release: "A",
    target_release: "B",
    class: "unchanged",
    status: "open",
    successor_scf_ids: ["IAC-01"],
    created_at: "2026-11-15T00:00:00Z",
    ...over,
  };
}

describe("pendingByClass", () => {
  test("groups open and rejected proposals; settled ones drop out", () => {
    const g = pendingByClass([
      proposal({ id: "1", class: "unchanged" }),
      proposal({ id: "2", class: "unchanged", status: "approved" }),
      proposal({ id: "3", class: "split", status: "rejected" }),
      proposal({ id: "4", class: "reused_id", status: "rebound" }),
      proposal({ id: "5", class: "no_successor", status: "kept_legacy" }),
    ]);
    expect(g.unchanged.map((p) => p.id)).toEqual(["1"]);
    expect(g.split.map((p) => p.id)).toEqual(["3"]);
    expect(g.reused_id).toEqual([]);
    expect(g.no_successor).toEqual([]);
    expect(g.renamed).toEqual([]);
  });
});

describe("needsExplicitAnchor", () => {
  test("split, no_successor and reused_id always name the anchor", () => {
    expect(needsExplicitAnchor("split")).toBe(true);
    expect(needsExplicitAnchor("no_successor")).toBe(true);
    expect(needsExplicitAnchor("reused_id")).toBe(true);
    expect(needsExplicitAnchor("unchanged")).toBe(false);
    expect(needsExplicitAnchor("renamed")).toBe(false);
  });
});

describe("edgesFor", () => {
  test("matches on legacy code and release", () => {
    const e = (id: string, code: string, rel: string) =>
      ({
        id,
        legacy_scf_id: code,
        legacy_release: rel,
      }) as EdgeRemapProposal;
    const edges = [
      e("1", "IAC-01", "A"),
      e("2", "IAC-01", "Z"),
      e("3", "X", "A"),
    ];
    expect(edgesFor(proposal({}), edges).map((x) => x.id)).toEqual(["1"]);
  });
});

describe("validators", () => {
  test.each([
    ["IAC-21", true],
    ["GOV-01.1", true],
    ["..", false],
    ["a/b", false],
    ["", false],
  ])("isSCFCode(%j) = %s", (v, ok) => {
    expect(isSCFCode(v)).toBe(ok);
  });

  test.each([
    ["2025.2", true],
    ["test-2026.3", true],
    ["..", false],
    ["a/b", false],
    ["", false],
  ])("isReleaseLabel(%j) = %s", (v, ok) => {
    expect(isReleaseLabel(v)).toBe(ok);
  });
});

describe("matchesAnchorQuery", () => {
  const a = { scf_id: "IAC-10", name: "Authenticator Management" };
  test("matches code or title, case-insensitive; blank matches nothing", () => {
    expect(matchesAnchorQuery(a, "iac-1")).toBe(true);
    expect(matchesAnchorQuery(a, "authenticator")).toBe(true);
    expect(matchesAnchorQuery(a, "network")).toBe(false);
    expect(matchesAnchorQuery(a, "  ")).toBe(false);
  });
});
