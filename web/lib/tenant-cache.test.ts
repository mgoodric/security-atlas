// #2621 — after a tenant switch no cached view renders the old tenant's
// data: every query shows its loading state, then the new tenant's.

import { QueryClient, QueryObserver } from "@tanstack/react-query";
import { describe, expect, it, vi } from "vitest";

import { peopleDirectoryQuery } from "./people-cache";
import { forgetTenantCaches } from "./tenant-cache";

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
