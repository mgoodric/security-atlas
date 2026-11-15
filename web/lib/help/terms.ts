// #2144 — the user-facing term list, read at build time.
//
// `web/help/terms.json` is the single source: the <Term> tooltip reads it
// here, and the docs site's Terms page is generated from the same file by
// `docs-site/hooks/terms_page.py`. The definitions are plain language for
// someone new to GRC; the precise developer meaning stays in GLOSSARY.md,
// which a term may link to (checked by `terms.test.ts`).
//
// The Terms page is on the published docs site (mkdocs `site_url`), not a
// same-origin route: the web app serves no `/docs` path (see
// `app/(authed)/evidence/push-cta.ts` for the same call).

import termsFile from "@/help/terms.json";

export type HelpTerm = {
  id: string;
  name: string;
  short: string;
  long: string;
  /** `GLOSSARY.md#<heading-anchor>` of the matching developer entry. */
  glossary?: string;
};

export const TERMS: readonly HelpTerm[] = termsFile.terms;

const BY_ID: ReadonlyMap<string, HelpTerm> = new Map(
  TERMS.map((t) => [t.id, t]),
);

/** The term with this id, or undefined. `terms.test.ts` fails the build on
 *  any `<Term id>` in the UI that this would not resolve. */
export function getTerm(id: string): HelpTerm | undefined {
  return BY_ID.get(id);
}

export const TERMS_PAGE_URL =
  "https://mgoodric.github.io/security-atlas/terms/" as const;

/** The term's full entry on the docs Terms page. The page gives each entry
 *  an anchor equal to its id. */
export function termHref(id: string): string {
  return `${TERMS_PAGE_URL}#${id}`;
}
