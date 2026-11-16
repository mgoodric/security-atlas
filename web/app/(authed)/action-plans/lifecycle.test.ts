import { describe, expect, it } from "vitest";

import { ACTION_PLAN_STATUSES } from "@/lib/api/action-plans";

import { bareUserId, planTransitions, viewerCanVerify } from "./lifecycle";

const noVerify = { hasLinkedControls: false, canVerify: false };
const verifier = { hasLinkedControls: false, canVerify: true };

const ids = (s: Parameters<typeof planTransitions>[0], ctx = noVerify) =>
  planTransitions(s, ctx).map((t) => t.id);

describe("planTransitions", () => {
  it("offers each transition the state machine allows, primary first", () => {
    expect(ids("draft")).toEqual(["start"]);
    expect(ids("in_progress")).toEqual(["complete", "block"]);
    expect(ids("blocked")).toEqual(["unblock", "complete"]);
    expect(ids("completed")).toEqual(["reopen"]);
    expect(ids("verified")).toEqual([]);
  });

  it("marks exactly one primary where there is a forward step", () => {
    for (const s of ACTION_PLAN_STATUSES) {
      const primaries = planTransitions(s, verifier).filter((t) => t.primary);
      expect(primaries.length).toBeLessThanOrEqual(1);
    }
    expect(planTransitions("completed", noVerify)[0].primary).toBe(false);
  });

  it("offers Verify only on a no-control plan to a verifier", () => {
    expect(ids("completed", verifier)).toEqual(["verify", "reopen"]);
    expect(
      ids("completed", { hasLinkedControls: true, canVerify: true }),
    ).toEqual(["reopen"]);
    expect(ids("in_progress", verifier)).not.toContain("verify");
  });

  it("targets the state each transition names", () => {
    const to = Object.fromEntries(
      [
        ...planTransitions("draft", noVerify),
        ...planTransitions("in_progress", noVerify),
        ...planTransitions("blocked", noVerify),
        ...planTransitions("completed", verifier),
      ].map((t) => [t.id, t.to]),
    );
    expect(to).toEqual({
      start: "in_progress",
      complete: "completed",
      block: "blocked",
      unblock: "in_progress",
      reopen: "in_progress",
      verify: "verified",
    });
  });
});

describe("viewerCanVerify", () => {
  const owner = "a1a1a1a1-0000-4000-8000-000000002108";
  const other = "b2b2b2b2-0000-4000-8000-000000002108";

  it("lets an approver who is not the owner verify", () => {
    expect(viewerCanVerify({ user_id: other, is_admin: true }, owner)).toBe(
      true,
    );
  });

  it("never lets the owner verify, whatever the id's prefix or case", () => {
    expect(viewerCanVerify({ user_id: owner, is_admin: true }, owner)).toBe(
      false,
    );
    expect(
      viewerCanVerify(
        { user_id: "user:" + owner.toUpperCase(), is_admin: true },
        owner,
      ),
    ).toBe(false);
  });

  it("refuses a non-approver, an unknown viewer and one with no user id", () => {
    expect(viewerCanVerify({ user_id: other, is_admin: false }, owner)).toBe(
      false,
    );
    expect(viewerCanVerify(undefined, owner)).toBe(false);
    expect(viewerCanVerify({ user_id: "", is_admin: true }, owner)).toBe(false);
  });

  it("strips the JWT subject prefix", () => {
    expect(bareUserId("user:ABC")).toBe("abc");
    expect(bareUserId(undefined)).toBe("");
  });
});
