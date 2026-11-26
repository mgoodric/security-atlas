// #2115 — the framework-scope wizard and the scope page's header action.
//
// The security-relevant case: the scope picker emits null for an
// incomplete choice, and the platform reads a stored null predicate as
// every cell. The wizard must build no body for it, and the Advanced
// editor must refuse a typed `null`.

import { describe, expect, test } from "vitest";

import type { FrameworkScope } from "@/lib/api/framework-scopes";

import {
  advancedText,
  errorMessage,
  headerAction,
  newScopeHref,
  parseAdvanced,
  validateScopeDraft,
  workingRevision,
  type ScopeDraft,
} from "./wizard";

const FV = "00000000-0000-0000-0000-000000002115";

function draft(over: Partial<ScopeDraft> = {}): ScopeDraft {
  return {
    frameworkVersionId: FV,
    name: "  SOC 2 system  ",
    predicate: { op: "true" },
    ...over,
  };
}

function scope(
  id: string,
  state: FrameworkScope["state"],
  created_at: string,
): FrameworkScope {
  return {
    id,
    framework_version_id: FV,
    name: id,
    state,
    predicate: { op: "true" },
    predicate_hash: "h",
    created_at,
    updated_at: created_at,
  };
}

describe("validateScopeDraft", () => {
  test("builds the create body with a trimmed name", () => {
    expect(validateScopeDraft(draft())).toEqual({
      ok: true,
      body: {
        framework_version_id: FV,
        name: "SOC 2 system",
        predicate: { op: "true" },
      },
    });
  });

  test("keeps a by-dimension predicate as given", () => {
    const p = { op: "eq", dim: "environment", value: "prod" };
    const r = validateScopeDraft(draft({ predicate: p }));
    expect(r.ok && r.body.predicate).toEqual(p);
  });

  test.each([
    ["null (the picker's incomplete choice)", null],
    ["undefined", undefined],
    ["an array", []],
    ["a string", "true"],
    ["a number", 1],
  ])("builds no body for a %s predicate", (_what, predicate) => {
    const r = validateScopeDraft(draft({ predicate }));
    expect(r.ok).toBe(false);
    expect(!r.ok && r.errors.scope).toBe(
      "Pick at least one value, or choose Everything.",
    );
    expect("body" in r).toBe(false);
  });

  test("names every missing field", () => {
    const r = validateScopeDraft({
      frameworkVersionId: "",
      name: "   ",
      predicate: null,
    });
    expect(r).toEqual({
      ok: false,
      errors: {
        frameworkVersion: "Choose the framework version to scope.",
        name: "Name the scope.",
        scope: "Pick at least one value, or choose Everything.",
      },
    });
  });
});

describe("parseAdvanced", () => {
  test("parses a predicate object", () => {
    expect(parseAdvanced('{"op":"true"}')).toEqual({
      ok: true,
      predicate: { op: "true" },
    });
  });

  test.each(["null", "[]", '"x"', "1", "true"])(
    "refuses %s, which is not a predicate object",
    (text) => {
      const r = parseAdvanced(text);
      expect(r.ok).toBe(false);
      expect(!r.ok && r.error).toMatch(/is a JSON object/);
    },
  );

  test("reports invalid JSON", () => {
    const r = parseAdvanced("{op:");
    expect(!r.ok && r.error).toMatch(/^Invalid JSON: /);
  });

  test("round-trips a by-dimension predicate through advancedText", () => {
    const p = {
      op: "and",
      args: [
        { op: "eq", dim: "environment", value: "prod" },
        { op: "in", dim: "geography", values: ["eu", "us"] },
      ],
    };
    expect(parseAdvanced(advancedText(p))).toEqual({ ok: true, predicate: p });
  });

  test("advancedText shows Everything for an incomplete choice", () => {
    expect(JSON.parse(advancedText(null))).toEqual({ op: "true" });
  });
});

describe("headerAction", () => {
  test("no revisions: create one", () => {
    expect(headerAction([])).toEqual({ kind: "create" });
  });

  test.each([
    ["draft", "submit"],
    ["review", "approve"],
    ["approved", "activate"],
  ] as const)("a %s revision offers %s", (state, kind) => {
    const s = scope("w", state, "2026-11-02T00:00:00Z");
    const a = headerAction([
      scope("a", "activated", "2026-11-01T00:00:00Z"),
      s,
    ]);
    expect(a).toEqual({ kind, scope: s });
  });

  test("only an activated revision: start a new revision from it", () => {
    const active = scope("a", "activated", "2026-11-02T00:00:00Z");
    const a = headerAction([
      scope("old", "superseded", "2026-11-01T00:00:00Z"),
      active,
    ]);
    expect(a).toEqual({ kind: "revise", scope: active });
  });

  test("only superseded revisions: revise the newest", () => {
    const newest = scope("s2", "superseded", "2026-11-02T00:00:00Z");
    expect(
      headerAction([scope("s1", "superseded", "2026-11-01T00:00:00Z"), newest]),
    ).toEqual({ kind: "revise", scope: newest });
  });

  test("the newest revision still in the workflow is the working one", () => {
    const older = scope("d1", "draft", "2026-11-01T00:00:00Z");
    const newer = scope("r2", "review", "2026-11-03T00:00:00Z");
    expect(workingRevision([older, newer])).toBe(newer);
  });
});

test("newScopeHref pre-fills the framework version", () => {
  expect(newScopeHref(FV)).toBe(
    `/framework-scopes/new?framework_version_id=${FV}`,
  );
  expect(newScopeHref()).toBe("/framework-scopes/new");
});

describe("errorMessage (#2433)", () => {
  const res = (body: unknown, status: number, statusText: string) =>
    new Response(JSON.stringify(body), { status, statusText });

  test("a refusal shows the platform's words", async () => {
    await expect(
      errorMessage(res({ error: "scope is frozen" }, 409, "Conflict")),
    ).resolves.toBe("scope is frozen");
  });

  test("a 5xx shows the status line, never its body", async () => {
    await expect(
      errorMessage(
        res({ error: "pq: deadlock detected" }, 500, "Internal Server Error"),
      ),
    ).resolves.toBe("500 Internal Server Error");
  });
});
