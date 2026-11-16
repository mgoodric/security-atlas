// #2148 — the diagram check.
//
// Every lifecycle diagram embedded in a help file must draw exactly the
// lifecycle the backend exports to docs/lifecycles/<name>.json (`just
// lifecycles-export`): the same states, initial and terminal states, and
// transitions with their actions and system actor. Each one must also have
// a current, clean light and dark SVG under public/help/diagrams/. This
// runs in the `Frontend · vitest` job, which merge-gate needs.

import { existsSync, readdirSync, readFileSync } from "node:fs";
import { createRequire } from "node:module";
import { join, resolve } from "node:path";
import { describe, expect, it } from "vitest";

import {
  diagramStamp,
  readStamp,
  readThemeTokens,
  sanitizeSvg,
  unsafeSvgParts,
  withStamp,
} from "./diagram-build";
import {
  describeLifecycle,
  diffLifecycle,
  extractLifecycleBlocks,
  lifecycleDiagramFile,
  parseStateDiagram,
  type LifecycleExport,
} from "./lifecycle-diagram";
import { HELP_MODULES } from "./modules";

const WEB_ROOT = resolve(__dirname, "../..");
const LIFECYCLES = resolve(WEB_ROOT, "..", "docs", "lifecycles");
const DIAGRAMS = join(WEB_ROOT, "public", "help", "diagrams");
const FIXTURES = join(__dirname, "__fixtures__", "lifecycles");
const require = createRequire(import.meta.url);

function lifecycle(name: string): LifecycleExport {
  return JSON.parse(
    readFileSync(join(LIFECYCLES, `${name}.json`), "utf8"),
  ) as LifecycleExport;
}

function fixture(name: string) {
  return parseStateDiagram(readFileSync(join(FIXTURES, name), "utf8"));
}

describe("the diagram check, on fixtures", () => {
  const exception = lifecycle("exception");

  it("passes a diagram that draws the exported lifecycle", () => {
    expect(diffLifecycle(fixture("exception-correct.mmd"), exception)).toEqual(
      [],
    );
  });

  it("fails a diagram with a missing transition", () => {
    expect(
      diffLifecycle(fixture("exception-missing-transition.mmd"), exception),
    ).toEqual(["missing transition active --> revoked: revoke"]);
  });

  it("fails a diagram with an extra state", () => {
    expect(
      diffLifecycle(fixture("exception-extra-state.mmd"), exception),
    ).toEqual([
      "extra state on_hold",
      "extra transition active --> on_hold: pause",
    ]);
  });

  it("fails a system move drawn as a person's", () => {
    const d = fixture("exception-correct.mmd");
    const expire = d.transitions.find((t) => t.action === "expire")!;
    expire.actor = "person";
    expect(diffLifecycle(d, exception)).toEqual([
      'transition active --> expired: expire is made by the system, drawn as the person (label it "expire (system)")',
    ]);
  });

  it("fails a changed initial or terminal state", () => {
    const d = fixture("exception-correct.mmd");
    d.initial = [{ state: "approved", action: "request" }];
    d.terminal = d.terminal.filter((s) => s !== "denied");
    expect(diffLifecycle(d, exception)).toEqual([
      "missing initial [*] --> requested: request",
      "extra initial [*] --> approved: request",
      "missing terminal state denied",
    ]);
  });
});

