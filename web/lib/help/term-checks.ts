// #2144 — build checks for the user term list.
//
// Pure functions over source text, so `terms.test.ts` can run them both
// against small fixtures (proving each check fails when it should) and
// against the real tree (the gate itself). Three checks:
//
//   1. every `<Term id="…">` in the UI names a term in `help/terms.json`,
//      and every `<Term>` has a literal id (a computed id can't be checked);
//   2. every term's `glossary` link resolves to a heading in GLOSSARY.md;
//   3. the linked entry uses the same term name, as its heading or as one
//      of its bold terms, so renaming either side fails the build.

import type { HelpTerm } from "./terms";

export type SourceFile = { path: string; source: string };

// `<Term` followed by a non-identifier character, so `<TermList` or
// `<Terms` never match.
const TERM_TAG = /<Term(?![\w$])([^>]*)>/g;
const LITERAL_ID =
  /\bid=(?:"([^"]*)"|'([^']*)'|\{\s*"([^"]*)"\s*\}|\{\s*'([^']*)'\s*\})/;

/** `source` with its comments blanked out, newlines kept, so a `<Term>` in
 *  prose isn't read as a usage and line numbers still match. A `//` right
 *  after `:` (a URL) is not treated as a comment. */
export function stripComments(source: string): string {
  const blank = (s: string) => s.replace(/[^\n]/g, " ");
  return source
    .replace(/\/\*[\s\S]*?\*\//g, blank)
    .replace(
      /(^|[^:])(\/\/.*)$/gm,
      (_m, pre: string, comment: string) => pre + blank(comment),
    );
}

/** Ids used by `<Term>` elements in one source file. A `<Term>` whose id is
 *  not a string literal is reported in `dynamic` by line number. */
export function findTermUsages(source: string): {
  ids: { id: string; line: number }[];
  dynamic: number[];
} {
  const ids: { id: string; line: number }[] = [];
  const dynamic: number[] = [];
  const code = stripComments(source);
  for (const m of code.matchAll(TERM_TAG)) {
    const line = code.slice(0, m.index).split("\n").length;
    const lit = LITERAL_ID.exec(m[1]);
    if (!lit) {
      dynamic.push(line);
      continue;
    }
    ids.push({ id: lit[1] ?? lit[2] ?? lit[3] ?? lit[4], line });
  }
  return { ids, dynamic };
}

/** Problems with `<Term>` usage across the given files. Empty means pass. */
export function checkTermUsages(
  files: SourceFile[],
  terms: readonly HelpTerm[],
): string[] {
  const known = new Set(terms.map((t) => t.id));
  const problems: string[] = [];
  for (const f of files) {
    const { ids, dynamic } = findTermUsages(f.source);
    for (const { id, line } of ids) {
      if (!known.has(id)) {
        problems.push(`${f.path}:${line}: unknown term id "${id}"`);
      }
    }
    for (const line of dynamic) {
      problems.push(
        `${f.path}:${line}: <Term> id must be a string literal so it can be checked`,
      );
    }
  }
  return problems;
}

/** Problems with the term list itself: duplicate or malformed ids, empty
 *  fields, and a short definition longer than two sentences. */
export function checkTermList(terms: readonly HelpTerm[]): string[] {
  const problems: string[] = [];
  const seen = new Set<string>();
  for (const t of terms) {
    if (!/^[a-z0-9]+(-[a-z0-9]+)*$/.test(t.id)) {
      problems.push(`term "${t.id}": id must be lowercase kebab-case`);
    }
    if (seen.has(t.id)) problems.push(`term "${t.id}": duplicate id`);
    seen.add(t.id);
    for (const field of ["name", "short", "long"] as const) {
      if (!t[field]?.trim()) problems.push(`term "${t.id}": empty ${field}`);
    }
    const sentences = (t.short.match(/[.!?](\s|$)/g) ?? []).length;
    if (sentences > 2) {
      problems.push(
        `term "${t.id}": short definition has ${sentences} sentences (at most 2)`,
      );
    }
  }
  return problems;
}

/** GitHub's heading anchor: lowercase, punctuation dropped, each space a
 *  hyphen. "Risk treatment (#1746)" → "risk-treatment-1746". */
export function githubSlug(heading: string): string {
  return heading
    .trim()
    .toLowerCase()
    .replace(/[^\p{L}\p{N} _-]/gu, "")
    .replace(/ /g, "-");
}

/** Comparison key for a term name: letters and digits only, lowercased, so
 *  "AuditPeriod", "audit period" and `audit_period` compare equal. */
export function nameKey(name: string): string {
  return name.toLowerCase().replace(/[^\p{L}\p{N}]/gu, "");
}

type GlossarySection = { heading: string; names: Set<string> };

/** GLOSSARY.md's `##`/`###` sections by anchor, each with the names it
 *  defines: its heading (less any trailing "(…)" provenance) and its bold
 *  terms. Fenced code is skipped. */
export function glossarySections(md: string): Map<string, GlossarySection> {
  const sections = new Map<string, GlossarySection>();
  let current: GlossarySection | null = null;
  let inFence = false;
  for (const line of md.split("\n")) {
    if (/^\s*```/.test(line)) {
      inFence = !inFence;
      continue;
    }
    if (inFence) continue;
    const h = /^#{2,3}\s+(.*?)\s*$/.exec(line);
    if (h) {
      const heading = h[1];
      current = { heading, names: new Set() };
      current.names.add(nameKey(heading.replace(/\s*\([^)]*\)\s*$/, "")));
      sections.set(githubSlug(heading), current);
      continue;
    }
    if (!current) continue;
    for (const b of line.matchAll(/\*\*(.+?)\*\*/g)) {
      current.names.add(nameKey(b[1]));
    }
  }
  return sections;
}

/** Problems with the terms' links into GLOSSARY.md. Empty means pass. */
export function checkGlossaryLinks(
  terms: readonly HelpTerm[],
  glossaryMd: string,
): string[] {
  const sections = glossarySections(glossaryMd);
  const problems: string[] = [];
  for (const t of terms) {
    if (t.glossary === undefined) continue;
    const m = /^GLOSSARY\.md#([^#\s]+)$/.exec(t.glossary);
    if (!m) {
      problems.push(
        `term "${t.id}": glossary link "${t.glossary}" must be GLOSSARY.md#<anchor>`,
      );
      continue;
    }
    const section = sections.get(m[1]);
    if (!section) {
      problems.push(
        `term "${t.id}": glossary link "${t.glossary}" matches no GLOSSARY.md heading`,
      );
      continue;
    }
    if (!section.names.has(nameKey(t.name))) {
      problems.push(
        `term "${t.id}": GLOSSARY.md "${section.heading}" doesn't use the name "${t.name}" (as its heading or a bold term); rename one to match`,
      );
    }
  }
  return problems;
}
