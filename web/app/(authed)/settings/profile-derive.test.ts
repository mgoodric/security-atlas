// Slice 154 — vitest unit coverage for profile-derive.ts.
//
// The three helpers are pure logic; they have no DOM dependency. The
// settings page renders them client-side, but the tests exercise them
// directly to lock down the contract.

import { describe, expect, it } from "vitest";

import {
  TIME_ZONE_OPTIONS,
  initialsFor,
  isCuratedTimeZone,
  tailRoles,
} from "./profile-derive";

describe("initialsFor", () => {
  it("returns first letter of first two words for a multi-word display name", () => {
    expect(initialsFor({ display_name: "Sam Rivera", email: "" })).toBe("SR");
    expect(initialsFor({ display_name: "Matt Goodrich", email: "" })).toBe(
      "MG",
    );
  });

  it("uppercases the output regardless of source case", () => {
    expect(initialsFor({ display_name: "sam rivera", email: "" })).toBe("SR");
  });

  it("uses first two letters of a single-word display name", () => {
    expect(initialsFor({ display_name: "Cleopatra", email: "x@y.z" })).toBe(
      "CL",
    );
  });

  it("falls back to email local-part when display name is empty", () => {
    expect(initialsFor({ display_name: "", email: "matt@example.com" })).toBe(
      "MA",
    );
  });

  it("falls back to email local-part when display name is missing", () => {
    expect(
      // The wire shape can technically deliver an unset display_name as
      // empty string; defensive cast for the test.
      initialsFor({
        display_name: "" as string,
        email: "rivera@sentinellabs.example",
      }),
    ).toBe("RI");
  });

  it("returns ?? when nothing usable is present", () => {
    expect(initialsFor({ display_name: "", email: "" })).toBe("??");
  });

  it("skips non-letter characters in the display name", () => {
    // "(unset)" parses as a single word; the parens are stripped; the
    // first two letters of "unset" win the avatar. The email fallback is
    // not consulted because the display_name yielded usable letters.
    expect(initialsFor({ display_name: "(unset)", email: "ab@cd.e" })).toBe(
      "UN",
    );
  });

  it("falls back through to email when display_name has no letters at all", () => {
    expect(initialsFor({ display_name: "()", email: "ab@cd.e" })).toBe("AB");
  });

  it("handles three-word names by taking the first two", () => {
    expect(
      initialsFor({ display_name: "Anna Karenina Tolstaya", email: "" }),
    ).toBe("AK");
  });

  it("handles a single-letter first word by borrowing the email second letter", () => {
    expect(initialsFor({ display_name: "X", email: "yankee@z.q" })).toBe("XY");
  });

  it("survives whitespace-only display_name", () => {
    expect(initialsFor({ display_name: "   ", email: "ab@cd.e" })).toBe("AB");
  });
});

describe("tailRoles", () => {
  it("returns empty array for empty or undefined input", () => {
    expect(tailRoles(undefined, false)).toEqual([]);
    expect(tailRoles([], false)).toEqual([]);
    expect(tailRoles(undefined, true)).toEqual([]);
  });

  it("drops admin when isAdmin is true (primary badge covers it)", () => {
    expect(tailRoles(["admin", "grc_engineer"], true)).toEqual([
      "grc_engineer",
    ]);
  });

  it("keeps admin when isAdmin is false (defensive — wire mismatch)", () => {
    // Belt-and-braces: if the wire reports admin in `roles` but
    // `is_admin: false`, render the tail honestly so the discrepancy
    // surfaces. The primary badge will show "user" and the tail will
    // show "admin" — that's the correct disambiguation, not a UI bug.
    expect(tailRoles(["admin", "grc_engineer"], false)).toEqual([
      "admin",
      "grc_engineer",
    ]);
  });

  it("drops the implicit 'user' pseudo-role", () => {
    expect(tailRoles(["user", "grc_engineer"], false)).toEqual([
      "grc_engineer",
    ]);
  });

  it("preserves input order so the wire order is honored", () => {
    expect(
      tailRoles(["control_owner", "grc_engineer", "auditor"], false),
    ).toEqual(["control_owner", "grc_engineer", "auditor"]);
  });

  it("dedupes defensively", () => {
    expect(tailRoles(["grc_engineer", "grc_engineer"], false)).toEqual([
      "grc_engineer",
    ]);
  });

  it("filters empty strings out (defensive against wire-level junk)", () => {
    expect(tailRoles(["", "grc_engineer", ""], false)).toEqual([
      "grc_engineer",
    ]);
  });

  it("returns empty when only admin + isAdmin", () => {
    expect(tailRoles(["admin"], true)).toEqual([]);
  });
});

describe("isCuratedTimeZone", () => {
  it("returns true for each curated zone", () => {
    for (const z of TIME_ZONE_OPTIONS) {
      expect(isCuratedTimeZone(z)).toBe(true);
    }
  });

  it("returns false for null / undefined / empty", () => {
    expect(isCuratedTimeZone(null)).toBe(false);
    expect(isCuratedTimeZone(undefined)).toBe(false);
    expect(isCuratedTimeZone("")).toBe(false);
  });

  it("returns false for a non-curated valid IANA zone", () => {
    // The backend accepts this via time.LoadLocation; the picker does
    // not list it. The page should render the out-of-band option
    // explicitly when this returns false.
    expect(isCuratedTimeZone("Australia/Sydney")).toBe(false);
  });

  it("returns false for invalid strings", () => {
    expect(isCuratedTimeZone("Mars/Olympus_Mons")).toBe(false);
    expect(isCuratedTimeZone("not a zone")).toBe(false);
  });
});

describe("TIME_ZONE_OPTIONS", () => {
  it("contains exactly nine zones (slice 154 curated list)", () => {
    expect(TIME_ZONE_OPTIONS).toHaveLength(9);
  });

  it("starts with America/Los_Angeles (primary-user persona)", () => {
    expect(TIME_ZONE_OPTIONS[0]).toBe("America/Los_Angeles");
  });

  it("ends with UTC", () => {
    expect(TIME_ZONE_OPTIONS[TIME_ZONE_OPTIONS.length - 1]).toBe("UTC");
  });

  it("has no duplicates", () => {
    expect(new Set(TIME_ZONE_OPTIONS).size).toBe(TIME_ZONE_OPTIONS.length);
  });
});
