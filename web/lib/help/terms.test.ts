// #2144 — the user term list's build checks.
//
// The fixture tests prove each check fails when it should (an unknown id,
// a computed id, a broken glossary anchor, a renamed glossary term). The
// "real tree" tests are the gate: they run the same checks over every
// source file in web/app and web/components and over the real GLOSSARY.md,
// so CI's `Frontend · vitest` job fails on any of those defects.

import { readdirSync, readFileSync } from "node:fs";
import { join, relative, resolve } from "node:path";
import { describe, expect, it } from "vitest";

import {
  checkGlossaryLinks,
  checkTermList,
  checkTermUsages,
  findTermUsages,
  githubSlug,
  glossarySections,
  type SourceFile,
} from "./term-checks";
import { getTerm, termHref, TERMS, type HelpTerm } from "./terms";

const FIXTURES = resolve(__dirname, "__fixtures__");
const WEB_ROOT = resolve(__dirname, "../..");
const REPO_ROOT = resolve(WEB_ROOT, "..");

const fixtureGlossary = readFileSync(join(FIXTURES, "glossary.md"), "utf8");
const fixturePage = readFileSync(
  join(FIXTURES, "page-with-terms.tsx.txt"),
  "utf8",
);

function term(over: Partial<HelpTerm> & { id: string }): HelpTerm {
  return {
    name: over.id,
    short: "A short definition.",
    long: "A longer definition.",
    ...over,
  };
}

const fixtureTerms: HelpTerm[] = [
  term({ id: "treatment", name: "Risk treatment" }),
  term({ id: "acceptance", name: "Acceptance status" }),
  term({ id: "audit-period", name: "Audit period" }),
];

describe("findTermUsages", () => {
  it("reads literal ids in each quoting style and flags computed ids", () => {
    const { ids, dynamic } = findTermUsages(fixturePage);
    expect(ids.map((u) => u.id)).toEqual([
      "treatment",
      "audit-period",
      "acceptance",
      "no-such-term",
    ]);
    expect(dynamic).toEqual([10]);
  });
});

describe("checkTermUsages (fixtures)", () => {
  it("fails on an unknown term id, naming the file and line", () => {
    const problems = checkTermUsages(
      [{ path: "page.tsx", source: fixturePage }],
      fixtureTerms,
    );
    expect(problems).toContain('page.tsx:9: unknown term id "no-such-term"');
  });

  it("fails on a computed id it cannot check", () => {
    const problems = checkTermUsages(
      [{ path: "page.tsx", source: fixturePage }],
      fixtureTerms,
    );
    expect(problems).toContain(
      "page.tsx:10: <Term> id must be a string literal so it can be checked",
    );
  });

  it("passes when every id is known", () => {
    const ok = '<Term id="treatment">x</Term> <Term id="acceptance">y</Term>';
    expect(
      checkTermUsages([{ path: "a.tsx", source: ok }], fixtureTerms),
    ).toEqual([]);
  });
});

describe("checkGlossaryLinks (fixtures)", () => {
  it("passes on a heading name and on a bold term", () => {
    expect(
      checkGlossaryLinks(
        [
          term({
            id: "treatment",
            name: "Risk treatment",
            glossary: "GLOSSARY.md#risk-treatment-1746",
          }),
          term({
            id: "acceptance",
            name: "Acceptance status",
            glossary: "GLOSSARY.md#risk-treatment-1746",
          }),
          term({
            id: "audit-period",
            name: "Audit period",
            glossary: "GLOSSARY.md#auditperiod-slice-028",
          }),
        ],
        fixtureGlossary,
      ),
    ).toEqual([]);
  });

  it("fails on a broken anchor", () => {
    const problems = checkGlossaryLinks(
      [term({ id: "treatment", glossary: "GLOSSARY.md#risk-treatment" })],
      fixtureGlossary,
    );
    expect(problems).toEqual([
      'term "treatment": glossary link "GLOSSARY.md#risk-treatment" matches no GLOSSARY.md heading',
    ]);
  });

  it("fails when the glossary entry uses a different name (renamed term)", () => {
    const problems = checkGlossaryLinks(
      [
        term({
          id: "acceptance",
          name: "Risk acceptance",
          glossary: "GLOSSARY.md#risk-treatment-1746",
        }),
      ],
      fixtureGlossary,
    );
    expect(problems).toHaveLength(1);
    expect(problems[0]).toContain('doesn\'t use the name "Risk acceptance"');
  });

  it("fails when the glossary heading is renamed under a term", () => {
    const renamed = fixtureGlossary.replace(
      "## Risk treatment (#1746)",
      "## Risk response (#1746)",
    );
    const problems = checkGlossaryLinks(
      [
        term({
          id: "treatment",
          name: "Risk treatment",
          glossary: "GLOSSARY.md#risk-treatment-1746",
        }),
      ],
      renamed,
    );
    expect(problems).toHaveLength(1);
    expect(problems[0]).toContain("matches no GLOSSARY.md heading");
  });

  it("ignores bold text inside fenced code", () => {
    const problems = checkGlossaryLinks(
      [
        term({
          id: "fenced",
          name: "Fenced term",
          glossary: "GLOSSARY.md#risk-treatment-1746",
        }),
      ],
      fixtureGlossary,
    );
    expect(problems).toHaveLength(1);
  });

  it("fails on a link that isn't GLOSSARY.md#anchor", () => {
    const problems = checkGlossaryLinks(
      [term({ id: "treatment", glossary: "docs/GLOSSARY.md" })],
      fixtureGlossary,
    );
    expect(problems[0]).toContain("must be GLOSSARY.md#<anchor>");
  });
});

