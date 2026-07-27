// Slice 536b — vitest coverage for the crosswalk review/edit client vocabulary.
//
// The load-bearing item here is `nextTiers`: it MIRRORS
// internal/crosswalktier.legalTransitions so the UI can hide buttons for moves
// the server would refuse. A mirror that drifts is worse than no mirror — it
// would offer a reviewer an approval the platform then 422s, or hide a legal
// one — so every edge of the machine is pinned below, including the two that
// carry the slice's boundaries: `verified` has exactly one outgoing move (the
// D-536b-1 demotion, never a re-approval and never a jump to rejected), and
// `draft` cannot reach `verified` without passing through review.

import { describe, expect, test, vi, beforeEach } from "vitest";

import {
  RELATIONSHIP_TYPES,
  conflictsForEdge,
  editCrosswalkEdge,
  highestSeverity,
  nextTiers,
  reviewQueueQuery,
  sortConflicts,
  transitionCrosswalkTier,
  type CrosswalkConflict,
  type MappingTier,
} from "./crosswalk-review";
import { APIError } from "./base";

// conflict builds a slice-536a finding fixture. Only the fields the helpers
// read are meaningful; the rest mirror the wire shape.
function conflict(
  over: Partial<CrosswalkConflict> & Pick<CrosswalkConflict, "severity">,
): CrosswalkConflict {
  return {
    kind: "competing_anchors",
    reason: "duplicate_equal_claim",
    edge_ids: [],
    anchor_scf_ids: [],
    detail: "",
    ...over,
  };
}

describe("nextTiers — mirrors the slice-483 state machine", () => {
  test.each([
    ["draft", ["under_review", "rejected"]],
    ["under_review", ["verified", "rejected"]],
    ["verified", ["under_review"]],
    ["rejected", []],
  ] as [MappingTier, MappingTier[]][])("from %s", (from, expected) => {
    expect(nextTiers(from)).toEqual(expected);
  });

  test("draft can never reach verified directly", () => {
    // The load-bearing P0-483 guard: a community draft must pass through
    // under_review. If this ever passes, the UI is offering a one-click
    // approval path the state machine does not have.
    expect(nextTiers("draft")).not.toContain("verified");
  });

  test("verified offers only the demotion — no self-approval, no direct reject", () => {
    // D-536b-1 added exactly one outgoing edge from verified. `verified ->
    // verified` would be a re-approval with no review; `verified -> rejected`
    // would let a mapping die without re-entering review.
    expect(nextTiers("verified")).toEqual(["under_review"]);
  });

  test("rejected is terminal", () => {
    expect(nextTiers("rejected")).toEqual([]);
  });

  test("no tier offers a transition into rejected-as-a-shortcut from verified", () => {
    const reachableFromVerified = nextTiers("verified");
    expect(reachableFromVerified).not.toContain("rejected");
    expect(reachableFromVerified).not.toContain("draft");
  });
});

describe("RELATIONSHIP_TYPES", () => {
  test("is exactly the five NIST IR 8477 STRM types", () => {
    // Mirrors internal/crosswalkedit.RelationshipType.IsValid. A sixth value
    // here would render an option the platform rejects 400.
    expect([...RELATIONSHIP_TYPES]).toEqual([
      "equal",
      "subset_of",
      "superset_of",
      "intersects_with",
      "no_relationship",
    ]);
  });
});

describe("reviewQueueQuery", () => {
  test("emits only the framework version when nothing else is set", () => {
    expect(reviewQueueQuery({ frameworkVersionId: "fv-1" })).toBe(
      "framework_version_id=fv-1",
    );
  });

  test("includes tier, conflicts_only, limit and offset when set", () => {
    const q = new URLSearchParams(
      reviewQueueQuery({
        frameworkVersionId: "fv-1",
        tier: "under_review",
        conflictsOnly: true,
        limit: 25,
        offset: 50,
      }),
    );
    expect(q.get("framework_version_id")).toBe("fv-1");
    expect(q.get("tier")).toBe("under_review");
    expect(q.get("conflicts_only")).toBe("true");
    expect(q.get("limit")).toBe("25");
    expect(q.get("offset")).toBe("50");
  });

  test("omits conflicts_only when false rather than sending false", () => {
    // The BFF forwards the parameter only for the literal string "true", so
    // sending "false" would be dead weight; omitting keeps the two layers
    // agreeing on what "off" looks like.
    expect(
      reviewQueueQuery({ frameworkVersionId: "fv-1", conflictsOnly: false }),
    ).toBe("framework_version_id=fv-1");
  });

  test("emits offset=0 explicitly when the caller asks for the first page", () => {
    expect(
      reviewQueueQuery({ frameworkVersionId: "fv-1", offset: 0 }),
    ).toContain("offset=0");
  });
});

