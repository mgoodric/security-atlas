// #2143 — module help: front-matter parsing, route mapping, and the build
// checks over the real help files in web/help/modules/.

import { existsSync, readdirSync, readFileSync } from "node:fs";
import { join, resolve } from "node:path";
import { describe, expect, it } from "vitest";

import { findDevNote } from "../../eslint-rules/no-dev-notes-in-ui.mjs";
import {
  HELP_MODULES,
  HELP_SOURCES,
  helpDocsHref,
  helpModuleForPath,
  parseHelpModule,
  type HelpModule,
} from "./modules";
import { getTerm } from "./terms";

const WEB_ROOT = resolve(__dirname, "../..");
const HELP_DIR = join(WEB_ROOT, "help", "modules");
const DIAGRAM_DIR = join(WEB_ROOT, "public", "help", "diagrams");

const FULL = `---
id: widgets
title: Widgets
routes: [/widgets, "/widgets/archive"]
terms: [anchor]
diagrams:
  - src: widget-states.svg
    alt: Widgets move from draft to live
  - src: other.svg
    alt: 'Second diagram'
---

Body **text**.
`;

describe("parseHelpModule", () => {
  it("reads every front-matter field and the body", () => {
    expect(parseHelpModule(FULL)).toEqual({
      id: "widgets",
      title: "Widgets",
      routes: ["/widgets", "/widgets/archive"],
      terms: ["anchor"],
      diagrams: [
        { src: "widget-states.svg", alt: "Widgets move from draft to live" },
        { src: "other.svg", alt: "Second diagram" },
      ],
      lifecycles: [],
      body: "Body **text**.",
    });
  });

  it("reads a flow list Prettier wrapped onto the next line", () => {
    const m = parseHelpModule(
      "---\nid: a\ntitle: A\nterms:\n  [anchor, crosswalk]\n---\nx",
    );
    expect(m.terms).toEqual(["anchor", "crosswalk"]);
  });

  it("defaults the optional lists to empty", () => {
    const m = parseHelpModule("---\nid: a\ntitle: A\ndiagrams: []\n---\nx");
    expect(m.routes).toEqual([]);
    expect(m.terms).toEqual([]);
    expect(m.diagrams).toEqual([]);
  });

  it.each([
    ["no opening fence", "id: a\n---\n"],
    ["no closing fence", "---\nid: a\ntitle: A\n"],
    ["a missing title", "---\nid: a\n---\n"],
    ["an unknown key", "---\nid: a\ntitle: A\nowner: me\n---\n"],
    ["a repeated key", "---\nid: a\nid: b\ntitle: A\n---\n"],
    ["a scalar where a list goes", "---\nid: a\ntitle: A\nroutes: /a\n---\n"],
    [
      "a diagram without alt text",
      "---\nid: a\ntitle: A\ndiagrams:\n  - src: a.svg\n---\n",
    ],
    [
      "a diagram path that leaves the diagrams directory",
      "---\nid: a\ntitle: A\ndiagrams:\n  - src: ../x.svg\n    alt: X\n---\n",
    ],
    [
      "a diagram that is not an svg or png",
      "---\nid: a\ntitle: A\ndiagrams:\n  - src: x.html\n    alt: X\n---\n",
    ],
    ["a flow-list diagram", "---\nid: a\ntitle: A\ndiagrams: [a.svg]\n---\n"],
    ["a line it cannot read", "---\nid: a\ntitle: A\n  stray\n---\n"],
  ])("rejects %s", (_name, source) => {
    expect(() => parseHelpModule(source)).toThrow();
  });
});

describe("helpModuleForPath", () => {
  const mods: HelpModule[] = [
    parseHelpModule("---\nid: a\ntitle: A\nroutes: [/things]\n---\n"),
    parseHelpModule("---\nid: b\ntitle: B\nroutes: [/things/special]\n---\n"),
  ];

  it("matches the route and pages below it", () => {
    expect(helpModuleForPath("/things", mods)?.id).toBe("a");
    expect(helpModuleForPath("/things/123", mods)?.id).toBe("a");
  });

  it("prefers the longest matching route", () => {
    expect(helpModuleForPath("/things/special", mods)?.id).toBe("b");
    expect(helpModuleForPath("/things/special/1", mods)?.id).toBe("b");
  });

  it("does not match a route that only shares a prefix", () => {
    expect(helpModuleForPath("/thingsmith", mods)).toBeUndefined();
  });

  it("returns nothing for a page no module covers", () => {
    expect(helpModuleForPath("/elsewhere", mods)).toBeUndefined();
  });
});

