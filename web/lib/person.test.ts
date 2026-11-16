// #2085 — resolvePerson: user, API key, removed user and unknown each
// render as specified, and no case shows a raw ID as visible text.

import { describe, expect, it } from "vitest";

import { buildDirectory, normaliseUserId, resolvePerson } from "./person";

const ALICE = "11111111-1111-4111-8111-111111111111";
const BOB = "22222222-2222-4222-8222-222222222222";
const KEY = "33333333-3333-4333-8333-333333333333";
const ORPHAN_KEY = "44444444-4444-4444-8444-444444444444";
const STRANGER = "55555555-5555-4555-8555-555555555555";

const dir = buildDirectory(
  [
    { id: ALICE, display_name: "Alice Admin", status: "active" },
    { id: BOB, display_name: "Bob Former", status: "disabled" },
  ],
  [{ id: KEY, owner_id: ALICE }, { id: ORPHAN_KEY }],
);

describe("normaliseUserId", () => {
  it("strips the user: prefix", () => {
    expect(normaliseUserId(`user:${ALICE}`)).toBe(ALICE);
  });
  it("lowercases", () => {
    expect(normaliseUserId(ALICE.toUpperCase())).toBe(ALICE);
  });
  it("rejects the nil UUID and non-UUIDs", () => {
    expect(normaliseUserId("00000000-0000-0000-0000-000000000000")).toBeNull();
    expect(normaliseUserId("jwt:abc")).toBeNull();
    expect(normaliseUserId("")).toBeNull();
  });
});

describe("resolvePerson", () => {
  it("renders a user as name and initials", () => {
    expect(resolvePerson(ALICE, dir)).toEqual({
      kind: "user",
      text: "Alice Admin",
      initials: "AA",
      title: "Alice Admin",
    });
  });

  it("resolves a user:-prefixed subject to the same user", () => {
    expect(resolvePerson(`user:${ALICE}`, dir).text).toBe("Alice Admin");
  });

  it("renders an API key with its owner and never its ID", () => {
    const p = resolvePerson(KEY, dir);
    expect(p.kind).toBe("api_key");
    expect(p.text).toBe("API key (owner: Alice Admin)");
    expect(p.title).not.toContain(KEY);
  });

  it("renders an API key without a resolvable owner as 'API key'", () => {
    expect(resolvePerson(ORPHAN_KEY, dir).text).toBe("API key");
  });

  it("renders a disabled user as Former user", () => {
    const p = resolvePerson(BOB, dir);
    expect(p.kind).toBe("former_user");
    expect(p.text).toBe("Former user");
  });

  it.each([
    ["another tenant's user", STRANGER],
    ["a JWT credential", "jwt:9f1c2d"],
    ["a user: subject that is a key ID", `user:${KEY}`],
    ["garbage", "not-an-id"],
  ])("renders %s as Unknown with the ID in the tooltip", (_, ref) => {
    const p = resolvePerson(ref, dir);
    expect(p.kind).toBe("unknown");
    expect(p.text).toBe("Unknown");
    expect(p.title).toContain(ref);
  });

  it("renders Unknown when the directory is unavailable", () => {
    const p = resolvePerson(ALICE, null);
    expect(p.text).toBe("Unknown");
    expect(p.title).toContain(ALICE);
  });

  it("renders Unknown for an empty reference", () => {
    expect(resolvePerson("", dir)).toEqual({
      kind: "unknown",
      text: "Unknown",
      title: "Unknown",
    });
    expect(resolvePerson(undefined, dir).text).toBe("Unknown");
  });
});