describe("conflict presentation helpers", () => {
  test("sortConflicts orders high, then medium, then low", () => {
    const sorted = sortConflicts([
      conflict({ severity: "low", reason: "explicitly_unmapped" }),
      conflict({ severity: "high", reason: "unmapped" }),
      conflict({ severity: "medium", reason: "equal_below_full_strength" }),
    ]);
    expect(sorted.map((c) => c.severity)).toEqual(["high", "medium", "low"]);
  });

  test("sortConflicts breaks severity ties by reason, so the order is total", () => {
    // Two findings at the same severity must not swap places between renders;
    // the backend's own ordering is deterministic (536a D6) and this preserves
    // that property through the re-sort.
    const input = [
      conflict({ severity: "high", reason: "zero_strength_only" }),
      conflict({ severity: "high", reason: "duplicate_equal_claim" }),
    ];
    expect(sortConflicts(input).map((c) => c.reason)).toEqual([
      "duplicate_equal_claim",
      "zero_strength_only",
    ]);
    // And it is stable under a reversed input.
    expect(sortConflicts([...input].reverse()).map((c) => c.reason)).toEqual([
      "duplicate_equal_claim",
      "zero_strength_only",
    ]);
  });

  test("sortConflicts does not mutate its input", () => {
    const input = [
      conflict({ severity: "low" }),
      conflict({ severity: "high" }),
    ];
    sortConflicts(input);
    expect(input.map((c) => c.severity)).toEqual(["low", "high"]);
  });

  test("conflictsForEdge selects only findings naming that edge", () => {
    const conflicts = [
      conflict({ severity: "high", reason: "a", edge_ids: ["e1", "e2"] }),
      conflict({ severity: "medium", reason: "b", edge_ids: ["e2"] }),
      conflict({ severity: "low", reason: "c", edge_ids: ["e3"] }),
    ];
    expect(conflictsForEdge(conflicts, "e1").map((c) => c.reason)).toEqual([
      "a",
    ]);
    // One finding can name several edges — the competing-anchor and sibling
    // divergence families are statements about a SET, so e2 sees both.
    expect(conflictsForEdge(conflicts, "e2").map((c) => c.reason)).toEqual([
      "a",
      "b",
    ]);
  });

  test("conflictsForEdge returns nothing for an edge-less finding", () => {
    // The orphaned-requirement family names no edge at all (536a D4), which is
    // why those findings stay at requirement level and must not vanish into a
    // per-row view that would silently drop them.
    const orphan = conflict({
      severity: "high",
      kind: "orphaned_requirement",
      reason: "unmapped",
      edge_ids: [],
    });
    expect(conflictsForEdge([orphan], "e1")).toEqual([]);
  });

  test("highestSeverity reports the worst finding, or undefined when clean", () => {
    expect(
      highestSeverity([
        conflict({ severity: "low" }),
        conflict({ severity: "medium" }),
      ]),
    ).toBe("medium");
    expect(
      highestSeverity([
        conflict({ severity: "medium" }),
        conflict({ severity: "high" }),
      ]),
    ).toBe("high");
    expect(highestSeverity([])).toBeUndefined();
  });
});

describe("the mutating calls", () => {
  beforeEach(() => {
    vi.restoreAllMocks();
  });

  test("editCrosswalkEdge PATCHes the edge's BFF path with the content body", async () => {
    let url = "";
    let init: RequestInit | undefined;
    vi.spyOn(globalThis, "fetch").mockImplementation(
      async (input: RequestInfo | URL, i?: RequestInit) => {
        url = typeof input === "string" ? input : input.toString();
        init = i;
        return new Response(JSON.stringify({ edge_id: "e1" }), { status: 200 });
      },
    );

    await editCrosswalkEdge("e1", {
      relationship_type: "subset_of",
      strength: 0.6,
      rationale: "r",
      note: "n",
    });

    expect(url).toBe("/api/admin/crosswalk-edges/e1");
    expect(init?.method).toBe("PATCH");
    expect(JSON.parse(init?.body as string)).toEqual({
      relationship_type: "subset_of",
      strength: 0.6,
      rationale: "r",
      note: "n",
    });
  });

  test("transitionCrosswalkTier POSTs to the tier sub-path — 483's endpoint", async () => {
    let url = "";
    let init: RequestInit | undefined;
    vi.spyOn(globalThis, "fetch").mockImplementation(
      async (input: RequestInfo | URL, i?: RequestInit) => {
        url = typeof input === "string" ? input : input.toString();
        init = i;
        return new Response(JSON.stringify({ to_tier: "verified" }), {
          status: 200,
        });
      },
    );

    await transitionCrosswalkTier("e1", "verified", "looks right");

    expect(url).toBe("/api/admin/crosswalk-edges/e1/tier");
    expect(init?.method).toBe("POST");
    expect(JSON.parse(init?.body as string)).toEqual({
      tier: "verified",
      note: "looks right",
    });
  });

  test("percent-encodes an edge id so it cannot escape its path segment", async () => {
    let url = "";
    vi.spyOn(globalThis, "fetch").mockImplementation(
      async (input: RequestInfo | URL) => {
        url = typeof input === "string" ? input : input.toString();
        return new Response("{}", { status: 200 });
      },
    );

    await transitionCrosswalkTier("../../v1/admin/tenants", "verified");

    expect(url).toBe(
      "/api/admin/crosswalk-edges/..%2F..%2Fv1%2Fadmin%2Ftenants/tier",
    );
  });

  test("surfaces the backend's own refusal wording as the APIError message", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response(JSON.stringify({ error: "the edit changes nothing" }), {
        status: 422,
      }),
    );

    await expect(
      editCrosswalkEdge("e1", {
        relationship_type: "equal",
        strength: 1,
        rationale: "",
      }),
    ).rejects.toMatchObject({
      status: 422,
      message: "the edit changes nothing",
    });
  });

  test("falls back to the status line when the error body is not JSON", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response("<html>gateway</html>", { status: 502 }),
    );

    const err = await transitionCrosswalkTier("e1", "verified").catch(
      (e: unknown) => e,
    );

    expect(err).toBeInstanceOf(APIError);
    expect((err as APIError).status).toBe(502);
  });
});
