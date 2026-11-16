import { describe, expect, test } from "vitest";

import {
  credentialOptions,
  frameworkOptions,
  matchesQuery,
  parsePastedId,
  userOptions,
} from "./entity-picker";

const U1 = "11111111-1111-1111-1111-111111111111";
const U2 = "22222222-2222-2222-2222-222222222222";
const K1 = "33333333-3333-3333-3333-333333333333";

describe("frameworkOptions", () => {
  test("one option per version, labelled by name and version, sorted", () => {
    const got = frameworkOptions([
      {
        id: "f1",
        name: "SOC 2",
        versions: [
          {
            id: "AAAAAAAA-0000-0000-0000-000000000001",
            version: "2017",
            status: "current",
          },
        ],
      },
      {
        id: "f2",
        name: "ISO/IEC 27001",
        versions: [
          { id: "b-2022", version: "2022", status: "current" },
          { id: "b-2013", version: "2013", status: "legacy" },
        ],
      },
      { id: "f3", name: "No versions" },
    ]);
    expect(got).toEqual([
      { value: "b-2013", label: "ISO/IEC 27001:2013", hint: "Legacy" },
      { value: "b-2022", label: "ISO/IEC 27001:2022", hint: "Current" },
      {
        value: "aaaaaaaa-0000-0000-0000-000000000001",
        label: "SOC 2 2017",
        hint: "Current",
      },
    ]);
  });
});

describe("userOptions", () => {
  test("lists active named users, normalised and sorted", () => {
    expect(
      userOptions([
        {
          id: `user:${U2.toUpperCase()}`,
          display_name: "Zed",
          status: "active",
        },
        { id: U1, display_name: "Alice", status: "active" },
        {
          id: "33333333-3333-3333-3333-333333333334",
          display_name: "Gone",
          status: "disabled",
        },
        { id: "not-a-uuid", display_name: "Bad", status: "active" },
        {
          id: "44444444-4444-4444-4444-444444444444",
          display_name: " ",
          status: "active",
        },
      ]),
    ).toEqual([
      { value: U1, label: "Alice" },
      { value: U2, label: "Zed" },
    ]);
  });
});

describe("credentialOptions", () => {
  test("labels a key by its owner, or plainly when it has none", () => {
    expect(
      credentialOptions(
        [{ id: K1.toUpperCase(), owner_id: U1 }, { id: "key-2" }],
        [{ id: U1, display_name: "Alice", status: "active" }],
      ),
    ).toEqual([
      { value: K1, label: "API key (owner: Alice)" },
      { value: "key-2", label: "API key" },
    ]);
  });
});

describe("parsePastedId", () => {
  test("accepts a UUID for every kind, lowercased", () => {
    for (const kind of ["framework", "user", "credential"] as const) {
      expect(parsePastedId([kind], ` ${U1.toUpperCase()} `)).toEqual({
        ok: true,
        id: U1,
      });
    }
  });

  test("a user ID may carry a user: prefix; other kinds may not", () => {
    expect(parsePastedId(["user"], `user:${U1}`)).toEqual({ ok: true, id: U1 });
    expect(parsePastedId(["framework"], `user:${U1}`).ok).toBe(false);
  });

  test("rejects the nil UUID and non-UUIDs with a message naming the kind", () => {
    const nil = parsePastedId(
      ["framework"],
      "00000000-0000-0000-0000-000000000000",
    );
    expect(nil.ok).toBe(false);
    const bad = parsePastedId(["user", "credential"], "not-an-id");
    expect(bad).toEqual({
      ok: false,
      error:
        '"not-an-id" is not a valid user or API key ID. Pick one from the list or paste its ID.',
    });
  });
});

describe("matchesQuery", () => {
  const opt = { value: U1, label: "ISO/IEC 27001:2022", hint: "Current" };
  test("matches label, hint and exact ID, case-insensitively", () => {
    expect(matchesQuery(opt, "")).toBe(true);
    expect(matchesQuery(opt, "iso")).toBe(true);
    expect(matchesQuery(opt, "current")).toBe(true);
    expect(matchesQuery(opt, U1.toUpperCase())).toBe(true);
    expect(matchesQuery(opt, "soc")).toBe(false);
  });
});
