// #2156 — the help-file parser and the route lookup, apart from the `.md`
// imports in `modules.ts`, so a Node reader that has no raw-Markdown loader
// (the Playwright route sweep, through `coverage.ts`) parses the same
// front matter the app does. `modules.ts` re-exports all of it.

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
  modules: readonly HelpModule[],
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
