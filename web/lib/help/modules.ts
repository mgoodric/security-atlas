// #2143 — in-app module help, read at build time.
//
// `web/help/modules/<id>.md` is the single source for a module's help:
// the "How this works" panel (`components/help/help-panel.tsx`) renders
// it here, and the docs site publishes the same file as a module page
// (`docs-site/hooks/help_pages.py`). Each file opens with front matter:
//
//   ---
//   id: controls                  # equals the file name
//   title: Controls
//   routes: [/controls]           # the pages this help covers
//   terms: [anchor, crosswalk]    # ids from web/help/terms.json
//   diagrams:                     # optional; files in public/help/diagrams/
//     - src: control-states.svg
//       alt: What the diagram shows, for a screen reader
//   ---
//
// The front matter is a deliberately small YAML subset (scalars, flow
// lists, which Prettier may wrap onto the next line, and a block list of
// scalar maps for `diagrams`), parsed here
// without a YAML dependency. The docs hook reads the same subset with
// PyYAML. `modules.test.ts` fails the build on a malformed file, an
// unknown term, a route claimed twice, or a file not registered below.
//
// A lifecycle diagram is not listed under `diagrams`: it is a ```mermaid
// block in the body (#2148, `lifecycle-diagram.ts`), pre-rendered to a
// light and a dark SVG, checked against the backend's exported lifecycle
// by `diagrams.test.ts`, and lifted out of the body into `lifecycles`.
//
// The `.md` imports resolve to the file's text: Turbopack's `raw` rule in
// `next.config.ts`, and a matching plugin in `vitest.config.ts`.

import actionPlansSource from "@/help/modules/action-plans.md";
import adminSource from "@/help/modules/admin.md";
import adminApiKeysSource from "@/help/modules/admin-api-keys.md";
import adminDemoSource from "@/help/modules/admin-demo.md";
import adminFeaturesSource from "@/help/modules/admin-features.md";
import adminSsoSource from "@/help/modules/admin-sso.md";
import adminSuperAdminsSource from "@/help/modules/admin-super-admins.md";
import adminTenantsSource from "@/help/modules/admin-tenants.md";
import adminUsersSource from "@/help/modules/admin-users.md";
import auditsSource from "@/help/modules/audits.md";
import boardPacksSource from "@/help/modules/board-packs.md";
import controlsSource from "@/help/modules/controls.md";
import controlsChecklistSource from "@/help/modules/controls-checklist.md";
import csfSource from "@/help/modules/csf.md";
import evidenceSource from "@/help/modules/evidence.md";
import exceptionsSource from "@/help/modules/exceptions.md";
import frameworkScopesSource from "@/help/modules/framework-scopes.md";
import personnelSecuritySource from "@/help/modules/personnel-security.md";
import policiesSource from "@/help/modules/policies.md";
import questionnairesSource from "@/help/modules/questionnaires.md";
import risksSource from "@/help/modules/risks.md";
import risksHierarchySource from "@/help/modules/risks-hierarchy.md";
import scfCatalogSource from "@/help/modules/scf-catalog.md";
import settingsSource from "@/help/modules/settings.md";
import vendorClaimsSource from "@/help/modules/vendor-claims.md";
import vendorsSource from "@/help/modules/vendors.md";
import {
  describeLifecycle,
  extractLifecycleBlocks,
  lifecycleDiagramFile,
  parseStateDiagram,
  type LifecycleDiagram,
} from "./lifecycle-diagram";

export type HelpDiagram = {
  /** File name under `web/public/help/diagrams/`. */
  src: string;
  alt: string;
};

/** #2148 — a lifecycle diagram from a ```mermaid block in the body. */
export type HelpLifecycle = {
  /** The lifecycle's name, as in `docs/lifecycles/<name>.json`. */
  lifecycle: string;
  /** The Mermaid source, for the diagram check. */
  source: string;
  diagram: LifecycleDiagram;
  /** The pre-rendered files under `public/help/diagrams/`. */
  light: string;
  dark: string;
  /** The text alternative: the states, then the transitions. */
  text: { states: string[]; transitions: string[] };
};

