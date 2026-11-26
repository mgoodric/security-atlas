// #2385 — super admins are named from the list, never shown by ID.

import { describe, expect, it } from "vitest";

import { resolvePerson } from "@/lib/person";

import { superAdminDirectory } from "./super-admin-directory";

const NAMED = "11111111-1111-4111-8111-111111111111";
const EMAIL_ONLY = "22222222-2222-4222-8222-222222222222";
const BARE = "33333333-3333-4333-8333-333333333333";

const dir = superAdminDirectory([
  {
    user_id: NAMED,
    granted_at: "2026-11-01T00:00:00Z",
    granted_via: "manual_grant",
    display_name: "Ada Admin",
    email: "ada@example.invalid",
  },
  {
    user_id: EMAIL_ONLY,
    granted_at: "2026-11-01T00:00:00Z",
    granted_via: "manual_grant",
    display_name: "",
    email: "grace@example.invalid",
  },
  {
    user_id: BARE,
    granted_at: "2026-11-01T00:00:00Z",
    granted_via: "bootstrap_first_install",
  },
]);

describe("superAdminDirectory", () => {
  it("names a row by its display name", () => {
    expect(resolvePerson(NAMED, dir).text).toBe("Ada Admin");
  });

  it("falls back to the email's local part", () => {
    expect(resolvePerson(EMAIL_ONLY, dir).text).toBe("grace");
  });

  it("reads Unknown, the ID in the tooltip only, when the row has no name", () => {
    const p = resolvePerson(BARE, dir);
    expect(p.text).toBe("Unknown");
    expect(p.title).toContain(BARE);
  });
});
