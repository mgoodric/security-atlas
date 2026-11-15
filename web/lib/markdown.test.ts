// Slice 672 — vitest coverage for the minimal markdown renderer
// (`web/lib/markdown.ts`).
//
// The load-bearing property is SECURITY: the input is HTML-escaped
// before any transform, so a `body_md` carrying `<script>` or an
// `onerror` handler renders inert. The tests pin that property plus the
// markdown subset the seeded policies use.

import { describe, expect, test } from "vitest";

import { escapeHtml, renderMarkdown } from "./markdown";

describe("escapeHtml", () => {
  test("escapes the five HTML-significant characters", () => {
    expect(escapeHtml(`<a href="x" id='y'>&`)).toBe(
      "&lt;a href=&quot;x&quot; id=&#39;y&#39;&gt;&amp;",
    );
  });
});

describe("renderMarkdown — security (load-bearing)", () => {
  test("inert-renders a raw <script> tag", () => {
    const html = renderMarkdown("<script>alert(1)</script>");
    expect(html).not.toContain("<script>");
    expect(html).toContain("&lt;script&gt;");
  });

  test("inert-renders an img onerror payload", () => {
    const html = renderMarkdown('![x](y) <img src=z onerror="alert(1)">');
    expect(html).not.toContain("<img");
    expect(html).toContain("&lt;img");
  });

  test("drops a javascript: link href to plain text", () => {
    const html = renderMarkdown("[click](javascript:alert(1))");
    expect(html).not.toContain("<a ");
    // The bracketed text survives as visible (escaped) text.
    expect(html).toContain("click");
  });

  test("keeps an http link with safe rel + target", () => {
    const html = renderMarkdown("[docs](https://example.com/a)");
    expect(html).toContain('href="https://example.com/a"');
    expect(html).toContain('rel="noopener noreferrer"');
    expect(html).toContain('target="_blank"');
  });

  // #2094 narrowed the allow-list to http, https and mailto: a relative
  // link now renders as its source text (see the header in markdown.ts).
  test("drops a relative link to plain text", () => {
    const html = renderMarkdown("[home](/dashboard)");
    expect(html).not.toContain("<a ");
    expect(html).toContain("[home](/dashboard)");
  });
});

describe("renderMarkdown — block grammar", () => {
  test("headings h1..h3", () => {
    const html = renderMarkdown("# A\n## B\n### C");
    expect(html).toContain("<h1>A</h1>");
    expect(html).toContain("<h2>B</h2>");
    expect(html).toContain("<h3>C</h3>");
  });

  test("unordered list", () => {
    const html = renderMarkdown("- one\n- two");
    expect(html).toContain("<ul>");
    expect(html).toContain("<li>one</li>");
    expect(html).toContain("<li>two</li>");
    expect(html).toContain("</ul>");
  });

  test("ordered list", () => {
    const html = renderMarkdown("1. first\n2. second");
    expect(html).toContain("<ol>");
    expect(html).toContain("<li>first</li>");
    expect(html).toContain("</ol>");
  });

  test("fenced code block escapes its contents", () => {
    const html = renderMarkdown("```\n<b>not bold</b>\n```");
    expect(html).toContain("<pre><code>");
    expect(html).toContain("&lt;b&gt;not bold&lt;/b&gt;");
  });

  test("horizontal rule", () => {
    const html = renderMarkdown("a\n\n---\n\nb");
    expect(html).toContain("<hr/>");
  });

  test("paragraph with bold + italic + inline code", () => {
    const html = renderMarkdown("This is **bold**, _italic_, and `code`.");
    expect(html).toContain("<strong>bold</strong>");
    expect(html).toContain("<em>italic</em>");
    expect(html).toContain("<code>code</code>");
    expect(html).toContain("<p>");
  });

  test("empty / falsy input returns empty string", () => {
    expect(renderMarkdown("")).toBe("");
  });

  test("multi-line paragraph joins with a soft break", () => {
    const html = renderMarkdown("line one\nline two");
    expect(html).toContain("line one<br/>line two");
  });

  test("inline code is not re-interpreted as emphasis", () => {
    const html = renderMarkdown("use `a_b_c` here");
    expect(html).toContain("<code>a_b_c</code>");
    // The underscores inside code must NOT become <em>.
    expect(html).not.toContain("<em>");
  });
});

