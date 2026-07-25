// Slice 448 — vitest for the client-side saved-views persistence module.
//
// Node-env (slice 069 P0-A3 — no JSX/DOM). Uses an in-memory store shim
// mirroring the slice-103 theme.test.ts pattern.

import { describe, expect, it } from "vitest";

import { ALL } from "./filters";
import {
  addView,
  findView,
  MAX_SAVED_VIEWS,
  MAX_VIEW_NAME_LENGTH,
  migrateLocalViewsToServer,
  parseView,
  readViews,
  removeView,
  sanitizeFilters,
  SAVED_VIEWS_STORAGE_KEY,
  writeViews,
  type MigratableStore,
  type SavedView,
  type SavedViewStore,
} from "./saved-views";

function memStore(initial?: Record<string, string>): SavedViewStore {
  const map = new Map<string, string>(Object.entries(initial ?? {}));
  return {
    getItem: (k) => map.get(k) ?? null,
    setItem: (k, v) => {
      map.set(k, v);
    },
  };
}

const FRESH_VIEW_FILTERS = {
  framework: "soc2",
  family: "IAC",
  result: "fail",
  freshness: "stale",
  scope: ALL,
};

describe("sanitizeFilters", () => {
  it("narrows to the allow-list keys and drops unknown keys", () => {
    const out = sanitizeFilters({
      framework: "soc2",
      family: "IAC",
      result: "pass",
      freshness: "fresh",
      scope: "cell-1",
      // Injection attempt — an arbitrary key that must NOT survive.
      malicious: "DROP TABLE controls",
      __proto__: { polluted: true },
    });
    expect(out).toEqual({
      framework: "soc2",
      family: "IAC",
      result: "pass",
      freshness: "fresh",
      scope: "cell-1",
    });
    expect((out as Record<string, unknown>).malicious).toBeUndefined();
  });

  it("falls back to ALL for missing or non-string values", () => {
    const out = sanitizeFilters({ framework: 42, family: null });
    expect(out.framework).toBe(ALL);
    expect(out.family).toBe(ALL);
    expect(out.result).toBe(ALL);
    expect(out.freshness).toBe(ALL);
    expect(out.scope).toBe(ALL);
  });

  it("returns all-ALL for a non-object input", () => {
    expect(sanitizeFilters("nope").framework).toBe(ALL);
    expect(sanitizeFilters(null).scope).toBe(ALL);
    expect(sanitizeFilters(undefined).family).toBe(ALL);
  });
});

describe("parseView", () => {
  it("returns a normalized view for a valid entry", () => {
    const v = parseView({
      id: "abc",
      name: "  Weekly triage  ",
      filters: FRESH_VIEW_FILTERS,
    });
    expect(v).not.toBeNull();
    expect(v?.id).toBe("abc");
    expect(v?.name).toBe("Weekly triage");
    expect(v?.filters.result).toBe("fail");
  });

  it("rejects an entry with no id or no name", () => {
    expect(parseView({ name: "x", filters: {} })).toBeNull();
    expect(parseView({ id: "x", name: "   ", filters: {} })).toBeNull();
    expect(parseView({ id: "", name: "x" })).toBeNull();
    expect(parseView(null)).toBeNull();
    expect(parseView("string")).toBeNull();
  });

  it("caps an over-long name", () => {
    const long = "a".repeat(MAX_VIEW_NAME_LENGTH + 50);
    const v = parseView({ id: "x", name: long, filters: {} });
    expect(v?.name.length).toBe(MAX_VIEW_NAME_LENGTH);
  });
});

describe("readViews / writeViews", () => {
  it("returns [] when nothing is stored", () => {
    expect(readViews(memStore())).toEqual([]);
  });

  it("returns [] on invalid JSON", () => {
    const store = memStore({ [SAVED_VIEWS_STORAGE_KEY]: "{not json" });
    expect(readViews(store)).toEqual([]);
  });

  it("returns [] when the stored blob is not an array", () => {
    const store = memStore({ [SAVED_VIEWS_STORAGE_KEY]: '{"id":"x"}' });
    expect(readViews(store)).toEqual([]);
  });

  it("round-trips a written list", () => {
    const store = memStore();
    const views: SavedView[] = [
      { id: "1", name: "View A", filters: sanitizeFilters(FRESH_VIEW_FILTERS) },
    ];
    writeViews(store, views);
    expect(readViews(store)).toEqual(views);
  });

  it("drops structurally invalid entries but keeps valid ones", () => {
    const store = memStore({
      [SAVED_VIEWS_STORAGE_KEY]: JSON.stringify([
        { id: "1", name: "Good", filters: FRESH_VIEW_FILTERS },
        { id: "", name: "Bad — no id" },
        { name: "Bad — no id field", filters: {} },
        { id: "2", name: "Also good", filters: {} },
      ]),
    });
    const out = readViews(store);
    expect(out.map((v) => v.id)).toEqual(["1", "2"]);
  });
});

