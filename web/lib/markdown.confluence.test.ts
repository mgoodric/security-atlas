// #2945 — the Confluence policy-sync converter
// (`connectors/confluence/internal/storagemd`) emits CommonMark/GFM, and
// a synced policy body is rendered by `web/lib/markdown.ts`. These tests
// render the converter's own golden fixtures, so a converter change that
// emits something the renderer shows as a visible escape artefact fails
// here, not on a policy page. The second half pins the renderer features
// that round trip relies on (backslash escapes, `<…>` destinations, `<br>`
// in table cells) together with the security cases they could open.

import { readdirSync, readFileSync } from "node:fs";
import { join } from "node:path";

import { describe, expect, test } from "vitest";

import { renderMarkdown } from "./markdown";

const FIXTURES = join(
  __dirname,
  "../../connectors/confluence/internal/storagemd/testdata",
);

const golden = (name: string) =>
  readFileSync(join(FIXTURES, `${name}.md.golden`), "utf8");

const a = (href: string, text: string) =>
  `<a href="${href}" rel="noopener noreferrer" target="_blank">${text}</a>`;

// The intended HTML for each fixture. Every converter fixture is listed,
// so a new one fails `covers every golden fixture` until it is pinned.
const EXPECTED: Record<string, string> = {
  basic: [
    "<h1>Access Control Policy</h1>",
    "<p>This policy applies to <strong>all</strong> staff and <em>contractors</em>.</p>",
    "<h2>Scope and purpose</h2>",
    `<p>See the ${a(
      "https://example.com/handbook",
      "staff handbook",
    )} and <code>MFA_REQUIRED</code>.<br/>Second line.</p>`,
    "<p>Literal *stars* and _underscores_ stay literal; so do [brackets].</p>",
    "<p><del>Retired</del> text, underlined and coloured, it\u2019s fine.</p>",
    "<hr/>",
    "<h6>Footnote</h6>",
  ].join("\n"),
  blocks: [
    "<blockquote><p>Quoted line one.</p>",
    "<p>Quoted line two.</p></blockquote>",
    "<pre><code>plain",
    "  preformatted</code></pre>",
    "<p># not a heading</p>",
    "<p>1. not a list</p>",
  ].join("\n"),
  links: [
    `<p>Read ${a("https://example.com/a%20b(1)", "the [draft] policy")} and ${a(
      "https://example.com/x?a=1&amp;b=2",
      "query",
    )}.</p>`,
    "<p>Escapes: a\\b, `tick`, &lt;tag&gt; and 2 * 3 = 6.</p>",
    `<p>${a("https://example.com/wiki/Some%20Page", "Some Page")}</p>`,
  ].join("\n"),
  table:
    "<table><thead><tr><th>Role</th><th>Responsibility</th></tr></thead>" +
    "<tbody><tr><td>CISO</td><td>Owns the policy<br/>Approves | exceptions</td></tr>" +
    "<tr><td><strong>Staff</strong></td><td></td></tr></tbody></table>",
  macros: [
    "<blockquote><p><strong>Info</strong></p>",
    "<p>Read this first.</p></blockquote>",
    "<blockquote><p><strong>Warning: Do not share</strong></p>",
    "<p>Credentials stay in the vault.</p>",
    "<ul>",
    "<li>Never in email</li>",
    "</ul></blockquote>",
    "<blockquote><p><strong>Note</strong></p>",
    "<p>A note.</p></blockquote>",
    "<pre><code>aws iam list-users",
    "echo &quot;a &lt;b&gt; &amp; ```&quot;</code></pre>",
    "<pre><code>raw *text*</code></pre>",
    "<p><strong>Details</strong></p>",
    "<p>Hidden detail.</p>",
    "<p>Approved 2026-03-01 per Security Charter.</p>",
  ].join("\n"),
  unsupported: [
    "<p>[Unsupported Confluence macro: toc]</p>",
    "<p>Owner: [Unsupported Confluence macro: jira] and [Unsupported Confluence macro: toc]</p>",
    "<p>[Unsupported Confluence element: ac:image]</p>",
    "<p>logo</p>",
  ].join("\n"),
  // The renderer has no nested lists or task checkboxes: nesting flattens
  // and `[x]` stays as text. Both read correctly; neither is an escape.
  lists: [
    "<ul>",
    "<li>First</li>",
    "<li>Second</li>",
    "<li>Nested <strong>one</strong></li>",
    "<li>Nested two</li>",
    "</ul>",
    "<ol>",
    "<li>Deep</li>",
    "<li>Deeper</li>",
    "</ol>",
    "<ul>",
    "<li>Third</li>",
    "</ul>",
    "<ol>",
    "<li>Three</li>",
    "<li>Four</li>",
    "</ol>",
    "<ul>",
    "<li>[x] Review annually</li>",
    "<li>[ ] Publish</li>",
    "</ul>",
  ].join("\n"),
};

const fixtureNames = readdirSync(FIXTURES)
  .filter((f) => f.endsWith(".md.golden"))
  .map((f) => f.slice(0, -".md.golden".length))
  .sort();

