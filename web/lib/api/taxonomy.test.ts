// #1818 — the Taxonomy page's pure rules: who sees edit controls, how
// usage reads, and when delete is offered.

import { describe, expect, test } from "vitest";

import { canManageTaxonomy, deleteBlockedReason, usageLabel } from "./taxonomy";

describe("canManageTaxonomy", () => {
  test("admin and grc_engineer may manage; everyone else views", () => {
    expect(canManageTaxonomy({ is_admin: true, owner_roles: [] })).toBe(true);
    expect(canManageTaxonomy({ owner_roles: ["admin"] })).toBe(true);
    expect(canManageTaxonomy({ owner_roles: ["viewer", "grc_engineer"] })).toBe(
      true,
    );
    expect(canManageTaxonomy({ owner_roles: ["viewer"] })).toBe(false);
    expect(
      canManageTaxonomy({ owner_roles: ["control_owner", "auditor"] }),
    ).toBe(false);
  });

  test("reads the credential's owner_roles, not the user_roles list", () => {
    // The platform guard checks the credential; a `roles` entry the JWT
    // does not carry would only earn a 403.
    const me = { is_admin: false, owner_roles: ["viewer"], roles: ["admin"] };
    expect(canManageTaxonomy(me)).toBe(false);
  });

  test("an unknown caller is view-only", () => {
    expect(canManageTaxonomy(undefined)).toBe(false);
    expect(canManageTaxonomy({})).toBe(false);
  });
});

describe("usageLabel", () => {
  test("pluralises risks and rules", () => {
    expect(usageLabel({ risk_count: 1, rule_count: 0 })).toBe(
      "1 risk, 0 rules",
    );
    expect(usageLabel({ risk_count: 3, rule_count: 1 })).toBe(
      "3 risks, 1 rule",
    );
  });
});

describe("deleteBlockedReason", () => {
  test("unused and not default: delete allowed", () => {
    expect(deleteBlockedReason({ risk_count: 0, rule_count: 0 })).toBeNull();
    expect(
      deleteBlockedReason({ risk_count: 0, rule_count: 0, is_default: false }),
    ).toBeNull();
  });

  test("in use by risks or rules: blocked with the counts", () => {
    expect(deleteBlockedReason({ risk_count: 2, rule_count: 0 })).toBe(
      "In use by 2 risks, 0 rules",
    );
    expect(deleteBlockedReason({ risk_count: 0, rule_count: 1 })).toBe(
      "In use by 0 risks, 1 rule",
    );
  });

  test("the default category is blocked even when unused", () => {
    expect(
      deleteBlockedReason({ risk_count: 0, rule_count: 0, is_default: true }),
    ).toBe("The default category can't be deleted");
  });
});
