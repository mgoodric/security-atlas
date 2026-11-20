// #2737 — fixture test for the no-raw-upstream-error ESLint rule. The
// "flags" cases carry over the #2645 regex sentinel's self-tests, plus the
// uncast `res.json()` reads the regex missed.
import { RuleTester } from "eslint";
import tsParser from "@typescript-eslint/parser";
import { describe, it } from "vitest";

import rule from "./no-raw-upstream-error.mjs";

RuleTester.describe = describe;
RuleTester.it = it;

const tester = new RuleTester({ languageOptions: { parser: tsParser } });

const err = (n = 1) =>
  Array.from({ length: n }, () => ({ messageId: "rawError" as const }));

const inAsync = (body: string) =>
  `async function f(res: Response) {\n${body}\n}`;

tester.run("no-raw-upstream-error", rule, {
  valid: [
    // The helpers.
    inAsync(`throw new Error(await upstreamErrorMessage(res));`),
    inAsync(
      `const raw = await res.json();\nreturn upstreamErrorText(res.status, raw, "x");`,
    ),
    // A prop named error.
    `function F({ error }: { error?: string }) {}`,
    `const p = props.error ?? "none";`,
    // An Error object, a query result, a state value.
    `try { g(); } catch (e) { console.log(e.error); }`,
    `const { data, error } = useQuery(opts);`,
    `const [error, setError] = useState<string | null>(null);`,
    // A parsed body's other fields.
    inAsync(`const j = await res.json();\nreturn j.items;`),
    inAsync(`const { items } = await res.json();`),
    // NextResponse.json(…) builds a response; it does not parse one.
    `return NextResponse.json({ error: "bad request" }, { status: 400 });`,
  ],
  invalid: [
    // Uncast reads (#2737).
    { code: inAsync(`return (await res.json()).error;`), errors: err() },
    { code: inAsync(`const { error } = await res.json();`), errors: err() },
    {
      code: inAsync(`const j = await res.json();\nmsg = j.error || "failed";`),
      errors: err(),
    },
    {
      code: inAsync(`const j = await res.json();\nmsg = j.error ?? "failed";`),
      errors: err(),
    },
    {
      code: inAsync(
        `let j;\nj = await res.json().catch(() => ({}));\nreturn j?.error;`,
      ),
      errors: err(),
    },
    {
      code: inAsync(
        `const j = (await res.json()) || {};\nconst { error } = j;`,
      ),
      errors: err(),
    },
    // The #2645 regex sentinel's shapes.
    {
      code: inAsync(
        `const j = (await res.json()) as { error?: string };\nif (j.error) msg = j.error;`,
      ),
      errors: err(3),
    },
    {
      code: inAsync(
        `const body = (await res.json()) as {\n  error?: string;\n} & Row;`,
      ),
      errors: err(),
    },
    {
      code: inAsync(
        `parsed = JSON.parse(await res.text()) as { error?: string };`,
      ),
      errors: err(),
    },
    {
      code: `function g(raw: unknown) { return typeof (raw as { error?: string }).error === "string"; }`,
      errors: err(),
    },
    { code: `type L = Row & { error?: string };`, errors: err() },
    {
      code: `function g(body: B, res: Response) { throw new Error(body.error ?? \`\${res.status} \${res.statusText}\`); }`,
      errors: err(),
    },
  ],
});
