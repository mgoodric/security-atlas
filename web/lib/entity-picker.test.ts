import { describe, expect, test } from "vitest";

import {
  controlOptions,
  credentialOptions,
  evidenceOptions,
  evidencePickerOptions,
  evidenceSearchOptions,
  frameworkOptions,
  matchesQuery,
  notListedError,
  parsePastedId,
  policyOptions,
  riskOptions,
  toggleSelection,
  uniqueOptions,
  machineActorOptions,
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

  // #2414: /v1/people sends a user with no display name under their
  // email's local-part (people.DisplayName), so they are listed by it.
  test("lists a user without a display name by the local-part /v1/people sends", () => {
    expect(
      userOptions([{ id: U1, display_name: "jdoe", status: "active" }]),
    ).toEqual([{ value: U1, label: "jdoe" }]);
  });
});

describe("machineActorOptions", () => {
  test("labels system actors and OAuth clients as <Person> does, the ID as the hint", () => {
    expect(
      machineActorOptions([
        "system:scheduler",
        "oauth_client:ci",
        "system",
        "system:scheduler",
        U1,
        "key_abc",
        "",
      ]),
    ).toEqual([
      { value: "oauth_client:ci", label: "OAuth client", hint: "ci" },
      { value: "system", label: "System", hint: undefined },
      { value: "system:scheduler", label: "System", hint: "scheduler" },
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

  test("matches keywords the list does not show", () => {
    const control = { value: U1, label: "MFA", keywords: ["soc2-starter"] };
    expect(matchesQuery(control, "starter")).toBe(true);
    expect(matchesQuery(control, "iso")).toBe(false);
  });
});

describe("controlOptions / riskOptions / policyOptions", () => {
  test("a control is its title, with SCF code and family, bundle searchable", () => {
    expect(
      controlOptions([
        {
          id: U1,
          title: "Multi-factor authentication",
          control_family: "IAC",
          scf_id: "IAC-06",
          lifecycle_state: "active",
          bundle_id: "soc2-starter",
        },
        {
          id: U2,
          title: "Custom control",
          control_family: "",
          scf_id: "",
          lifecycle_state: "active",
          bundle_id: "",
        },
      ]),
    ).toEqual([
      {
        value: U1,
        label: "Multi-factor authentication",
        kind: "control",
        hint: "IAC-06 · IAC",
        keywords: ["soc2-starter"],
      },
      {
        value: U2,
        label: "Custom control",
        kind: "control",
        hint: undefined,
        keywords: undefined,
      },
    ]);
  });

  test("a risk is its title, with the category name as the hint", () => {
    expect(
      riskOptions([
        {
          id: U1,
          title: "Stale access",
          category: "ops",
          category_name: "Operational",
        },
        { id: U2, title: "Vendor", category: "third_party" },
      ]),
    ).toEqual([
      { value: U1, label: "Stale access", kind: "risk", hint: "Operational" },
      { value: U2, label: "Vendor", kind: "risk", hint: "third_party" },
    ]);
  });

  test("a policy is its title, with version and status as the hint", () => {
    const [p] = policyOptions([
      { id: U1, title: "Access policy", version: "2", status: "published" },
    ]);
    expect(p.value).toBe(U1);
    expect(p.label).toBe("Access policy");
    expect(p.hint).toMatch(/^v2 · /);
    expect(p.kind).toBe("policy");
  });
});

describe("evidenceOptions (#2432)", () => {
  test("a record is its kind and observed day, with the result labelled", () => {
    expect(
      evidenceOptions([
        {
          evidence_id: U1.toUpperCase(),
          evidence_kind: "okta.mfa_policy",
          observed_at: "2026-11-01T23:30:00Z",
          result: "na",
        },
      ]),
    ).toEqual([
      {
        value: U1,
        label: "okta.mfa_policy · 2026-11-01",
        kind: "evidence",
        hint: "Not applicable",
      },
    ]);
  });

  test("a record with no kind still has a label, never the raw ID", () => {
    const [o] = evidenceOptions([
      {
        evidence_id: U2,
        evidence_kind: null,
        observed_at: "2026-11-01T00:00:00Z",
        result: "pass",
      },
    ]);
    expect(o.label).toMatch(/^Evidence · /);
    expect(o.label).not.toContain(U2);
    expect(o.hint).toBe("Pass");
  });

  test("a pasted evidence ID parses; a list-only picker names both kinds", () => {
    expect(parsePastedId(["evidence"], U1)).toEqual({ ok: true, id: U1 });
    expect(notListedError(["control", "evidence"], ` ${U1} `)).toBe(
      `"${U1}" is not in the list. Pick a control or evidence record from the list.`,
    );
  });
});

describe("evidenceSearchOptions / uniqueOptions (#2432)", () => {
  test("a search hit is labelled by its title and kept by the query that found it", () => {
    const [o] = evidenceSearchOptions(
      [{ id: U1.toUpperCase(), title: "legacy.backup · BCD-11" }],
      " legacy backup ",
    );
    expect(o).toEqual({
      value: U1,
      label: "legacy.backup · BCD-11",
      kind: "evidence",
      keywords: ["legacy backup"],
    });
    // The server matched it per token; the client filter keeps it.
    expect(matchesQuery(o, "legacy backup")).toBe(true);
  });

  test("a refusal drops cached evidence, listed and found alike", () => {
    // A query keeps its last data after a failed refetch: the titles
    // cached before the 403 are still there, and must not be offered.
    const cached = {
      listed: [
        {
          evidence_id: U1,
          evidence_kind: "okta.mfa_policy",
          observed_at: "2026-11-01T00:00:00Z",
          result: "pass" as const,
        },
      ],
      found: [{ id: U2, title: "legacy.backup · BCD-11" }],
      searchFor: "legacy",
    };
    const offered = evidencePickerOptions({ ...cached, refused: false });
    expect(offered.map((o) => o.value)).toEqual([U1, U2]);
    expect(evidencePickerOptions({ ...cached, refused: true })).toEqual([]);
  });

  test("a record both listed and found appears once, the first kept", () => {
    expect(
      uniqueOptions([
        { value: U1, label: "listed" },
        { value: U2, label: "other" },
        { value: U1, label: "found" },
      ]),
    ).toEqual([
      { value: U1, label: "listed" },
      { value: U2, label: "other" },
    ]);
  });
});

describe("toggleSelection", () => {
  test("adds in pick order and removes", () => {
    expect(toggleSelection([], U1)).toEqual([U1]);
    expect(toggleSelection([U1], U2)).toEqual([U1, U2]);
    expect(toggleSelection([U1, U2], U1)).toEqual([U2]);
  });

  test("refuses an add at the cap but always allows a removal", () => {
    expect(toggleSelection([U1], U2, 1)).toEqual([U1]);
    expect(toggleSelection([U1], U1, 1)).toEqual([]);
  });
});