export type HelpModule = {
  id: string;
  title: string;
  routes: string[];
  /** Term ids from `web/help/terms.json`. */
  terms: string[];
  diagrams: HelpDiagram[];
  lifecycles: HelpLifecycle[];
  /** The Markdown body after the front matter, without its lifecycle
   *  diagram blocks. */
  body: string;
};

/** Every module help file, by file name. A new file is added here.
 *  A new module also gets a first-visit hint (#2145), so add its id to
 *  `userprefs.HintModules` (internal/auth/userprefs/hints.go) and widen the
 *  `user_onboarding_hint_dismissals` module CHECK in a new migration;
 *  until then the hint shows but its dismissal is refused with a 400. */
export const HELP_SOURCES: Readonly<Record<string, string>> = {
  "controls.md": controlsSource,
  // #2150: Controls.
  "evidence.md": evidenceSource,
  "exceptions.md": exceptionsSource,
  "controls-checklist.md": controlsChecklistSource,
  // #2151: Risks.
  "risks.md": risksSource,
  "risks-hierarchy.md": risksHierarchySource,
  "action-plans.md": actionPlansSource,
  // #2155: Settings & admin.
  "settings.md": settingsSource,
  "admin.md": adminSource,
  "admin-users.md": adminUsersSource,
  "admin-api-keys.md": adminApiKeysSource,
  "admin-sso.md": adminSsoSource,
  "admin-features.md": adminFeaturesSource,
  "admin-tenants.md": adminTenantsSource,
  "admin-super-admins.md": adminSuperAdminsSource,
  "admin-demo.md": adminDemoSource,
  // #2152: Audit.
  "audits.md": auditsSource,
  "framework-scopes.md": frameworkScopesSource,
  "board-packs.md": boardPacksSource,
  "csf.md": csfSource,
  "vendor-claims.md": vendorClaimsSource,
  // #2154: Library.
  "policies.md": policiesSource,
  "vendors.md": vendorsSource,
  "personnel-security.md": personnelSecuritySource,
  "questionnaires.md": questionnairesSource,
  "scf-catalog.md": scfCatalogSource,
};

export const HELP_DOCS_BASE_URL =
  "https://mgoodric.github.io/security-atlas/help/" as const;

/** The module's page on the published docs site. Absolute, because the
 *  shared Markdown renderer drops relative links and the web app serves no
 *  `/docs` path. */
export function helpDocsHref(id: string): string {
  return `${HELP_DOCS_BASE_URL}${id}/`;
}

/** The URL the app serves a diagram file from (`web/public`). */
export function helpDiagramSrc(file: string): string {
  return `/help/diagrams/${file}`;
}

function unquote(s: string): string {
  const t = s.trim();
  if (
    t.length >= 2 &&
    ((t.startsWith('"') && t.endsWith('"')) ||
      (t.startsWith("'") && t.endsWith("'")))
  ) {
    return t.slice(1, -1);
  }
  return t;
}

function flowList(raw: string, key: string): string[] {
  const t = raw.trim();
  if (!t.startsWith("[") || !t.endsWith("]")) {
    throw new Error(`front matter "${key}" must be a [list]`);
  }
  const inner = t.slice(1, -1).trim();
  return inner === "" ? [] : inner.split(",").map(unquote);
}

type FrontMatter = Record<string, string | Record<string, string>[]>;

function parseFrontMatterLines(lines: string[]): FrontMatter {
  const out: FrontMatter = {};
  let list: Record<string, string>[] | null = null;
  let lastKey = "";
  for (const line of lines) {
    if (line.trim() === "" || line.trim().startsWith("#")) continue;
    const item = /^\s+-\s+([a-z_]+):\s*(.*)$/.exec(line);
    const cont = /^\s+([a-z_]+):\s*(.*)$/.exec(line);
    const top = /^([a-z_]+):\s*(.*)$/.exec(line);
    const wrapped = /^\s+(\[.*\])\s*$/.exec(line);
    if (wrapped && list && list.length === 0) {
      // A flow list Prettier wrapped onto the line after its key.
      list = null;
      out[lastKey] = wrapped[1];
    } else if (item && list) {
      list.push({ [item[1]]: unquote(item[2]) });
    } else if (cont && list && list.length > 0) {
      list[list.length - 1][cont[1]] = unquote(cont[2]);
    } else if (top) {
      if (top[1] in out) throw new Error(`front matter repeats "${top[1]}"`);
      lastKey = top[1];
      if (top[2].trim() === "") {
        list = [];
        out[top[1]] = list;
      } else {
        list = null;
        out[top[1]] = top[2];
      }
    } else {
      throw new Error(`front matter line not understood: ${line.trim()}`);
    }
  }
  return out;
}

