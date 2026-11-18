// #2116 — the policy detail header's action per status and the create /
// edit form's rules.

import { describe, expect, test } from "vitest";

import type { Policy } from "@/lib/api/policies";

import {
  AUDIENCE_OPTIONS,
  audienceLabel,
  createInput,
  EMPTY_FORM,
  formFromPolicy,
  isDraft,
  needsControlsWarning,
  nextVersion,
  primaryAction,
  unrecognisedAudiences,
  validatePolicyForm,
} from "./lifecycle";

const ID = "6f1c2a52-3b0e-4b8e-9a51-1d6a3f0c2116";

function policy(over: Partial<Policy> = {}): Policy {
  return {
    id: ID,
    title: "Access Control Policy",
    version: "1.0",
    body_md: "# Access",
    owner_role: "security_lead",
    approver_role: "cto",
    linked_control_ids: ["c1"],
    acknowledgment_required_roles: ["viewer"],
    status: "published",
    source_attribution: "tenant_authored",
    created_by: "u1",
    created_at: "2026-11-01T00:00:00Z",
    updated_at: "2026-11-01T00:00:00Z",
    ...over,
  };
}

describe("primaryAction", () => {
  test.each([
    ["draft", "submit", undefined],
    ["under_review", "approve", undefined],
    ["approved", "publish", `/policies/${ID}/publish`],
    ["published", "new-version", `/policies/new?predecessor=${ID}`],
  ])("%s offers %s", (status, kind, href) => {
    const a = primaryAction({ id: ID, status });
    expect(a?.kind).toBe(kind);
    expect(a && "href" in a ? a.href : undefined).toBe(href);
  });

  test("a superseded policy offers nothing", () => {
    expect(primaryAction({ id: ID, status: "superseded" })).toBeNull();
  });

  test("only a draft is editable", () => {
    expect(isDraft({ status: "draft" })).toBe(true);
    for (const s of ["under_review", "approved", "published", "superseded"]) {
      expect(isDraft({ status: s })).toBe(false);
    }
  });
});

describe("needsControlsWarning", () => {
  test("warns on the way to publishing with no linked control", () => {
    for (const status of ["draft", "under_review", "approved"]) {
      expect(needsControlsWarning({ status, linked_control_ids: [] })).toBe(
        true,
      );
      expect(needsControlsWarning({ status, linked_control_ids: ["c1"] })).toBe(
        false,
      );
    }
  });

  test("a published or superseded policy is past the check", () => {
    for (const status of ["published", "superseded"]) {
      expect(needsControlsWarning({ status, linked_control_ids: [] })).toBe(
        false,
      );
    }
  });
});

describe("validatePolicyForm", () => {
  test("names every missing required field", () => {
    const errs = validatePolicyForm({ ...EMPTY_FORM, version: " " }, "create");
    expect(Object.keys(errs).sort()).toEqual([
      "approver_role",
      "body_md",
      "owner_role",
      "title",
      "version",
    ]);
  });

  test("an edit does not check the version it cannot change", () => {
    const v = { ...formFromPolicy(policy(), "edit"), version: "" };
    expect(validatePolicyForm(v, "edit")).toEqual({});
  });

  test("no linked control is not an error: the draft saves", () => {
    const v = { ...formFromPolicy(policy(), "edit"), linked_control_ids: [] };
    expect(validatePolicyForm(v, "create")).toEqual({});
  });
});

describe("new version", () => {
  test("starts from the published policy with the version bumped", () => {
    const v = formFromPolicy(policy({ version: "1.4" }), "new-version");
    expect(v.version).toBe("1.5");
    expect(v.title).toBe("Access Control Policy");
    expect(v.linked_control_ids).toEqual(["c1"]);
    expect(createInput(v, ID)).toMatchObject({
      version: "1.5",
      predecessor_id: ID,
    });
  });

  test("a first version names no predecessor", () => {
    expect(createInput({ ...EMPTY_FORM, title: " T " })).not.toHaveProperty(
      "predecessor_id",
    );
    expect(createInput({ ...EMPTY_FORM, title: " T " }).title).toBe("T");
  });

  test.each([
    ["1.0", "1.1"],
    ["1.09", "1.10"],
    ["v2", "v3"],
    ["2026-rc1", "2026-rc2"],
    ["draft", "draft.1"],
    ["", "1.0"],
  ])("nextVersion(%j) = %j", (from, to) => {
    expect(nextVersion(from)).toBe(to);
  });
});

// #2524 — a policy's audience is the roles plus everyone; a free-text value
// from before shows flagged, may stay on an edit, and blocks a create.
describe("audience", () => {
  test("options are the roles plus everyone", () => {
    expect(AUDIENCE_OPTIONS).toEqual([
      "admin",
      "grc_engineer",
      "control_owner",
      "auditor",
      "viewer",
      "everyone",
    ]);
    expect(audienceLabel("everyone")).toBe("Everyone (all members)");
    expect(audienceLabel("viewer")).toBe("viewer");
  });

  test("free-text values are unrecognised", () => {
    expect(unrecognisedAudiences(["viewer", "employee", "everyone"])).toEqual([
      "employee",
    ]);
  });

  test("a create with a free-text audience is refused; an edit keeps it", () => {
    const v = {
      ...EMPTY_FORM,
      title: "T",
      owner_role: "o",
      approver_role: "a",
      body_md: "b",
      acknowledgment_required_roles: ["employee"],
    };
    expect(
      validatePolicyForm(v, "create").acknowledgment_required_roles,
    ).toContain("employee");
    expect(
      validatePolicyForm(v, "edit").acknowledgment_required_roles,
    ).toBeUndefined();
    expect(
      validatePolicyForm(
        { ...v, acknowledgment_required_roles: ["everyone"] },
        "create",
      ).acknowledgment_required_roles,
    ).toBeUndefined();
  });
});