describe("Confluence converter fixtures render through web/lib/markdown", () => {
  test("covers every golden fixture", () => {
    expect(fixtureNames).toEqual(Object.keys(EXPECTED).sort());
  });

  test.each(fixtureNames)("%s renders the intended HTML", (name) => {
    expect(renderMarkdown(golden(name))).toBe(EXPECTED[name]);
  });

  test.each(fixtureNames)("%s shows no escape artefacts", (name) => {
    const html = renderMarkdown(golden(name));
    // A backslash before ASCII punctuation, or a `<br>` shown as text.
    expect(html.replace(/a\\b/g, "")).not.toMatch(
      /\\(?:[!-/:-@[-`{-~]|&(?:amp|lt|gt|quot|#39);)/,
    );
    expect(html).not.toContain("&lt;br");
  });
});

describe("backslash escapes", () => {
  test("escaped punctuation renders literally and opens nothing", () => {
    expect(renderMarkdown("\\*a\\* \\_b\\_ \\~~c\\~~ \\`d\\`")).toBe(
      "<p>*a* _b_ ~~c~~ `d`</p>",
    );
  });

  test("an escaped < cannot start an autolink", () => {
    expect(renderMarkdown("\\<https://example.com>")).toBe(
      "<p>&lt;https://example.com&gt;</p>",
    );
  });

  test("an escaped HTML character stays escaped", () => {
    expect(
      renderMarkdown("\\<script\\>alert(1)\\</script\\> \\& \\\" \\'"),
    ).toBe("<p>&lt;script&gt;alert(1)&lt;/script&gt; &amp; &quot; &#39;</p>");
  });

  test("a backslash before a non-punctuation character stays", () => {
    expect(renderMarkdown("C:\\Users and a\\b")).toBe(
      "<p>C:\\Users and a\\b</p>",
    );
  });

  test("a backslash inside a code span is literal", () => {
    expect(renderMarkdown("`a\\*b`")).toBe("<p><code>a\\*b</code></p>");
  });

  test("escaped brackets may sit in link text", () => {
    expect(renderMarkdown("[x \\[y\\]](https://example.com)")).toBe(
      `<p>${a("https://example.com", "x [y]")}</p>`,
    );
  });

  test("an escape in an href stands for its character", () => {
    expect(renderMarkdown("[x](https://example.com/a\\_b)")).toBe(
      `<p>${a("https://example.com/a_b", "x")}</p>`,
    );
  });

  test("an escaped scheme colon does not smuggle javascript:", () => {
    expect(renderMarkdown("[x](javascript\\:alert(1))")).not.toContain("<a");
    expect(renderMarkdown("[x](<javascript\\:alert(1)>)")).not.toContain("<a");
  });
});

describe("<…> link destinations", () => {
  test("allow spaces (percent-encoded) and parentheses", () => {
    expect(renderMarkdown("[x](<https://example.com/a b(1)>)")).toBe(
      `<p>${a("https://example.com/a%20b(1)", "x")}</p>`,
    );
  });

  test("keep the scheme allow-list", () => {
    for (const href of [
      "javascript:alert(1)",
      "JavaScript:alert(1)",
      "data:text/html,<script>alert(1)</script>",
      "vbscript:msgbox",
      "//evil.example",
      "java\tscript:alert(1)",
      "/relative",
    ]) {
      const html = renderMarkdown(`[x](<${href}>)`);
      expect(html, href).not.toContain("<a");
      expect(html, href).not.toContain("<script");
    }
  });

  test("a quote in the destination cannot break out of href", () => {
    const html = renderMarkdown(
      '[x](<https://example.com/" onmouseover="alert(1)>)',
    );
    expect(html).not.toMatch(/<a [^>]*onmouseover/);
  });

  test("is not an image", () => {
    expect(renderMarkdown("![alt](<https://example.com/a b.png>)")).toBe(
      "<p>alt</p>",
    );
  });
});

describe("<br> in table cells", () => {
  const table = (cell: string) => renderMarkdown(`| h |\n| --- |\n| ${cell} |`);
  const td = (inner: string) =>
    `<table><thead><tr><th>h</th></tr></thead><tbody><tr><td>${inner}</td></tr></tbody></table>`;

  test("each spelling breaks the cell", () => {
    expect(table("a<br>b<br/>c<br />d<BR>e")).toBe(
      td("a<br/>b<br/>c<br/>d<br/>e"),
    );
  });

  test("an escaped or code-span <br> is text", () => {
    expect(table("a\\<br>b `c<br>d`")).toBe(
      td("a&lt;br&gt;b <code>c&lt;br&gt;d</code>"),
    );
  });

  test("other HTML in a cell stays inert", () => {
    expect(table("<br onload=alert(1)><img src=x onerror=alert(1)>")).toBe(
      td("&lt;br onload=alert(1)&gt;&lt;img src=x onerror=alert(1)&gt;"),
    );
  });

  test("outside a table <br> is text", () => {
    expect(renderMarkdown("a<br>b")).toBe("<p>a&lt;br&gt;b</p>");
  });
});