function scalar(fm: FrontMatter, key: string): string {
  const v = fm[key];
  if (typeof v !== "string" || unquote(v) === "") {
    throw new Error(`front matter needs "${key}"`);
  }
  return unquote(v);
}

function list(fm: FrontMatter, key: string): string[] {
  const v = fm[key];
  if (v === undefined) return [];
  if (typeof v !== "string") throw new Error(`"${key}" must be a [list]`);
  return flowList(v, key);
}

function diagrams(fm: FrontMatter): HelpDiagram[] {
  const v = fm.diagrams;
  if (v === undefined) return [];
  if (typeof v === "string") {
    if (flowList(v, "diagrams").length > 0) {
      throw new Error('"diagrams" entries need a src and an alt');
    }
    return [];
  }
  return v.map((d) => {
    if (!d.src || !d.alt) {
      throw new Error('each diagram needs a "src" and an "alt"');
    }
    // A bare file name only, the same rule the docs hook applies, so no
    // `src` can point outside public/help/diagrams/.
    if (!/^[a-z0-9-]+\.(svg|png)$/.test(d.src)) {
      throw new Error(`bad diagram file name "${d.src}"`);
    }
    return { src: d.src, alt: d.alt };
  });
}

/** Parse one help file. Throws on malformed front matter. */
export function parseHelpModule(source: string): HelpModule {
  const lines = source.replace(/\r\n/g, "\n").split("\n");
  if (lines[0] !== "---") throw new Error("help file must open with ---");
  const end = lines.indexOf("---", 1);
  if (end < 0) throw new Error("front matter is not closed with ---");
  const fm = parseFrontMatterLines(lines.slice(1, end));
  const known = new Set(["id", "title", "routes", "terms", "diagrams"]);
  for (const key of Object.keys(fm)) {
    if (!known.has(key)) throw new Error(`unknown front matter key "${key}"`);
  }
  const id = scalar(fm, "id");
  const { body, blocks } = extractLifecycleBlocks(
    lines
      .slice(end + 1)
      .join("\n")
      .trim(),
  );
  return {
    id,
    title: scalar(fm, "title"),
    routes: list(fm, "routes"),
    terms: list(fm, "terms"),
    diagrams: diagrams(fm),
    lifecycles: blocks.map(({ lifecycle, source }) => {
      const diagram = parseStateDiagram(source);
      return {
        lifecycle,
        source,
        diagram,
        light: lifecycleDiagramFile(id, lifecycle, "light"),
        dark: lifecycleDiagramFile(id, lifecycle, "dark"),
        text: describeLifecycle(diagram),
      };
    }),
    body,
  };
}

export const HELP_MODULES: readonly HelpModule[] =
  Object.values(HELP_SOURCES).map(parseHelpModule);

function covers(route: string, pathname: string): boolean {
  return pathname === route || pathname.startsWith(`${route}/`);
}

/**
 * The help module for the page at `pathname`: the module with the longest
 * route that is the page itself or a parent of it, so `/controls` covers
 * `/controls/{id}` until a module claims the deeper route. Undefined when
 * no module covers the page, and the page shows no help button.
 */
export function helpModuleForPath(
  pathname: string,
  modules: readonly HelpModule[] = HELP_MODULES,
): HelpModule | undefined {
  let best: { mod: HelpModule; len: number } | undefined;
  for (const mod of modules) {
    for (const route of mod.routes) {
      if (covers(route, pathname) && (!best || route.length > best.len)) {
        best = { mod, len: route.length };
      }
    }
  }
  return best?.mod;
}
