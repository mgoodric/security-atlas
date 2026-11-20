// #2621 — after a tenant switch no cached view renders the old tenant's
// data: every query shows its loading state, then the new tenant's.

import {
  MutationObserver,
  QueryClient,
  QueryObserver,
} from "@tanstack/react-query";
import { describe, expect, it, vi } from "vitest";

import { peopleDirectoryQuery } from "./people-cache";
import { getQueryClient } from "./queryClient";
import { currentTenantEpoch, forgetTenantCaches } from "./tenant-cache";

const TENANT_A = "aaaaaaaa-0000-4000-8000-000000000001";

describe("forgetTenantCaches", () => {
  it("drops every cached query's data, tenant-keyed or not", () => {
    const qc = new QueryClient();
    // Keys without a tenant, as most browser queries are (#2399 D4).
    qc.setQueryData(["controls"], ["tenant A control"]);
    qc.setQueryData(["dashboard", "counts"], { open: 47 });
    qc.setQueryData(peopleDirectoryQuery(TENANT_A).queryKey, ["Alice"]);

    forgetTenantCaches(qc);

    for (const q of qc.getQueryCache().getAll()) {
      expect(q.state.data).toBeUndefined();
      expect(q.state.status).toBe("pending");
    }
  });

  it("shows a mounted view loading, then the new tenant's data", async () => {
    const qc = new QueryClient({
      defaultOptions: { queries: { staleTime: 60_000 } },
    });
    let tenant = "A";
    const queryFn = () => Promise.resolve(`tenant ${tenant} title`);
    const observer = new QueryObserver(qc, { queryKey: ["title"], queryFn });
    const seen: (string | undefined)[] = [];
    const unsubscribe = observer.subscribe((r) => seen.push(r.data));
    await vi.waitFor(() =>
      expect(observer.getCurrentResult().data).toBe("tenant A title"),
    );

    tenant = "B";
    seen.length = 0;
    forgetTenantCaches(qc);
    await vi.waitFor(() =>
      expect(observer.getCurrentResult().data).toBe("tenant B title"),
    );

    expect(seen).not.toContain("tenant A title");
    expect(seen[0]).toBeUndefined();
    unsubscribe();
  });

  // #2715 — pins resetQueries' cancel of an in-flight fetch: an answer
  // the old tenant asked for, arriving after the switch, never renders.
  it("drops a tenant-A fetch that resolves after the reset", async () => {
    const qc = new QueryClient();
    const pending: Record<string, (v: string) => void> = {};
    let tenant = "A";
    const queryFn = () => {
      const asked = tenant;
      return new Promise<string>((resolve) => {
        pending[asked] = resolve;
      });
    };
    const observer = new QueryObserver(qc, { queryKey: ["title"], queryFn });
    const seen: (string | undefined)[] = [];
    const unsubscribe = observer.subscribe((r) => seen.push(r.data));
    await vi.waitFor(() => expect(pending.A).toBeDefined());

    tenant = "B";
    forgetTenantCaches(qc);
    await vi.waitFor(() => expect(pending.B).toBeDefined());
    pending.A("tenant A title");
    await new Promise((r) => setTimeout(r, 0));
    expect(observer.getCurrentResult().data).toBeUndefined();

    pending.B("tenant B title");
    await vi.waitFor(() =>
      expect(observer.getCurrentResult().data).toBe("tenant B title"),
    );
    expect(seen).not.toContain("tenant A title");
    expect(qc.getQueryData(["title"])).toBe("tenant B title");
    unsubscribe();
  });
});

