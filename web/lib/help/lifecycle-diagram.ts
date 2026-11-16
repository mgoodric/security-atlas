// #2148 — lifecycle diagrams in module help.
//
// A help file embeds a lifecycle as a Mermaid state diagram in a fenced
// block whose first line names the lifecycle it draws:
//
//   ```mermaid
//   %% lifecycle: exception
//   stateDiagram-v2
//     [*] --> requested: request
//     requested --> approved: approve
//     active --> expired: expire (system)
//     expired --> [*]
//   ```
//
// The name is a lifecycle exported to `docs/lifecycles/<name>.json` by
// `just lifecycles-export` from the backend's declared transition tables.
// `diagrams.test.ts` is the diagram check: it parses every embedded diagram
// and fails on any difference from that JSON (a state, a transition, its
// action, a system-actor move, an initial or terminal state).
//
// Mermaid never reaches the browser. `scripts/render-help-diagrams.mjs`
// pre-renders each diagram to a light and a dark SVG under
// `public/help/diagrams/`, and the panel shows them as images. This module
// only reads the diagram text, so the panel can list the states and
// transitions as the diagram's text alternative.
//
// The parser accepts a deliberately small subset of `stateDiagram-v2` (the
// header, `direction`, `%%` comments, and `A --> B: label` edges) and
// throws on anything else, so no line can draw a state the check did not
// see.

export const SYSTEM_SUFFIX = " (system)";

export type LifecycleBlock = {
  /** The lifecycle's name, as in `docs/lifecycles/<name>.json`. */
  lifecycle: string;
  /** The Mermaid source inside the fence. */
  source: string;
};

export type DiagramTransition = {
  from: string;
  to: string;
  action: string;
  actor: "person" | "system";
};

export type LifecycleDiagram = {
  /** Every state, in order of first appearance. */
  states: string[];
  /** Edges from `[*]`: a state a record is created in, and the action. */
  initial: { state: string; action: string }[];
  /** States with an edge to `[*]`. */
  terminal: string[];
  transitions: DiagramTransition[];
};

/** The shape of a `docs/lifecycles/<name>.json` file the check reads. */
export type LifecycleExport = {
  name: string;
  title: string;
  states: string[];
  initial: { state: string; action: string }[];
  terminal: string[];
  transitions: {
    from: string;
    to: string;
    action: string;
    actor: "person" | "system";
  }[];
};

const FENCE_OPEN = /^```mermaid\s*$/;
const LIFECYCLE_LINE = /^%%\s*lifecycle:\s*([a-z][a-z0-9_]*)\s*$/;
const STATE = "(\\[\\*\\]|[a-z][a-z0-9_]*)";
const EDGE = new RegExp(`^${STATE}\\s*-->\\s*${STATE}(?:\\s*:\\s*(.+))?$`);
const ACTION = /^[a-z][a-z0-9_]*$/;

/**
 * Split a help body into its prose and its lifecycle diagrams. The
 * diagrams are removed from the prose, which the panel renders as
 * Markdown; a Mermaid fence without a `%% lifecycle:` first line throws.
 */
export function extractLifecycleBlocks(body: string): {
  body: string;
  blocks: LifecycleBlock[];
} {
  const lines = body.split("\n");
  const kept: string[] = [];
  const blocks: LifecycleBlock[] = [];
  for (let i = 0; i < lines.length; i += 1) {
    if (!FENCE_OPEN.test(lines[i])) {
      kept.push(lines[i]);
      continue;
    }
    const end = lines.indexOf("```", i + 1);
    if (end < 0) throw new Error("mermaid block is not closed with ```");
    const inner = lines.slice(i + 1, end);
    const named = LIFECYCLE_LINE.exec(inner[0]?.trim() ?? "");
    if (!named) {
      throw new Error(
        'a mermaid block must open with "%% lifecycle: <name>" naming a lifecycle in docs/lifecycles/',
      );
    }
    if (blocks.some((b) => b.lifecycle === named[1])) {
      throw new Error(`lifecycle "${named[1]}" is drawn twice`);
    }
    blocks.push({ lifecycle: named[1], source: inner.join("\n") });
    i = end;
  }
  return {
    body: kept
      .join("\n")
      .replace(/\n{3,}/g, "\n\n")
      .trim(),
    blocks,
  };
}

/** Parse the `stateDiagram-v2` subset described above. Throws on any
 *  line outside it. */