describe("parseStateDiagram", () => {
  it("reads states, initial and terminal states, and transitions", () => {
    const d = parseStateDiagram(
      "stateDiagram-v2\n  direction LR\n  [*] --> a: make\n  a --> b_c: go\n  b_c --> a: tidy_up (system)\n  b_c --> [*]",
    );
    expect(d).toEqual({
      states: ["a", "b_c"],
      initial: [{ state: "a", action: "make" }],
      terminal: ["b_c"],
      transitions: [
        { from: "a", to: "b_c", action: "go", actor: "person" },
        { from: "b_c", to: "a", action: "tidy_up", actor: "system" },
      ],
    });
  });

  it.each([
    ["no header", "a --> b: go"],
    ["an empty diagram", "%% nothing"],
    ["a state declaration", "stateDiagram-v2\n  state a"],
    ["a note", "stateDiagram-v2\n  note right of a: hi"],
    ["a transition without an action", "stateDiagram-v2\n  a --> b"],
    ["a labelled edge to [*]", "stateDiagram-v2\n  a --> [*]: end"],
    ["a label that is not an action", "stateDiagram-v2\n  a --> b: Go now"],
    ["[*] to [*]", "stateDiagram-v2\n  [*] --> [*]"],
    ["a click directive", 'stateDiagram-v2\n  click a "javascript:alert(1)"'],
  ])("rejects %s", (_, src) => {
    expect(() => parseStateDiagram(src)).toThrow();
  });

  it("lists states and transitions as the text alternative", () => {
    expect(describeLifecycle(fixture("exception-correct.mmd"))).toEqual({
      states: [
        "requested (starts here on request)",
        "approved",
        "denied (final)",
        "active",
        "expired (final)",
        "revoked (final)",
      ],
      transitions: [
        "requested to approved: approve",
        "requested to denied: deny",
        "approved to active: activate",
        "active to expired: expire, done automatically by the system",
        "active to revoked: revoke",
      ],
    });
  });
});

describe("extractLifecycleBlocks", () => {
  it("lifts lifecycle blocks out of the prose", () => {
    const { body, blocks } = extractLifecycleBlocks(
      "Intro.\n\n```mermaid\n%% lifecycle: policy\nstateDiagram-v2\n```\n\nAfter.",
    );
    expect(body).toBe("Intro.\n\nAfter.");
    expect(blocks).toEqual([
      { lifecycle: "policy", source: "%% lifecycle: policy\nstateDiagram-v2" },
    ]);
  });

  it.each([
    ["a mermaid block naming no lifecycle", "```mermaid\nstateDiagram-v2\n```"],
    ["an unclosed block", "```mermaid\n%% lifecycle: policy\n"],
    [
      "one lifecycle drawn twice",
      "```mermaid\n%% lifecycle: a\n```\n```mermaid\n%% lifecycle: a\n```",
    ],
  ])("rejects %s", (_, body) => {
    expect(() => extractLifecycleBlocks(body)).toThrow();
  });
});

describe("the real help files", () => {
  const lifecycles = HELP_MODULES.flatMap((m) =>
    m.lifecycles.map((lc) => ({ mod: m.id, ...lc })),
  );

  it("embed at least one lifecycle diagram", () => {
    expect(lifecycles.length).toBeGreaterThan(0);
  });

  it("draw only lifecycles the backend exports", () => {
    const unknown = lifecycles
      .filter((lc) => !existsSync(join(LIFECYCLES, `${lc.lifecycle}.json`)))
      .map((lc) => `${lc.mod}: ${lc.lifecycle}`);
    expect(unknown).toEqual([]);
  });

  it("draw exactly the exported states and transitions", () => {
    const diffs = lifecycles.flatMap((lc) =>
      diffLifecycle(lc.diagram, lifecycle(lc.lifecycle)).map(
        (d) => `${lc.mod} (${lc.lifecycle}): ${d}`,
      ),
    );
    expect(diffs).toEqual([]);
  });
});

