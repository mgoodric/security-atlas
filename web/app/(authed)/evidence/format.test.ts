// Slice 099 — unit tests for the /evidence cell formatters.

import { describe, expect, test } from "vitest";

import {
  HASH_PREFIX_LEN,
  hashPrefix,
  observedAtLabel,
  prettyJSON,
  scopeLabel,
  sourceSummary,
} from "./format";

describe("hashPrefix", () => {
  test("renders exactly HASH_PREFIX_LEN characters", () => {
    const full =
      "7a4f0123456789abcdef0123456789abcdef0123456789abcdef0123456789b2c1";
    const prefix = hashPrefix(full);
    expect(prefix.length).toBe(HASH_PREFIX_LEN);
    expect(prefix).toBe("7a4f0123");
  });

  test("HASH_PREFIX_LEN is 8 (P0-A2)", () => {
    // Anti-criterion P0-A2 of slice 099 pins the prefix length to 8.
    // If the constant ever drifts, this assertion catches it.
    expect(HASH_PREFIX_LEN).toBe(8);
  });

  test("renders empty string for empty input", () => {
    expect(hashPrefix("")).toBe("");
  });
});

describe("scopeLabel", () => {
  test("renders a shortened UUID with ellipsis when present", () => {
    const out = scopeLabel("22222222-3333-4444-5555-666666666666");
    expect(out).toBe("22222222…");
  });

  test("renders an em-dash when scope_cell is null", () => {
    expect(scopeLabel(null)).toBe("—");
  });
});

describe("sourceSummary", () => {
  test("renders actor_type and actor_id when both present", () => {
    const out = sourceSummary({
      actor_type: "connector",
      actor_id: "aws-connector",
    });
    expect(out).toBe("connector · aws-connector");
  });

  test("falls back to actor_type alone when actor_id missing", () => {
    expect(sourceSummary({ actor_type: "connector" })).toBe("connector");
  });

  test("falls back to actor_id alone when actor_type missing", () => {
    expect(sourceSummary({ actor_id: "manual:sam-r" })).toBe("manual:sam-r");
  });

  test("renders an em-dash for null source", () => {
    expect(sourceSummary(null)).toBe("—");
  });

  test("renders an em-dash for shape without actor fields", () => {
    expect(sourceSummary({ unrelated: 42 })).toBe("—");
  });

  test("ignores non-string actor values (defensive)", () => {
    expect(
      sourceSummary({
        actor_type: 12 as unknown as string,
        actor_id: { nested: true } as unknown as string,
      }),
    ).toBe("—");
  });
});

describe("prettyJSON", () => {
  test("renders an indented JSON string", () => {
    const out = prettyJSON({ a: 1, b: [2, 3] });
    expect(out).toContain("\n");
    expect(out).toContain('"a": 1');
    expect(out).toContain('"b": [');
  });
});

describe("observedAtLabel", () => {
  test("passes the timestamp through verbatim today", () => {
    expect(observedAtLabel("2026-05-16T09:42:00Z")).toBe(
      "2026-05-16T09:42:00Z",
    );
  });
});
