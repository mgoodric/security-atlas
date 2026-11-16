// #2143 — module help files import as their text (Turbopack's `raw` rule
// in next.config.ts; the `help-markdown` plugin in vitest.config.ts).
declare module "*.md" {
  const source: string;
  export default source;
}