// #2094 — the shared <Markdown> component's renderer. Each case on the
// security list for the ticket is pinned below.
describe("renderMarkdown — raw HTML is escaped (#2094)", () => {
  test.each([
    ["script", "<script>alert(1)</script>", "<script", "&lt;script&gt;"],
    ["img onerror", '<img src=x onerror="alert(1)">', "<img", "&lt;img"],
    [
      "iframe",
      '<iframe src="https://evil.example"></iframe>',
      "<iframe",
      "&lt;iframe",
    ],
    [
      "html inside a table cell",
      "| a |\n| - |\n| <b>x</b> |",
      "<b>",
      "&lt;b&gt;",
    ],
    [
      "html inside a block quote",
      "> <script>x</script>",
      "<script",
      "&lt;script&gt;",
    ],
  ])("%s", (_name, md, banned, expected) => {
    const html = renderMarkdown(md);
    expect(html).not.toContain(banned);
    expect(html).toContain(expected);
  });

  test("html inside a fenced code block stays escaped", () => {
    const html = renderMarkdown("```\n<script>alert(1)</script>\n```");
    expect(html).toContain(
      "<pre><code>&lt;script&gt;alert(1)&lt;/script&gt;</code></pre>",
    );
  });

  test("html inside an inline code span stays escaped", () => {
    expect(renderMarkdown("`<iframe>`")).toContain(
      "<code>&lt;iframe&gt;</code>",
    );
  });

  test("an input NUL cannot forge a placeholder", () => {
    const html = renderMarkdown("a \x000\x00 [x](https://e.example) b");
    expect(html.match(/<a /g)).toHaveLength(1);
  });
});

describe("renderMarkdown — link allow-list (#2094)", () => {
  test.each([
    ["javascript:", "[x](javascript:alert(1))"],
    ["mixed-case JaVaScRiPt:", "[x](JaVaScRiPt:alert(1))"],
    ["data:", "[x](data:text/html;base64,PHNjcmlwdD4=)"],
    ["vbscript:", "[x](vbscript:msgbox(1))"],
    ["entity-encoded javascript&#58;", "[x](javascript&#58;alert(1))"],
    ["entity-encoded colon &colon;", "[x](javascript&colon;alert(1))"],
    ["protocol-relative //evil", "[x](//evil.example/a)"],
    ["relative /path (dropped by design)", "[x](/dashboard)"],
    ["fragment #id (dropped by design)", "[x](#top)"],
    ["scheme-less host", "[x](evil.example)"],
    ["javascript: autolink", "<javascript:alert(1)>"],
    ["data: autolink", "<data:text/html,x>"],
    ["javascript: reference link", "[x][r]\n\n[r]: javascript:alert(1)"],
    ["protocol-relative reference link", "[x][r]\n\n[r]: //evil.example"],
    ["https: with no host", "[x](https:/evil)"],
  ])("drops %s", (_name, md) => {
    const html = renderMarkdown(md);
    expect(html).not.toContain("<a ");
    expect(html).not.toMatch(/href=/i);
  });

  test("keeps a mailto: link", () => {
    const html = renderMarkdown("[mail us](mailto:security@example.com)");
    expect(html).toContain(
      '<a href="mailto:security@example.com" rel="noopener noreferrer" target="_blank">mail us</a>',
    );
  });

  test.each([
    ["inline http", "[x](http://example.com)", "http://example.com"],
    [
      "inline https",
      "[x](https://example.com/a_b_c?q=1&r=2)",
      "https://example.com/a_b_c?q=1&amp;r=2",
    ],
    ["autolink", "<https://example.com/x>", "https://example.com/x"],
    ["mailto autolink", "<mailto:a@example.com>", "mailto:a@example.com"],
    [
      "reference link",
      "[x][docs]\n\n[docs]: https://example.com/d",
      "https://example.com/d",
    ],
    [
      "collapsed reference link",
      "[Docs][]\n\n[docs]: <https://example.com/c>",
      "https://example.com/c",
    ],
  ])("keeps %s with rel + target", (_name, md, href) => {
    const html = renderMarkdown(md);
    expect(html).toContain(`href="${href}"`);
    expect(html).toContain('rel="noopener noreferrer"');
    expect(html).toContain('target="_blank"');
  });

  test("a reference definition is not rendered as text", () => {
    expect(
      renderMarkdown("[x][d]\n\n[d]: https://example.com/def"),
    ).not.toContain("[d]:");
  });

  test("an underscore in a URL is not turned into emphasis", () => {
    const html = renderMarkdown("[x](https://example.com/a_b_c)");
    expect(html).not.toContain("<em>");
  });

  test("every anchor carries rel and target", () => {
    const html = renderMarkdown(
      "[a](https://a.example) <https://b.example> [c][c]\n\n[c]: mailto:c@example.com",
    );
    const anchors = html.match(/<a [^>]*>/g) ?? [];
    expect(anchors).toHaveLength(3);
    for (const a of anchors) {
      expect(a).toContain('rel="noopener noreferrer"');
      expect(a).toContain('target="_blank"');
    }
  });

  test("a quote in an allowed href cannot break out of the attribute", () => {
    const html = renderMarkdown(
      '[x](https://e.example/"onmouseover="alert(1))',
    );
    // The quote stays an entity inside href; no attribute is opened.
    expect(html).toContain('href="https://e.example/&quot;onmouseover=&quot;');
    expect(html).not.toContain('"onmouseover="');
  });
});