// #2715 — a mutation in flight across the switch does not write the old
// tenant's response into the cache. These run the app's real QueryClient
// (getQueryClient() builds a fresh one outside the browser) through a
// MutationObserver, the path useMutation takes, so a change in
// query-core's callback ordering fails here.
describe("tenantGuardedMutationCache", () => {
  function deferredMutation(qc: QueryClient, key: string[]) {
    let resolve: ((v: string) => void) | undefined;
    const settled = vi.fn();
    const observer = new MutationObserver(qc, {
      mutationFn: () =>
        new Promise<string>((r) => {
          resolve = r;
        }),
      onSuccess: (v: string) => {
        qc.setQueryData(key, v);
      },
      onSettled: settled,
    });
    const unsubscribe = observer.subscribe(() => {});
    const done = observer.mutate(undefined);
    const started = () => vi.waitFor(() => expect(resolve).toBeDefined());
    return {
      observer,
      done,
      settled,
      unsubscribe,
      started,
      resolve: (v: string) => resolve!(v),
    };
  }

  it("drops the onSuccess of a mutation that started before the switch", async () => {
    const qc = getQueryClient();
    const m = deferredMutation(qc, ["risks", "review-cadence"]);
    await m.started();

    forgetTenantCaches(qc);
    m.resolve("tenant A: 90 days");
    await expect(m.done).resolves.toBe("tenant A: 90 days");

    expect(qc.getQueryData(["risks", "review-cadence"])).toBeUndefined();
    // The server write did succeed: no false "save failed".
    expect(m.observer.getCurrentResult().status).toBe("success");
    expect(m.observer.getCurrentResult().error).toBeNull();
    // onSettled still runs, so UI resets (spinners, flags) still happen.
    expect(m.settled).toHaveBeenCalledTimes(1);
    m.unsubscribe();
  });

  it("lets a mutation that started after the switch write", async () => {
    const qc = getQueryClient();
    forgetTenantCaches(qc);
    const m = deferredMutation(qc, ["vendors", "initial-review-window"]);
    await m.started();

    m.resolve("tenant B: 30 days");
    await m.done;

    expect(qc.getQueryData(["vendors", "initial-review-window"])).toBe(
      "tenant B: 30 days",
    );
    expect(m.settled).toHaveBeenCalledTimes(1);
    m.unsubscribe();
  });

  it("lets a mutation write when no switch happened", async () => {
    const qc = getQueryClient();
    const m = deferredMutation(qc, ["k"]);
    await m.started();
    m.resolve("same tenant");
    await m.done;
    expect(qc.getQueryData(["k"])).toBe("same tenant");
    m.unsubscribe();
  });
});

// #2768 — a handler that writes the cache after a plain awaited request
// (exceptions submit, questionnaire draftAll) is not a mutation, so the
// guard above never sees it. It compares currentTenantEpoch() before and
// after the await; this is that handler's shape, on the app's real
// QueryClient. tenant-switch-guard.test.ts checks the handlers use it.
describe("currentTenantEpoch", () => {
  function deferredHandler(qc: QueryClient, key: string[]) {
    let resolve: ((v: string) => void) | undefined;
    const navigated = vi.fn();
    const done = (async () => {
      const epoch = currentTenantEpoch(qc);
      const resp = await new Promise<string>((r) => {
        resolve = r;
      });
      if (currentTenantEpoch(qc) !== epoch) return;
      qc.setQueryData(key, resp);
      navigated(resp);
    })();
    return { done, navigated, resolve: (v: string) => resolve!(v) };
  }

  it("drops a write whose request was in flight across the switch", async () => {
    const qc = getQueryClient();
    const h = deferredHandler(qc, ["exceptions", "x1"]);

    forgetTenantCaches(qc);
    h.resolve("tenant A exception");
    await h.done;

    expect(qc.getQueryData(["exceptions", "x1"])).toBeUndefined();
    expect(h.navigated).not.toHaveBeenCalled();
  });

  it("lets a write whose request started after the switch land", async () => {
    const qc = getQueryClient();
    forgetTenantCaches(qc);
    const h = deferredHandler(qc, ["questionnaire-run", "q1", "r1"]);

    h.resolve("tenant B run");
    await h.done;

    expect(qc.getQueryData(["questionnaire-run", "q1", "r1"])).toBe(
      "tenant B run",
    );
    expect(h.navigated).toHaveBeenCalledWith("tenant B run");
  });

  it("moves on every reset, on any QueryClient", () => {
    const qc = new QueryClient();
    const before = currentTenantEpoch(qc);
    forgetTenantCaches(qc);
    expect(currentTenantEpoch(qc)).toBe(before + 1);
    forgetTenantCaches(qc);
    expect(currentTenantEpoch(qc)).toBe(before + 2);
  });
});