describe("checkTermList", () => {
  it("flags duplicate ids, bad ids and over-long short definitions", () => {
    const problems = checkTermList([
      term({ id: "a" }),
      term({ id: "a" }),
      term({ id: "Bad_Id" }),
      term({ id: "long", short: "One. Two. Three." }),
    ]);
    expect(problems).toEqual([
      'term "a": duplicate id',
      'term "Bad_Id": id must be lowercase kebab-case',
      'term "long": short definition has 3 sentences (at most 2)',
    ]);
  });
});

describe("githubSlug", () => {
  it("matches GitHub's heading anchors", () => {
    expect(githubSlug("Risk treatment (#1746)")).toBe("risk-treatment-1746");
    expect(githubSlug("Evidence freshness + drift (slice 016)")).toBe(
      "evidence-freshness--drift-slice-016",
    );
    expect(githubSlug("`super_admin` role (slice 142, in design)")).toBe(
      "super_admin-role-slice-142-in-design",
    );
  });
});

// ---- the gate: the real term list, the real UI, the real glossary ----

function sourceFiles(dir: string): SourceFile[] {
  const out: SourceFile[] = [];
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    const p = join(dir, entry.name);
    if (entry.isDirectory()) {
      if (entry.name === "node_modules" || entry.name === "__fixtures__") {
        continue;
      }
      out.push(...sourceFiles(p));
    } else if (
      /\.tsx?$/.test(entry.name) &&
      !/\.test\.tsx?$/.test(entry.name)
    ) {
      out.push({
        path: relative(WEB_ROOT, p),
        source: readFileSync(p, "utf8"),
      });
    }
  }
  return out;
}

describe("the real term list", () => {
  const uiFiles = [
    ...sourceFiles(join(WEB_ROOT, "app")),
    ...sourceFiles(join(WEB_ROOT, "components")),
  ];

  it("is well formed", () => {
    expect(checkTermList(TERMS)).toEqual([]);
  });

  it("defines every <Term> id used in web/app and web/components", () => {
    expect(checkTermUsages(uiFiles, TERMS)).toEqual([]);
  });

  it("is used: the first adoption points carry <Term>", () => {
    const used = new Set(
      uiFiles.flatMap((f) => findTermUsages(f.source).ids).map((u) => u.id),
    );
    for (const id of ["posture", "residual-risk", "evidence-freshness"]) {
      expect(used).toContain(id);
    }
  });

  it("links only to GLOSSARY.md entries that exist and use the same name", () => {
    const glossary = readFileSync(join(REPO_ROOT, "GLOSSARY.md"), "utf8");
    expect(checkGlossaryLinks(TERMS, glossary)).toEqual([]);
    // At least one link, so this check is not vacuous.
    expect(TERMS.some((t) => t.glossary)).toBe(true);
    expect(glossarySections(glossary).size).toBeGreaterThan(0);
  });

  it("resolves ids and builds the Terms page link", () => {
    expect(getTerm("posture")?.name).toBe("Posture");
    expect(getTerm("nope")).toBeUndefined();
    expect(termHref("posture")).toBe(
      "https://mgoodric.github.io/security-atlas/terms/#posture",
    );
  });
});
