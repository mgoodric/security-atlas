// #2082 — fixture test for the no-dev-notes-in-ui ESLint rule.
import { RuleTester } from "eslint";
import tsParser from "@typescript-eslint/parser";
import { describe, it } from "vitest";

import rule from "./no-dev-notes-in-ui.mjs";

RuleTester.describe = describe;
RuleTester.it = it;

const tester = new RuleTester({
  languageOptions: {
    parser: tsParser,
    parserOptions: { ecmaFeatures: { jsx: true } },
  },
});

const err = (what: string) => ({
  messageId: "devNote" as const,
  data: { what },
});

tester.run("no-dev-notes-in-ui", rule, {
  valid: [
    // API paths in non-JSX code are fine.
    `const res = await fetch("/v1/controls");`,
    `const url = \`/v1/policies/\${id}\`;`,
    // Comments are not rendered.
    `// slice 123 — canvas § 4.3\nconst x = <p>Controls</p>;`,
    // Non-visible props may carry paths.
    `const a = <a href="/v1/export">Export</a>;`,
    `const f = <form action="/v1/evidence:push" />;`,
    `const d = <div data-testid="slice-1" className="x" />;`,
    // Ordinary copy.
    `const p = <p title="Slice and dice">Ask an admin to enable this.</p>;`,
    `const p = <p>{"Version 1 of the policy"}</p>;`,
    `const p = <input placeholder="Search controls" aria-label="Search" />;`,
  ],
  invalid: [
    {
      code: `const p = <p>Ships in slice 42.</p>;`,
      errors: [err("a slice number")],
    },
    {
      code: `const p = <p>{"Slice 42 adds this"}</p>;`,
      errors: [err("a slice number")],
    },
    {
      code: `const p = <p>See canvas § 7.2</p>;`,
      errors: [err("a canvas section")],
    },
    {
      code: `const p = <p>Calls GET /v1/metrics.</p>;`,
      errors: [err("an API path")],
    },
    {
      code: `const p = <p>{\`POST /v1/risks\`}</p>;`,
      errors: [err("an API path")],
    },
    {
      code: `const p = <p>{ok ? "Saved" : "slice 9 WIP"}</p>;`,
      errors: [err("a slice number")],
    },
    {
      code: `const i = <img alt="slice 3 screenshot" />;`,
      errors: [err("a slice number")],
    },
    {
      code: `const i = <input placeholder={"GET /v1/x"} />;`,
      errors: [err("an API path")],
    },
    {
      code: `const i = <C description="per canvas §5" />;`,
      errors: [err("a canvas section")],
    },
    {
      code: `const i = <C title={\`Slice\${n}\`} aria-label="slice 1" />;`,
      errors: [err("a slice number")],
    },
  ],
});
