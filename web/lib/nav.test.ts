// #2081 — the grouped sidebar: groups, labels, and the gates per role,
// flag and catalog.

import { describe, expect, test } from "vitest";

import { csfVersionFromFrameworks } from "@/lib/api/self.server";
import { buildNav, NAV_GROUPS, type NavFacts } from "@/lib/nav";
import { PAGE_TITLES } from "@/lib/page-titles";

const ALL_ON = { "board.reporting": true, "oscal.export": true };
const CSF = "11111111-1111-4111-8111-1111111c5f20";

// The bodies `/v1/me` returns for each role. Only a super-admin passes
// shouldShowAdminEntry (#2216).
const ME = {
  superAdmin: { is_admin: false, roles: ["super_admin"] },
  admin: { is_admin: false, roles: ["admin", "grc_engineer"] },
  viewer: { is_admin: false, roles: ["viewer"] },
  auditor: { is_admin: false, roles: ["auditor"] },
  unknown: {},
};

function facts(over: Partial<NavFacts>): NavFacts {
  return { me: ME.viewer, modules: {}, csfVersionId: null, ...over };
}

function labels(f: NavFacts): string[] {
  return buildNav(f).flatMap((g) => g.items.map((i) => i.label));
}

describe("buildNav — groups", () => {
  test("lists every page in its group, in order, with everything on", () => {
    const nav = buildNav(
      facts({ me: ME.superAdmin, modules: ALL_ON, csfVersionId: CSF }),
    );
    expect(nav.map((g) => [g.label, g.items.map((i) => i.label)])).toEqual([
      ["Program", ["Dashboard", "Calendar", "Metrics", "Activity"]],
      [
        "Controls",
        ["Controls", "Evidence", "Exceptions", "Implementation checklist"],
      ],
      [
        "Risks",
        ["Risk register", "Risk hierarchy", "Risk taxonomy", "Action plans"],
      ],
      [
        "Audit",
        [
          "Audit periods",
          "Framework scopes",
          "Board packs",
          "CSF assessment",
          "Vendor claims",
        ],
      ],
      [
        "Library",
        [
          "Policies",
          "Vendors",
          "Personnel security",
          "Questionnaires",
          "SCF catalog",
        ],
      ],
      ["Settings & admin", ["Settings", "Admin"]],
    ]);
  });

  test("every entry's label is the title of the route it opens", () => {
    const nav = buildNav(
      facts({ me: ME.superAdmin, modules: ALL_ON, csfVersionId: CSF }),
    );
    for (const item of nav.flatMap((g) => g.items)) {
      expect(item.label).toBe(PAGE_TITLES[item.route]);
    }
  });

  test("every titled route is in exactly one group", () => {
    const routes = NAV_GROUPS.flatMap((g) => g.routes);
    expect(new Set(routes).size).toBe(routes.length);
    expect([...routes].sort()).toEqual(Object.keys(PAGE_TITLES).sort());
  });

  test("CSF assessment links to the loaded framework version", () => {
    const csf = buildNav(facts({ csfVersionId: CSF }))
      .flatMap((g) => g.items)
      .find((i) => i.route === "/csf");
    expect(csf?.href).toBe(`/csf/${CSF}`);
  });
});

describe("buildNav — gates", () => {
  test("flags off: Board packs and Vendor claims are not listed", () => {
    const l = labels(facts({ modules: {} }));
    expect(l).not.toContain("Board packs");
    expect(l).not.toContain("Vendor claims");
  });

  test("each flag shows only its own entry", () => {
    const board = labels(facts({ modules: { "board.reporting": true } }));
    expect(board).toContain("Board packs");
    expect(board).not.toContain("Vendor claims");
    const oscal = labels(facts({ modules: { "oscal.export": true } }));
    expect(oscal).toContain("Vendor claims");
    expect(oscal).not.toContain("Board packs");
  });

  test("no CSF framework loaded: CSF assessment is not listed", () => {
    expect(labels(facts({ csfVersionId: null }))).not.toContain(
      "CSF assessment",
    );
  });

  test("Admin is listed for a super-admin only", () => {
    expect(labels(facts({ me: ME.superAdmin }))).toContain("Admin");
    expect(labels(facts({ me: ME.admin }))).not.toContain("Admin");
    expect(labels(facts({ me: ME.unknown }))).not.toContain("Admin");
  });

  // The restructure must not show a viewer or an auditor anything the
  // flat pre-#2081 nav hid: no Admin, no flag-gated entry with its flag
  // off. These are the entries such a caller saw before, under the new
  // labels, plus the four pages that were URL-only (Exceptions,
  // Implementation checklist, Risk hierarchy, Framework scopes).
  for (const role of ["viewer", "auditor"] as const) {
    test(`${role}: no Admin and no gated entry with flags off`, () => {
      expect(labels(facts({ me: ME[role], modules: {} }))).toEqual([
        "Dashboard",
        "Calendar",
        "Metrics",
        "Activity",
        "Controls",
        "Evidence",
        "Exceptions",
        "Implementation checklist",
        "Risk register",
        "Risk hierarchy",
        "Risk taxonomy",
        "Action plans",
        "Audit periods",
        "Framework scopes",
        "Policies",
        "Vendors",
        "Personnel security",
        "Questionnaires",
        "SCF catalog",
        "Settings",
      ]);
    });

    test(`${role}: flags on add the gated entries but never Admin`, () => {
      const l = labels(
        facts({ me: ME[role], modules: ALL_ON, csfVersionId: CSF }),
      );
      expect(l).toContain("Board packs");
      expect(l).toContain("Vendor claims");
      expect(l).toContain("CSF assessment");
      expect(l).not.toContain("Admin");
    });
  }
});

describe("csfVersionFromFrameworks", () => {
  test("returns the nist_csf framework's latest version", () => {
    expect(
      csfVersionFromFrameworks({
        frameworks: [
          { slug: "scf", latest_version_id: "scf-v" },
          { slug: "nist_csf", latest_version_id: CSF },
        ],
      }),
    ).toBe(CSF);
  });

  test.each([
    ["no nist_csf framework", { frameworks: [{ slug: "iso27001" }] }],
    [
      "nist_csf with no version",
      { frameworks: [{ slug: "nist_csf", latest_version_id: "" }] },
    ],
    ["a malformed body", { frameworks: "nope" }],
    ["null", null],
  ])("returns null for %s", (_, body) => {
    expect(csfVersionFromFrameworks(body)).toBeNull();
  });
});