describe("addView", () => {
  it("appends a valid view", () => {
    const r = addView([], "id-1", "Weekly", FRESH_VIEW_FILTERS);
    expect(r.ok).toBe(true);
    if (r.ok) {
      expect(r.views).toHaveLength(1);
      expect(r.views[0]).toMatchObject({ id: "id-1", name: "Weekly" });
    }
  });

  it("rejects an empty / whitespace name", () => {
    expect(addView([], "id", "   ", FRESH_VIEW_FILTERS)).toEqual({
      ok: false,
      reason: "empty-name",
    });
  });

  it("rejects a case-insensitive duplicate name", () => {
    const existing = addView([], "id-1", "Triage", FRESH_VIEW_FILTERS);
    expect(existing.ok).toBe(true);
    if (existing.ok) {
      const dup = addView(
        existing.views,
        "id-2",
        "  triage ",
        FRESH_VIEW_FILTERS,
      );
      expect(dup).toEqual({ ok: false, reason: "duplicate-name" });
    }
  });

  it("rejects past the cap", () => {
    const full: SavedView[] = Array.from(
      { length: MAX_SAVED_VIEWS },
      (_, i) => ({
        id: `id-${i}`,
        name: `View ${i}`,
        filters: sanitizeFilters(FRESH_VIEW_FILTERS),
      }),
    );
    expect(addView(full, "overflow", "One more", FRESH_VIEW_FILTERS)).toEqual({
      ok: false,
      reason: "cap-reached",
    });
  });

  it("sanitizes the stored filters (no arbitrary keys persist)", () => {
    const r = addView([], "id-1", "x", {
      ...FRESH_VIEW_FILTERS,
      injected: "x",
    } as never);
    expect(r.ok).toBe(true);
    if (r.ok) {
      expect(
        (r.views[0].filters as Record<string, unknown>).injected,
      ).toBeUndefined();
    }
  });
});

describe("removeView / findView", () => {
  const views: SavedView[] = [
    { id: "1", name: "A", filters: sanitizeFilters(FRESH_VIEW_FILTERS) },
    { id: "2", name: "B", filters: sanitizeFilters(FRESH_VIEW_FILTERS) },
  ];

  it("removes by id", () => {
    expect(removeView(views, "1").map((v) => v.id)).toEqual(["2"]);
  });

  it("is a no-op for an absent id", () => {
    expect(removeView(views, "nope")).toHaveLength(2);
  });

  it("finds by id, null when absent", () => {
    expect(findView(views, "2")?.name).toBe("B");
    expect(findView(views, "nope")).toBeNull();
  });
});

// Slice 468 — AC-467-3: one-time localStorage -> server migration.
describe("migrateLocalViewsToServer", () => {
  function migratableStore(initial?: Record<string, string>): {
    store: MigratableStore;
    removed: () => boolean;
  } {
    const map = new Map<string, string>(Object.entries(initial ?? {}));
    let didRemove = false;
    return {
      store: {
        getItem: (k) => map.get(k) ?? null,
        setItem: (k, v) => {
          map.set(k, v);
        },
        removeItem: (k) => {
          map.delete(k);
          if (k === SAVED_VIEWS_STORAGE_KEY) didRemove = true;
        },
      },
      removed: () => didRemove,
    };
  }

  it("uploads each local view, then clears the storage key", async () => {
    const local: SavedView[] = [
      { id: "1", name: "Weekly", filters: sanitizeFilters(FRESH_VIEW_FILTERS) },
      {
        id: "2",
        name: "Audit",
        filters: sanitizeFilters({ ...FRESH_VIEW_FILTERS, family: ALL }),
      },
    ];
    const { store, removed } = migratableStore({
      [SAVED_VIEWS_STORAGE_KEY]: JSON.stringify(local),
    });
    const calls: { name: string; filters: Record<string, string> }[] = [];
    const create = async (name: string, filters: Record<string, string>) => {
      calls.push({ name, filters });
    };

    const n = await migrateLocalViewsToServer(store, create);

    expect(n).toBe(2);
    expect(calls.map((c) => c.name)).toEqual(["Weekly", "Audit"]);
    // ALL-valued (inactive) keys are dropped from the uploaded payload.
    expect(calls[0].filters).not.toHaveProperty("family", ALL);
    expect(calls[0].filters.family).toBe("IAC");
    expect(calls[1].filters).not.toHaveProperty("family");
    // The local key is cleared so the migration is idempotent.
    expect(removed()).toBe(true);
  });

  it("swallows per-view upload failures (best-effort)", async () => {
    const local: SavedView[] = [
      { id: "1", name: "Dup", filters: sanitizeFilters(FRESH_VIEW_FILTERS) },
    ];
    const { store, removed } = migratableStore({
      [SAVED_VIEWS_STORAGE_KEY]: JSON.stringify(local),
    });
    const create = async () => {
      throw new Error("409 duplicate");
    };
    // Must not throw; key still cleared.
    await expect(migrateLocalViewsToServer(store, create)).resolves.toBe(1);
    expect(removed()).toBe(true);
  });

  it("is a no-op when no local views exist (clears any corrupt key)", async () => {
    const { store, removed } = migratableStore();
    let createCalls = 0;
    const create = async () => {
      createCalls += 1;
    };
    const n = await migrateLocalViewsToServer(store, create);
    expect(n).toBe(0);
    expect(createCalls).toBe(0);
    expect(removed()).toBe(true);
  });
});