describe("the real help files", () => {
  const files = readdirSync(HELP_DIR).filter((f) => f.endsWith(".md"));

  it("registers every file in web/help/modules", () => {
    expect(Object.keys(HELP_SOURCES).sort()).toEqual(files.sort());
  });

  it("names each module after its file", () => {
    for (const file of files) {
      const mod = parseHelpModule(readFileSync(join(HELP_DIR, file), "utf8"));
      expect(`${mod.id}.md`).toBe(file);
    }
  });

  it("references only terms that exist in terms.json", () => {
    const unknown = HELP_MODULES.flatMap((m) =>
      m.terms.filter((t) => !getTerm(t)).map((t) => `${m.id}: ${t}`),
    );
    expect(unknown).toEqual([]);
  });

  it("claims each route at most once", () => {
    const routes = HELP_MODULES.flatMap((m) => m.routes);
    expect(routes.length).toBe(new Set(routes).size);
    for (const r of routes) expect(r).toMatch(/^\/[a-z0-9/-]*$/);
  });

  it("points every diagram at a file in public/help/diagrams", () => {
    const missing = HELP_MODULES.flatMap((m) =>
      m.diagrams
        .filter((d) => !existsSync(join(DIAGRAM_DIR, d.src)))
        .map((d) => `${m.id}: ${d.src}`),
    );
    expect(missing).toEqual([]);
  });

  it("uses only absolute links, which the Markdown renderer keeps", () => {
    for (const m of HELP_MODULES) {
      const links = [...m.body.matchAll(/\]\(([^)\s]+)/g)].map((x) => x[1]);
      for (const href of links) expect(href).toMatch(/^https:\/\//);
    }
  });

  it("carries no developer notes", () => {
    for (const m of HELP_MODULES) {
      expect(findDevNote(`${m.title}\n${m.body}`)).toBeNull();
    }
  });

  it("covers Controls, and leaves an unmapped page without help", () => {
    expect(helpModuleForPath("/controls")?.id).toBe("controls");
    expect(helpModuleForPath("/no-such-page")).toBeUndefined();
    expect(helpDocsHref("controls")).toBe(
      "https://mgoodric.github.io/security-atlas/help/controls/",
    );
  });

  // #2155 — every Settings & admin page resolves to its own module.
  it.each([
    ["/settings", "settings"],
    ["/admin", "admin"],
    ["/admin/users", "admin-users"],
    ["/admin/api-keys", "admin-api-keys"],
    ["/admin/sso", "admin-sso"],
    ["/admin/features", "admin-features"],
    ["/admin/tenants", "admin-tenants"],
    ["/admin/super-admins", "admin-super-admins"],
    ["/admin/demo", "admin-demo"],
  ])("covers %s with the %s help", (route, id) => {
    expect(helpModuleForPath(route)?.id).toBe(id);
  });

  // #2666 — since #2503 a tenant admin manages SSO, Users, API keys and
  // Features for its own tenant; Tenants, Super admins and Demo stay
  // super-admin only. Each page's "Who can do this" says which.
  it.each([
    ["admin-sso", true],
    ["admin-users", true],
    ["admin-api-keys", true],
    ["admin-features", true],
    ["admin-tenants", false],
    ["admin-super-admins", false],
    ["admin-demo", false],
  ])("%s says whether a tenant admin can do it", (id, tenantAdmin) => {
    const body = HELP_MODULES.find((m) => m.id === id)?.body ?? "";
    const who = body.split("## Who can do this")[1] ?? "";
    expect(who).not.toBe("");
    if (tenantAdmin) {
      expect(who).toContain("An admin of the tenant, or a super admin");
      expect(who).not.toMatch(/Only super admins/);
    } else {
      expect(who.trim()).toBe("Only super admins.");
    }
  });

  // #2152 — every Audit page resolves to its own module.
  it.each([
    ["/audits", "audits"],
    ["/audits/new", "audits"],
    ["/audits/abc", "audits"],
    ["/framework-scopes", "framework-scopes"],
    ["/framework-scopes/new", "framework-scopes"],
    ["/board-packs", "board-packs"],
    ["/board-packs/abc", "board-packs"],
    ["/csf/abc", "csf"],
    ["/oscal/component-definitions", "vendor-claims"],
    ["/oscal/component-definitions/abc", "vendor-claims"],
  ])("covers %s with the %s help", (route, id) => {
    expect(helpModuleForPath(route)?.id).toBe(id);
  });

  // #2152 — the audit-period and framework-scope lifecycles are embedded.
  it.each([
    ["audits", "audit_period"],
    ["framework-scopes", "framework_scope"],
  ])("embeds the %s lifecycle diagram", (id, lifecycle) => {
    const mod = HELP_MODULES.find((m) => m.id === id);
    expect(mod?.lifecycles.map((l) => l.lifecycle)).toEqual([lifecycle]);
  });

  // #2154 — every Library page resolves to its own module.
  it.each([
    ["/policies", "policies"],
    ["/policies/new", "policies"],
    ["/policies/abc", "policies"],
    ["/policies/abc/edit", "policies"],
    ["/policies/abc/publish", "policies"],
    ["/vendors", "vendors"],
    ["/vendors/new", "vendors"],
    ["/vendors/abc/reviews/new", "vendors"],
    ["/personnel-security", "personnel-security"],
    ["/personnel-security/new", "personnel-security"],
    ["/personnel-security/abc", "personnel-security"],
    ["/questionnaires", "questionnaires"],
    ["/questionnaires/abc", "questionnaires"],
    ["/catalog/scf", "scf-catalog"],
    ["/catalog/scf/abc", "scf-catalog"],
    ["/catalog/audited-under", "scf-catalog"],
  ])("covers %s with the %s help", (route, id) => {
    expect(helpModuleForPath(route)?.id).toBe(id);
  });

  // #2154 — the policy lifecycle is embedded.
  it("embeds the policy lifecycle diagram", () => {
    const mod = HELP_MODULES.find((m) => m.id === "policies");
    expect(mod?.lifecycles.map((l) => l.lifecycle)).toEqual(["policy"]);
  });

  // #2150 — every Controls page resolves to its own module; the
  // checklist claims its deeper route from the Controls help.
  it.each([
    ["/controls", "controls"],
    ["/controls/abc", "controls"],
    ["/controls/abc/attest", "controls"],
    ["/controls/imports", "controls"],
    ["/controls/checklist", "controls-checklist"],
    ["/evidence", "evidence"],
    ["/evidence/abc", "evidence"],
    ["/exceptions", "exceptions"],
    ["/exceptions/new", "exceptions"],
    ["/exceptions/abc", "exceptions"],
    ["/exceptions/abc/revoke", "exceptions"],
  ])("covers %s with the %s help", (route, id) => {
    expect(helpModuleForPath(route)?.id).toBe(id);
  });

  // #2150 — control state on Controls; the exception lifecycle moved from
  // Controls to Exceptions.
  it.each([
    ["controls", "control_state"],
    ["exceptions", "exception"],
  ])("embeds the %s lifecycle diagram", (id, lifecycle) => {
    const mod = HELP_MODULES.find((m) => m.id === id);
    expect(mod?.lifecycles.map((l) => l.lifecycle)).toEqual([lifecycle]);
  });

  // #2151 — every Risks page resolves to its own module; the hierarchy
  // claims its deeper route from the register, and the taxonomy and each
  // risk's pages stay on the register.
  it.each([
    ["/risks", "risks"],
    ["/risks/new", "risks"],
    ["/risks/taxonomy", "risks"],
    ["/risks/abc", "risks"],
    ["/risks/abc/treatment", "risks"],
    ["/risks/abc/acceptance/approve", "risks"],
    ["/risks/abc/close", "risks"],
    ["/risks/hierarchy", "risks-hierarchy"],
    ["/action-plans", "action-plans"],
    ["/action-plans/new", "action-plans"],
    ["/action-plans/abc", "action-plans"],
    ["/action-plans/abc/edit", "action-plans"],
  ])("covers %s with the %s help", (route, id) => {
    expect(helpModuleForPath(route)?.id).toBe(id);
  });

  // #2151 — the register shows both risk lifecycles; action plans its own.
  it.each([
    ["risks", ["risk_treatment", "risk_status"]],
    ["risks-hierarchy", []],
    ["action-plans", ["action_plan"]],
  ])("embeds the %s lifecycle diagrams", (id, lifecycles) => {
    const mod = HELP_MODULES.find((m) => m.id === id);
    expect(mod?.lifecycles.map((l) => l.lifecycle)).toEqual(lifecycles);
  });

  // #2149 — every Program page resolves to its own module; the full
  // needs-attention list stays on the dashboard help, and a metric's page on
  // the metrics help.
  it.each([
    ["/dashboard", "dashboard"],
    ["/needs-attention", "dashboard"],
    ["/approvals", "approvals"],
    ["/calendar", "calendar"],
    ["/dashboards/metrics", "metrics"],
    ["/dashboards/metrics/abc", "metrics"],
    ["/activity", "activity"],
  ])("covers %s with the %s help", (route, id) => {
    expect(helpModuleForPath(route)?.id).toBe(id);
  });

  // #2149 — no Program page owns an exported lifecycle, so none embeds one.
  it.each(["dashboard", "approvals", "calendar", "metrics", "activity"])(
    "embeds no lifecycle diagram in the %s help",
    (id) => {
      const mod = HELP_MODULES.find((m) => m.id === id);
      expect(mod).toBeDefined();
      expect(mod?.lifecycles).toEqual([]);
    },
  );

  // #2592 — the audit workspace (with a control open) and a requirement's
  // page each resolve to their own module; /audit claims neither /audits
  // nor /audit-log, the redirect the activity help covers once it lands.
  it.each([
    ["/audit", "audit-workspace"],
    ["/audit/abc", "audit-workspace"],
    ["/requirements/abc", "requirements"],
    ["/audits", "audits"],
  ])("covers %s with the %s help", (route, id) => {
    expect(helpModuleForPath(route)?.id).toBe(id);
  });

  it("does not cover /audit-log with the audit workspace help", () => {
    expect(helpModuleForPath("/audit-log")).toBeUndefined();
  });

  // #2592 — neither page owns an exported lifecycle.
  it.each(["audit-workspace", "requirements"])(
    "embeds no lifecycle diagram in the %s help",
    (id) => {
      const mod = HELP_MODULES.find((m) => m.id === id);
      expect(mod).toBeDefined();
      expect(mod?.lifecycles).toEqual([]);
    },
  );

  // #2145 — a module's hint dismissal is refused unless its id is in
  // userprefs.HintModules and the migration's CHECK, so the three lists
  // move together.
  it("lists every module in the hint allow-list and its CHECK", () => {
    const ids = HELP_MODULES.map((m) => m.id).sort();
    const goSrc = readFileSync(
      join(WEB_ROOT, "..", "internal", "auth", "userprefs", "hints.go"),
      "utf8",
    );
    const goList = /var HintModules = \[\]string\{([\s\S]*?)\n\}/.exec(goSrc);
    expect(goList).not.toBeNull();
    const goIds = [...goList![1].matchAll(/"([a-z0-9-]+)"/g)].map((x) => x[1]);
    expect(goIds.sort()).toEqual(ids);

    const migrations = readdirSync(join(WEB_ROOT, "..", "migrations", "sql"))
      .filter((f) => f.endsWith(".sql") && !f.endsWith(".down.sql"))
      .sort();
    const latestCheck = migrations
      .map((f) =>
        readFileSync(join(WEB_ROOT, "..", "migrations", "sql", f), "utf8"),
      )
      .map((sql) =>
        /user_onboarding_hint_dismissals_module_check\s+CHECK\s*\(\s*module IN \(([^)]*)\)/.exec(
          sql,
        ),
      )
      .filter((m) => m !== null)
      .pop();
    expect(latestCheck).toBeDefined();
    const sqlIds = [...latestCheck![1].matchAll(/'([a-z0-9-]+)'/g)].map(
      (x) => x[1],
    );
    expect(sqlIds.sort()).toEqual(ids);
  });
});
