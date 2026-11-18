import { describe, expect, it } from "vitest";

import {
  NO_BULK_ACTIONS,
  anyBulkAction,
  bulkActionGates,
  exceptionRequestHref,
  perItemMessage,
  runPerItem,
  summarize,
} from "./bulk-actions";

const ALL_MODULES = { actionPlans: true, exceptions: true };

describe("bulkActionGates", () => {
  it("offers nothing until /v1/me answers", () => {
    expect(bulkActionGates(undefined, ALL_MODULES)).toEqual(NO_BULK_ACTIONS);
  });

  it("offers a viewer nothing", () => {
    const g = bulkActionGates(
      { is_approver: false, roles: ["viewer"] },
      ALL_MODULES,
    );
    expect(g).toEqual(NO_BULK_ACTIONS);
    expect(anyBulkAction(g)).toBe(false);
  });

  it("offers an approver every action", () => {
    expect(
      bulkActionGates({ is_approver: true, roles: ["admin"] }, ALL_MODULES),
    ).toEqual({
      assignOwner: true,
      evaluate: true,
      linkPlan: true,
      requestException: true,
    });
  });

  it("offers a control owner evaluate only", () => {
    expect(
      bulkActionGates(
        { is_approver: false, roles: ["control_owner"] },
        ALL_MODULES,
      ),
    ).toEqual({ ...NO_BULK_ACTIONS, evaluate: true });
  });

  it("drops link-to-plan and request-exception when their module is off", () => {
    const g = bulkActionGates(
      { is_approver: true, roles: ["grc_engineer"] },
      { actionPlans: false, exceptions: false },
    );
    expect(g).toEqual({
      assignOwner: true,
      evaluate: true,
      linkPlan: false,
      requestException: false,
    });
  });
});

describe("runPerItem", () => {
  it("sorts each id into succeeded or refused, keeping input order", async () => {
    const r = await runPerItem(["a", "b", "c", "d"], async (id) => {
      if (id === "b") throw new Error("forbidden");
      if (id === "d") throw new Error("");
    });
    expect(r.succeeded).toEqual(["a", "c"]);
    expect(r.refused).toEqual([
      { id: "b", reason: "forbidden" },
      { id: "d", reason: "Refused" },
    ]);
  });

  it("never runs more than the concurrency at once", async () => {
    let live = 0;
    let peak = 0;
    const ids = Array.from({ length: 10 }, (_, i) => String(i));
    await runPerItem(
      ids,
      async () => {
        live += 1;
        peak = Math.max(peak, live);
        await new Promise((res) => setTimeout(res, 1));
        live -= 1;
      },
      3,
    );
    expect(peak).toBe(3);
  });

  it("handles an empty batch", async () => {
    expect(await runPerItem([], async () => {})).toEqual({
      succeeded: [],
      refused: [],
    });
  });
});

describe("summarize / perItemMessage", () => {
  it("names the count and, when any, the refusals", () => {
    expect(summarize({ succeeded: ["a"], refused: [] }, "evaluated")).toBe(
      "1 control evaluated.",
    );
    expect(
      summarize(
        { succeeded: ["a", "b"], refused: [{ id: "c", reason: "x" }] },
        "evaluated",
      ),
    ).toBe("2 controls evaluated, 1 refused.");
  });

  it("labels each refusal and marks a partial failure as an error", () => {
    const m = perItemMessage(
      { succeeded: ["a"], refused: [{ id: "c", reason: "forbidden" }] },
      "linked to the plan",
      (id) => `Control ${id.toUpperCase()}`,
    );
    expect(m.kind).toBe("error");
    expect(m.refusals).toEqual([{ label: "Control C", reason: "forbidden" }]);
  });
});

describe("exceptionRequestHref", () => {
  it("repeats control_id once per selected control", () => {
    expect(exceptionRequestHref(["a", "b"])).toBe(
      "/exceptions/new?control_id=a&control_id=b",
    );
  });
});