describe("the pre-rendered SVGs", () => {
  const mermaidVersion = (
    JSON.parse(
      readFileSync(require.resolve("mermaid/package.json"), "utf8"),
    ) as { version: string }
  ).version;
  const tokens = readThemeTokens(
    readFileSync(join(WEB_ROOT, "app", "globals.css"), "utf8"),
  );
  const expected = HELP_MODULES.flatMap((m) =>
    m.lifecycles.flatMap((lc) =>
      (["light", "dark"] as const).map((theme) => ({
        file: lifecycleDiagramFile(m.id, lc.lifecycle, theme),
        stamp: diagramStamp({
          mermaidVersion,
          theme,
          tokens: tokens[theme],
          source: lc.source,
        }),
      })),
    ),
  );

  it("exist for every diagram and theme, and are current (else run `just help-diagrams`)", () => {
    const stale = expected
      .filter(({ file, stamp }) => {
        const path = join(DIAGRAMS, file);
        return (
          !existsSync(path) || readStamp(readFileSync(path, "utf8")) !== stamp
        );
      })
      .map((e) => e.file);
    expect(stale).toEqual([]);
  });

  it("leave no rendered file behind that no help file draws", () => {
    const want = new Set(expected.map((e) => e.file));
    const orphans = readdirSync(DIAGRAMS).filter(
      (f) => /-(light|dark)\.svg$/.test(f) && !want.has(f),
    );
    expect(orphans).toEqual([]);
  });

  it("carry no script, handler, external link or foreignObject", () => {
    const unsafe = readdirSync(DIAGRAMS)
      .filter((f) => f.endsWith(".svg"))
      .flatMap((f) =>
        unsafeSvgParts(readFileSync(join(DIAGRAMS, f), "utf8")).map(
          (p) => `${f}: ${p}`,
        ),
      );
    expect(unsafe).toEqual([]);
  });

  it("pin Mermaid exactly, as a devDependency only", () => {
    const pkg = JSON.parse(
      readFileSync(join(WEB_ROOT, "package.json"), "utf8"),
    ) as {
      dependencies: Record<string, string>;
      devDependencies: Record<string, string>;
    };
    expect(pkg.dependencies.mermaid).toBeUndefined();
    expect(pkg.devDependencies.mermaid).toBe(mermaidVersion);
  });
});

describe("sanitizeSvg", () => {
  it("strips scripts, event handlers and non-fragment links", () => {
    const dirty =
      '<svg onload="alert(1)"><script>alert(2)</script><script src="x.js"/>' +
      '<g ONCLICK=\'go()\' class="node"><a xlink:href="javascript:alert(3)"><text>a</text></a>' +
      '<a href="https://evil.example">b</a><use href="#arrow"/></g></svg>';
    const clean = sanitizeSvg(dirty);
    expect(unsafeSvgParts(clean)).toEqual([]);
    expect(clean).toBe(
      '<svg><g class="node"><a><text>a</text></a><a>b</a><use href="#arrow"/></g></svg>',
    );
  });

  it("removes a script that a first pass would reassemble, and odd end tags", () => {
    const clean = sanitizeSvg(
      '<svg><scr<script>x</script>ipt>alert(1)</script\t\n bar><g o onclick="a()"nclick="b()"/></svg>',
    );
    expect(unsafeSvgParts(clean)).toEqual([]);
    expect(clean).not.toMatch(/script|onclick/i);
  });

  it("refuses an SVG with HTML labels", () => {
    expect(() =>
      sanitizeSvg("<svg><foreignObject><div>x</div></foreignObject></svg>"),
    ).toThrow(/foreignObject/);
  });

  it("stamps and reads back a rendered file", () => {
    const stamp = "a".repeat(64);
    expect(readStamp(withStamp("<svg/>", "web/help/modules/x.md", stamp))).toBe(
      stamp,
    );
    expect(readStamp("<svg/>")).toBeUndefined();
  });
});

describe("readThemeTokens", () => {
  it("reads each token from the light and dark blocks", () => {
    const tokens = readThemeTokens(
      readFileSync(join(WEB_ROOT, "app", "globals.css"), "utf8"),
    );
    expect(tokens.light.background).toBe("oklch(1 0 0)");
    expect(tokens.dark.background).toBe("oklch(0.145 0 0)");
  });

  it("throws on a missing token", () => {
    expect(() => readThemeTokens(":root,\n.paper {\n}\n.dark {\n}\n")).toThrow(
      /--background/,
    );
  });
});