describe("renderMarkdown — images are never rendered (#2094)", () => {
  test.each([
    ["https image", "![logo](https://example.com/p.png)"],
    ["javascript image", "![x](javascript:alert(1))"],
    ["reference image", "![pixel][p]\n\n[p]: https://tracker.example/p.gif"],
  ])("%s renders no <img> and no link", (_name, md) => {
    const html = renderMarkdown(md);
    expect(html).not.toContain("<img");
    expect(html).not.toContain("<a ");
  });

  test("alt text is kept", () => {
    expect(renderMarkdown("![diagram](https://example.com/d.png)")).toBe(
      "<p>diagram</p>",
    );
  });
});

describe("renderMarkdown — tables and block quotes (#2094)", () => {
  test("a GFM table renders as a table", () => {
    const html = renderMarkdown(
      "| Control | Owner |\n| :--- | ---: |\n| MFA | **IT** |\n| Backups | Ops |",
    );
    expect(html).toContain(
      "<table><thead><tr><th>Control</th><th>Owner</th></tr></thead>",
    );
    expect(html).toContain("<tr><td>MFA</td><td><strong>IT</strong></td></tr>");
    expect(html).toContain("<tr><td>Backups</td><td>Ops</td></tr>");
    expect(html).not.toContain("|");
  });

  test("an escaped pipe stays inside its cell; short rows are padded", () => {
    const html = renderMarkdown("a | b\n--|--\nx \\| y\n");
    expect(html).toContain("<td>x | y</td><td></td>");
  });

  test("a block quote renders as a blockquote with inner markdown", () => {
    const html = renderMarkdown("> Note: **read** this\n> - one\n> - two");
    expect(html).toContain("<blockquote>");
    expect(html).toContain("<strong>read</strong>");
    expect(html).toContain("<li>one</li>");
    expect(html).not.toContain("&gt;");
  });

  test("nested block quotes nest, and depth is bounded", () => {
    expect(renderMarkdown(">> deep")).toContain("<blockquote><blockquote>");
    const html = renderMarkdown(">".repeat(5000) + " x");
    expect(html.match(/<blockquote>/g)).toHaveLength(8);
  });
});