export function parseStateDiagram(source: string): LifecycleDiagram {
  const states: string[] = [];
  const see = (s: string) => {
    if (s !== "[*]" && !states.includes(s)) states.push(s);
  };
  const out: LifecycleDiagram = {
    states,
    initial: [],
    terminal: [],
    transitions: [],
  };
  let header = false;
  for (const raw of source.split("\n")) {
    const line = raw.trim();
    if (line === "" || line.startsWith("%%")) continue;
    if (!header) {
      if (line !== "stateDiagram-v2") {
        throw new Error(
          'a lifecycle diagram must start with "stateDiagram-v2"',
        );
      }
      header = true;
      continue;
    }
    if (/^direction\s+(LR|RL|TB|BT)$/.test(line)) continue;
    const edge = EDGE.exec(line);
    if (!edge) throw new Error(`diagram line not understood: ${line}`);
    const [, from, to, label] = edge;
    see(from);
    see(to);
    if (from === "[*]" && to === "[*]") {
      throw new Error(`diagram line not understood: ${line}`);
    }
    if (to === "[*]") {
      if (label) throw new Error(`an edge to [*] takes no label: ${line}`);
      if (!out.terminal.includes(from)) out.terminal.push(from);
      continue;
    }
    if (!label) throw new Error(`a transition needs its action: ${line}`);
    const system = label.endsWith(SYSTEM_SUFFIX);
    const action = system ? label.slice(0, -SYSTEM_SUFFIX.length) : label;
    if (!ACTION.test(action)) {
      throw new Error(`"${label}" is not an action name: ${line}`);
    }
    if (from === "[*]") {
      out.initial.push({ state: to, action });
    } else {
      out.transitions.push({
        from,
        to,
        action,
        actor: system ? "system" : "person",
      });
    }
  }
  if (!header) throw new Error("the lifecycle diagram is empty");
  return out;
}

function setDiff(label: string, want: string[], got: string[]): string[] {
  const out: string[] = [];
  for (const w of want) {
    if (!got.includes(w)) out.push(`missing ${label} ${w}`);
  }
  for (const g of got) {
    if (!want.includes(g)) out.push(`extra ${label} ${g}`);
  }
  return out;
}

const edgeKey = (t: { from: string; to: string; action: string }) =>
  `${t.from} --> ${t.to}: ${t.action}`;

/**
 * Every difference between a diagram and its exported lifecycle, one line
 * each ("missing transition a --> b: act", "extra state x"). Empty when
 * the diagram draws exactly what the backend allows.
 */
export function diffLifecycle(
  diagram: LifecycleDiagram,
  lifecycle: LifecycleExport,
): string[] {
  const diffs = [
    ...setDiff("state", lifecycle.states, diagram.states),
    ...setDiff(
      "initial",
      lifecycle.initial.map((i) => `[*] --> ${i.state}: ${i.action}`),
      diagram.initial.map((i) => `[*] --> ${i.state}: ${i.action}`),
    ),
    ...setDiff("terminal state", lifecycle.terminal, diagram.terminal),
    ...setDiff(
      "transition",
      lifecycle.transitions.map(edgeKey),
      diagram.transitions.map(edgeKey),
    ),
  ];
  for (const want of lifecycle.transitions) {
    const got = diagram.transitions.find((t) => edgeKey(t) === edgeKey(want));
    if (got && got.actor !== want.actor) {
      diffs.push(
        `transition ${edgeKey(want)} is made by the ${
          want.actor
        }, drawn as the ${got.actor}` +
          (want.actor === "system"
            ? ` (label it "${want.action}${SYSTEM_SUFFIX}")`
            : ""),
      );
    }
  }
  return diffs;
}

function readable(id: string): string {
  return id.replace(/_/g, " ");
}

/**
 * The diagram's text alternative: one line per state, then one per
 * transition, in the diagram's order. The panel lists these under the
 * image so a screen reader gets everything the picture shows.
 */
export function describeLifecycle(diagram: LifecycleDiagram): {
  states: string[];
  transitions: string[];
} {
  return {
    states: diagram.states.map((s) => {
      const marks = [
        ...diagram.initial
          .filter((i) => i.state === s)
          .map((i) => `starts here on ${readable(i.action)}`),
        ...(diagram.terminal.includes(s) ? ["final"] : []),
      ];
      return marks.length
        ? `${readable(s)} (${marks.join("; ")})`
        : readable(s);
    }),
    transitions: diagram.transitions.map(
      (t) =>
        `${readable(t.from)} to ${readable(t.to)}: ${readable(t.action)}` +
        (t.actor === "system" ? ", done automatically by the system" : ""),
    ),
  };
}

export type DiagramTheme = "light" | "dark";

/** The pre-rendered file for one lifecycle in one module, in one theme,
 *  under `public/help/diagrams/`. */
export function lifecycleDiagramFile(
  moduleId: string,
  lifecycle: string,
  theme: DiagramTheme,
): string {
  return `${moduleId}-${lifecycle.replace(/_/g, "-")}-${theme}.svg`;
}
