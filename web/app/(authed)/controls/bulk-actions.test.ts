import { describe, expect, it } from "vitest";

import {
  MAX_RATE_LIMIT_ATTEMPTS,
  NO_BULK_ACTIONS,
  abortableSleep,
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
      { concurrency: 3 },
    );
    expect(peak).toBe(3);
  });

  const rateLimited = (wait?: number) =>
    Object.assign(new Error("too many evaluations; retry later"), {
      status: 429,
      retryAfterSeconds: wait,
    });

  it("waits out a Retry-After and retries the rate-limited item (#2618)", async () => {
    const tries = new Map<string, number>();
    const waits: number[] = [];
    const progress: number[] = [];
    const r = await runPerItem(
      ["a", "b", "c"],
      async (id) => {
        const n = (tries.get(id) ?? 0) + 1;
        tries.set(id, n);
        if (id === "b" && n < 3) throw rateLimited(10);
      },
      {
        concurrency: 1,
        sleep: async (ms) => {
          waits.push(ms);
        },
        onProgress: (p) => progress.push(p.succeeded),
      },
    );
    expect(r).toEqual({ succeeded: ["a", "b", "c"], refused: [] });
    expect(tries.get("b")).toBe(3);
    expect(waits.length).toBeGreaterThanOrEqual(2);
    expect(Math.max(...waits)).toBeLessThanOrEqual(10_000);
    expect(progress).toEqual([1, 2, 3]);
  });

  it("refuses as rate-limited when there is no Retry-After to wait for", async () => {
    let calls = 0;
    const r = await runPerItem(["a"], async () => {
      calls += 1;
      throw rateLimited(undefined);
    });
    expect(calls).toBe(1);
    expect(r.refused).toEqual([
      {
        id: "a",
        reason: "too many evaluations; retry later",
        kind: "rate_limited",
        retryAfterSeconds: undefined,
      },
    ]);
  });

  it("stops retrying after MAX_RATE_LIMIT_ATTEMPTS", async () => {
    let calls = 0;
    const r = await runPerItem(
      ["a"],
      async () => {
        calls += 1;
        throw rateLimited(5);
      },
      { sleep: async () => {} },
    );
    expect(calls).toBe(MAX_RATE_LIMIT_ATTEMPTS);
    expect(r.refused[0]).toMatchObject({
      kind: "rate_limited",
      retryAfterSeconds: 5,
    });
  });

  it("cancels the items not yet run when the signal aborts", async () => {
    const ctl = new AbortController();
    const ran: string[] = [];
    const r = await runPerItem(
      ["a", "b", "c"],
      async (id) => {
        ran.push(id);
        if (id === "a") ctl.abort();
      },
      { concurrency: 1, signal: ctl.signal },
    );
    expect(ran).toEqual(["a"]);
    expect(r.succeeded).toEqual(["a"]);
    expect(r.refused.map((x) => [x.id, x.kind])).toEqual([
      ["b", "cancelled"],
      ["c", "cancelled"],
    ]);
  });

  it("cancels an item waiting out a Retry-After", async () => {
    const ctl = new AbortController();
    const r = await runPerItem(
      ["a"],
      async () => {
        throw rateLimited(60);
      },
      {
        signal: ctl.signal,
        sleep: (ms, signal) => {
          const p = abortableSleep(ms, signal);
          ctl.abort();
          return p;
        },
      },
    );
    expect(r.refused).toEqual([
      { id: "a", reason: "Cancelled", kind: "cancelled" },
    ]);
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

  it("collapses rate-limited and cancelled refusals into one line each (#2618)", () => {
    const m = perItemMessage(
      {
        succeeded: ["a"],
        refused: [
          { id: "b", reason: "forbidden" },
          { id: "c", reason: "x", kind: "rate_limited", retryAfterSeconds: 10 },
          { id: "d", reason: "x", kind: "rate_limited", retryAfterSeconds: 60 },
          { id: "e", reason: "Cancelled", kind: "cancelled" },
        ],
      },
      "evaluated",
      (id) => id.toUpperCase(),
    );
    expect(m.text).toBe("1 control evaluated, 4 refused.");
    expect(m.refusals).toEqual([
      { label: "B", reason: "forbidden" },
      {
        label: "2 controls not evaluated",
        reason: "rate limit reached, retry in ~60s",
      },
      { label: "1 control not evaluated", reason: "cancelled" },
    ]);
  });
});

describe("exceptionRequestHref", () => {
  it("repeats control_id once per selected control", () => {
    expect(exceptionRequestHref(["a", "b"])).toBe(
      "/exceptions/new?control_id=a&control_id=b",
    );
  });
});
